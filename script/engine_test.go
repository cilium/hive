// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"golang.org/x/term"
)

func engineState(t *testing.T, ctx context.Context) *State {
	t.Helper()
	s, err := NewState(ctx, t.TempDir(), []string{"NAME=value"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.CloseAndWait(io.Discard)) })
	return s
}

func executeSource(e *Engine, s *State, source string, interactive bool) error {
	if interactive {
		return e.ExecuteLine(s, source, io.Discard)
	}
	return e.Execute(s, "test", bufio.NewReader(strings.NewReader(source)), io.Discard)
}

func TestExecuteSyntax(t *testing.T) {
	cases := []struct {
		name, source string
		want         []string
	}{
		{"expansions", "record ${NAME}; record ${/}; record ${:}", []string{"value", string(os.PathSeparator), string(os.PathListSeparator)}},
		{"quoting", "record 'a; # { }'; record 'it''s'; record '${NAME}'; record a{b}c", []string{"a; # { }", "it's", "${NAME}", "a{b}c"}},
		{"inline", "record before; foreach x A B { record ${x}; record inner }; record after", []string{"before", "A", "inner", "B", "inner", "after"}},
		{"multiline", "record before; foreach x A B { # opening\nrecord ${x} # ignored { ; }\n} # closing\nrecord after", []string{"before", "A", "B", "after"}},
		{"nested", "foreach x A B { foreach y 1 2 { record ${x}${y} } }", []string{"A1", "A2", "B1", "B2"}},
		{"anonymous", "{ record one; { record two } }; record three", []string{"one", "two", "three"}},
		{"empty", ";; when true {}; foreach x {}; repeat i 0 { record wrong }; record done;;", []string{"done"}},
		{"comments", "# outer section\nwhen true {\n# inner section\nrecord yes\n}\n", []string{"yes"}},
	}
	for _, tc := range cases {
		for _, interactive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interactive=%v", tc.name, interactive), func(t *testing.T) {
				e, s := NewEngine(), engineState(t, context.Background())
				var got []string
				e.Cmds["record"] = Command(CmdUsage{}, func(_ *State, args ...string) (WaitFunc, error) {
					got = append(got, strings.Join(args, " "))
					return nil, nil
				})
				require.NoError(t, executeSource(e, s, tc.source, interactive))
				require.Equal(t, tc.want, got)
			})
		}
	}
}

func TestBlockErrorsAndStatus(t *testing.T) {
	cases := []struct {
		name, source string
		parseError   bool
		errorText    string
	}{
		{"negated named", "! foreach x A { fail }", false, ""},
		{"inherited negation", "! { foreach x A { fail } }", false, ""},
		{"nested anonymous negation", "! { { foreach x A { fail } } }", false, ""},
		{"optional", "? foreach x A { fail }", false, ""},
		{"negated success", "! foreach x A { echo ok }", false, "unexpected success"},
		{"negated parse error", "! foreach x A {\necho '\n}", true, "unterminated quoted argument"},
		{"optional parse error", "? foreach x A {\necho '\n}", true, "unterminated quoted argument"},
		{"retry parse error", "! retry --max-retries=1 {\necho '\n}", true, "unterminated quoted argument"},
		{"unclosed", "when true {", true, "unterminated block"},
		{"extra close", "echo ok; }", true, "unexpected closing brace"},
		{"retry prefix", "* foreach x A {}", true, "use the retry block command"},
		{"until failure prefix", "!* foreach x A {}", true, "use the retry block command"},
		{"missing body", "foreach x A", false, "requires a body"},
	}
	for _, tc := range cases {
		for _, interactive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interactive=%v", tc.name, interactive), func(t *testing.T) {
				e, s := NewEngine(), engineState(t, context.Background())
				e.Cmds["fail"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { return nil, errors.New("failed") })
				err := executeSource(e, s, tc.source, interactive)
				if tc.errorText == "" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, tc.errorText)
				}
				require.Equal(t, tc.parseError, errors.Is(err, ParseError))
			})
		}
	}
}

func TestBlockSourceLines(t *testing.T) {
	s := engineState(t, context.Background())
	err := executeSource(NewEngine(), s, "# section\n\nforeach x A {\n  when true {\n    echo hi; missing\n  }\n}\n", false)
	require.ErrorContains(t, err, "test:5: missing: unknown command")
}

