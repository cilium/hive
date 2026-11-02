// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// DefaultBlockCmds returns the block commands included in a basic script
// engine.
func DefaultBlockCmds() map[string]BlockCmd {
	return map[string]BlockCmd{
		"foreach": Foreach(),
		"when":    When(),
		"unless":  Unless(),
		"repeat":  Repeat(),
		"retry":   Retry(),
		"withenv": WithEnv(),
	}
}

// WithEnv returns a block command that executes its body with temporary
// environment variables.
func WithEnv() BlockCmd {
	return BlockCommand(
		CmdUsage{
			Summary: "execute a block with temporary environment variables",
			Args:    "assignments...",
			Detail: []string{
				"Assignments use KEY=VALUE syntax and are visible only while the block runs.",
				"Changes to other variables made inside the block persist after it finishes.",
			},
		},
		func(_ *State, args []string, callBlock CallBlock) error {
			if len(args) == 0 {
				return fmt.Errorf("%w: withenv requires at least one KEY=VALUE assignment", ErrUsage)
			}
			extraEnv := make(map[string]string, len(args))
			for _, assignment := range args {
				key, value, ok := strings.Cut(assignment, "=")
				if !ok {
					return fmt.Errorf("withenv assignment %q must use KEY=VALUE syntax", assignment)
				}
				if err := validateBlockVariable(key); err != nil {
					return err
				}
				extraEnv[key] = value
			}
			return callBlock(extraEnv)
		},
	)
}

// Foreach returns a block command that executes its body once for each value,
// setting variable to the current value for that call.
func Foreach() BlockCmd {
	return BlockCommand(
		CmdUsage{
			Summary: "execute a block for each value",
			Args:    "variable values...",
			Detail: []string{
				"The variable is set to each value for the corresponding block execution.",
				"Changes to the variable inside the block are discarded after that execution; changes to other variables persist.",
			},
		},
		func(_ *State, args []string, callBlock CallBlock) error {
			if len(args) < 2 {
				return fmt.Errorf("%w: foreach requires a variable and at least one value", ErrUsage)
			}
			if err := validateBlockVariable(args[0]); err != nil {
				return err
			}
			for _, value := range args[1:] {
				if err := callBlock(map[string]string{args[0]: value}); err != nil {
					return err
				}
			}
			return nil
		},
	)
}

// When returns a block command that executes its body if its argument is "t"
// or "true", ignoring case.
func When() BlockCmd {
	return BlockCommand(
		CmdUsage{
			Summary: "execute a block when its argument is true",
			Args:    "value",
			Detail: []string{
				"The block runs when value is 't' or 'true', ignoring case.",
				"All other values skip the block.",
			},
		},
		func(_ *State, args []string, callBlock CallBlock) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: when requires one value", ErrUsage)
			}
			value := strings.ToLower(args[0])
			if value == "t" || value == "true" {
				return callBlock(nil)
			}
			return nil
		},
	)
}

// Unless returns a block command that executes its body if its argument is not
// "t" or "true", ignoring case.
func Unless() BlockCmd {
	return BlockCommand(
		CmdUsage{
			Summary: "execute a block when its argument is false",
			Args:    "value",
			Detail: []string{
				"The block is skipped when value is 't' or 'true', ignoring case.",
				"All other values run the block.",
			},
		},
		func(_ *State, args []string, callBlock CallBlock) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: unless requires one value", ErrUsage)
			}
			value := strings.ToLower(args[0])
			if value != "t" && value != "true" {
				return callBlock(nil)
			}
			return nil
		},
	)
}

