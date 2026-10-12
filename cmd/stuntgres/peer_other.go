//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package main

import (
	"fmt"
	"net"
	"runtime"
)

func watchPeerShutdown(socket net.Conn, onClose func(), onError func(error)) (func(), error) {
	return nil, fmt.Errorf("%w: %s", errPeerWatchUnsupported, runtime.GOOS)
}
