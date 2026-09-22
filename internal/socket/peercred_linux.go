//go:build linux

package socket

import (
	"fmt"
	"net"
	"syscall"
)

// peerCreds reads SO_PEERCRED off a connected unix socket: the kernel-
// verified (uid, pid) of the process on the other end. This is the only
// input the resolver ever trusts (AGENT-CONTRACT.md §Observed identity).
func peerCreds(conn *net.UnixConn) (uid, pid int, err error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, 0, fmt.Errorf("socket: syscall conn: %w", err)
	}

	var ucred *syscall.Ucred
	var sockErr error
	ctrlErr := raw.Control(func(fd uintptr) {
		ucred, sockErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if ctrlErr != nil {
		return 0, 0, fmt.Errorf("socket: control: %w", ctrlErr)
	}
	if sockErr != nil {
		return 0, 0, fmt.Errorf("socket: SO_PEERCRED: %w", sockErr)
	}
	return int(ucred.Uid), int(ucred.Pid), nil
}
