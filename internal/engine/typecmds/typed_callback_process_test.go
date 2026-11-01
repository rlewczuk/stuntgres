package typecmds_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

const typedCallbackPrefix = "typed-callback-phase:"

func typedCallbackBinary(t *testing.T) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func typedCallbackPhase(phase string) {
	fmt.Fprintln(os.Stdout, typedCallbackPrefix+phase)
}

// Real safety bounds for callback children. Expected failures are driven by
// injected signals, never by these clocks, so they are hang guards sized for a
// loaded machine: re-executing the test binary alone can take seconds there.
// The child watchdog expires before the parent's operation bound so a stalled
// child reports its stacks; reaping outlasts the pipe-copy delay it includes.
const (
	typedCallbackStartupBound   = 30 * time.Second
	typedCallbackOperationBound = 30 * time.Second
	typedCallbackWatchdogBound  = 25 * time.Second
	typedCallbackPipeCopyBound  = 10 * time.Second
	typedCallbackReapBound      = 15 * time.Second
)

// READY owns no operation time. The parent must explicitly admit START after
// observing that the fixture and registered callback are installed.
func typedCallbackAdmit(t *testing.T) func() {
	t.Helper()
	typedCallbackPhase("ready")
	var start [1]byte
	if _, err := io.ReadFull(os.Stdin, start[:]); err != nil || start[0] != 'S' {
		t.Fatalf("callback admission: %q, %v", start, err)
	}
	timer := time.NewTimer(typedCallbackWatchdogBound)
	stop := typedCallbackWatchdog(timer.C)
	return func() {
		stop()
		timer.Stop()
	}
}

func typedCallbackWatchdog(expired <-chan time.Time) func() {
	stopped := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-stopped:
			return
		case <-expired:
		}
		stack := make([]byte, 1<<20)
		n := runtime.Stack(stack, true)
		fmt.Fprintf(os.Stderr, "typed callback operation watchdog expired\n%s", stack[:n])
		os.Exit(2)
	}()
	return func() {
		close(stopped)
		<-joined
	}
}

const typedCallbackLogLimit = 1 << 20

type typedCallbackLog struct {
	buffer   bytes.Buffer
	overflow bool
}

func (w *typedCallbackLog) Write(p []byte) (int, error) {
	remaining := typedCallbackLogLimit - w.buffer.Len()
	if len(p) > remaining {
		n, _ := w.buffer.Write(p[:remaining])
		w.overflow = true
		return n, fmt.Errorf("callback diagnostic stream exceeded 1 MiB")
	}
	return w.buffer.Write(p)
}

func (w *typedCallbackLog) String() string {
	if w.overflow {
		return w.buffer.String() + "\n[callback diagnostic stream exceeded 1 MiB; proof failed]\n"
	}
	return w.buffer.String()
}

type typedCallbackOutput struct {
	log     typedCallbackLog
	pending string
	phases  chan string
}

func (w *typedCallbackOutput) Write(p []byte) (int, error) {
	n, err := w.log.Write(p)
	if err != nil {
		return n, err
	}
	w.pending += string(p)
	for {
		line, rest, ok := strings.Cut(w.pending, "\n")
		if !ok {
			return n, nil
		}
		w.pending = rest
		if phase, ok := strings.CutPrefix(line, typedCallbackPrefix); ok {
			select {
			case w.phases <- phase:
			default:
				return n, fmt.Errorf("callback phase protocol overflow")
			}
		}
	}
}

// Injected expiration signals supplement, NEVER replace, finite real safety
// clocks. Missing protocol progress must fail even when an injected channel
// stays open. The operation signal factory is called only after READY.
// observe executes after ownership transition, before START.
type typedCallbackBudgets struct {
	startup   <-chan time.Time
	operation func() (<-chan time.Time, func())
	observe   func(string, bool, bool)
}

type typedCallbackResult struct {
	phase  string
	err    error
	output string
	reaped bool
}

