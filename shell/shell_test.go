// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package shell

import (
	"context"
	"flag"
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
}

func TestInteractiveShell(t *testing.T) {
	sock := path.Join(t.TempDir(), "shell.sock")
	cfg := Config{sock}
	fixture(t, cfg)

	// Interactive use
	cmd := exec.Command(os.Args[0], "-client", sock)
	cmd.Stdin = strings.NewReader("help help\r\nexit\r\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "CombinedOutput")

	require.Contains(t, string(out), "test> help help")
	require.Contains(t, string(out), "log help text")

	// Non-interactive with failure
	cmd = exec.Command(os.Args[0], "-client", sock, "unknown")
	out, err = cmd.CombinedOutput()
	require.Contains(t, string(out), "unknown: unknown command")
	require.ErrorContains(t, err, "exit status 1")
}
