//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"io"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func tryDecodeBytes(data []byte) []byte {
	for _, codec := range []encoding.Encoding{
		charmap.CodePage437,
		charmap.CodePage850,
		charmap.CodePage852,
		charmap.CodePage866,
		charmap.CodePage1250,
		charmap.CodePage1251,
		charmap.CodePage1252,
		charmap.CodePage1253,
		charmap.CodePage1254,
		charmap.CodePage1255,
		charmap.CodePage1256,
		charmap.CodePage1257,
		charmap.CodePage1258,
		unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM),
		unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM),
	} {
		decoded, err := io.ReadAll(
			transform.NewReader(
				strings.NewReader(string(data)),
				codec.NewDecoder(),
			),
		)
		if err == nil && len(decoded) > 0 {
			return decoded
		}
	}

	return data
}