func runTypedCallbackChild(binary, selector, kind string, budgets typedCallbackBudgets) (result typedCallbackResult) {
	result.phase = "start"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := osexec.CommandContext(ctx, binary, "-test.run="+selector, "-test.count=1", "-test.timeout=0")
	cmd.Env = append(os.Environ(), "STUNTGRES_TYPED_CALLBACK_CASE="+kind)
	// Bound pipe-copy cleanup as well as process cancellation. This is not an
	// extra success budget: any expiry remains a failure.
	cmd.WaitDelay = typedCallbackPipeCopyBound
	stdout := &typedCallbackOutput{phases: make(chan string, 32)}
	var stderr typedCallbackLog
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		result.err = err
		return
	}
	defer input.Close()
	startupTimer := time.NewTimer(typedCallbackStartupBound)
	defer startupTimer.Stop()
	if err := cmd.Start(); err != nil {
		result.err = err
		return
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	// This single exit path owns cancellation and Wait on EVERY started-child
	// outcome, including protocol errors, timeouts and early successful exits.
	defer func() {
		cancel()
		input.Close()
		if !result.reaped {
			reap := time.NewTimer(typedCallbackReapBound)
			defer reap.Stop()
			select {
			case <-wait:
				result.reaped = true
			case <-reap.C:
				result.err = fmt.Errorf("%v; callback child failed to reap after cancellation", result.err)
			}
		}
		if result.reaped {
			result.output = stdout.log.String() + stderr.String()
			if stdout.log.overflow || stderr.overflow {
				result.err = fmt.Errorf("%v; callback diagnostics exceeded bounded capture", result.err)
			}
		}
	}()
	startup := startupTimer.C
	injectedStartup := budgets.startup
	var operation <-chan time.Time
	var injectedOperation <-chan time.Time
	stopOperation := func() {}
	defer func() { stopOperation() }()
	result.phase = "startup"
	sequence := []string{"setup", "ready", "execute", "callback", "restored", "assertions", "teardown", "complete"}
	next := 0
	deadlineError := func() error {
		select {
		case <-startup:
			return fmt.Errorf("callback startup deadline expired (real safety bound)")
		case <-operation:
			return fmt.Errorf("callback operation deadline expired (real safety bound)")
		case <-injectedStartup:
			return fmt.Errorf("callback injected startup deadline expired")
		case <-injectedOperation:
			return fmt.Errorf("callback injected operation deadline expired")
		default:
			return nil
		}
	}
	accept := func(phase string) error {
		if err := deadlineError(); err != nil {
			return err
		}
		if next == len(sequence) || phase != sequence[next] {
			return fmt.Errorf("unexpected callback phase %q after %s", phase, result.phase)
		}
		next++
		result.phase = phase
		if phase == "ready" {
			startupTimer.Stop()
			startup = nil
			injectedStartup = nil
			timer := time.NewTimer(typedCallbackOperationBound)
			operation = timer.C
			stopOperation = func() { timer.Stop() }
			if budgets.operation != nil {
				signal, stopSignal := budgets.operation()
				injectedOperation = signal
				stopOperation = func() {
					stopSignal()
					timer.Stop()
				}
			}
		}
		if budgets.observe != nil {
			budgets.observe(phase, startup != nil, operation != nil)
		}
		if phase == "ready" {
			if _, err := input.Write([]byte{'S'}); err != nil {
				return fmt.Errorf("callback START: %w", err)
			}
			// Keep stdin open until final cancellation so independent probes can
			// block without timers and prove that the parent actually kills them.
		}
		return nil
	}
	for {
		select {
		case phase := <-stdout.phases:
			if result.err = accept(phase); result.err != nil {
				return
			}
		case err := <-wait:
			result.reaped = true
			// Wait synchronizes both output-copy goroutines. Drain phase records
			// before judging success; exit can race the last queued record.
			for len(stdout.phases) != 0 {
				if result.err = accept(<-stdout.phases); result.err != nil {
					return
				}
			}
			result.err = err
			if result.err == nil {
				result.err = deadlineError()
			}
			if result.err == nil && next != len(sequence) {
				result.err = fmt.Errorf("callback child exited before complete")
			}
			return
		case <-startup:
			result.err = fmt.Errorf("callback startup deadline expired (real safety bound)")
			return
		case <-operation:
			result.err = fmt.Errorf("callback operation deadline expired (real safety bound)")
			return
		case <-injectedStartup:
			result.err = fmt.Errorf("callback injected startup deadline expired")
			return
		case <-injectedOperation:
			result.err = fmt.Errorf("callback injected operation deadline expired")
			return
		}
	}
}

