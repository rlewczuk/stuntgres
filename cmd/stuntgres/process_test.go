//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestPID(t *testing.T, path string, record pidRecord) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestServerPID(t *testing.T) {
	t.Run("OwnershipAndStaleRecovery", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "server.pid")
		writeTestPID(t, path, pidRecord{99999999, "/missing/server", "1"})
		owner, err := acquirePID(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { owner.Close() })
		actual, _, err := processIdentity(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		record, err := readPID(path)
		if err != nil || record != actual {
			t.Fatalf("PID record = %+v, %v; want %+v", record, err, actual)
		}
		for _, name := range []string{path, path + ".lock"} {
			info, err := os.Stat(name)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("private file %s: %v, %v", name, info, err)
			}
		}
		if second, err := acquirePID(path); err == nil {
			second.Close()
			t.Fatal("accepted conflicting lifetime ownership")
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("PID record remains after close: %v", err)
		}
		if _, err := os.Stat(path + ".lock"); err != nil {
			t.Fatalf("stable sibling lock disappeared: %v", err)
		}
		if err := owner.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
	})
	t.Run("ClosePreservesDifferentRecord", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "server.pid")
		owner, err := acquirePID(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { owner.Close() })
		replacement := owner.record
		replacement.StartTime = "0"
		writeTestPID(t, path, replacement)
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		actual, err := readPID(path)
		if err != nil || actual != replacement {
			t.Fatalf("removed someone else's record: %+v, %v", actual, err)
		}
	})
	t.Run("UnsafeRecordsNeverSignal", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "server.pid")
		owner, err := acquirePID(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { owner.Close() })
		for _, field := range []string{"pid", "executable", "start_time"} {
			record := owner.record
			switch field {
			case "pid":
				record.PID = -1
			case "executable":
				record.Executable += ".wrong"
			case "start_time":
				record.StartTime = "0"
			}
			writeTestPID(t, path, record)
			if err := controlProcess("dump", path); err == nil {
				t.Fatalf("accepted incorrect %s", field)
			}
		}
		writeTestPID(t, path, owner.record)
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		// This correct identity belongs to the test itself, but the lock is
		// released. The validation must reject it before any signal delivery.
		writeTestPID(t, path, owner.record)
		if err := controlProcess("stop", path); err == nil || !strings.Contains(err.Error(), "not held") {
			t.Fatalf("unowned live PID accepted: %v", err)
		}
	})
	t.Run("DumpAndStopActualChild", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "server.pid")
		child := startProcessHelper(t, "lifecycle", path)
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

type processTestChild struct {
	command *exec.Cmd
	pipe    *os.File
	output  *bufio.Reader
}

func startProcessHelper(t *testing.T, mode, path string) *processTestChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServerProcessHelper$")
	command.Env = append(os.Environ(), "STUNTGRES_PROCESS_TEST="+mode, "STUNTGRES_PROCESS_PID="+path, "STUNTGRES_READY_FD=3")
	command.ExtraFiles = []*os.File{writer}
	stdout, err := command.StdoutPipe()
	if err != nil {
		reader.Close()
		writer.Close()
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		reader.Close()
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()
	t.Cleanup(func() {
		reader.Close()
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	})
	return &processTestChild{command, reader, bufio.NewReader(stdout)}
}

func (child *processTestChild) ready(t *testing.T) readiness {
	t.Helper()
	if err := child.pipe.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message readiness
	if err := json.NewDecoder(child.pipe).Decode(&message); err != nil {
		t.Fatalf("readiness decode: %v", err)
	}
	return message
}

func TestServerStartupFailure(t *testing.T) {
	t.Run("ConflictReportsFailureAndExits", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "server.pid")
		owner, err := acquirePID(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { owner.Close() })
		child := startProcessHelper(t, "conflict", path)
		message := child.ready(t)
		if message.Address != "" || !strings.Contains(message.Error, "already held") {
			t.Fatalf("startup conflict readiness: %+v", message)
		}
		if err := child.command.Wait(); err == nil {
			t.Fatal("startup failure exited successfully")
		}
		record, err := readPID(path)
		if err != nil || record != owner.record {
			t.Fatalf("failed child disturbed owner: %+v, %v", record, err)
		}
	})
	t.Run("NoInheritedDescriptorIsNoOp", func(t *testing.T) {
		t.Setenv("STUNTGRES_READY_FD", "")
		os.Unsetenv("STUNTGRES_READY_FD")
		if err := notifyReady("", errors.New("failure")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("RejectInvalidDescriptor", func(t *testing.T) {
		t.Setenv("STUNTGRES_READY_FD", "99")
		if err := notifyReady("127.0.0.1:1", nil); err == nil {
			t.Fatal("accepted non-inherited descriptor")
		}
	})
}

// This helper is the current test executable, not a substitute server binary.
// It exercises actual OS ownership, listening, readiness and signal lifetimes.
func TestServerProcessHelper(t *testing.T) {
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
