package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Keep PostgreSQL's exact PID/query admission and diagnostics on the existing
// fixture path. Native needs exact worker identity without a deep launcher frame.
func orderedSetActiveCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, operation func(context.Context) fixture.RelationOutcome) {
	t.Helper()
	if b.PG {
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, query, "sortOrderedAggregate", nil, operation)
		return
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	done := make(chan fixture.RelationOutcome, 1)
	identity := make(chan []byte, 1)
	joined := false
	start := time.Now()
	var id []byte
	var out fixture.RelationOutcome
	var samples, incomplete, growth int
	var captureTotal, captureMax, waitTotal, firstSample, lastSample, witness, canceled, resultObserved time.Duration
	var lastPhase string
	lastFrame := make([]byte, 0, 512)
	var firstSource, firstStaging, firstSort time.Duration
	parentDeadline, _ := p.Ctx.Deadline()
	childDeadline, _ := ctx.Deadline()
	parentAtLaunch := time.Until(parentDeadline)
	childAtLaunch := time.Until(childDeadline)
	var parentAtBoundary, childAtBoundary time.Duration
	var parentBoundaryErr, childBoundaryErr error
	var joinOwner orderedSetCancellationJoin
	join := func() {
		if joined || joinOwner.attempted {
			return
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		out, joined = joinOwner.receive(done, timer.C)
		if joined {
			resultObserved = time.Since(start)
		}
	}
	// Cleanup precedes outer statement/connection cleanup even when an assertion
	// calls Fatal. Keep the primary failure and append the eventual real outcome.
	defer func() {
		if canceled == 0 {
			parentAtBoundary = time.Until(parentDeadline)
			childAtBoundary = time.Until(childDeadline)
			parentBoundaryErr = p.Ctx.Err()
			childBoundaryErr = ctx.Err()
		}
		if canceled == 0 {
			canceled = time.Since(start)
		}
		cancel()
		join()
		if !joined {
			t.Error("ordered aggregate cleanup join exceeded 5s")
		}
		if out.Rows != nil {
			out.Rows.Close()
		}
		if out.Stmt != nil {
			out.Stmt.Close()
		}
		if t.Failed() {
			t.Logf("ordered budgets: parent_launch=%s child_launch=%s parent_boundary=%s child_boundary=%s parent_boundary_err=%v child_boundary_err=%v", parentAtLaunch, childAtLaunch, parentAtBoundary, childAtBoundary, parentBoundaryErr, childBoundaryErr)
			t.Logf("ordered observation brackets (offsets from launch): worker=%s samples=%d incomplete=%d growth=%d capture_total=%s capture_max=%s waited=%s first_complete=%s last_complete=%s first_source=%s first_staging=%s first_sort=%s witness=%s cancel=%s result_observed=%s cleanup_end=%s joined=%t last_phase=%s last_frame=%q parent_err=%v child_err=%v actual_error=%T %v rows=%t result=%t stmt=%t",
				id, samples, incomplete, growth, captureTotal, captureMax, waitTotal, firstSample, lastSample, firstSource, firstStaging, firstSort, witness, canceled, resultObserved, time.Since(start), joined, lastPhase, lastFrame, p.Ctx.Err(), ctx.Err(), out.Err, out.Err, out.Rows != nil, out.Result != nil, out.Stmt != nil)
		}
	}()
	go func() {
		var own [128]byte
		n := runtime.Stack(own[:], false)
		fields := bytes.Fields(own[:n])
		identity <- bytes.Clone(fields[1])
		done <- operation(ctx)
	}()
	early := func(r fixture.RelationOutcome) {
		out = r
		joined = true
		resultObserved = time.Since(start)
		t.Fatal(orderedSetObservationFailure(out.Err), out.Err)
	}
	select {
	case id = <-identity:
	case r := <-done:
		early(r)
	case <-ctx.Done():
		t.Fatal("observer deadline before worker identity", ctx.Err())
	}
	stacks := make([]byte, 64<<10)
	poll := time.NewTimer(time.Hour)
	if !poll.Stop() {
		<-poll.C
	}
	defer poll.Stop()
	for {
		select {
		case r := <-done:
			early(r)
		default:
		}
		if ctx.Err() != nil {
			select {
			case r := <-done:
				early(r)
			default:
			}
			t.Fatal("observer deadline without observed sort", ctx.Err())
		}
		before := time.Now()
		n := runtime.Stack(stacks, true)
		cost := time.Since(before)
		samples++
		captureTotal += cost
		if cost > captureMax {
			captureMax = cost
		}
		complete := n < len(stacks)
		if complete {
			lastSample = time.Since(start)
			if firstSample == 0 {
				firstSample = lastSample
			}
			live := orderedSetWorkerFrames(stacks[:n], id, true)
			phase, frame := orderedSetObservationPhase(live)
			lastPhase = phase
			lastFrame = append(lastFrame[:0], frame...)
			if lastPhase == "source" && firstSource == 0 {
				firstSource = lastSample
			}
			if lastPhase == "staging" && firstStaging == 0 {
				firstStaging = lastSample
			}
			if bytes.Contains(live, []byte("stuntgres/internal/engine/exec.sortOrderedAggregate")) {
				witness = time.Since(start)
				firstSort = witness
				parentAtBoundary = time.Until(parentDeadline)
				childAtBoundary = time.Until(childDeadline)
				parentBoundaryErr = p.Ctx.Err()
				childBoundaryErr = ctx.Err()
				if ctx.Err() != nil || p.Ctx.Err() != nil {
					t.Fatal("witness without live cancellation budget")
				}
				canceled = time.Since(start)
				cancel()
				join()
				if !joined {
					t.Fatal("ordered aggregate cancellation join exceeded 5s")
				}
				if out.Rows != nil || out.Result != nil || out.Stmt != nil {
					t.Fatal("canceled sort published output")
				}
				fixture.LexicalOrdinary(t, out.Err, context.Canceled)
				return
			}
		} else {
			incomplete++
			// Incomplete captures never admit. Grow once, then pace exactly as a miss.
			if len(stacks) > int(^uint(0)>>1)/2 {
				t.Fatal("ordered observation stack buffer growth overflow")
			}
			stacks = make([]byte, 2*len(stacks))
			growth++
		}
		before = time.Now()
		poll.Reset(orderedSetObservationWait(cost))
		select {
		case <-ctx.Done():
		case r := <-done:
			waitTotal += time.Since(before)
			early(r)
		case <-poll.C:
		}
		waitTotal += time.Since(before)
	}
}

// complete is false for a full (potentially truncated) runtime.Stack buffer.
// Parse bytes in place and inspect only the exact worker's live frames.
func orderedSetWorkerSorting(sample, id []byte, complete bool) bool {
	return bytes.Contains(orderedSetWorkerFrames(sample, id, complete), []byte("stuntgres/internal/engine/exec.sortOrderedAggregate"))
}

func orderedSetWorkerFrames(sample, id []byte, complete bool) []byte {
	if !complete || len(id) == 0 {
		return nil
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return nil
		}
	}
	for len(sample) > 0 {
		block, rest, found := bytes.Cut(sample, []byte("\n\n"))
		header, frames, ok := bytes.Cut(block, []byte("\n"))
		if ok {
			h, ok := bytes.CutPrefix(header, []byte("goroutine "))
			if ok {
				worker, state, ok := bytes.Cut(h, []byte(" "))
				if ok && bytes.Equal(worker, id) && len(state) > 2 && state[0] == '[' && bytes.HasSuffix(state, []byte("]:")) {
					live, _, _ := bytes.Cut(frames, []byte("\ncreated by "))
					return live
				}
			}
		}
		if !found {
			break
		}
		sample = rest
	}
	return nil
}

