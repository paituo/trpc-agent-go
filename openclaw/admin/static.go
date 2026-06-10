//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	// Register common web file MIME types to ensure they are served with the
	// correct Content-Type header. Go's mime.TypeByExtension relies on the
	// OS registry on Windows, which may not have these mappings configured.
	addMimeType(".css", "text/css")
	addMimeType(".js", "text/javascript")
	addMimeType(".mjs", "text/javascript")
	addMimeType(".jsx", "text/javascript")
	addMimeType(".ts", "text/javascript")
	addMimeType(".tsx", "text/javascript")
	addMimeType(".json", "application/json")
	addMimeType(".svg", "image/svg+xml")
	addMimeType(".woff", "font/woff")
	addMimeType(".woff2", "font/woff2")
	addMimeType(".wasm", "application/wasm")
}

func addMimeType(ext, typ string) {
	if mime.TypeByExtension(ext) == "" {
		mime.AddExtensionType(ext, typ)
	}
}

// StaticMount maps a local directory to a URL path prefix.
// When StaticMount is registered in Config, the admin service
// serves files from Dir at the given Path.
type StaticMount struct {
	// Path is the URL path prefix, e.g. "/chat/".
	// Must end with "/" to match as a subtree pattern.
	Path string `json:"path" yaml:"path"`
	// Dir is the local file system directory, e.g. "./dist".
	Dir string `json:"dir" yaml:"dir"`
}

// registerStaticMounts adds a http.FileServer handler to the mux
// for each StaticMount entry.
func registerStaticMounts(mux *http.ServeMux, mounts []StaticMount) {
	for _, m := range mounts {
		path := m.Path
		if path == "" {
			path = "/"
		}
		if !strings.HasSuffix(path, "/") {
			path += "/"
		}
		absDir, err := filepath.Abs(m.Dir)
		if err == nil {
			fmt.Fprintf(os.Stderr, "[admin] static mount: %s -> %s\n", path, absDir)
		}
		handler := http.StripPrefix(
			strings.TrimSuffix(path, "/"),
			http.FileServer(http.Dir(m.Dir)),
		)
		mux.Handle(path, handler)
	}
}