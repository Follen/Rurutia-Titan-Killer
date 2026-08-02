package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	gameImageName         = "WowClassic.exe"
	gamePathMarker        = `\_classic_titan_\`
	stillActive           = uint32(259)
	processStatusRunning  = "running"
	processStatusExiting  = "exiting"
	processStatusResidual = "residual"
)

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	terminateThread      = kernel32.NewProc("TerminateThread")
	getProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

type processIdentity struct {
	PID          uint32
	CreationTime int64
	ThreadID     uint32
	ExitCode     uint32
	Path         string
}

type inspectedProcess struct {
	ProcessView
	identity processIdentity
}

func inspectGameProcesses() ([]inspectedProcess, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("创建进程快照失败: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("读取进程快照失败: %w", err)
	}

	result := make([]inspectedProcess, 0)
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name, gameImageName) {
			if process, ok := inspectProcess(entry.ProcessID, entry.Threads); ok {
				result = append(result, process)
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err == syscall.ERROR_NO_MORE_FILES {
				break
			}
			return nil, fmt.Errorf("遍历进程快照失败: %w", err)
		}
	}
	return result, nil
}

func inspectProcess(pid, threadCount uint32) (inspectedProcess, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return inspectedProcess{}, false
	}
	defer windows.CloseHandle(handle)

	path, err := processPath(handle)
	if err != nil || !isTargetPath(path) {
		return inspectedProcess{}, false
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return inspectedProcess{}, false
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return inspectedProcess{}, false
	}
	memoryMB := workingSetMB(pid)

	status, statusLabel := classifyProcess(exitCode, threadCount)
	threadID := uint32(0)
	if status == processStatusResidual {
		threadID, _ = soleThreadID(pid)
	}

	return inspectedProcess{
		ProcessView: ProcessView{
			PID:         pid,
			Threads:     threadCount,
			Status:      status,
			StatusLabel: statusLabel,
			Path:        path,
			MemoryMB:    memoryMB,
		},
		identity: processIdentity{
			PID:          pid,
			CreationTime: created.Nanoseconds(),
			ThreadID:     threadID,
			ExitCode:     exitCode,
			Path:         path,
		},
	}, true
}

func classifyProcess(exitCode, threadCount uint32) (string, string) {
	if exitCode == stillActive {
		return processStatusRunning, "正常运行"
	}
	if threadCount == 1 {
		return processStatusResidual, "退出残留"
	}
	return processStatusExiting, "正在退出"
}

func workingSetMB(pid uint32) uint64 {
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ,
		false,
		pid,
	)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(handle)

	counters := processMemoryCounters{Size: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	result, _, _ := getProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.Size),
	)
	if result == 0 {
		return 0
	}
	return uint64(counters.WorkingSetSize) / (1024 * 1024)
}

func processPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func isTargetPath(path string) bool {
	return strings.EqualFold(filepath.Base(path), gameImageName) &&
		strings.Contains(strings.ToLower(path), strings.ToLower(gamePathMarker))
}

func soleThreadID(pid uint32) (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return 0, err
	}
	var found uint32
	count := 0
	for {
		if entry.OwnerProcessID == pid {
			found = entry.ThreadID
			count++
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if err == syscall.ERROR_NO_MORE_FILES {
				break
			}
			return 0, err
		}
	}
	if count != 1 {
		return 0, fmt.Errorf("PID %d 复核时存在 %d 个线程", pid, count)
	}
	return found, nil
}

func terminateResidual(expected processIdentity) (bool, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, expected.PID)
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return false, nil
		}
		return false, fmt.Errorf("PID %d 无法打开残留进程: %w", expected.PID, err)
	}
	var exitCode uint32
	var created, exited, kernel, user windows.Filetime
	path, pathErr := processPath(process)
	exitErr := windows.GetExitCodeProcess(process, &exitCode)
	timesErr := windows.GetProcessTimes(process, &created, &exited, &kernel, &user)
	windows.CloseHandle(process)
	if pathErr != nil || exitErr != nil || timesErr != nil {
		return false, fmt.Errorf("PID %d 复核失败", expected.PID)
	}
	if exitCode == stillActive || created.Nanoseconds() != expected.CreationTime || !isTargetPath(path) {
		return false, nil
	}

	threadID, err := soleThreadID(expected.PID)
	if err != nil {
		return false, err
	}
	thread, err := windows.OpenThread(windows.THREAD_TERMINATE|windows.THREAD_QUERY_LIMITED_INFORMATION, false, threadID)
	if err != nil {
		return false, fmt.Errorf("PID %d 无法打开残留线程: %w", expected.PID, err)
	}
	defer windows.CloseHandle(thread)

	result, _, callErr := terminateThread.Call(uintptr(thread), uintptr(exitCode))
	if result == 0 {
		if callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return false, fmt.Errorf("PID %d 清理失败: %w", expected.PID, callErr)
	}
	return true, nil
}

func formatCleanupAction(processes []inspectedProcess) string {
	parts := make([]string, len(processes))
	for i, process := range processes {
		parts[i] = fmt.Sprintf("%d", process.PID)
	}
	return "已清理 PID " + strings.Join(parts, ", ")
}
