//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
)

// watchPeerShutdown uses EV_EOF rather than consuming readable TCP bytes.
// EV_CLEAR suppresses repeated readiness for queued protocol data while keeping
// the later EOF transition observable. Only stop releases monitor resources.
func watchPeerShutdown(socket net.Conn, onClose func(), onError func(error)) (stop func(), err error) {
	conn, ok := socket.(syscall.Conn)
	if !ok {
		return nil, fmt.Errorf("peer shutdown watcher requires a raw socket")
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("peer shutdown socket control: %w", err)
	}
	// Kqueue and Pipe lack atomic CLOEXEC on some supported BSDs. Coordinate
	// their creation with os/exec's fork lock until both are close-on-exec.
	syscall.ForkLock.RLock()
	poll, err := syscall.Kqueue()
	if err == nil {
		syscall.CloseOnExec(poll)
	}
	if err != nil {
		poll = -1
	}
	pipe := []int{-1, -1}
	if err == nil {
		err = syscall.Pipe(pipe)
		if err == nil {
			syscall.CloseOnExec(pipe[0])
			syscall.CloseOnExec(pipe[1])
		}
	}
	syscall.ForkLock.RUnlock()
	cleanup := func() {
		for _, fd := range []int{pipe[1], pipe[0], poll} {
			if fd >= 0 {
				if closeErr := syscall.Close(fd); closeErr != nil {
					onError(fmt.Errorf("peer shutdown watcher close: %w", closeErr))
				}
			}
		}
	}
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("peer shutdown kqueue/wake pipe: %w", err)
	}
	for _, fd := range pipe {
		if err = syscall.SetNonblock(fd, true); err != nil {
			cleanup()
			return nil, fmt.Errorf("peer shutdown nonblocking wake pipe: %w", err)
		}
	}
	add := func(fd int) error {
		var event syscall.Kevent_t
		// SetKevent handles NetBSD's unsigned Filter and Flags and the
		// architecture-specific Ident width without platform field casts.
		syscall.SetKevent(&event, fd, syscall.EVFILT_READ, syscall.EV_ADD|syscall.EV_ENABLE|syscall.EV_CLEAR)
		for {
			_, err := syscall.Kevent(poll, []syscall.Kevent_t{event}, nil, nil)
			if err != syscall.EINTR {
				return err
			}
		}
	}
	if err = add(pipe[0]); err != nil {
		cleanup()
		return nil, fmt.Errorf("peer shutdown wake registration: %w", err)
	}
	var registrationErr error
	err = raw.Control(func(fd uintptr) {
		registrationErr = add(int(fd))
	})
	if err != nil || registrationErr != nil {
		cleanup()
		if err == nil {
			err = registrationErr
		}
		return nil, fmt.Errorf("peer shutdown socket registration: %w", err)
	}
	var stopping atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		var events [2]syscall.Kevent_t
		for {
			n, waitErr := syscall.Kevent(poll, nil, events[:], nil)
			if waitErr == syscall.EINTR {
				continue
			}
			if stopping.Load() {
				return
			}
			if waitErr != nil {
				onError(fmt.Errorf("peer shutdown watcher wait: %w", waitErr))
				onClose()
				return
			}
			for _, event := range events[:n] {
				if uint64(event.Ident) == uint64(pipe[0]) {
					return
				}
				if event.Flags&syscall.EV_ERROR != 0 {
					onError(fmt.Errorf("peer shutdown watcher event: %w", syscall.Errno(event.Data)))
					onClose()
					return
				}
				if event.Flags&syscall.EV_EOF != 0 {
					onClose()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			stopping.Store(true)
			for {
				_, wakeErr := syscall.Write(pipe[1], []byte{1})
				if wakeErr == syscall.EINTR {
					continue
				}
				if wakeErr != nil && wakeErr != syscall.EAGAIN {
					onError(fmt.Errorf("peer shutdown watcher wake: %w", wakeErr))
					// Writer closure also wakes EVFILT_READ with EV_EOF.
					if closeErr := syscall.Close(pipe[1]); closeErr != nil {
						onError(fmt.Errorf("peer shutdown watcher wake close: %w", closeErr))
					}
					pipe[1] = -1
				}
				break
			}
			<-done
			cleanup()
		})
	}, nil
}
