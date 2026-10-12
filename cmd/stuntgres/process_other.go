//go:build !linux

package main

import (
	"errors"
	"os"
	"os/signal"
)

type pidOwnership struct{}

func acquirePID(string) (*pidOwnership, error) {
	return nil, errors.New("PID ownership is supported only on Linux")
}

func (*pidOwnership) Close() error { return nil }

func controlProcess(string, string) error {
	return errors.New("process controls are supported only on Linux")
}

func background(serveOptions) error {
	return errors.New("background servers are supported only on Linux")
}

func notifyReady(string, error) error {
	if _, exists := os.LookupEnv("STUNTGRES_READY_FD"); exists {
		return errors.New("daemon readiness is supported only on Linux")
	}
	return nil
}

func lifecycleSignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	return ch, func() { signal.Stop(ch) }
}

func isDumpSignal(os.Signal) bool { return false }
