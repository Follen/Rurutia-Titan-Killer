package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCleanupMessageRoundTrip(t *testing.T) {
	want := cleanupRequest{
		Version: cleanupProtocolVersion,
		Identity: processIdentity{
			PID:          33988,
			CreationTime: 123456789,
			ThreadCount:  1,
			ThreadID:     42,
			ExitCode:     0,
			Path:         `D:\Game\World of Warcraft\_classic_titan_\WowClassic.exe`,
		},
	}
	var wire bytes.Buffer
	if err := writeCleanupMessage(&wire, want); err != nil {
		t.Fatal(err)
	}
	var got cleanupRequest
	if err := readCleanupMessage(&wire, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestCleanupMessageRejectsUnknownAndTrailingJSON(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"version":1,"identity":{},"unknown":true}`),
		[]byte(`{"version":1,"identity":{}} {}`),
	} {
		var wire bytes.Buffer
		_ = binary.Write(&wire, binary.LittleEndian, uint32(len(payload)))
		_, _ = wire.Write(payload)
		var request cleanupRequest
		if err := readCleanupMessage(&wire, &request); err == nil {
			t.Fatalf("payload %q was accepted", payload)
		}
	}
}

func TestCleanupMessageRejectsOversize(t *testing.T) {
	var wire bytes.Buffer
	_ = binary.Write(&wire, binary.LittleEndian, uint32(maxCleanupMessageSize+1))
	var request cleanupRequest
	if err := readCleanupMessage(&wire, &request); err == nil {
		t.Fatal("oversized message was accepted")
	}
}

func TestCleanupConnectionCallsValidatedServiceTermination(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	want := processIdentity{PID: 33988, CreationTime: 987654321, ThreadCount: 1, ThreadID: 7, ExitCode: 0, Path: `D:\Game\_classic_titan_\WowClassic.exe`}
	called := make(chan struct{})
	go func() {
		serveCleanupConnection(server, func(got processIdentity, component string) (bool, error) {
			defer close(called)
			if got != want {
				t.Errorf("identity = %#v, want %#v", got, want)
			}
			if component != "service" {
				t.Errorf("component = %q, want service", component)
			}
			return true, nil
		})
		_ = server.Close()
	}()

	if err := writeCleanupMessage(client, cleanupRequest{Version: cleanupProtocolVersion, Identity: want}); err != nil {
		t.Fatal(err)
	}
	var response cleanupResponse
	if err := readCleanupMessage(client, &response); err != nil {
		t.Fatal(err)
	}
	<-called
	if !response.Terminated || response.Error != "" || response.Win32Error != 0 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestCleanupConnectionPreservesWin32Error(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		serveCleanupConnection(server, func(processIdentity, string) (bool, error) {
			return false, errors.Join(errors.New("denied"), windows.ERROR_ACCESS_DENIED)
		})
		_ = server.Close()
	}()
	if err := writeCleanupMessage(client, cleanupRequest{Version: cleanupProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	var response cleanupResponse
	if err := readCleanupMessage(client, &response); err != nil {
		t.Fatal(err)
	}
	if response.Win32Error != uint32(windows.ERROR_ACCESS_DENIED) || !strings.Contains(response.Error, "denied") {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestIntegrityLabel(t *testing.T) {
	tests := map[uint32]string{
		0x0000: "untrusted(0x0)",
		0x1000: "low(0x1000)",
		0x2000: "medium(0x2000)",
		0x3000: "high(0x3000)",
		0x4000: "system(0x4000)",
		0x5000: "protected(0x5000)",
	}
	for rid, want := range tests {
		if got := integrityLabel(rid); got != want {
			t.Errorf("integrityLabel(%#x) = %q, want %q", rid, got, want)
		}
	}
}
