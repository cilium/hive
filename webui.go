// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
)

//go:embed webui/*
var webuiFS embed.FS

func (h *Hive) webUIHandler(log *slog.Logger) (http.Handler, error) {
	sub, err := fs.Sub(webuiFS, "webui")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/graph.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := h.PrintGraphJSON(w, log); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux, nil
}
