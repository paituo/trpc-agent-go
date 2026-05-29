//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package platform

import (
	"reflect"
	"testing"
)

func TestShell_ReturnsValidSpec(t *testing.T) {
	spec, err := Shell()
	if err != nil {
		t.Fatalf("Shell() returned error: %v", err)
	}
	if spec.Command == "" {
		t.Fatal("Shell() returned empty Command")
	}
	if len(spec.Args) == 0 {
		t.Fatal("Shell() returned empty Args")
	}
	t.Logf("Shell detected: %s %v", spec.Command, spec.Args)
}

func TestBuildCommand_ReturnsValidCommand(t *testing.T) {
	const testCommand = "echo hello"
	cmd, args, err := BuildCommand(nil, testCommand)
	if err != nil {
		t.Fatalf("BuildCommand() returned error: %v", err)
	}
	if cmd == "" {
		t.Fatal("BuildCommand() returned empty cmd")
	}
	if len(args) < 2 {
		t.Fatalf("BuildCommand() returned too few args: %v", args)
	}
	if args[len(args)-1] != testCommand {
		t.Errorf("BuildCommand() last arg = %q, want %q (user command should be the final argument)", args[len(args)-1], testCommand)
	}
	t.Logf("BuildCommand: %s %v", cmd, args)
}

func TestShell_ConsistentResults(t *testing.T) {
	spec1, err1 := Shell()
	spec2, err2 := Shell()
	if (err1 != nil) != (err2 != nil) {
		t.Fatal("Shell() error state inconsistent across calls")
	}
	if err1 == nil && !reflect.DeepEqual(spec1, spec2) {
		t.Fatalf("Shell() returned different results: %v vs %v", spec1, spec2)
	}
}