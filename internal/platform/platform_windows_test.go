//go:build windows

package platform

import (
	"strings"
	"testing"
)

func TestShell_Windows(t *testing.T) {
	s := Shell()
	if s == "" {
		t.Fatal("Shell() returned empty string")
	}
	if !strings.Contains(strings.ToLower(s), "powershell") && s != "cmd" {
		t.Fatalf("unexpected shell on Windows: %s", s)
	}
}

func TestBuildCommand_Windows_PowerShell(t *testing.T) {
	name, args := BuildCommand("Write-Host hello")
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