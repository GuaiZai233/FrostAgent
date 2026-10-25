//go:build windows

package machine

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func shellArgs(command string) []string {
	return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}
}
func prepareProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

// Start suspended so user code cannot spawn children before job assignment.
// The job is private, non-inheritable, and killed if the bridge crashes.
func runProcessTree(ctx context.Context, cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return err
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		return err
	}
	abort := func(err error) error { _ = cmd.Process.Kill(); _ = cmd.Wait(); return err }
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(err)
	}
	err = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if err != nil {
		return abort(err)
	}
	if err := resumeInitialThread(uint32(cmd.Process.Pid)); err != nil {
		return abort(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	// A command is not complete while its descendants still own this job.
	// Cancellation requests termination, but we keep the operation admitted
	// until the OS confirms zero active processes.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var waitErr error
	parentDone, terminated := false, false
	for {
		if ctx.Err() != nil && !terminated {
			if err := windows.TerminateJobObject(job, 1); err != nil {
				// Keep the sole Wait goroutine and execution lease alive.
				// Failure to request termination is not proof of exit.
			} else {
				terminated = true
			}
		}
		select {
		case waitErr = <-wait:
			parentDone = true
			wait = nil
		case <-ticker.C:
		}
		var accounting struct {
			User, Kernel, PeriodUser, PeriodKernel int64
			Faults, Total, Active, Terminated      uint32
		}
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
		if err != nil {
			// Cannot release the machine lease without knowing descendants
			// stopped. Kill the job and keep checking instead of inventing a
			// successful termination acknowledgement.
			_ = windows.TerminateJobObject(job, 1)
			continue
		}
		if parentDone && accounting.Active == 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return waitErr
		}
	}
}

func resumeInitialThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return errors.New("suspended process thread unavailable")
}