// Clamp before doubling, including extreme duration inputs. The 25ms requested
// ceiling was retained after finite native/race cancellation and paced-growth
// cohorts, not proven optimal or starvation-proof. Actual waits can exceed it;
// capture wall is not CPU time.
func orderedSetObservationWait(cost time.Duration) time.Duration {
	if cost >= 25*time.Millisecond/2 {
		return 25 * time.Millisecond
	}
	if cost <= 5*time.Millisecond/2 {
		return 5 * time.Millisecond
	}
	return 2 * cost
}

// One owner permits one receive window. Timeout is terminal: deferred cleanup
// must not create another allowance or consume a late result as an on-time join.
type orderedSetCancellationJoin struct{ attempted bool }

func (j *orderedSetCancellationJoin) receive(done <-chan fixture.RelationOutcome, expired <-chan time.Time) (fixture.RelationOutcome, bool) {
	if j.attempted {
		return fixture.RelationOutcome{}, false
	}
	j.attempted = true
	select {
	case out := <-done:
		return out, true
	case <-expired:
		return fixture.RelationOutcome{}, false
	}
}

func TestOrderedSetCancellationJoin(t *testing.T) {
	t.Run("timeout-is-terminal", func(t *testing.T) {
		done := make(chan fixture.RelationOutcome, 1)
		expired := make(chan time.Time)
		close(expired)
		var owner orderedSetCancellationJoin
		if _, joined := owner.receive(done, expired); joined {
			t.Fatal("expired join reported success")
		}
		lateErr := errors.New("late operation result")
		done <- fixture.RelationOutcome{Err: lateErr}
		// A fresh, unexpired timeout source cannot grant a second receive window.
		if _, joined := owner.receive(done, make(chan time.Time)); joined {
			t.Fatal("timeout was retried")
		}
		select {
		case out := <-done:
			if out.Err != lateErr {
				t.Fatal("late outcome changed")
			}
		default:
			t.Fatal("second attempt consumed the late operation result")
		}
	})
	t.Run("first-result-owned", func(t *testing.T) {
		done := make(chan fixture.RelationOutcome, 1)
		actualErr := errors.New("actual operation result")
		done <- fixture.RelationOutcome{Err: actualErr}
		var owner orderedSetCancellationJoin
		out, joined := owner.receive(done, make(chan time.Time))
		if !joined || out.Err != actualErr {
			t.Fatal("first outcome not retained")
		}
		if _, joined := owner.receive(done, make(chan time.Time)); joined {
			t.Fatal("second receive attempted")
		}
	})
}