// Repeat returns a block command that executes its body count times, setting
// variable to the zero-based iteration index for each call.
func Repeat() BlockCmd {
	return BlockCommand(
		CmdUsage{
			Summary: "repeat a block a fixed number of times",
			Args:    "variable count",
			Detail: []string{
				"The variable is set to the zero-based iteration index for each block execution.",
				"Changes to the variable inside the block are discarded after that execution; changes to other variables persist.",
			},
		},
		func(_ *State, args []string, callBlock CallBlock) error {
			if len(args) != 2 {
				return fmt.Errorf("%w: repeat requires a variable and count", ErrUsage)
			}
			if err := validateBlockVariable(args[0]); err != nil {
				return err
			}
			count, err := strconv.Atoi(args[1])
			if err != nil || count < 0 {
				return fmt.Errorf("repeat count must be a non-negative integer, got %q", args[1])
			}
			for i := 0; i < count; i++ {
				if err := callBlock(map[string]string{args[0]: strconv.Itoa(i)}); err != nil {
					return err
				}
			}
			return nil
		},
	)
}

func validateBlockVariable(name string) error {
	if name == "" || strings.ContainsAny(name, "=\x00") {
		return fmt.Errorf("invalid block variable name %q", name)
	}
	return nil
}

// Retry returns a block command that repeats its body until it succeeds.
//
// The --max-retries flag limits the number of attempts after the initial
// attempt; zero means unlimited retries. The --interval flag sets a fixed delay
// between attempts. By default, the engine's exponential retry interval is
// used. With --until-failure, the block repeats until it fails, and that
// failure is treated as success.
func Retry() BlockCmd {
	return BlockCommand(
		CmdUsage{
			Summary: "repeat a block until it succeeds or fails",
			Flags: func(fs *pflag.FlagSet) {
				fs.Int("max-retries", 0, "maximum retries after the first attempt; 0 means unlimited")
				fs.Duration("interval", 0, "fixed delay between retries; 0 uses the engine retry interval")
				fs.Bool("until-failure", false, "repeat until the block fails")
			},
			Detail: []string{
				"The block body is run at least once. Its environment changes persist between attempts and after the block, except for variables supplied through the block call's extra environment.",
				"With --until-failure, a block failure ends the retry successfully. Parse errors, stop, and context cancellation still end the script.",
			},
		},
		func(s *State, args []string, callBlock CallBlock) error {
			if len(args) != 0 {
				return fmt.Errorf("%w: retry does not take positional arguments", ErrUsage)
			}
			maxRetries, err := s.Flags.GetInt("max-retries")
			if err != nil {
				return err
			}
			interval, err := s.Flags.GetDuration("interval")
			if err != nil {
				return err
			}
			untilFailure, err := s.Flags.GetBool("until-failure")
			if err != nil {
				return err
			}
			if maxRetries < 0 {
				return errors.New("--max-retries must be non-negative")
			}
			if interval < 0 {
				return errors.New("--interval must be non-negative")
			}

			retryInterval, maxRetryInterval := defaultRetryInterval, defaultMaxRetryInterval
			if s.engine != nil {
				if s.engine.RetryInterval > 0 {
					retryInterval = s.engine.RetryInterval
				}
				if s.engine.MaxRetryInterval > 0 {
					maxRetryInterval = s.engine.MaxRetryInterval
				}
			}
			backoff := exponentialBackoff{interval: retryInterval, max: maxRetryInterval}

			retries := 0
			for {
				err := callBlock(nil)
				if err == nil {
					if !untilFailure {
						return nil
					}
				} else {
					if retryCannotContinue(s, err) {
						return err
					}
					if untilFailure {
						s.Logf("[retry] block failed as requested after %d retries\n", retries)
						return nil
					}
				}

				if maxRetries > 0 && retries >= maxRetries {
					if untilFailure {
						return fmt.Errorf("block did not fail after %d retries", retries)
					}
					return err
				}

				delay := interval
				if delay == 0 {
					delay = backoff.get()
				}
				if delay > 0 {
					s.Logf("[retry] running block again in %s\n", delay)
					timer := time.NewTimer(delay)
					select {
					case <-s.Context().Done():
						timer.Stop()
						return s.Context().Err()
					case <-timer.C:
					}
				}
				retries++
			}
		},
	)
}

func retryCannotContinue(s *State, err error) bool {
	if s.Context().Err() != nil || errors.Is(err, ParseError) {
		return true
	}
	var stop stopError
	return errors.As(err, &stop)
}
