//go:build linux && !noservertests

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerPIDSocketLifecycle(t *testing.T) {
	t.Run("DumpAndStopActualChild", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "server.pid")
		child := startProcessHelper(t, "lifecycle", path, "TestServerSocketProcessHelper")
		message := child.ready(t)
		if message.Error != "" || message.Address == "" {
			t.Fatalf("child readiness: %+v", message)
		}
		connection, err := net.DialTimeout("tcp", message.Address, time.Second)
		if err != nil {
			t.Fatalf("readiness preceded listening: %v", err)
		}
		connection.Close()
		record, err := readPID(path)
		if err != nil || record.PID != child.command.Process.Pid {
			t.Fatalf("child PID: %+v, %v", record, err)
		}
		if err := controlProcess("dump", path); err != nil {
			t.Fatal(err)
		}
		line, err := child.output.ReadString('\n')
		if err != nil || line != "dump\n" {
			t.Fatalf("dump delivery: %q, %v", line, err)
		}
		if err := controlProcess("stop", path); err != nil {
			t.Fatal(err)
		}
		if err := child.command.Wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("child record remains: %v", err)
		}
		if err := controlProcess("dump", path); err == nil {
			t.Fatal("signaled retired child")
		}
	})
}

// This helper is the current test executable, not a substitute server binary.
// It exercises actual OS ownership, listening, readiness and signal lifetimes.
func TestServerSocketProcessHelper(t *testing.T) {
	mode := os.Getenv("STUNTGRES_PROCESS_TEST")
	if mode == "" {
		return
	}
	owner, err := acquirePID(os.Getenv("STUNTGRES_PROCESS_PID"))
	if err != nil {
		if readyErr := notifyReady("", err); readyErr != nil {
			fmt.Fprintln(os.Stderr, readyErr)
		}
		os.Exit(1)
	}
	if mode != "lifecycle" {
		owner.Close()
		fmt.Fprintln(os.Stderr, "unexpected acquisition in failure helper")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		notifyReady("", err)
		owner.Close()
		os.Exit(1)
	}
	signals, stop := lifecycleSignals()
	if err := notifyReady(listener.Addr().String(), nil); err != nil {
		listener.Close()
		owner.Close()
		os.Exit(1)
	}
	for value := range signals {
		if isDumpSignal(value) {
			fmt.Fprintln(os.Stdout, "dump")
			continue
		}
		listener.Close()
		stop()
		if err := owner.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}
