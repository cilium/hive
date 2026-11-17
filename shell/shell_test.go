// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package shell

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"net"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/hive/job"
)

var client = flag.String("client", "", "Act as client to given unix socket")

func TestMain(m *testing.M) {
	flag.Parse()
	if *client != "" {
		cfg := Config{ShellSockPath: *client}
		args := flag.Args()
		if len(args) > 0 {
			executeShell(cfg, "test> ", nil, args)
		} else {
			interactiveShell(cfg, "test> ", nil)
			return
		}
	} else {
		os.Exit(m.Run())
	}
}

func fixture(t *testing.T, cfg Config) {
	h := hive.New(
		job.Cell,
		cell.SimpleHealthCell,
		cell.Provide(
			func(r job.Registry, lc cell.Lifecycle, health cell.Health) job.Group {
				return r.NewGroup(health)
			},
		),
		ServerCell(cfg.ShellSockPath),
	)

	log := hivetest.Logger(t)
	require.NoError(t,
		h.Start(log, context.TODO()),
		"Start")
	t.Cleanup(func() {
		assert.NoError(t,
			h.Stop(log, context.TODO()),
			"Stop")
	})

	// Wait for the socket file to appear to avoid the 1s retry backoff
	for range 100 {
		_, err := os.Stat(cfg.ShellSockPath)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
}

func TestShellExchange(t *testing.T) {
	sock := path.Join(t.TempDir(), "shell.sock")
	cfg := Config{sock}
	fixture(t, cfg)

	var buf strings.Builder
	err := ShellExchange(cfg, &buf, "help")
	assert.NoError(t, err, "ShellExchangeWithConfig")
	assert.Contains(t, buf.String(), "commands:")

	err = ShellExchange(cfg, &buf, "unknown")
	assert.ErrorContains(t, err, "unknown: unknown command")

	err = ShellExchange(cfg, &buf, "help -a fore")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "foreach")
}

func TestInteractiveShell(t *testing.T) {
	sock := path.Join(t.TempDir(), "shell.sock")
	cfg := Config{sock}
	fixture(t, cfg)

	// Interactive use
	cmd := exec.Command(os.Args[0], "-client", sock)
	cmd.Stdin = strings.NewReader(strings.Join([]string{
		"unknown",
		"help help",
		"fore\t foo bar {",
		"echo shell-${foo}-result",
		"}",
		"foreach foo bar baz { echo shell-${foo}-result }",
		"foreach foo A B {",
		"foreach bar 1 2 {",
		"echo nested-${foo}-${bar}",
		"}",
		"}",
		"foreach foo C D { foreach bar 3 4 { echo nested-${foo}-${bar} } }",
		"echo top-${/}-one; echo top-${/}-two",
		"foreach foo X Y { echo loop-${foo}; echo block-${foo}-done }",
		"retry --int\t 1ms --max-retries=1 { echo retry-${/}-ok }",
		"echo completion-${/}-before; fore\t item A { ech\t completion-${item}-inline }",
		"foreach item B { when true { ech\t completion-${item}-nested } }",
		"foreach item C { retry --int\t 1ms --max-retries=1 { echo completion-${item}-flag } }",
		"exit",
	}, "\r\n") + "\r\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "CombinedOutput")

	require.Contains(t, string(out), "unknown: unknown command")
	require.Contains(t, string(out), "test> help help")
	require.Contains(t, string(out), "log help text")

	normalized := strings.ReplaceAll(string(out), "\r\n", "\n")
	for _, value := range []string{
		"shell-bar-result", "shell-baz-result",
		"nested-A-1", "nested-A-2", "nested-B-1", "nested-B-2",
		"nested-C-3", "nested-C-4", "nested-D-3", "nested-D-4",
		"completion-/-before", "completion-A-inline", "completion-B-nested", "completion-C-flag",
		"top-/-one", "top-/-two", "loop-X", "loop-Y", "block-X-done", "block-Y-done", "retry-/-ok",
	} {
		require.Contains(t, normalized, "\n"+value+"\n")
	}
	require.NotContains(t, normalized, "parse error")
	require.Contains(t, normalized, "\n> echo nested-A-1\n")
	require.NotContains(t, normalized, "> echo nested-${foo}-${bar}\n")
	require.Equal(t, 1, strings.Count(normalized, "unknown: unknown command"))

	// Non-interactive with failure
	cmd = exec.Command(os.Args[0], "-client", sock, "unknown")
	out, err = cmd.CombinedOutput()
	require.Contains(t, string(out), "unknown: unknown command")
	require.ErrorContains(t, err, "exit status 1")
}

func TestShellAutocompleteStatements(t *testing.T) {
	sock := path.Join(t.TempDir(), "shell.sock")
	fixture(t, Config{sock})
	conn, err := net.Dial("unix", sock)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	bio := bufio.NewReader(conn)
	for _, tc := range []struct{ input, want string }{
		{"echo first; fore", "echo first; foreach"},
		{"echo 'a;#{}'; fore", "echo 'a;#{}'; foreach"},
		{"foreach x A { ech", "foreach x A { echo"},
		{"foreach x A { when true { ech", "foreach x A { when true { echo"},
		{"foreach x A { echo ${x}; ech", "foreach x A { echo ${x}; echo"},
		{"foreach x A { echo a }; ech", "foreach x A { echo a }; echo"},
		{"echo first; [unknown] ! fore", "echo first; [unknown] ! foreach"},
		{"foreach x A { retry --int", "foreach x A { retry --interval"},
		{"foreach x A { retry\t --int", "foreach x A { retry\t --interval"},
		{"echo first;  ", "echo first;  cat"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, pos, ok := autocomplete(conn, bio)(tc.input, len(tc.input), '\t')
			require.True(t, ok)
			require.Equal(t, tc.want, got)
			require.Equal(t, len(tc.want), pos)
		})
	}
	input := "foreach x A { ech }"
	got, pos, ok := autocomplete(conn, bio)(input, len(input)-2, '\t')
	require.True(t, ok)
	require.Equal(t, "foreach x A { echo }", got)
	require.Equal(t, len("foreach x A { echo"), pos)
	// File completion exercises argument suggestions and preserves their spacing.
	file := path.Join(t.TempDir(), "completion-file")
	require.NoError(t, os.WriteFile(file, []byte("data"), 0600))
	input = "echo first; cat\t  " + file[:len(file)-3]
	got, _, ok = autocomplete(conn, bio)(input, len(input), '\t')
	require.True(t, ok)
	require.Equal(t, "echo first; cat\t  "+file, got)
	// A server error must finish the query so the next completion can proceed.
	_, _, ok = autocomplete(conn, bio)("missing argument", len("missing argument"), '\t')
	require.False(t, ok)
	got, _, ok = autocomplete(conn, bio)("fore", len("fore"), '\t')
	require.True(t, ok)
	require.Equal(t, "foreach", got)
	var output bytes.Buffer
	require.NoError(t, ShellExchange(Config{sock}, &output, "echo ok"))
}
