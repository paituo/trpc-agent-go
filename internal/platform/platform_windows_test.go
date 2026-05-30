//go:build windows

package platform

import (
	"context"
	"strings"
	"testing"
)

func TestShell_Windows(t *testing.T) {
	spec, err := Shell()
	if err != nil {
		t.Fatalf("Shell() returned error: %v", err)
	}
	if spec.Command == "" {
		t.Fatal("Shell() returned empty command")
	}
	if !strings.Contains(strings.ToLower(spec.Command), "powershell") && !strings.Contains(strings.ToLower(spec.Command), "cmd") {
		t.Fatalf("unexpected shell on Windows: %s", spec.Command)
	}
}

func TestBuildCommand_Windows_PowerShell(t *testing.T) {
	name, args, err := BuildCommand(context.Background(), "Write-Host hello")
	if err != nil {
		t.Fatalf("BuildCommand returned error: %v", err)
	}
	if name == "" {
		t.Fatal("BuildCommand returned empty name")
	}
	if strings.Contains(strings.ToLower(name), "powershell") {
		found := false
		for _, a := range args {
			if a == "-NoProfile" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected -NoProfile flag for PowerShell")
		}
	}
}