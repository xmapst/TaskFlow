package pty

import (
	"bytes"
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// killSession terminates every process in the shell's session. An interactive
// shell runs jobs in their own process groups, so signalling only the shell's
// group would miss them; the session ID is the one thing they all share.
// Processes that deliberately called setsid are outside the session and stay.
func (t *terminal) killSession(sid int) error {
	// The shell's own group first: it covers the foreground job even if /proc is
	// unavailable (restricted container, hidepid).
	err := unix.Kill(-sid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		err = nil
	}
	entries, readErr := os.ReadDir("/proc")
	if readErr != nil {
		return err
	}
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid == sid {
			continue
		}
		if processSession(pid) != sid {
			continue
		}
		if killErr := unix.Kill(pid, unix.SIGKILL); killErr != nil && !errors.Is(killErr, unix.ESRCH) {
			err = errors.Join(err, killErr)
		}
	}
	return err
}

// processSession returns the session ID from /proc/<pid>/stat, or 0 if the
// process is gone or unreadable.
func processSession(pid int) int {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// "pid (comm) state ppid pgrp session ...": comm may contain spaces and
	// parentheses, so parse from the last ')'.
	_, rest, ok := bytes.Cut(stat[bytes.LastIndexByte(stat, ')')+1:], []byte(" "))
	if !ok {
		return 0
	}
	fields := bytes.Fields(rest) // state ppid pgrp session
	if len(fields) < 4 {
		return 0
	}
	sid, err := strconv.Atoi(string(fields[3]))
	if err != nil {
		return 0
	}
	return sid
}
