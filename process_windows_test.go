package main

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestClassifyProcess(t *testing.T) {
	tests := []struct {
		name        string
		exitCode    uint32
		threadCount uint32
		wantStatus  string
	}{
		{name: "active game", exitCode: stillActive, threadCount: 98, wantStatus: processStatusRunning},
		{name: "active startup", exitCode: stillActive, threadCount: 1, wantStatus: processStatusRunning},
		{name: "exiting", exitCode: 0, threadCount: 4, wantStatus: processStatusExiting},
		{name: "residual", exitCode: 0, threadCount: 1, wantStatus: processStatusResidual},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, _ := classifyProcess(test.exitCode, test.threadCount)
			if status != test.wantStatus {
				t.Fatalf("classifyProcess(%d, %d) = %q, want %q", test.exitCode, test.threadCount, status, test.wantStatus)
			}
		})
	}
}

func TestIsTargetPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: `D:\Game\World of Warcraft\_classic_titan_\WowClassic.exe`, want: true},
		{path: `d:\game\world of warcraft\_CLASSIC_TITAN_\WOWCLASSIC.EXE`, want: true},
		{path: `D:\Game\World of Warcraft\_classic_\WowClassic.exe`, want: false},
		{path: `D:\Game\World of Warcraft\_classic_titan_\Wow.exe`, want: false},
	}

	for _, test := range tests {
		if got := isTargetPath(test.path); got != test.want {
			t.Errorf("isTargetPath(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestOpenThreadForTerminationEnablesDebugPrivilegeAfterAccessDenied(t *testing.T) {
	originalOpenThread := openThread
	originalEnableDebugPrivilege := enableDebugPrivilegeForTermination
	originalRecordPrivilege := recordPrivilegeForTermination
	t.Cleanup(func() {
		openThread = originalOpenThread
		enableDebugPrivilegeForTermination = originalEnableDebugPrivilege
		recordPrivilegeForTermination = originalRecordPrivilege
	})
	recordPrivilegeForTermination = func(string, error) {}

	attempts := 0
	openThread = func(desiredAccess uint32, inheritHandle bool, threadID uint32) (windows.Handle, error) {
		attempts++
		if desiredAccess != windows.THREAD_TERMINATE {
			t.Fatalf("desired access = %#x, want THREAD_TERMINATE", desiredAccess)
		}
		if inheritHandle {
			t.Fatal("thread handle should not be inheritable")
		}
		if threadID != 42 {
			t.Fatalf("thread ID = %d, want 42", threadID)
		}
		if attempts == 1 {
			return 0, windows.ERROR_ACCESS_DENIED
		}
		return windows.Handle(123), nil
	}
	privilegeEnabled := false
	enableDebugPrivilegeForTermination = func() error {
		privilegeEnabled = true
		return nil
	}

	handle, err := openThreadForTermination(42, "test")
	if err != nil {
		t.Fatalf("openThreadForTermination returned error: %v", err)
	}
	if handle != windows.Handle(123) {
		t.Fatalf("handle = %v, want 123", handle)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !privilegeEnabled {
		t.Fatal("debug privilege was not enabled")
	}
}

func TestOpenThreadForTerminationDoesNotRetryOtherErrors(t *testing.T) {
	originalOpenThread := openThread
	originalEnableDebugPrivilege := enableDebugPrivilegeForTermination
	originalRecordPrivilege := recordPrivilegeForTermination
	t.Cleanup(func() {
		openThread = originalOpenThread
		enableDebugPrivilegeForTermination = originalEnableDebugPrivilege
		recordPrivilegeForTermination = originalRecordPrivilege
	})
	recordPrivilegeForTermination = func(string, error) {}

	attempts := 0
	openThread = func(uint32, bool, uint32) (windows.Handle, error) {
		attempts++
		return 0, windows.ERROR_INVALID_PARAMETER
	}
	enableDebugPrivilegeForTermination = func() error {
		t.Fatal("debug privilege should not be enabled")
		return nil
	}

	_, err := openThreadForTermination(42, "test")
	if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		t.Fatalf("error = %v, want ERROR_INVALID_PARAMETER", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
