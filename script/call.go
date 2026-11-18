// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/txtar"
)

// Call returns a command that executes another script in the current engine
// and state. If the called txtar contains files, it runs in a fresh working
// directory initialized with a copy of the caller's working directory.
func Call() Cmd {
	return Command(
		CmdUsage{
			Summary: "execute another script",
			Args:    "file",
			Detail: []string{
				"Relative paths are resolved from the current working directory. Use $DATADIR to refer to files beside the original test script.",
				"The file may be a plain script or a txtar whose comment is the script and whose files are its fixtures.",
				"A txtar with files runs in an isolated directory initialized with the caller's current files. Conflicting fixture paths are an error.",
				"The called script inherits the caller's environment, but environment changes and working directory changes are restored when it returns.",
			},
		},
		func(s *State, args ...string) (WaitFunc, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("%w: call requires one script file", ErrUsage)
			}
			if s.engine == nil {
				return nil, fmt.Errorf("call: no script engine is executing")
			}

			file := s.Path(args[0])
			archive, err := txtar.ParseFile(file)
			if err != nil {
				return nil, err
			}

			oldPwd := s.Getwd()
			oldEnv := append([]string(nil), s.env...)
			oldEnvMap := maps.Clone(s.envMap)
			defer func() {
				s.pwd = oldPwd
				s.env = oldEnv
				s.envMap = oldEnvMap
			}()

			callDir := ""
			if len(archive.Files) > 0 {
				callDir, err = os.MkdirTemp(s.workdir, ".script-call-")
				if err != nil {
					return nil, err
				}
				if err := copyCallWorkspace(oldPwd, callDir, s.workdir); err != nil {
					return nil, err
				}
				if err := s.Chdir(callDir); err != nil {
					return nil, err
				}

				for _, f := range archive.Files {
					name := s.Path(s.ExpandEnv(f.Name, false))
					if _, err := os.Lstat(name); err == nil {
						return nil, fmt.Errorf("call: fixture %q conflicts with a caller file", f.Name)
					} else if !os.IsNotExist(err) {
						return nil, err
					}
				}
				if err := s.ExtractFiles(archive); err != nil {
					return nil, err
				}
			}

			run := func() error {
				return s.engine.Execute(s, file, bufio.NewReader(bytes.NewReader(archive.Comment)), s.logOut)
			}
			if callDir != "" {
				if err := s.Setenv("WORK", callDir); err != nil {
					return nil, err
				}
			}
			err = run()
			if err != nil {
				return nil, err
			}
			return nil, nil
		},
	)
}

// copyCallWorkspace copies the caller's current working tree into a call
// directory. The directory used for call workspaces is excluded when copying
// the root workspace so prior calls are not copied recursively.
func copyCallWorkspace(src, dst, workdir string) error {
	if err := os.MkdirAll(dst, 0777); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dst {
			return filepath.SkipDir
		}
		if path == workdir && entry.IsDir() {
			// The destination and older call workspaces are implementation details
			// under workdir, not input files for a new call.
			return nil
		}
		if path != src && filepath.Dir(path) == workdir && strings.HasPrefix(entry.Name(), ".script-call-") && entry.IsDir() {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("call: cannot copy special file %q", path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0777); err != nil {
			return err
		}
		return copyCallFile(path, target, info.Mode().Perm())
	})
}

func copyCallFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
