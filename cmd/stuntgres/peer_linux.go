//go:build linux

package main

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
)

// watchPeerShutdown observes TCP shutdown without reading protocol bytes. The
// poll descriptor and wake pipe belong to stop, never to the polling worker.
func watchPeerShutdown(socket net.Conn, onClose func(), onError func(error)) (stop func(), err error) {
	conn, ok := socket.(syscall.Conn)
	if !ok {
		return nil, fmt.Errorf("peer shutdown watcher requires a raw socket")
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("peer shutdown socket control: %w", err)
	}
	poll, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("peer shutdown epoll: %w", err)
	}
	pipe := []int{-1, -1}
	cleanup := func() {
		for _, fd := range []int{pipe[1], pipe[0], poll} {
			if fd >= 0 {
				if closeErr := syscall.Close(fd); closeErr != nil {
					onError(fmt.Errorf("peer shutdown watcher close: %w", closeErr))
				}
			}
		}
	}
	if err = syscall.Pipe2(pipe, syscall.O_CLOEXEC|syscall.O_NONBLOCK); err != nil {
		cleanup()
		return nil, fmt.Errorf("peer shutdown wake pipe: %w", err)
	}
	add := func(fd int, events uint32) error {
		event := syscall.EpollEvent{Events: events, Fd: int32(fd)}
		for {
			err := syscall.EpollCtl(poll, syscall.EPOLL_CTL_ADD, fd, &event)
			if err != syscall.EINTR {
				return err
			}
		}
	}
	if err = add(pipe[0], syscall.EPOLLIN); err != nil {
		cleanup()
		return nil, fmt.Errorf("peer shutdown wake registration: %w", err)
	}
	var registrationErr error
	err = raw.Control(func(fd uintptr) {
		// Control keeps the original descriptor valid throughout registration;
		// no duplicate socket prolongs its lifetime after the connection closes.
		registrationErr = add(int(fd), syscall.EPOLLRDHUP|syscall.EPOLLHUP|syscall.EPOLLERR)
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
		var events [2]syscall.EpollEvent
		for {
			n, waitErr := syscall.EpollWait(poll, events[:], -1)
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
				if int(event.Fd) == pipe[0] {
					return
				}
				if event.Events&(syscall.EPOLLRDHUP|syscall.EPOLLHUP|syscall.EPOLLERR) != 0 {
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
					// Closing the writer also wakes epoll through EPOLLHUP.
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
