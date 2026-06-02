//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"sync"
	"syscall"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var (
	kernel32GetConsoleOutputCP = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleOutputCP")
	kernel32GetACP             = syscall.NewLazyDLL("kernel32.dll").NewProc("GetACP")

	cachedEncoding     encoding.Encoding
	cachedEncodingOnce sync.Once
)

// getConsoleEncoding returns the encoding for the current console output.
// Result is cached via sync.Once — code page doesn't change during process lifetime.
// On Chinese Windows this is typically GBK (code page 936).
func getConsoleEncoding() encoding.Encoding {
	cachedEncodingOnce.Do(func() {
		cachedEncoding = resolveConsoleEncoding()
	})
	return cachedEncoding
}

func resolveConsoleEncoding() encoding.Encoding {
	cp := getConsoleCodePage()
	switch cp {
	case 65001: // UTF-8 — no conversion needed
		return encoding.Nop
	case 936: // GBK / GB2312 — Simplified Chinese
		return simplifiedchinese.GBK
	case 950: // Big5 — Traditional Chinese
		// For now, Big5 falls through to encoding.Nop.
		// Traditional Chinese support can be added later.
		return encoding.Nop
	case 1200: // UTF-16 LE
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	default:
		return encoding.Nop // Unknown code page, read as UTF-8
	}
}

func getConsoleCodePage() uint32 {
	// Try console output code page first
	ret, _, _ := kernel32GetConsoleOutputCP.Call()
	if ret == 0 {
		// Fall back to system ANSI code page (more reliable for subprocesses)
		ret, _, _ = kernel32GetACP.Call()
	}
	if ret == 0 {
		return 65001 // Default to UTF-8
	}
	return uint32(ret)
}

// decodeConsoleOutput converts console-encoded bytes to UTF-8.
func decodeConsoleOutput(input []byte) string {
	enc := getConsoleEncoding()
	if enc == encoding.Nop {
		return string(input)
	}
	decoder := enc.NewDecoder()
	result, _, err := transform.Bytes(decoder, input)
	if err != nil {
		return string(input) // Keep original bytes on failure
	}
	return string(result)
}