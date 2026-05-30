package platform

import (
	"context"
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
	cmd, args, err := BuildCommand(context.Background(), testCommand)
	if err != nil {
		t.Fatalf("BuildCommand() returned error: %v", err)
	}
	if cmd == "" {
		t.Fatal("BuildCommand() returned empty command name")
	}
	if len(args) == 0 {
		t.Fatal("BuildCommand() returned empty args")
	}
}