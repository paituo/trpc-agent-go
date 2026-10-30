//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package source

import (
	"net/url"
	"path/filepath"
	"strings"
)

// FileURI converts an absolute filesystem path into a file:// URL that
// round-trips through url.Parse on every platform. Windows drive paths
// need a leading slash before the volume name ("file:///C:/dir/file");
// url.URL{Scheme: "file", Path: "C:\\dir"} instead produces an opaque
// URL that url.Parse rejects.
func FileURI(absPath string) string {
	p := filepath.ToSlash(absPath)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
