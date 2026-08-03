package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	integrityUntrustedRID = 0x0000
	integrityLowRID       = 0x1000
	integrityMediumRID    = 0x2000
	integrityHighRID      = 0x3000
	integritySystemRID    = 0x4000
	integrityProtectedRID = 0x5000
)

type cleanupDiagnostic struct {
	Timestamp       string `json:"timestamp"`
	Component       string `json:"component"`
	Action          string `json:"action"`
	PID             uint32 `json:"pid,omitempty"`
	CreationTime    int64  `json:"creationTime,omitempty"`
	ThreadCount     uint32 `json:"threadCount,omitempty"`
	ThreadID        uint32 `json:"threadId,omitempty"`
	ExitCode        uint32 `json:"exitCode"`
	Path            string `json:"path,omitempty"`
	CallerIntegrity string `json:"callerIntegrity"`
	TargetIntegrity string `json:"targetIntegrity,omitempty"`
	Succeeded       bool   `json:"succeeded"`
	Win32Error      uint32 `json:"win32Error"`
	Error           string `json:"error,omitempty"`
}

func diagnosticsPath() string {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "Follen", "lychee-titan-cleanner", "cleanup.log")
}

func processIntegrityLevel(process windows.Handle) (string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()

	var required uint32
	err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &required)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", err
	}
	if required == 0 {
		return "", fmt.Errorf("TokenIntegrityLevel 返回空数据")
	}

	buffer := make([]byte, required)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buffer[0], required, &required); err != nil {
		return "", err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
	sid := label.Label.Sid
	count := sid.SubAuthorityCount()
	if count == 0 {
		return "", fmt.Errorf("完整性 SID 没有子授权")
	}
	rid := sid.SubAuthority(uint32(count - 1))
	return integrityLabel(rid), nil
}

func integrityLabel(rid uint32) string {
	switch {
	case rid < integrityLowRID:
		return fmt.Sprintf("untrusted(0x%X)", rid)
	case rid < integrityMediumRID:
		return fmt.Sprintf("low(0x%X)", rid)
	case rid < integrityHighRID:
		return fmt.Sprintf("medium(0x%X)", rid)
	case rid < integritySystemRID:
		return fmt.Sprintf("high(0x%X)", rid)
	case rid < integrityProtectedRID:
		return fmt.Sprintf("system(0x%X)", rid)
	default:
		return fmt.Sprintf("protected(0x%X)", rid)
	}
}

func currentIntegrityLevel() string {
	level, err := processIntegrityLevel(windows.CurrentProcess())
	if err != nil {
		return "unknown: " + err.Error()
	}
	return level
}

func rawWin32Error(err error) uint32 {
	if err == nil {
		return 0
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return uint32(errno)
	}
	return 0
}

func recordPrivilegeDiagnostic(component string, err error) {
	recordDiagnostic(cleanupDiagnostic{
		Component:  component,
		Action:     "privilege.enable.SeDebugPrivilege",
		Succeeded:  err == nil,
		Win32Error: rawWin32Error(err),
		Error:      errorText(err),
	})
}

func recordCleanupDiagnostic(component string, expected processIdentity, action, targetIntegrity string, err error) {
	recordDiagnostic(cleanupDiagnostic{
		Component:       component,
		Action:          action,
		PID:             expected.PID,
		CreationTime:    expected.CreationTime,
		ThreadCount:     expected.ThreadCount,
		ThreadID:        expected.ThreadID,
		ExitCode:        expected.ExitCode,
		Path:            expected.Path,
		TargetIntegrity: targetIntegrity,
		Succeeded:       err == nil,
		Win32Error:      rawWin32Error(err),
		Error:           errorText(err),
	})
}

func recordServiceDiagnostic(action string, err error) {
	recordDiagnostic(cleanupDiagnostic{
		Component:  "service",
		Action:     action,
		Succeeded:  err == nil,
		Win32Error: rawWin32Error(err),
		Error:      errorText(err),
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func recordDiagnostic(entry cleanupDiagnostic) {
	entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	entry.CallerIntegrity = currentIntegrityLevel()
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')

	path := diagnosticsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	_, _ = file.Write(data)
	_ = file.Close()
}
