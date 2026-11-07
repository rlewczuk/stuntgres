//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package pgserver

import (
	"fmt"
	"net"
	"runtime"
)

func watchSocketPeerShutdown(socket net.Conn, onClose func(), onError func(error)) (func(), error) {
	return nil, fmt.Errorf("%w: %s", errPeerWatchUnsupported, runtime.GOOS)
}
