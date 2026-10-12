//go:build linux

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// This subprocess exercises the real prompt helper on a kernel PTY. Exit 3
// means an ordinary prompt error, not default signal termination.
func TestServerUsersTTYSignalChild(t *testing.T) {
	mode := os.Getenv("STUNTGRES_TTY_PROMPT_CHILD")
	if mode == "" {
		return
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, os.Stdin.Fd(), syscall.F_GETFL, 0)
	if errno != 0 {
		os.Exit(4)
	}
	var out io.Writer = os.Stderr
	if mode == "output-error" {
		out = serverUsersFailWriter{}
	}
	password, err := readPasswordPair(os.Stdin, out)
	after, _, errno := syscall.Syscall(syscall.SYS_FCNTL, os.Stdin.Fd(), syscall.F_GETFL, 0)
	if errno != 0 || flags != after {
		os.Exit(4)
	}
	if _, statErr := os.Stdin.Stat(); statErr != nil {
		os.Exit(4)
	}
	if err != nil {
		if password != "" {
			os.Exit(4)
		}
		os.Exit(3)
	}
	if mode != "matching" || password != "private tty secret" {
		os.Exit(4)
	}
	os.Exit(0)
}

func ttyTestIoctl(t *testing.T, file *os.File, request uintptr, value unsafe.Pointer) {
	t.Helper()
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), request, uintptr(value))
	if errno != 0 {
		t.Fatal(errno)
	}
}

func ttyTestPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	var unlock int32
	ttyTestIoctl(t, master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock))
	var number uint32
	ttyTestIoctl(t, master, syscall.TIOCGPTN, unsafe.Pointer(&number))
	slave, err := os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(number), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func TestServerUsersTTYSignals(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		prompt int
		signal syscall.Signal
	}{
		{"InterruptFirst", "interrupt", 1, syscall.SIGINT},
		{"InterruptSecond", "interrupt", 2, syscall.SIGINT},
		{"TerminateFirst", "interrupt", 1, syscall.SIGTERM},
		{"TerminateSecond", "interrupt", 2, syscall.SIGTERM},
		{"Matching", "matching", 2, 0},
		{"Mismatch", "mismatch", 2, 0},
		{"EOF", "eof", 1, 0},
		{"OutputError", "output-error", 0, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			master, slave := ttyTestPair(t)
			var original syscall.Termios
			ttyTestIoctl(t, slave, syscall.TCGETS, unsafe.Pointer(&original))
			original.Lflag |= syscall.ECHO | syscall.ECHONL | syscall.ICANON
			ttyTestIoctl(t, slave, syscall.TCSETS, unsafe.Pointer(&original))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServerUsersTTYSignalChild$")
			cmd.Env = append(os.Environ(), "STUNTGRES_TTY_PROMPT_CHILD="+test.mode)
			cmd.Stdin = slave
			output, err := cmd.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			defer func() {
				if !joined {
					cmd.Process.Kill()
					cmd.Wait()
				}
			}()
			readPrompt := func(want string) {
				t.Helper()
				text := make([]byte, len(want))
				if _, err := io.ReadFull(output, text); err != nil {
					t.Fatalf("waiting for prompt %q: %v", want, err)
				}
				if string(text) != want {
					t.Fatalf("prompt = %q, want %q", text, want)
				}
				var quiet syscall.Termios
				ttyTestIoctl(t, slave, syscall.TCGETS, unsafe.Pointer(&quiet))
				if quiet.Lflag&(syscall.ECHO|syscall.ECHONL) != 0 {
					t.Fatal("prompt did not disable actual terminal echo")
				}
			}
			if test.prompt > 0 {
				readPrompt("Password: ")
			}
			if test.prompt == 2 {
				if _, err := io.WriteString(master, "private tty secret\n"); err != nil {
					t.Fatal(err)
				}
				readPrompt("\nRepeat password: ")
			}
			if test.signal != 0 {
				if err := cmd.Process.Signal(test.signal); err != nil {
					t.Fatal(err)
				}
			} else if test.mode == "matching" || test.mode == "mismatch" {
				line := "private tty secret\n"
				if test.mode == "mismatch" {
					line = "different secret\n"
				}
				if _, err := io.WriteString(master, line); err != nil {
					t.Fatal(err)
				}
			} else if test.mode == "eof" {
				if _, err := master.Write([]byte{original.Cc[syscall.VEOF]}); err != nil {
					t.Fatal(err)
				}
			}
			rest, readErr := io.ReadAll(output)
			err = cmd.Wait()
			joined = true
			if ctx.Err() != nil {
				t.Fatal("prompt subprocess did not exit within deadline")
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			wantExit := 3
			if test.mode == "matching" {
				wantExit = 0
			}
			if cmd.ProcessState.ExitCode() != wantExit {
				t.Fatalf("exit = %d, want %d: %v; output %q", cmd.ProcessState.ExitCode(), wantExit, err, rest)
			}
			var restored syscall.Termios
			ttyTestIoctl(t, slave, syscall.TCGETS, unsafe.Pointer(&restored))
			if restored != original {
				t.Fatal("terminal settings were not restored after child exit")
			}
			if strings.Contains(string(rest), "secret") {
				t.Fatal("password leaked to prompt output")
			}
			fd := int(master.Fd())
			if err := syscall.SetNonblock(fd, true); err != nil {
				t.Fatal(err)
			}
			var buffer [256]byte
			n, readErr := syscall.Read(fd, buffer[:])
			if n > 0 || (readErr != syscall.EAGAIN && readErr != syscall.EIO) {
				t.Fatalf("unexpected terminal echo: %q (%v)", buffer[:max(n, 0)], readErr)
			}
		})
	}
}
