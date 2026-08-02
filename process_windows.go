package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
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
	kernel32                           = windows.NewLazySystemDLL("kernel32.dll")
	terminateThread                    = kernel32.NewProc("TerminateThread")
	getProcessMemoryInfo               = kernel32.NewProc("K32GetProcessMemoryInfo")
	openThread                         = windows.OpenThread
	terminateProcess                   = windows.TerminateProcess
	enableDebugPrivilegeForTermination = enableDebugPrivilege
	recordPrivilegeForTermination      = recordPrivilegeDiagnostic
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
	PID          uint32 `json:"pid"`
	CreationTime int64  `json:"creationTime"`
	ThreadCount  uint32 `json:"threadCount"`
	ThreadID     uint32 `json:"threadId"`
	ExitCode     uint32 `json:"exitCode"`
	Path         string `json:"path"`
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
		threadID, err = soleThreadID(pid)
		if err != nil {
			return inspectedProcess{}, false
		}
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
			ThreadCount:  threadCount,
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

func enableDebugPrivilege() error {
	var token windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY,
		&token,
	); err != nil {
		return fmt.Errorf("打开当前进程令牌失败: %w", err)
	}
	defer token.Close()

	name, err := windows.UTF16PtrFromString("SeDebugPrivilege")
	if err != nil {
		return err
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return fmt.Errorf("查询 SeDebugPrivilege 失败: %w", err)
	}

	privileges := windows.Tokenprivileges{PrivilegeCount: 1}
	privileges.Privileges[0] = windows.LUIDAndAttributes{
		Luid:       luid,
		Attributes: windows.SE_PRIVILEGE_ENABLED,
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil); err != nil {
		return fmt.Errorf("启用 SeDebugPrivilege 失败: %w", err)
	}
	if err := windows.GetLastError(); err != nil {
		if errors.Is(err, windows.ERROR_NOT_ALL_ASSIGNED) {
			return fmt.Errorf("当前管理员令牌未分配 SeDebugPrivilege: %w", err)
		}
		return fmt.Errorf("启用 SeDebugPrivilege 后检查失败: %w", err)
	}
	return nil
}

func openThreadForTermination(threadID uint32, component string) (windows.Handle, error) {
	thread, err := openThread(windows.THREAD_TERMINATE, false, threadID)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return thread, err
	}
	privilegeErr := enableDebugPrivilegeForTermination()
	recordPrivilegeForTermination(component, privilegeErr)
	if privilegeErr != nil {
		return 0, fmt.Errorf("打开线程被拒绝，且调试权限启用失败: %v: %w", privilegeErr, windows.ERROR_ACCESS_DENIED)
	}
	return openThread(windows.THREAD_TERMINATE, false, threadID)
}

type validatedResidual struct {
	process         windows.Handle
	threadID        uint32
	exitCode        uint32
	targetIntegrity string
}

func openValidatedResidual(expected processIdentity, additionalAccess uint32) (validatedResidual, bool, error) {
	process, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|additionalAccess,
		false,
		expected.PID,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return validatedResidual{}, false, nil
		}
		return validatedResidual{}, false, fmt.Errorf("PID %d 打开残留进程失败: %w", expected.PID, err)
	}
	valid := false
	defer func() {
		if !valid {
			windows.CloseHandle(process)
		}
	}()

	var exitCode uint32
	var created, exited, kernel, user windows.Filetime
	path, pathErr := processPath(process)
	exitErr := windows.GetExitCodeProcess(process, &exitCode)
	timesErr := windows.GetProcessTimes(process, &created, &exited, &kernel, &user)
	if pathErr != nil || exitErr != nil || timesErr != nil {
		return validatedResidual{}, false, fmt.Errorf(
			"PID %d 进程复核失败: path=%v exit=%v times=%v",
			expected.PID,
			pathErr,
			exitErr,
			timesErr,
		)
	}
	if expected.ThreadCount != 1 ||
		exitCode == stillActive ||
		exitCode != expected.ExitCode ||
		created.Nanoseconds() != expected.CreationTime ||
		!strings.EqualFold(filepath.Clean(path), filepath.Clean(expected.Path)) ||
		!isTargetPath(path) {
		return validatedResidual{}, false, nil
	}

	threadID, err := soleThreadID(expected.PID)
	if err != nil {
		return validatedResidual{}, false, err
	}
	if threadID != expected.ThreadID {
		return validatedResidual{}, false, nil
	}

	targetIntegrity, integrityErr := processIntegrityLevel(process)
	if integrityErr != nil {
		targetIntegrity = "unknown: " + integrityErr.Error()
	}
	valid = true
	return validatedResidual{
		process:         process,
		threadID:        threadID,
		exitCode:        exitCode,
		targetIntegrity: targetIntegrity,
	}, true, nil
}

func isPrivilegeError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) ||
		errors.Is(err, windows.ERROR_NOT_ALL_ASSIGNED)
}

func terminateResidual(expected processIdentity) (bool, error) {
	terminated, err := terminateResidualWithComponent(expected, "gui")
	if err == nil || !isPrivilegeError(err) {
		return terminated, err
	}

	serviceTerminated, serviceErr := terminateResidualViaService(expected)
	if serviceErr != nil {
		return false, errors.Join(err, fmt.Errorf("LocalSystem 服务回退失败: %w", serviceErr))
	}
	return serviceTerminated, nil
}

func terminateResidualWithComponent(expected processIdentity, component string) (bool, error) {
	validated, ok, err := openValidatedResidual(expected, 0)
	if err != nil {
		recordCleanupDiagnostic(component, expected, "thread.validate", "unknown", err)
		return false, err
	}
	if !ok {
		return false, nil
	}
	defer windows.CloseHandle(validated.process)

	thread, threadErr := openThreadForTermination(validated.threadID, component)
	if threadErr == nil {
		result, _, callErr := terminateThread.Call(uintptr(thread), uintptr(validated.exitCode))
		windows.CloseHandle(thread)
		if result != 0 {
			recordCleanupDiagnostic(component, expected, "thread.terminate", validated.targetIntegrity, nil)
			return true, nil
		}
		if callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		threadErr = callErr
	}
	recordCleanupDiagnostic(component, expected, "thread.terminate", validated.targetIntegrity, threadErr)
	if !isPrivilegeError(threadErr) {
		return false, fmt.Errorf("PID %d 残留线程清理失败: %w", expected.PID, threadErr)
	}

	processFallback, ok, processErr := openValidatedResidual(expected, windows.PROCESS_TERMINATE)
	if processErr != nil {
		recordCleanupDiagnostic(component, expected, "process.validate", "unknown", processErr)
		return false, fmt.Errorf("PID %d 进程级回退准备失败: %w", expected.PID, processErr)
	}
	if !ok {
		return false, nil
	}
	defer windows.CloseHandle(processFallback.process)

	processErr = terminateProcess(processFallback.process, processFallback.exitCode)
	recordCleanupDiagnostic(component, expected, "process.terminate", processFallback.targetIntegrity, processErr)
	if processErr != nil {
		return false, fmt.Errorf("PID %d 进程级回退清理失败: %w", expected.PID, processErr)
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
