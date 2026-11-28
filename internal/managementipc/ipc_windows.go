//go:build windows

package managementipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const defaultEndpoint = `\\.\pipe\FrostAgent.Management`

func localPipe(path string) bool {
	return strings.HasPrefix(strings.ToLower(path), `\\.\pipe\`) && !strings.ContainsAny(strings.TrimPrefix(strings.ToLower(path), `\\.\pipe\`), "\\/")
}
func processToken(pid uint32) (windows.Token, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	err = windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token)
	return token, err
}
func serviceIdentity(token windows.Token, pid uint32) bool {
	user, err := token.GetTokenUser()
	if err != nil {
		return false
	}
	return administrator(token) || user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) || registeredServicePID(pid)
}

// Only the administrator-configured FrostAgent service may lend trust to a
// non-administrator account. Shared LocalService/NetworkService SIDs are not identities.
func registeredServicePID(pid uint32) bool {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(manager)
	name, _ := windows.UTF16PtrFromString("FrostAgent")
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(service)
	var status windows.SERVICE_STATUS_PROCESS
	var size uint32
	err = windows.QueryServiceStatusEx(service, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&status)), uint32(unsafe.Sizeof(status)), &size)
	return err == nil && status.ProcessId == pid && (status.CurrentState == windows.SERVICE_RUNNING || status.CurrentState == windows.SERVICE_START_PENDING)
}

type pipeListener struct{ net.Listener }

func (l *pipeListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		handle, ok := conn.(interface{ Fd() uintptr })
		var pid uint32
		if !ok || windows.GetNamedPipeClientProcessId(windows.Handle(handle.Fd()), &pid) != nil {
			conn.Close()
			continue
		}
		token, err := processToken(pid)
		if err != nil {
			conn.Close()
			continue
		}
		elevated := administrator(token)
		token.Close()
		if !elevated {
			conn.Close()
			continue
		}
		return conn, nil
	}
}
func Listen(path string) (net.Listener, error) {
	if !localPipe(path) {
		return nil, errors.New("IPC must use a local named pipe")
	}
	token := windows.GetCurrentProcessToken()
	if !serviceIdentity(token, uint32(os.Getpid())) {
		return nil, errors.New("Windows backend must run elevated or as a Windows service account")
	}
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	// The service SID needs to create server instances. Client admission also
	// checks elevation, so a non-administrator service account cannot manage.
	descriptor := "D:P(A;;GA;;;BA)(A;;GA;;;SY)"
	if !administrator(token) && !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		descriptor += fmt.Sprintf("(A;;0x0012019f;;;%s)", user.User.Sid.String())
	}
	pipe, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: descriptor, InputBufferSize: 65536, OutputBufferSize: 65536})
	if err != nil {
		return nil, err
	}
	return &pipeListener{Listener: pipe}, nil
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	if !localPipe(path) {
		return nil, errors.New("remote named pipes are forbidden")
	}
	if !administrator(windows.GetCurrentProcessToken()) {
		return nil, errors.New("local management requires an elevated administrator terminal")
	}
	conn, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		return nil, err
	}
	handle, ok := conn.(interface{ Fd() uintptr })
	var pid uint32
	if !ok || windows.GetNamedPipeServerProcessId(windows.Handle(handle.Fd()), &pid) != nil {
		conn.Close()
		return nil, errors.New("cannot verify local management server identity")
	}
	token, err := processToken(pid)
	if err != nil {
		conn.Close()
		return nil, errors.New("cannot read local management server identity")
	}
	trusted := serviceIdentity(token, pid)
	token.Close()
	if !trusted {
		conn.Close()
		return nil, errors.New("untrusted local management server")
	}
	return conn, nil
}

func administrator(token windows.Token) bool {
	if !token.IsElevated() {
		return false
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return false
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) && group.Attributes&windows.SE_GROUP_ENABLED != 0 && group.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0 {
			return true
		}
	}
	return false
}
