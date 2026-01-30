// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

// Command constructs the cobra command for hive. The hive
// command can be used to inspect the dependency graph.
func (h *Hive) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "hive",
		Short:        "Inspect the hive",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return h.PrintObjects(os.Stdout, slog.Default())
		},
		TraverseChildren: false,
	}
	h.RegisterFlags(cmd.PersistentFlags())

	cmd.AddCommand(
		&cobra.Command{
			Use:          "dot-graph",
			Short:        "Output the dependencies graph in graphviz dot format",
			SilenceUsage: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return h.PrintDotGraph()
			},
			TraverseChildren: false,
		})

	webUI := &cobra.Command{
		Use:          "web-ui",
		Short:        "Serve the dependency graph explorer UI",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, _ := cmd.Flags().GetString("listen")
			handler, err := h.webUIHandler(slog.Default())
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			srv := &http.Server{Handler: handler}
			go func() {
				<-cmd.Context().Done()
				_ = srv.Shutdown(context.Background())
			}()
			fmt.Fprintf(os.Stdout, "Web UI listening on http://%s\n", ln.Addr().String())
			return srv.Serve(ln)
		},
		TraverseChildren: false,
	}
	webUI.Flags().String("listen", "127.0.0.1:8080", "listen address for the web UI")
	cmd.AddCommand(webUI)

	return cmd
}