func TestStopInBlocks(t *testing.T) {
	for _, header := range []string{"foreach x A B", "repeat i 2", "retry --until-failure --max-retries=1", "when true", "unless false", "withenv NAME=inner", "!", "?", "*"} {
		for _, interactive := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interactive=%v", header, interactive), func(t *testing.T) {
				e, s := NewEngine(), engineState(t, context.Background())
				calls := 0
				e.Cmds["record"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { calls++; return nil, nil })
				source := header + " { when true { stop done }; record }; record"
				if header == "*" {
					source = "* { stop done; record }; record"
				}
				err := executeSource(e, s, source, interactive)
				if interactive {
					var stop stopError
					require.ErrorAs(t, err, &stop)
				} else {
					require.NoError(t, err)
				}
				require.Zero(t, calls)
				value, ok := s.LookupEnv("NAME")
				require.True(t, ok)
				require.Equal(t, "value", value)
			})
		}
	}
}

func TestBlockInvocationMetadata(t *testing.T) {
	e, s := NewEngine(), engineState(t, context.Background())
	e.BlockCmds["custom"] = BlockCommand(CmdUsage{Flags: func(fs *pflag.FlagSet) { fs.String("label", "", "label") }}, func(s *State, args []string, body CallBlock) error {
		flags := s.Flags
		for range 2 {
			if err := body(map[string]string{"NAME": "temporary"}); err != nil {
				return err
			}
			require.Same(t, flags, s.Flags)
			label, err := s.Flags.GetString("label")
			require.NoError(t, err)
			require.Equal(t, "outer", label)
		}
		return nil
	})
	require.NoError(t, e.ExecuteLine(s, "custom --label=outer { foreach x A B { echo $x }; env NAME=changed; env OTHER=persisted }", io.Discard))
	value, _ := s.LookupEnv("NAME")
	require.Equal(t, "value", value)
	value, _ = s.LookupEnv("OTHER")
	require.Equal(t, "persisted", value)
}

func TestBlockMetadataAndCompletion(t *testing.T) {
	e, s := NewEngine(), engineState(t, context.Background())
	calls := 0
	usage := CmdUsage{
		Summary: "custom block help",
		Flags:   func(fs *pflag.FlagSet) { fs.String("label", "", "label") },
		AutocompleteArgs: func(_ *State, before []string, prefix string) []string {
			if prefix == "pre" {
				return []string{strings.Join(append(before, "prefix"), "-")}
			}
			return nil
		},
		AutocompleteFlag: func(_ *State, before []string, flag, prefix string) []string {
			require.Equal(t, "label", flag)
			require.Equal(t, "pre", prefix)
			return []string{"prefix"}
		},
	}
	e.BlockCmds["custom"] = BlockCommand(usage, func(*State, []string, CallBlock) error { calls++; return nil })
	for _, source := range []string{"custom --help", "custom --help {}"} {
		require.NoError(t, e.ExecuteLine(s, source, io.Discard))
		require.Contains(t, s.Stdout(), "custom block help")
	}
	cases := []struct{ input, want string }{
		{"pre", "custom prefix\n"}, {"one pre", "custom one one-prefix\n"},
		{"--lab", "custom --label\n"}, {"--label=pre", "custom --label=prefix\n"}, {"--label pre", "custom --label prefix\n"},
	}
	for _, tc := range cases {
		for _, body := range []string{"", " {}"} {
			require.NoError(t, e.ExecuteLine(s, "custom --autocomplete='"+tc.input+"'"+body, io.Discard))
			require.Equal(t, tc.want, s.Stdout())
		}
		terminal := term.NewTerminal(new(bytes.Buffer), "")
		input := "custom " + tc.input
		completed, _, ok := e.Autocomplete(terminal, s)(input, len(input), '\t')
		require.True(t, ok)
		require.Equal(t, strings.TrimSuffix(tc.want, "\n"), completed)
	}
	require.NoError(t, e.ExecuteLine(s, "retry --autocomplete='--int'", io.Discard))
	require.Equal(t, "retry --interval\n", s.Stdout())
	require.Zero(t, calls)
}

func TestLegacyRetryReplaysBlocks(t *testing.T) {
	e, s := NewEngine(), engineState(t, context.Background())
	e.RetryInterval = time.Millisecond
	e.MaxRetryInterval = time.Millisecond
	e.MaxRetries = 1
	attempts, blocks := 0, 0
	e.Cmds["record"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { blocks++; return nil, nil })
	e.Cmds["flaky"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("retry")
		}
		return nil, nil
	})
	require.NoError(t, e.ExecuteLine(s, "when true { record }; * flaky", io.Discard))
	require.Equal(t, 2, blocks)
	require.Equal(t, 2, attempts)
}

