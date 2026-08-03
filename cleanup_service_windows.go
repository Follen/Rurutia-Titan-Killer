package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	cleanupServiceName        = "LycheeTitanCleannerService"
	cleanupServiceDisplayName = "荔枝时光服进程专清工具服务"
	cleanupPipeName           = `\\.\pipe\LycheeTitanCleanner.v1`
	cleanupProtocolVersion    = 1
	maxCleanupMessageSize     = 4096
	cleanupIOTimeout          = 5 * time.Second
	cleanupServiceStartWait   = 8 * time.Second
	cleanupPipeSDDL           = "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
)

type cleanupRequest struct {
	Version  int             `json:"version"`
	Identity processIdentity `json:"identity"`
}

type cleanupResponse struct {
	Version    int    `json:"version"`
	Terminated bool   `json:"terminated"`
	Win32Error uint32 `json:"win32Error"`
	Error      string `json:"error,omitempty"`
}

type cleanupWindowsService struct{}

func runAsCleanupService() (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	return true, svc.Run(cleanupServiceName, cleanupWindowsService{})
}

func (cleanupWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	privilegeErr := enableDebugPrivilege()
	recordPrivilegeDiagnostic("service", privilegeErr)

	listener, err := winio.ListenPipe(cleanupPipeName, &winio.PipeConfig{
		SecurityDescriptor: cleanupPipeSDDL,
		MessageMode:        false,
		InputBufferSize:    maxCleanupMessageSize,
		OutputBufferSize:   maxCleanupMessageSize,
	})
	if err != nil {
		recordServiceDiagnostic("pipe.listen", err)
		return false, rawWin32Error(err)
	}
	serveDone := make(chan struct{})
	go func() {
		serveCleanupConnections(listener)
		close(serveDone)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			changes <- request.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			_ = listener.Close()
			<-serveDone
			return false, 0
		}
	}
	_ = listener.Close()
	<-serveDone
	return false, 0
}

func serveCleanupConnections(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(cleanupIOTimeout))
			serveCleanupConnection(connection, terminateResidualWithComponent)
		}()
	}
}

func serveCleanupConnection(connection io.ReadWriter, terminate func(processIdentity, string) (bool, error)) {
	var request cleanupRequest
	if err := readCleanupMessage(connection, &request); err != nil {
		_ = writeCleanupMessage(connection, cleanupResponse{Version: cleanupProtocolVersion, Error: err.Error(), Win32Error: rawWin32Error(err)})
		return
	}
	if request.Version != cleanupProtocolVersion {
		err := fmt.Errorf("不支持的 IPC 协议版本 %d", request.Version)
		_ = writeCleanupMessage(connection, cleanupResponse{Version: cleanupProtocolVersion, Error: err.Error()})
		return
	}

	terminated, err := terminate(request.Identity, "service")
	response := cleanupResponse{
		Version:    cleanupProtocolVersion,
		Terminated: terminated,
		Win32Error: rawWin32Error(err),
		Error:      errorText(err),
	}
	_ = writeCleanupMessage(connection, response)
}

func readCleanupMessage(reader io.Reader, value any) error {
	var length uint32
	if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
		return fmt.Errorf("读取 IPC 消息长度失败: %w", err)
	}
	if length == 0 || length > maxCleanupMessageSize {
		return fmt.Errorf("IPC 消息长度 %d 超出限制", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("读取 IPC 消息失败: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("解析 IPC 消息失败: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("IPC 消息包含多余 JSON 数据")
	}
	return nil
}

func writeCleanupMessage(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > maxCleanupMessageSize {
		return fmt.Errorf("IPC 响应长度 %d 超出限制", len(payload))
	}
	if err := binary.Write(writer, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}
	for len(payload) > 0 {
		written, writeErr := writer.Write(payload)
		if writeErr != nil {
			return writeErr
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

func terminateResidualViaService(expected processIdentity) (bool, error) {
	if err := ensureCleanupServiceRunning(); err != nil {
		return false, err
	}

	timeout := cleanupIOTimeout
	connection, err := winio.DialPipe(cleanupPipeName, &timeout)
	if err != nil {
		return false, fmt.Errorf("连接清理服务失败: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(cleanupIOTimeout))

	request := cleanupRequest{Version: cleanupProtocolVersion, Identity: expected}
	if err := writeCleanupMessage(connection, request); err != nil {
		return false, fmt.Errorf("发送清理请求失败: %w", err)
	}
	var response cleanupResponse
	if err := readCleanupMessage(connection, &response); err != nil {
		return false, fmt.Errorf("读取清理响应失败: %w", err)
	}
	if response.Version != cleanupProtocolVersion {
		return false, fmt.Errorf("清理服务返回协议版本 %d", response.Version)
	}
	if response.Error != "" {
		if response.Win32Error != 0 {
			return false, fmt.Errorf("清理服务: %s: %w", response.Error, syscall.Errno(response.Win32Error))
		}
		return false, errors.New(response.Error)
	}
	return response.Terminated, nil
}

func ensureCleanupServiceRunning() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	desiredBinaryPath := syscall.EscapeArg(executable) + " " + syscall.EscapeArg("--service")

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务控制管理器失败: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(cleanupServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		service, err = manager.CreateService(cleanupServiceName, executable, mgr.Config{
			DisplayName:      cleanupServiceDisplayName,
			Description:      "仅处理经完整身份复核的荔枝时光服退出残留进程",
			StartType:        mgr.StartManual,
			ServiceStartName: "LocalSystem",
		}, "--service")
	}
	if err != nil {
		return fmt.Errorf("打开或安装清理服务失败: %w", err)
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("查询清理服务状态失败: %w", err)
	}
	config, err := service.Config()
	if err != nil {
		return fmt.Errorf("读取清理服务配置失败: %w", err)
	}
	configChanged := config.BinaryPathName != desiredBinaryPath ||
		!strings.EqualFold(config.ServiceStartName, "LocalSystem") ||
		config.StartType != mgr.StartManual ||
		config.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS
	if configChanged {
		if status.State != svc.Stopped {
			if _, err := service.Control(svc.Stop); err != nil {
				return fmt.Errorf("停止旧清理服务失败: %w", err)
			}
			if err := waitForServiceState(service, svc.Stopped, cleanupServiceStartWait); err != nil {
				return err
			}
			status.State = svc.Stopped
		}
		config.BinaryPathName = desiredBinaryPath
		config.DisplayName = cleanupServiceDisplayName
		config.Description = "仅处理经完整身份复核的荔枝时光服退出残留进程"
		config.StartType = mgr.StartManual
		config.ServiceType = windows.SERVICE_WIN32_OWN_PROCESS
		config.ServiceStartName = "LocalSystem"
		if err := service.UpdateConfig(config); err != nil {
			return fmt.Errorf("更新清理服务配置失败: %w", err)
		}
	}
	if status.State == svc.Running {
		return nil
	}
	if status.State == svc.Stopped {
		if err := service.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return fmt.Errorf("启动清理服务失败: %w", err)
		}
	}

	return waitForServiceState(service, svc.Running, cleanupServiceStartWait)
}

func waitForServiceState(service *mgr.Service, desired svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil {
			return fmt.Errorf("等待清理服务时查询失败: %w", err)
		}
		if status.State == desired {
			return nil
		}
		if desired == svc.Running && status.State == svc.Stopped {
			return fmt.Errorf("清理服务启动后停止，Win32=%d Service=%d", status.Win32ExitCode, status.ServiceSpecificExitCode)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("等待清理服务状态 %d 超时", desired)
}