func orderedSetObservationFailure(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "worker deadline before observed sort"
	}
	if err == nil {
		return "completed before observation / missed sort"
	}
	return "unexpected operation error before observed sort"
}

func orderedSetObservationPhase(live []byte) (string, []byte) {
	phase := "unknown"
	if bytes.Contains(live, []byte("stuntgres/internal/engine/exec.sortOrderedAggregate")) {
		phase = "sort"
	} else if bytes.Contains(live, []byte("stuntgres/internal/engine/exec.(*JoinNode)")) || bytes.Contains(live, []byte(".Drain(")) {
		phase = "source"
	} else if _, tail, ok := bytes.Cut(live, []byte("/aggregate_query.go:")); ok {
		line := 0
		for _, c := range tail {
			if c < '0' || c > '9' {
				break
			}
			if line > 10000 {
				break
			}
			line = line*10 + int(c-'0')
		}
		// Only this source bracket is the materialized-input staging loop.
		if line >= 159 && line <= 302 {
			phase = "staging"
		}
	}
	// Retain at most the top live frame/location, without per-sample allocation.
	frame, rest, ok := bytes.Cut(live, []byte("\n"))
	if ok {
		location, _, _ := bytes.Cut(rest, []byte("\n"))
		frame = live[:len(frame)+1+len(location)]
	}
	if len(frame) > 480 {
		frame = frame[:480]
	}
	return phase, frame
}

