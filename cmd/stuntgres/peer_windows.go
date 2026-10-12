//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	peerWinsock     = syscall.NewLazyDLL("ws2_32.dll")
	peerCreateEvent = peerWinsock.NewProc("WSACreateEvent")
	peerCloseEvent  = peerWinsock.NewProc("WSACloseEvent")
	peerEventSelect = peerWinsock.NewProc("WSAEventSelect")
	peerWaitEvents  = peerWinsock.NewProc("WSAWaitForMultipleEvents")
	peerSetEvent    = peerWinsock.NewProc("WSASetEvent")
	peerIoctl       = peerWinsock.NewProc("WSAIoctl")
	peerLastError   = peerWinsock.NewProc("WSAGetLastError")
)

const (
	peerFDClosed     = 1 << 5
	peerWSAFailed    = uint32(0xffffffff)
	peerWaitTimeout  = 258
	peerTCPInfo      = 0xc8000027 // mstcpip.h: _WSAIORW(IOC_VENDOR, 39).
	peerStatePollMS  = 100
	peerTCPInfoBytes = 88 // Windows SDK TCP_INFO_v0, including trailing alignment.
)

// peerWSACall retrieves the Winsock (not GetLastError) status on the same OS
// thread as the failed call. LazyProc.Call's generic error is not authoritative
// for these APIs, and a goroutine can otherwise move between the two calls.
func peerWSACall(proc *syscall.LazyProc, failure uint32, args ...uintptr) (uintptr, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, _ := proc.Call(args...)
	if uint32(result) != failure {
		return result, nil
	}
	code, _, _ := peerLastError.Call()
	if code == 0 {
		return result, fmt.Errorf("%s failed without a Winsock error code", proc.Name)
	}
	return result, fmt.Errorf("%s: %w", proc.Name, syscall.Errno(code))
}

// peerTCPState uses the documented version-zero SIO_TCP_INFO ABI. It requires
// Windows 10 1703 / Server 2016 or a provider implementing this IOCTL; setup
// probes support instead of pretending FD_CLOSE alone detects an unread FIN.
// https://learn.microsoft.com/en-us/windows/win32/winsock/sio-tcp-info
// https://learn.microsoft.com/en-us/windows/win32/api/mstcpip/ns-mstcpip-tcp_info_v0
func peerTCPState(raw syscall.RawConn) (uint32, error) {
	var version, returned uint32
	var info [peerTCPInfoBytes]byte
	var queryErr error
	err := raw.Control(func(fd uintptr) {
		_, queryErr = peerWSACall(peerIoctl, peerWSAFailed, fd, peerTCPInfo,
			uintptr(unsafe.Pointer(&version)), unsafe.Sizeof(version),
			uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)),
			uintptr(unsafe.Pointer(&returned)), 0, 0)
	})
	if err != nil {
		return 0, err
	}
	if queryErr != nil {
		return 0, queryErr
	}
	if returned != peerTCPInfoBytes {
		return 0, fmt.Errorf("SIO_TCP_INFO returned %d bytes, expected %d", returned, peerTCPInfoBytes)
	}
	return binary.LittleEndian.Uint32(info[:4]), nil
}

func peerTCPClosed(state uint32) bool {
	// TCPSTATE, not MIB_TCP_STATE: CLOSED=0, CLOSE_WAIT=7, CLOSING=8,
	// LAST_ACK=9, TIME_WAIT=10. FIN_WAIT_* alone is a local send shutdown.
	return state == 0 || state >= 7 && state <= 10
}

func watchPeerShutdown(socket net.Conn, onClose func(), onError func(error)) (func(), error) {
	tcp, ok := socket.(*net.TCPConn)
	if !ok {
		return nil, fmt.Errorf("Windows peer watcher requires TCP: %w", errPeerWatchUnsupported)
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return nil, err
	}
	initialState, err := peerTCPState(raw)
	if err != nil {
		// These are the provider/OS unsupported-command errors, not transient
		// failures that can safely be downgraded to an ordinary reader.
		if errors.Is(err, syscall.Errno(10022)) || errors.Is(err, syscall.Errno(10045)) {
			return nil, fmt.Errorf("SIO_TCP_INFO unavailable (%v): %w", err, errPeerWatchUnsupported)
		}
		return nil, err
	}
	stopEvent, err := peerWSACall(peerCreateEvent, 0)
	if err != nil {
		return nil, err
	}
	closeEvent, err := peerWSACall(peerCreateEvent, 0)
	if err != nil {
		if _, closeErr := peerWSACall(peerCloseEvent, 0, stopEvent); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}
	closeEvents := func() {
		for _, event := range [...]uintptr{closeEvent, stopEvent} {
			if _, closeErr := peerWSACall(peerCloseEvent, 0, event); closeErr != nil {
				onError(closeErr)
			}
		}
	}
	var associationErr error
	err = raw.Control(func(fd uintptr) {
		_, associationErr = peerWSACall(peerEventSelect, peerWSAFailed, fd, closeEvent, peerFDClosed)
	})
	if err != nil || associationErr != nil {
		closeEvents()
		return nil, errors.Join(err, associationErr)
	}

	// WSAEventSelect changes only event selection/nonblocking mode; it does
	// not replace the socket's IOCP association or its overlapped WSARecv /
	// WSASend operations. The synchronous state IOCTL has nil OVERLAPPED and
	// therefore does not submit a foreign completion to Go's IOCP.
	// FD_CLOSE may be delayed until queued bytes are read. Query actual TCP
	// state on a bounded event-wait timeout to observe CLOSE_WAIT without
	// reading any protocol bytes. There is no second reader or Go timer.
	// https://learn.microsoft.com/en-us/windows/win32/api/winsock2/nf-winsock2-wsaeventselect
	stopping := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		events := [...]uintptr{stopEvent, closeEvent}
		state := initialState
		for {
			select {
			case <-stopping:
				return
			default:
			}
			if peerTCPClosed(state) {
				onClose()
				return
			}
			result, waitErr := peerWSACall(peerWaitEvents, peerWSAFailed,
				uintptr(len(events)), uintptr(unsafe.Pointer(&events[0])), 0, peerStatePollMS, 0)
			if waitErr != nil {
				onError(waitErr)
				onClose()
				return
			}
			switch uint32(result) {
			case 0: // Stop has priority when both events are signaled.
				return
			case 1: // Only FD_CLOSE is associated with this event.
				onClose()
				return
			case peerWaitTimeout:
				var stateErr error
				state, stateErr = peerTCPState(raw)
				if stateErr != nil {
					if !errors.Is(stateErr, net.ErrClosed) {
						onError(stateErr)
					}
					onClose()
					return
				}
			default:
				onError(fmt.Errorf("WSAWaitForMultipleEvents returned unexpected status %d", result))
				onClose()
				return
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(stopping)
			if _, wakeErr := peerWSACall(peerSetEvent, 0, stopEvent); wakeErr != nil {
				onError(wakeErr)
				// The bounded wait still observes stopping if signaling fails.
			}
			<-done
			// Control protects against socket close/handle reuse. A closed Go
			// socket has already removed the Winsock event association.
			var detachErr error
			controlErr := raw.Control(func(fd uintptr) {
				_, detachErr = peerWSACall(peerEventSelect, peerWSAFailed, fd, 0, 0)
			})
			if controlErr != nil && !errors.Is(controlErr, net.ErrClosed) {
				onError(controlErr)
			}
			if detachErr != nil {
				onError(detachErr)
				// Never release an event handle still associated with a socket.
				if closeErr := socket.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
					onError(closeErr)
				}
			}
			closeEvents()
		})
	}, nil
}
