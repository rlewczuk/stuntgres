//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// userLock locks a stable sibling inode, never the atomically replaced userfile.
func userLock(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() error {
		return errors.Join(syscall.Flock(int(f.Fd()), syscall.LOCK_UN), f.Close())
	}, nil
}

// passwordTerminalReader uses bounded polls instead of leaving a blocking read
// goroutine behind on interruption. Its independent file description leaves the
// caller's stdin open and its blocking mode unchanged.
type passwordTerminalReader struct {
	fd      int
	signals chan os.Signal
}

func (r *passwordTerminalReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		select {
		case sig := <-r.signals:
			return 0, fmt.Errorf("password prompt interrupted by %s", sig)
		default:
		}
		poll := struct {
			fd      int32
			events  int16
			revents int16
		}{fd: int32(r.fd), events: 1} // POLLIN
		_, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&poll)), 1, 50)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, errno
		}
		if poll.revents == 0 {
			continue
		}
		// A pending signal takes precedence over input becoming ready.
		select {
		case sig := <-r.signals:
			return 0, fmt.Errorf("password prompt interrupted by %s", sig)
		default:
		}
		n, err := syscall.Read(r.fd, p)
		if err == syscall.EAGAIN || err == syscall.EINTR {
			continue
		}
		if n < 0 {
			n = 0
		}
		if n == 0 && err == nil {
			err = io.EOF
		}
		return n, err
	}
}

func disablePasswordEcho(in *os.File) (io.Reader, func() error, error) {
	var original syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, in.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&original)))
	if errno == syscall.ENOTTY {
		return in, func() error { return nil }, nil
	}
	if errno != 0 {
		return nil, nil, errno
	}
	// Reopening, unlike dup, does not share O_NONBLOCK with the caller.
	fd, err := syscall.Open(fmt.Sprintf("/proc/self/fd/%d", in.Fd()), syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	set := func(term *syscall.Termios) error {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, in.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(term)))
		if errno != 0 {
			return errno
		}
		return nil
	}
	restore := func() error {
		// Keep signal handling installed until the terminal is safe again.
		err := set(&original)
		signal.Stop(signals)
		return errors.Join(err, syscall.Close(fd))
	}
	quiet := original
	quiet.Lflag &^= syscall.ECHO | syscall.ECHONL
	if err := set(&quiet); err != nil {
		return nil, nil, errors.Join(err, restore())
	}
	return &passwordTerminalReader{fd: fd, signals: signals}, restore, nil
}
