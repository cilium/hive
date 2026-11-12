// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

type statementKind uint8

const (
	simpleStatement statementKind = iota
	commentStatement
)

type statement struct {
	text string
	line int
	kind statementKind
}

// scanDelimiters visits unquoted command delimiters and ignores variable expansions.
func scanDelimiters(line string, visit func(int, byte)) {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; c {
		case '\'':
			if quoted && i+1 < len(line) && line[i+1] == '\'' {
				i++
			} else {
				quoted = !quoted
			}
		case '$':
			if !quoted && i+1 < len(line) && line[i+1] == '{' {
				i += 2
				for i < len(line) && line[i] != '}' {
					i++
				}
			}
		case '#':
			if !quoted {
				visit(i, c)
				return
			}
		}
	}
}

// statementReader reads commands while retaining their physical source lines.
type statementReader struct {
	reader  *bufio.Reader
	line    int
	pending []statement
	eof     bool
}

func (r *statementReader) next() (statement, error) {
	for len(r.pending) == 0 {
		if r.reader == nil || r.eof {
			return statement{}, io.EOF
		}
		line, err := r.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return statement{}, err
		}
		r.eof = err == io.EOF
		if line == "" && r.eof {
			return statement{}, io.EOF
		}
		r.line++
		r.pending = splitStatements(strings.TrimSuffix(line, "\n"), r.line)
	}
	next := r.pending[0]
	r.pending = r.pending[1:]
	return next, nil
}

func splitStatements(line string, lineno int) []statement {
	var out []statement
	start := 0
	emit := func(end int, kind statementKind) {
		text := strings.TrimSpace(line[start:end])
		if text != "" {
			out = append(out, statement{text, lineno, kind})
		}
	}
	scanDelimiters(line, func(i int, c byte) {
		switch c {
		case '#':
			emit(i, simpleStatement)
			if i == 0 {
				out = append(out, statement{line, lineno, commentStatement})
			}
			start = len(line)
			return
		}
		start = i + 1
	})
	emit(len(line), simpleStatement)
	return out
}

// SplitCompletionInput identifies the command being entered at the end of input.
// Prefix contains earlier statements, conditions, status prefixes, and leading
// whitespace. Args includes the whitespace after the command name; an empty Args
// means the cursor is still in the name. No commands or conditions are evaluated.
func SplitCompletionInput(input string) (prefix, name, args string, err error) {
	start, offset := 0, 0
	comment := false
	for line := range strings.SplitAfterSeq(input, "\n") {
		comment = false
		scanDelimiters(line, func(i int, c byte) {
			switch c {
			case ';', '{', '}':
				start = offset + i + 1
			case '#':
				comment = true
			}
		})
		offset += len(line)
		if strings.HasSuffix(line, "\n") {
			start = offset
			comment = false
		}
	}
	if comment {
		return "", "", "", errors.New("cannot complete a comment")
	}
	cmd, err := parse("<stdin>", 0, input[start:])
	if err != nil {
		return "", "", "", err
	}
	if cmd == nil {
		return input, "", "", nil
	}
	end := start + cmd.nameEnd
	return input[:end-len(cmd.name)], cmd.name, input[end:], nil
}
