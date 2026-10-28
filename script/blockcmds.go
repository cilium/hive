// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/pflag"
)

// DefaultBlockCmds returns the block commands included in a basic script
// engine.
func DefaultBlockCmds() map[string]BlockCmd {
	return map[string]BlockCmd{
		"retry": Retry(),
	}
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
