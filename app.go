package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getlantern/systray"
)

const (
	guardianScanInterval = 15 * time.Second
	maxHistoryEntries    = 100
)

type ProcessView struct {
	PID         uint32 `json:"pid"`
	Threads     uint32 `json:"threads"`
	MemoryMB    uint64 `json:"memoryMB"`
	Status      string `json:"status"`
	StatusLabel string `json:"statusLabel"`
	Path        string `json:"path"`
}

type CleanupRecord struct {
	ID        string `json:"id"`
	PID       uint32 `json:"pid"`
	Threads   uint32 `json:"threads"`
	MemoryMB  uint64 `json:"memoryMB"`
	CleanedAt string `json:"cleanedAt"`
}

type GuardianStatus struct {
	Enabled      bool            `json:"enabled"`
	State        string          `json:"state"`
	StateLabel   string          `json:"stateLabel"`
	LastScan     string          `json:"lastScan"`
	LastAction   string          `json:"lastAction"`
	LastError    string          `json:"lastError"`
	TotalKilled  int             `json:"totalKilled"`
	ScanInterval int             `json:"scanInterval"`
	Processes    []ProcessView   `json:"processes"`
	History      []CleanupRecord `json:"history"`
}

type settings struct {
	Enabled     bool            `json:"enabled"`
	TotalKilled int             `json:"totalKilled"`
	History     []CleanupRecord `json:"history"`
}

type App struct {
	ctx context.Context

	mu          sync.RWMutex
	scanMu      sync.Mutex
	enabled     bool
	cancel      context.CancelFunc
	lastScan    time.Time
	lastAction  string
	lastError   string
	totalKilled int
	processes   []ProcessView
	history     []CleanupRecord
	quitting    atomic.Bool
	trayStarted atomic.Bool
}

func NewApp() *App {
	return &App{
		processes: make([]ProcessView, 0),
		history:   make([]CleanupRecord, 0),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startTray()
	stored := loadSettings()
	a.enabled = stored.Enabled
	a.totalKilled = stored.TotalKilled
	a.history = append([]CleanupRecord(nil), stored.History...)
	if a.enabled {
		a.startGuardian()
	} else {
		go a.scan(false)
	}
}

func (a *App) shutdown(context.Context) {
	if a.trayStarted.Load() {
		systray.Quit()
	}
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.mu.Unlock()
}

func (a *App) GetStatus() GuardianStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.statusLocked()
}

func (a *App) SetGuardianEnabled(enabled bool) GuardianStatus {
	a.mu.Lock()
	changed := a.enabled != enabled
	a.enabled = enabled
	if !enabled && a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.mu.Unlock()

	if changed {
		a.persistSettings()
	}
	if enabled && changed {
		a.startGuardian()
	} else if !enabled {
		go a.scan(false)
	}

	return a.GetStatus()
}

func (a *App) ClearHistory() GuardianStatus {
	a.mu.Lock()
	a.history = make([]CleanupRecord, 0)
	a.mu.Unlock()
	a.persistSettings()
	return a.GetStatus()
}

func (a *App) startGuardian() {
	a.mu.Lock()
	if a.cancel != nil {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.mu.Unlock()

	go func() {
		a.scan(true)
		ticker := time.NewTicker(guardianScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.scan(true)
			}
		}
	}()
}

func (a *App) scan(clean bool) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()

	processes, err := inspectGameProcesses()
	if err != nil {
		a.mu.Lock()
		a.lastScan = time.Now()
		a.lastError = err.Error()
		a.mu.Unlock()
		return
	}

	attempted := make([]inspectedProcess, 0)
	var cleanupErrors []error
	if clean {
		for _, process := range processes {
			if process.Status != processStatusResidual {
				continue
			}
			if err := terminateResidual(process.identity); err != nil {
				cleanupErrors = append(cleanupErrors, err)
				continue
			}
			attempted = append(attempted, process)
		}
	}

	killed := make([]inspectedProcess, 0, len(attempted))
	if len(attempted) > 0 {
		time.Sleep(250 * time.Millisecond)
		refreshed, refreshErr := inspectGameProcesses()
		if refreshErr != nil {
			cleanupErrors = append(cleanupErrors, refreshErr)
		} else {
			processes = refreshed
			for _, candidate := range attempted {
				if containsProcess(refreshed, candidate.identity) {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("PID %d 仍在系统中，将在下次扫描重试", candidate.PID))
					continue
				}
				killed = append(killed, candidate)
			}
		}
	}

	views := make([]ProcessView, 0, len(processes))
	for _, process := range processes {
		views = append(views, process.ProcessView)
	}

	a.mu.Lock()
	a.lastScan = time.Now()
	a.processes = views
	a.lastError = ""
	if len(cleanupErrors) > 0 {
		a.lastError = errors.Join(cleanupErrors...).Error()
	}
	if len(killed) > 0 {
		now := time.Now()
		a.totalKilled += len(killed)
		a.lastAction = formatCleanupAction(killed)
		for index, process := range killed {
			record := CleanupRecord{
				ID:        fmt.Sprintf("%d-%d", now.UnixNano()+int64(index), process.PID),
				PID:       process.PID,
				Threads:   process.Threads,
				MemoryMB:  process.MemoryMB,
				CleanedAt: now.Format("2006-01-02 15:04:05"),
			}
			a.history = append([]CleanupRecord{record}, a.history...)
		}
		if len(a.history) > maxHistoryEntries {
			a.history = a.history[:maxHistoryEntries]
		}
	}
	a.mu.Unlock()

	if len(killed) > 0 {
		a.persistSettings()
	}
}

func containsProcess(processes []inspectedProcess, expected processIdentity) bool {
	for _, process := range processes {
		if process.PID == expected.PID && process.identity.CreationTime == expected.CreationTime {
			return true
		}
	}
	return false
}

func (a *App) statusLocked() GuardianStatus {
	state := "off"
	label := "守护已关闭"
	if a.enabled {
		state = "watching"
		label = "守护运行中"
	}
	lastScan := "尚未扫描"
	if !a.lastScan.IsZero() {
		lastScan = a.lastScan.Format("15:04:05")
	}
	return GuardianStatus{
		Enabled:      a.enabled,
		State:        state,
		StateLabel:   label,
		LastScan:     lastScan,
		LastAction:   a.lastAction,
		LastError:    a.lastError,
		TotalKilled:  a.totalKilled,
		ScanInterval: int(guardianScanInterval / time.Second),
		Processes:    append([]ProcessView(nil), a.processes...),
		History:      append([]CleanupRecord(nil), a.history...),
	}
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "LuluWoWResidualGuard")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func loadSettings() settings {
	path, err := settingsPath()
	if err != nil {
		return settings{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return settings{}
	}
	var value settings
	if json.Unmarshal(data, &value) != nil {
		return settings{}
	}
	return value
}

func (a *App) persistSettings() {
	a.mu.RLock()
	value := settings{
		Enabled:     a.enabled,
		TotalKilled: a.totalKilled,
		History:     append([]CleanupRecord(nil), a.history...),
	}
	a.mu.RUnlock()

	path, err := settingsPath()
	if err != nil {
		return
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}