func TestOrderedSetObservationWait(t *testing.T) {
	for _, tc := range []struct{ cost, want time.Duration }{
		{-1, 5 * time.Millisecond}, {0, 5 * time.Millisecond}, {time.Millisecond, 5 * time.Millisecond},
		{2500 * time.Microsecond, 5 * time.Millisecond}, {4 * time.Millisecond, 8 * time.Millisecond},
		{12 * time.Millisecond, 24 * time.Millisecond}, {12500 * time.Microsecond, 25 * time.Millisecond},
		{time.Duration(1<<63 - 1), 25 * time.Millisecond},
	} {
		if got := orderedSetObservationWait(tc.cost); got != tc.want {
			t.Errorf("cost=%s wait=%s want=%s", tc.cost, got, tc.want)
		}
	}
}

func TestOrderedSetObservationDiagnostic(t *testing.T) {
	// Assert categories are distinct, not their presentation wording.
	categories := map[string]bool{}
	for _, err := range []error{nil, context.DeadlineExceeded, context.Canceled, errors.New("operation failure")} {
		category := orderedSetObservationFailure(err)
		if category == "" {
			t.Fatal("missing category")
		}
		categories[category] = true
	}
	if len(categories) != 3 {
		t.Fatal("completion, worker deadline and unexpected errors must be distinct")
	}
	if phase, _ := orderedSetObservationPhase([]byte("exec.openAggregateNode()\n file.go:200")); phase != "unknown" {
		t.Fatal("ambiguous aggregate frame inferred a phase")
	}
	if orderedSetObservationFailure(errors.Join(context.DeadlineExceeded, errors.New("wrapped"))) != orderedSetObservationFailure(context.DeadlineExceeded) {
		t.Fatal("wrapped worker deadline lost its category")
	}
	for _, tc := range []struct{ sample, want string }{
		{"exec.openAggregateNode()\n /src/aggregate_query.go:160 +0x1\n", "staging"},
		{"exec.openAggregateNode()\n /src/aggregate_query.go:305 +0x1\n", "unknown"},
		{"stuntgres/internal/engine/exec.(*JoinNode).Open()\n /src/query_join.go:22\n", "source"},
		{"stuntgres/internal/engine/exec.sortOrderedAggregate()\n /src/aggregate_query.go:413\n", "sort"},
	} {
		if got, _ := orderedSetObservationPhase([]byte(tc.sample)); got != tc.want {
			t.Errorf("phase=%s want=%s", got, tc.want)
		}
	}
	if _, frame := orderedSetObservationPhase(bytes.Repeat([]byte("x"), 2048)); len(frame) > 480 {
		t.Fatal("unbounded diagnostic frame")
	}
}

func TestOrderedSetWorkerSorting(t *testing.T) {
	for _, tc := range []struct {
		name, sample, id string
		complete, want   bool
	}{
		{"exact", "goroutine 42 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate.func1()\n\tfile.go:1\n", "42", true, true},
		{"unrelated", "goroutine 43 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n\tfile.go:1\n", "42", true, false},
		{"created-only", "goroutine 42 [running]:\nruntime.park()\n\tfile.go:1\ncreated by stuntgres/internal/engine/exec.sortOrderedAggregate in goroutine 1\n\tfile.go:2\n", "42", true, false},
		{"truncated", "goroutine 42 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n", "42", false, false},
		{"empty", "", "42", true, false},
		{"empty-id", "goroutine 42 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n", "", true, false},
		{"invalid-id", "goroutine x [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n", "x", true, false},
		{"prefix-id", "goroutine 420 [running]:\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n", "42", true, false},
		{"malformed", "goroutine 42 ???\nstuntgres/internal/engine/exec.sortOrderedAggregate()\n", "42", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderedSetWorkerSorting([]byte(tc.sample), []byte(tc.id), tc.complete); got != tc.want {
				t.Fatalf("sorting=%t want %t", got, tc.want)
			}
		})
	}
}