// Independent real-process lifecycle controls deliberately do not exercise SQL
// or stand in for the five genuine provider callbacks in the owning regression.
func TestTypedCallbackProcessOwnership(t *testing.T) {
	if kind := os.Getenv("STUNTGRES_TYPED_CALLBACK_CASE"); kind != "" {
		typedCallbackProcessControl(t, kind)
		return
	}
	binary := typedCallbackBinary(t)
	t.Run("phase-copy", func(t *testing.T) {
		output := &typedCallbackOutput{phases: make(chan string, 1)}
		if _, bypass := any(output).(io.ReaderFrom); bypass {
			t.Fatal("stdout ReaderFrom bypasses phase Write interception")
		}
		reader, writer := io.Pipe()
		copyDone, writeDone := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := io.Copy(output, reader)
			copyDone <- err
		}()
		text := "ordinary output\n" + typedCallbackPrefix + "ready\n"
		go func() {
			for _, fragment := range []string{"ordinary output\n", "typed-callback-", "phase:rea", "dy\n"} {
				if _, err := io.WriteString(writer, fragment); err != nil {
					writeDone <- err
					return
				}
			}
			writeDone <- nil
			// Deliberately leave the source open: EOF must not own delivery.
		}()
		defer func() {
			reader.Close()
			writer.Close()
			deadline := time.NewTimer(typedCallbackPipeCopyBound)
			defer deadline.Stop()
			for copyDone != nil || writeDone != nil {
				select {
				case <-copyDone:
					copyDone = nil
				case <-writeDone:
					writeDone = nil
				case <-deadline.C:
					t.Error("streaming proof failed to join its pipe owners")
					return
				}
			}
		}()
		deadline := time.NewTimer(typedCallbackPipeCopyBound)
		defer deadline.Stop()
		select {
		case phase := <-output.phases:
			if phase != "ready" {
				t.Fatal("copy changed phase", phase)
			}
		case <-deadline.C:
			t.Fatal("stdout copy swallowed the admission phase before EOF")
		}
		writer.Close()
		select {
		case err := <-copyDone:
			copyDone = nil
			if err != nil || output.log.String() != text {
				t.Fatal("copy changed bytes or failed", err, output.log.String())
			}
		case <-deadline.C:
			t.Fatal("stdout copy failed to finish after EOF")
		}
		select {
		case err := <-writeDone:
			writeDone = nil
			if err != nil {
				t.Fatal("phase writer failed", err)
			}
		case <-deadline.C:
			t.Fatal("phase writer failed to join")
		}
		overflow := &typedCallbackOutput{phases: make(chan string, 1)}
		oversize := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", typedCallbackLogLimit+1))}
		if _, err := io.Copy(overflow, oversize); err == nil || !overflow.log.overflow || overflow.log.buffer.Len() != typedCallbackLogLimit || !strings.Contains(overflow.log.String(), "proof failed") {
			t.Fatal("diagnostic overflow was not bounded and explicit", err, overflow.log.buffer.Len())
		}
		protocol := &typedCallbackOutput{phases: make(chan string, 1)}
		records := struct{ io.Reader }{strings.NewReader(typedCallbackPrefix + "setup\n" + typedCallbackPrefix + "ready\n")}
		if _, err := io.Copy(protocol, records); err == nil || !strings.Contains(err.Error(), "phase protocol overflow") {
			t.Fatal("phase overflow did not fail closed", err)
		}
		if len(protocol.phases) != 1 || <-protocol.phases != "setup" {
			t.Fatal("phase overflow changed admitted prefix")
		}
	})
	for _, kind := range []string{"success", "startup", "setup-failure", "early-exit", "protocol", "admission", "operation", "watchdog", "assertions", "teardown", "exit"} {
		t.Run(kind, func(t *testing.T) {
			startup, operation := make(chan time.Time), make(chan time.Time)
			startedOperation := false
			budgets := typedCallbackBudgets{
				startup: startup,
				operation: func() (<-chan time.Time, func()) {
					if startedOperation {
						t.Error("operation budget was refreshed")
					}
					startedOperation = true
					return operation, func() {}
				},
				observe: func(phase string, startupActive, operationActive bool) {
					if phase == "setup" {
						if !startupActive || operationActive || startedOperation {
							t.Error("setup consumed operation ownership")
						}
						if kind == "startup" {
							close(startup)
						}
					} else if startupActive || !operationActive || !startedOperation {
						t.Error("admitted phase lacks operation ownership", phase)
					}
					if phase == "ready" {
						// An expired former startup budget cannot cancel an
						// admitted operation. Success must still reach real exit.
						close(startup)
					}
					if (kind == "operation" && phase == "execute") || (kind == "teardown" && phase == "teardown") || (kind == "exit" && phase == "complete") {
						close(operation)
					}
				},
			}
			result := runTypedCallbackChild(binary, "^TestTypedCallbackProcessOwnership$", kind, budgets)
			if !result.reaped {
				t.Fatal("started child was not reaped", result.err)
			}
			if kind == "success" {
				if result.err != nil || result.phase != "complete" {
					t.Fatal(result.phase, result.err, result.output)
				}
				return
			}
			want := map[string]string{"startup": "injected startup deadline", "setup-failure": "exit status 3", "early-exit": "before complete", "protocol": "unexpected callback phase", "admission": "", "operation": "injected operation deadline", "watchdog": "exit status 2", "assertions": "exit status 3", "teardown": "injected operation deadline", "exit": "injected operation deadline"}[kind]
			if result.err == nil || !strings.Contains(result.err.Error(), want) {
				t.Fatal("expected fail-closed outcome", want, result.phase, result.err, result.output)
			}
			wantPhase := map[string]string{"startup": "setup", "setup-failure": "setup", "early-exit": "startup", "protocol": "setup", "admission": "ready", "operation": "execute", "watchdog": "execute", "assertions": "assertions", "teardown": "teardown", "exit": "complete"}[kind]
			if result.phase != wantPhase {
				t.Fatal("failure attributed to wrong phase", wantPhase, result.phase)
			}
			if kind == "watchdog" && !strings.Contains(result.output, "typed callback operation watchdog expired") {
				t.Fatal("child watchdog did not own termination", result.output)
			}
		})
	}
	t.Run("start-failure", func(t *testing.T) {
		result := runTypedCallbackChild(t.TempDir()+"/absent", "^TestTypedCallbackProcessOwnership$", "success", typedCallbackBudgets{})
		if result.err == nil || result.reaped || result.phase != "start" {
			t.Fatal("start failure was not closed", result)
		}
	})
}

func typedCallbackProcessControl(t *testing.T, kind string) {
	if kind == "early-exit" {
		return
	}
	typedCallbackPhase("setup")
	if kind == "setup-failure" {
		os.Exit(3)
	}
	block := func() {
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		os.Exit(4) // EOF is failure, never a successful simulated completion.
	}
	if kind == "startup" {
		block()
	}
	if kind == "protocol" {
		typedCallbackPhase("invalid")
		block()
	}
	if kind == "admission" {
		typedCallbackPhase("ready")
		os.Exit(3)
	}
	stop := typedCallbackAdmit(t)
	defer stop()
	for _, phase := range []string{"execute", "callback", "restored", "assertions", "teardown", "complete"} {
		typedCallbackPhase(phase)
		if kind == "watchdog" && phase == "execute" {
			expired := make(chan time.Time)
			typedCallbackWatchdog(expired)
			close(expired)
			block()
		}
		if (kind == "operation" && phase == "execute") || (kind == "teardown" && phase == "teardown") || (kind == "exit" && phase == "complete") {
			block()
		}
		if kind == "assertions" && phase == "assertions" {
			os.Exit(3)
		}
	}
}
