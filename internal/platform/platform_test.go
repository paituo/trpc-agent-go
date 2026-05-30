package platform

import (
	"testing"
)

func TestShell_NonEmpty(t *testing.T) {
	s := Shell()
	if s == "" {
		t.Fatal("Shell() returned empty string")
	}
}

func TestBuildCommand_ReturnsNameAndArgs(t *testing.T) {
	name, args := BuildCommand("-c echo hello")
	if name == "" {
		t.Fatal("BuildCommand returned empty name")
	}
	if len(args) == 0 {
		t.Fatal("BuildCommand returned empty args")
	}
}