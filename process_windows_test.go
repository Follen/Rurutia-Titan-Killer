package main

import "testing"

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
