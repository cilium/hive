// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

type statementKind uint8

const (
	simpleStatement statementKind = iota
	openBlock
	closeBlock
	commentStatement
)

type statement struct {
	text string
	line int
	kind statementKind
}

// scanDelimiters visits unquoted command delimiters. Braces must be standalone
// tokens; braces in variable expansions and argument text are not delimiters.
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
		case ';':
			if !quoted {
				visit(i, c)
			}
		case '{', '}':
			boundary := func(c byte) bool { return strings.ContainsRune(" \t\r\n;{}", rune(c)) }
			if !quoted && (i == 0 || boundary(line[i-1])) && (i+1 == len(line) || boundary(line[i+1]) || line[i+1] == '#') {
				visit(i, c)
			}
		}
	}
}

// BlockDepth returns the number of unclosed block delimiters in input.
// Quoted text, comments, and variable expansions do not contain delimiters.
// A negative result indicates an unexpected closing delimiter.
func BlockDepth(input string) int {
	depth := 0
	for line := range strings.SplitSeq(input, "\n") {
		scanDelimiters(line, func(_ int, c byte) {
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
		})
	}
	return depth
}

// StripComment removes the unquoted comment at the end of an input line.
func StripComment(line string) string {
	end := len(line)
	scanDelimiters(line, func(i int, c byte) {
		if c == '#' {
			end = i
		}
	})
	return line[:end]
}

// statementReader splits physical lines into commands and block delimiters.
// A captured block can be replayed without reparsing or changing source lines.
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
		if text != "" || kind == openBlock {
			out = append(out, statement{text, lineno, kind})
		}
	}
	scanDelimiters(line, func(i int, c byte) {
		switch c {
		case ';':
			emit(i, simpleStatement)
		case '{':
			emit(i, openBlock)
		case '}':
			emit(i, simpleStatement)
			out = append(out, statement{"}", lineno, closeBlock})
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

func (r *statementReader) block(header statement) ([]statement, error) {
	var body []statement
	depth := 1
	for {
		stmt, err := r.next()
		if err == io.EOF {
			return nil, fmt.Errorf("unterminated block starting with %q", header.text+" {")
		}
		if err != nil {
			return nil, err
		}
		switch stmt.kind {
		case openBlock:
			depth++
		case closeBlock:
			depth--
		}
		if depth == 0 {
			return body, nil
		}
		body = append(body, stmt)
	}
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
