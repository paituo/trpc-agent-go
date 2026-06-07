//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package luaexec

import "os"

// readFileBytes reads a file and returns its raw bytes.
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// writeFileBytes writes data to a file.
func writeFileBytes(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