func TestBlockEnvironmentRestoration(t *testing.T) {
	for _, ending := range []string{"", "; fail", "; stop done", "; echo '"} {
		t.Run(ending, func(t *testing.T) {
			e, s := NewEngine(), engineState(t, context.Background())
			require.NoError(t, s.Setenv("EMPTY", ""))
			e.Cmds["set"] = Command(CmdUsage{}, func(s *State, args ...string) (WaitFunc, error) { return nil, s.Setenv(args[0], args[1]) })
			e.Cmds["fail"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { return nil, errors.New("failed") })
			// Use a newline for the syntax-error case so its closing brace is parsed.
			source := "withenv NAME=outer NEW=temporary EMPTY=temporary {\n" +
				"withenv NAME=inner { set NAME changed; set OTHER persisted }; set NAME changed; set NEW changed; set EMPTY changed" + ending + "\n}"
			err := executeSource(e, s, source, false)
			if ending == "; fail" {
				require.ErrorContains(t, err, "failed")
			} else if ending == "; echo '" {
				require.ErrorIs(t, err, ParseError)
			} else {
				require.NoError(t, err)
			}
			value, ok := s.LookupEnv("NAME")
			require.True(t, ok)
			require.Equal(t, "value", value)
			value, ok = s.LookupEnv("EMPTY")
			require.True(t, ok)
			require.Empty(t, value)
			_, ok = s.LookupEnv("NEW")
			require.False(t, ok)
			value, ok = s.LookupEnv("OTHER")
			require.True(t, ok)
			require.Equal(t, "persisted", value)
			require.Contains(t, s.Environ(), "NAME=value")
			require.Contains(t, s.Environ(), "EMPTY=")
			for _, entry := range s.Environ() {
				require.False(t, strings.HasPrefix(entry, "NEW="))
			}
		})
	}
	e, s := NewEngine(), engineState(t, context.Background())
	var got []string
	e.Cmds["record"] = Command(CmdUsage{}, func(_ *State, args ...string) (WaitFunc, error) { got = append(got, args...); return nil, nil })
	require.NoError(t, e.ExecuteLine(s, "withenv NAME=outer { withenv NAME=inner { record $NAME }; record $NAME }; record $NAME", io.Discard))
	require.Equal(t, []string{"inner", "outer", "value"}, got)
}

func TestBlockConditions(t *testing.T) {
	e, s := NewEngine(), engineState(t, context.Background())
	e.Conds["yes"] = BoolCondition("true", true)
	e.Conds["no"] = BoolCondition("false", false)
	source := `[no] foreach x A { env RESULT=wrong }; [yes] { foreach x A { env RESULT=$x } }; [no] { env RESULT=wrong }`
	require.NoError(t, e.ExecuteLine(s, source, io.Discard))
	value, _ := s.LookupEnv("RESULT")
	require.Equal(t, "A", value)
}

func TestExpandedBlockLog(t *testing.T) {
	e, s := NewEngine(), engineState(t, context.Background())
	var log bytes.Buffer
	source := "foreach tbl backends frontends services { echo $tbl }; withenv NAME='two words' { echo $NAME; echo '$NAME'; echo '' }"
	require.NoError(t, e.ExecuteLine(s, source, &log))
	text := log.String()
	for _, command := range []string{"> echo backends\n", "> echo frontends\n", "> echo services\n", "> echo 'two words'\n", "> echo '$NAME'\n", "> echo ''\n"} {
		require.Contains(t, text, command)
	}
	require.NotContains(t, text, "> echo $tbl")
	require.Equal(t, 1, strings.Count(text, "> echo backends\n"))
}

func TestNestedRetryBlocks(t *testing.T) {
	e, s := NewEngine(), engineState(t, context.Background())
	e.RetryInterval = time.Microsecond
	e.MaxRetryInterval = time.Microsecond
	attempts := map[string]int{}
	e.Cmds["flaky"] = Command(CmdUsage{}, func(_ *State, args ...string) (WaitFunc, error) {
		attempts[args[0]]++
		if attempts[args[0]]%2 == 1 {
			return nil, errors.New("retry")
		}
		return nil, nil
	})
	source := `retry --max-retries=1 { retry --max-retries=1 { flaky inner }; flaky outer }`
	require.NoError(t, e.ExecuteLine(s, source, io.Discard))
	require.Equal(t, map[string]int{"inner": 4, "outer": 2}, attempts)
}

func TestBlockArgumentValidation(t *testing.T) {
	for _, tc := range []struct{ source, message string }{
		{"foreach {}", "requires a variable"},
		{"foreach '' A {}", "invalid block variable"},
		{"repeat i -1 {}", "non-negative integer"},
		{"repeat i nope {}", "non-negative integer"},
		{"repeat i {}", "requires a variable and count"},
		{"when {}", "requires one value"},
		{"unless a b {}", "requires one value"},
		{"withenv {}", "requires at least one"},
		{"withenv bad {}", "KEY=VALUE"},
		{"withenv '=bad' {}", "invalid block variable"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			e, s := NewEngine(), engineState(t, context.Background())
			require.ErrorContains(t, e.ExecuteLine(s, tc.source, io.Discard), tc.message)
		})
	}
}
