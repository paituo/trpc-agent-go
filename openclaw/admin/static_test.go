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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterStaticMounts(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<html>ok</html>"), 0644)
	require.NoError(t, err)

	mux := http.NewServeMux()
	registerStaticMounts(mux, []StaticMount{
		{Path: "/test/", Dir: tmpDir},
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	// Test root of the mounted path
	resp, err := http.Get(server.URL + "/test/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Test a file within the mounted path
	resp2, err := http.Get(server.URL + "/test/index.html")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	// Test the handler is NOT registered on root
	resp3, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp3.Body.Close()
	require.Equal(t, http.StatusNotFound, resp3.StatusCode)
}

func TestServiceHandlerWithStaticMount(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<html>served via admin</html>"), 0644)
	require.NoError(t, err)

	svc := New(Config{
		AppName:     "test-app",
		InstanceID:  "test-instance",
		GatewayAddr: "127.0.0.1:8080",
		AdminAddr:   "127.0.0.1:18789",
		StaticMounts: []StaticMount{
			{Path: "/static-app/", Dir: tmpDir},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/static-app/", nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "static mount via Service.Handler() should return 200")

	// Verify the actual content
	require.Contains(t, rr.Body.String(), "served via admin")
}