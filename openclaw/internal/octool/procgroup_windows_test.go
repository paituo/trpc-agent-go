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
	"os/exec"
	"testing"
	"time"
)

func TestJobObject_CreateAndClose(t *testing.T) {
	j, err := newJobObject()
	if err != nil {
		t.Fatalf("newJobObject() failed: %v", err)
	}
	if err := j.close(); err != nil {
		t.Fatalf("close() failed: %v", err)
	}
}

func TestJobObject_AssignProcess(t *testing.T) {
	j, err := newJobObject()
	if err != nil {
		t.Fatalf("newJobObject() failed: %v", err)
	}
	defer func() { _ = j.close() }()

	cmd := exec.Command("cmd.exe", "/d", "/c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start cmd.exe: %v", err)
	}

	if err := j.assignProcess(cmd.Process); err != nil {
		t.Fatalf("assignProcess() failed: %v", err)
	}

	cmd.Wait() // Process already terminated, but it was assigned correctly.
}

func TestJobObject_KillOnClose(t *testing.T) {
	j, err := newJobObject()
	if err != nil {
		t.Fatalf("newJobObject() failed: %v", err)
	}
	if err := j.enableKillOnClose(); err != nil {
		t.Fatalf("enableKillOnClose() failed: %v", err)
	}

	// Start a long-running process (timeout command)
	cmd := exec.Command("cmd.exe", "/d", "/c", "timeout /t 60 /nobreak")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start cmd.exe: %v", err)
	}

	if err := j.assignProcess(cmd.Process); err != nil {
		t.Fatalf("assignProcess() failed: %v", err)
	}

	// Also start a child process to verify tree termination
	var childCmd *exec.Cmd
	childCmd = exec.Command("cmd.exe", "/d", "/c", "timeout /t 60 /nobreak")
	if err := childCmd.Start(); err != nil {
		t.Logf("warning: cannot start child process for tree test: %v", err)
	} else {
		if err := j.assignProcess(childCmd.Process); err != nil {
			t.Logf("warning: cannot assign child process: %v", err)
		}
	}

	// Close the job object → should kill all processes
	start := time.Now()
	if err := j.close(); err != nil {
		t.Fatalf("close() failed: %v", err)
	}

	// Verify the main process terminated quickly
	err = cmd.Wait()
	elapsed := time.Since(start)
	t.Logf("process terminated in %v, wait error: %v", elapsed, err)

	if elapsed > 5*time.Second {
		t.Errorf("process took too long to terminate after job close: %v", elapsed)
	}

	// Verify child also terminated
	if childCmd != nil && childCmd.Process != nil {
		childDead := make(chan error, 1)
		go func() {
			childDead <- childCmd.Wait()
		}()
		select {
		case <-childDead:
			t.Log("child process also terminated")
		case <-time.After(5 * time.Second):
			t.Log("warning: child process may still be running")
		}
	}
}

func TestJobObject_DoubleClose(t *testing.T) {
	j, err := newJobObject()
	if err != nil {
		t.Fatalf("newJobObject() failed: %v", err)
	}
	_ = j.enableKillOnClose()

	cmd := exec.Command("cmd.exe", "/d", "/c", "exit 0")
	_ = cmd.Start()
	_ = j.assignProcess(cmd.Process)
	_ = cmd.Wait()

	// First close should succeed.
	if err := j.close(); err != nil {
		t.Fatalf("first close() failed: %v", err)
	}
	// Second close should be a no-op.
	if err := j.close(); err != nil {
		t.Errorf("second close() should be no-op, got: %v", err)
	}
}