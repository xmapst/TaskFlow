package conpty

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// job ties a child's whole process tree to one Job Object. KILL_ON_JOB_CLOSE
// also reaps the tree if the host dies without calling Close.
type job struct {
	mu     sync.Mutex
	handle windows.Handle
}

// newJob creates a kill-on-close job. A nil result means the platform refused
// (for example a restrictive parent job); callers then fall back to killing the
// direct child only.
func newJob() *job {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(h)
		return nil
	}
	return &job{handle: h}
}

// assign adds a process (still suspended, so it has spawned nothing yet).
func (j *job) assign(process windows.Handle) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return windows.AssignProcessToJobObject(j.handle, process)
}

// terminate kills every process in the job. It reports false if the job is gone.
func (j *job) terminate() (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return false, nil
	}
	return true, windows.TerminateJobObject(j.handle, 1)
}

// close releases the handle; with KILL_ON_JOB_CLOSE this kills any survivors.
func (j *job) close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle != 0 {
		_ = windows.CloseHandle(j.handle)
		j.handle = 0
	}
}
