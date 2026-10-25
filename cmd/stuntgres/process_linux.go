//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const processDeadline = 30 * time.Second

type pidRecord struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	StartTime  string `json:"start_time"`
}

type pidOwnership struct {
	path   string
	lock   *os.File
	record pidRecord
}

func processIdentity(pid int) (pidRecord, bool, error) {
	if pid <= 0 {
		return pidRecord{}, false, errors.New("PID must be positive")
	}
	base := "/proc/" + strconv.Itoa(pid)
	data, err := os.ReadFile(base + "/stat")
	if err != nil {
		return pidRecord{}, false, err
	}
	// comm may contain spaces and parentheses; fields after its final ')' start
	// with state (field 3), making starttime (field 22) index 19.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return pidRecord{}, false, errors.New("malformed process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return pidRecord{}, false, errors.New("incomplete process stat")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return pidRecord{}, false, fmt.Errorf("invalid process start time: %w", err)
	}
	executable, err := os.Readlink(base + "/exe")
	if err != nil {
		return pidRecord{}, fields[0] == "Z", err
	}
	return pidRecord{pid, executable, fields[19]}, fields[0] == "Z", nil
}

func syncPIDDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func acquirePID(path string) (*pidOwnership, error) {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("PID ownership already held: %w", err)
	}
	owned := false
	defer func() {
		if !owned {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			lock.Close()
		}
	}()
	if err := lock.Chmod(0600); err != nil {
		return nil, err
	}
	record, _, err := processIdentity(os.Getpid())
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".stuntgres-pid-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if err = json.NewEncoder(file).Encode(record); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, err
	}
	if err := syncPIDDirectory(path); err != nil {
		os.Remove(path)
		return nil, err
	}
	owned = true
	return &pidOwnership{path, lock, record}, nil
}

func readPID(path string) (pidRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return pidRecord{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return pidRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return pidRecord{}, errors.New("invalid PID record file")
	}
	var record pidRecord
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return record, errors.New("trailing PID record data")
	}
	if record.PID <= 0 || record.Executable == "" || record.StartTime == "" {
		return record, errors.New("invalid PID record")
	}
	return record, nil
}

func (owner *pidOwnership) Close() error {
	if owner == nil || owner.lock == nil {
		return nil
	}
	var result error
	record, err := readPID(owner.path)
	if err == nil && record == owner.record {
		result = os.Remove(owner.path)
		if result == nil {
			result = syncPIDDirectory(owner.path)
		}
	} else if err != nil && !os.IsNotExist(err) {
		result = err
	}
	if err := syscall.Flock(int(owner.lock.Fd()), syscall.LOCK_UN); result == nil {
		result = err
	}
	if err := owner.lock.Close(); result == nil {
		result = err
	}
	owner.lock = nil
	return result
}

func controlProcess(command, path string) error {
	var sig syscall.Signal
	switch command {
	case "dump":
		sig = syscall.SIGUSR1
	case "stop":
		sig = syscall.SIGTERM
	default:
		return fmt.Errorf("unknown process control %q", command)
	}
	record, err := readPID(path)
	if err != nil {
		return err
	}
	// FindProcess pins Linux's process handle before identity validation, so a
	// reused numeric PID cannot become the signal target after validation.
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return err
	}
	defer process.Release()
	actual, zombie, err := processIdentity(record.PID)
	if err != nil || zombie || actual != record {
		return errors.New("stale or mismatched PID record")
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		return errors.New("PID lifetime lock is not held")
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
		return err
	}
	current, err := readPID(path)
	if err != nil || current != record {
		return errors.New("PID ownership changed before signaling")
	}
	if err := process.Signal(sig); err != nil {
		return err
	}
	if command == "dump" {
		return nil // Delivery is not a synchronous dump-completion guarantee.
	}
	timer := time.NewTimer(processDeadline)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		lockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if lockErr == nil {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			actual, zombie, identityErr := processIdentity(record.PID)
			if os.IsNotExist(identityErr) || zombie || (identityErr == nil && actual != record) {
				return nil
			}
		} else if !errors.Is(lockErr, syscall.EWOULDBLOCK) {
			return lockErr
		}
		select {
		case <-timer.C:
			return errors.New("timed out waiting for server to stop")
		case <-ticker.C:
		}
	}
}

type readiness struct {
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

func notifyReady(address string, startupErr error) error {
	value, exists := os.LookupEnv("STUNTGRES_READY_FD")
	if !exists {
		return nil
	}
	os.Unsetenv("STUNTGRES_READY_FD")
	if value != "3" {
		return errors.New("invalid inherited readiness descriptor")
	}
	file := os.NewFile(3, "stuntgres-readiness")
	defer file.Close()
	message := readiness{Address: address}
	if startupErr != nil {
		message.Address = ""
		message.Error = startupErr.Error()
	} else if address == "" {
		return errors.New("ready server has no listen address")
	}
	return json.NewEncoder(file).Encode(message)
}

func background(options serveOptions) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		writer.Close()
		return err
	}
	defer null.Close()
	environment := append(childEnvironment(os.Environ()), "STUNTGRES_READY_FD=3")
	process, err := os.StartProcess(executable, append([]string{executable}, serveArguments(options)...), &os.ProcAttr{
		Env:   environment,
		Files: []*os.File{null, os.Stdout, os.Stderr, writer},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	writer.Close()
	if err != nil {
		return err
	}
	type readyResult struct {
		message readiness
		err     error
	}
	ready := make(chan readyResult, 1)
	go func() {
		var message readiness
		err := json.NewDecoder(io.LimitReader(reader, 65536)).Decode(&message)
		ready <- readyResult{message, err}
	}()
	timer := time.NewTimer(processDeadline)
	defer timer.Stop()
	var failure error
	select {
	case result := <-ready:
		switch {
		case result.err != nil:
			failure = fmt.Errorf("daemon readiness: %w", result.err)
		case result.message.Error != "":
			failure = errors.New(result.message.Error)
		case result.message.Address == "":
			failure = errors.New("daemon readiness has no listen address")
		default:
			return process.Release()
		}
	case <-timer.C:
		failure = errors.New("timed out waiting for daemon readiness")
	}
	reader.Close()
	// Failed startup children must always be reaped. SIGKILL is a last-resort
	// startup cleanup only; the public stop command never escalates SIGTERM.
	process.Signal(syscall.SIGTERM)
	joined := make(chan error, 1)
	go func() { _, err := process.Wait(); joined <- err }()
	cleanup := time.NewTimer(5 * time.Second)
	defer cleanup.Stop()
	select {
	case <-joined:
	case <-cleanup.C:
		process.Kill()
		<-joined
	}
	return failure
}

func lifecycleSignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGUSR1, syscall.SIGTERM, syscall.SIGINT)
	return ch, func() { signal.Stop(ch) }
}

func isDumpSignal(value os.Signal) bool { return value == syscall.SIGUSR1 }
