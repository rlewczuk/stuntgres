package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const orderedSetWorkerLabel = "stuntgres.ordered-set-cancel"
const orderedSetSortFrame = "stuntgres/internal/engine/exec.sortOrderedAggregate.func1"

// Keep PostgreSQL's exact PID/query admission and diagnostics on the existing
// fixture path. Native profiles the labeled worker, not all-goroutine stack text.
func orderedSetActiveCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, operation func(context.Context) fixture.RelationOutcome) {
	t.Helper()
	if b.PG {
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, query, "sortOrderedAggregate", nil, operation)
		return
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	done := make(chan fixture.RelationOutcome, 1)
	entered := make(chan time.Time, 1)
	finished := make(chan time.Time, 1)
	// Routes are serial and each owns one operation. The test's full name gives
	// its worker a distinct identity even when other packages are being tested.
	worker := t.Name()
	joined := false
	start := time.Now()
	var out fixture.RelationOutcome
	var samples int
	var captureTotal, captureMax time.Duration
	var launchAt, resultAt, witnessAt, cancelAt, joinAt time.Duration
	var firstCapture, lastCapture, requestedWait, actualWait time.Duration
	var lastPhase string
	var lastFrame []byte
	var joinOwner orderedSetCancellationJoin
	join := func() {
		if joined || joinOwner.attempted {
			return
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		out, joined = joinOwner.receive(done, timer.C)
		joinAt = time.Since(start)
		if joined {
			resultAt = joinAt
		}
	}
	// Cleanup precedes outer statement/connection cleanup even when an assertion
	// calls Fatal. Keep one terminal join allowance and the eventual real outcome.
	defer func() {
		if cancelAt == 0 {
			cancelAt = time.Since(start)
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
		var workerEntered, workerFinished time.Duration
		select {
		case at := <-entered:
			workerEntered = at.Sub(start)
		default:
		}
		select {
		case at := <-finished:
			workerFinished = at.Sub(start)
		default:
		}
		if t.Failed() {
			t.Logf("ordered observation: worker=%q samples=%d capture_total=%s capture_max=%s elapsed=%s launch=%s worker_entered=%s worker_finished=%s first_capture=%s last_capture=%s requested_wait=%s actual_wait=%s last_phase=%s last_frame=%q witness=%s cancel=%s result_received=%s join=%s joined=%t parent_err=%v child_err=%v actual_error=%T %v rows=%t result=%t stmt=%t",
				worker, samples, captureTotal, captureMax, time.Since(start), launchAt, workerEntered, workerFinished, firstCapture, lastCapture, requestedWait, actualWait, lastPhase, lastFrame, witnessAt, cancelAt, resultAt, joinAt, joined, p.Ctx.Err(), ctx.Err(), out.Err, out.Err, out.Rows != nil, out.Result != nil, out.Stmt != nil)
		}
	}()
	launchAt = time.Since(start)
	go pprof.Do(ctx, pprof.Labels(orderedSetWorkerLabel, worker), func(ctx context.Context) {
		entered <- time.Now()
		actual := operation(ctx)
		finished <- time.Now()
		done <- actual
	})
	early := func(r fixture.RelationOutcome) {
		out = r
		joined = true
		resultAt = time.Since(start)
		t.Fatal(orderedSetObservationFailure(out.Err), out.Err)
	}
	profile := pprof.Lookup("goroutine")
	var sample bytes.Buffer
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
			t.Fatal("observer deadline without observed sort; worker pending at boundary", ctx.Err())
		}
		sample.Reset()
		before := time.Now()
		// debug=1 uses the runtime goroutine profile and symbolizes its records;
		// debug=2 would return to the expensive runtime.Stack path. bytes.Buffer
		// grows as needed: a truncated fixed-size text capture cannot admit.
		if err := profile.WriteTo(&sample, 1); err != nil {
			t.Fatal("ordered aggregate goroutine profile", err)
		}
		cost := time.Since(before)
		samples++
		captureTotal += cost
		if cost > captureMax {
			captureMax = cost
		}
		lastCapture = time.Since(start)
		if firstCapture == 0 {
			firstCapture = lastCapture
		}
		owned := orderedSetWorkerRecord(sample.Bytes(), worker)
		lastPhase = orderedSetObservationPhase(owned)
		top, _, _ := bytes.Cut(owned, []byte("\n"))
		if len(top) > 240 {
			top = top[:240]
		}
		lastFrame = append(lastFrame[:0], top...)
		if lastPhase == "sort" {
			witnessAt = time.Since(start)
			if ctx.Err() != nil || p.Ctx.Err() != nil {
				t.Fatal("witness without live cancellation budget")
			}
			cancelAt = time.Since(start)
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
		wait := orderedSetObservationWait(cost)
		requestedWait += wait
		poll.Reset(wait)
		before = time.Now()
		select {
		case <-ctx.Done():
		case r := <-done:
			actualWait += time.Since(before)
			early(r)
		case <-poll.C:
		}
		actualWait += time.Since(before)
	}
}

// A goroutine profile groups live stacks by PCs AND labels. Require the owned
// operation's exact label and the actual comparator in the same record, never a
// launcher name or a sort frame belonging to another operation. Profile labels
// avoid depending on a deep wrapper frame surviving the profiler's stack limit.
func orderedSetWorkerFrame(sample []byte, worker, wantFrame string) bool {
	return orderedSetRecordFrame(orderedSetWorkerRecord(sample, worker), wantFrame)
}

// Return only the owned record's live frames. Absence/truncation is unknown,
// never evidence that the operation did not reach a phase between captures.
func orderedSetWorkerRecord(sample []byte, worker string) []byte {
	if worker == "" {
		return nil
	}
	for len(sample) > 0 {
		block, rest, found := bytes.Cut(sample, []byte("\n\n"))
		_, lines, _ := bytes.Cut(block, []byte("\n"))
		for remaining := lines; len(remaining) > 0; {
			line, tail, _ := bytes.Cut(remaining, []byte("\n"))
			remaining = tail
			if labels, ok := bytes.CutPrefix(line, []byte("# labels: ")); ok {
				var values map[string]string
				if json.Unmarshal(labels, &values) == nil && values[orderedSetWorkerLabel] == worker {
					return tail
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

func orderedSetRecordFrame(record []byte, wantFrame string) bool {
	for len(record) > 0 {
		line, rest, _ := bytes.Cut(record, []byte("\n"))
		record = rest
		fields := bytes.Fields(line)
		if len(fields) < 3 || !bytes.Equal(fields[0], []byte("#")) {
			continue
		}
		function, _, _ := bytes.Cut(fields[2], []byte("+"))
		if bytes.Equal(function, []byte(wantFrame)) {
			return true
		}
	}
	return false
}

func orderedSetObservationPhase(record []byte) string {
	if orderedSetRecordFrame(record, orderedSetSortFrame) {
		return "sort"
	}
	if orderedSetRecordFrame(record, "stuntgres/internal/engine/exec.(*Stage).Drain") ||
		orderedSetRecordFrame(record, "stuntgres/internal/engine/exec.OpenJoinNode") {
		return "source"
	}
	// openAggregateNode alone spans input, staging and finalization, so it
	// cannot establish staging without a separately justified source location.
	return "unknown"
}

// Rest in proportion to the actual all-worker profile cost. The ceiling is the
// existing admission horizon, not extra time: the caller always selects the
// original child context and worker result. Rest itself proves no progress.
func orderedSetObservationWait(cost time.Duration) time.Duration {
	if cost >= 15*time.Second/2 {
		return 15 * time.Second
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

func TestOrderedSetWorkerSorting(t *testing.T) {
	const owned = "# labels: {\"stuntgres.ordered-set-cancel\":\"worker\"}\n"
	const other = "# labels: {\"stuntgres.ordered-set-cancel\":\"other\"}\n"
	const sort = "# 0x123 stuntgres/internal/engine/exec.sortOrderedAggregate.func1+0x10 /src/aggregate_query.go:477\n"
	const idle = "# 0x456 runtime.chanrecv+0x20 /src/runtime/chan.go:1\n"
	for _, tc := range []struct {
		name, sample, worker string
		want                 bool
	}{
		{"owned-sort", "1 @ 0x123\n" + owned + sort, "worker", true},
		{"other-sort", "1 @ 0x123\n" + other + sort, "worker", false},
		{"separate-records", "1 @ 0x123\n" + other + sort + "\n1 @ 0x456\n" + owned + idle, "worker", false},
		{"owned-idle", "1 @ 0x456\n" + owned + idle, "worker", false},
		{"no-label", "1 @ 0x123\n" + sort, "worker", false},
		{"malformed-label", "1 @ 0x123\n# labels: {broken}\n" + sort, "worker", false},
		{"launcher-only", "1 @ 0x456\n" + owned + "# 0x456 reference_test.orderedSetActiveCancel+0x10 /src/test.go:1\n", "worker", false},
		{"sort-name-prefix", "1 @ 0x123\n" + owned + "# 0x123 stuntgres/internal/engine/exec.sortOrderedAggregate.func10+0x10 /src/test.go:1\n", "worker", false},
		{"unframed-sort-text", "1 @ 0x123\n" + owned + "stuntgres/internal/engine/exec.sortOrderedAggregate.func1\n", "worker", false},
		{"empty", "", "worker", false},
		{"empty-worker", "1 @ 0x123\n" + owned + sort, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderedSetWorkerFrame([]byte(tc.sample), tc.worker, orderedSetSortFrame); got != tc.want {
				t.Fatalf("sorting=%t want %t", got, tc.want)
			}
		})
	}
}

func TestOrderedSetWorkerProfile(t *testing.T) {
	// Exercise the actual runtime's label/frame text format, not only invented
	// parser inputs. The callback frame is near the top even in deep test stacks.
	pprof.Do(context.Background(), pprof.Labels(orderedSetWorkerLabel, t.Name()), func(context.Context) {
		pc, _, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("missing profile callback frame")
		}
		frame := runtime.FuncForPC(pc).Name()
		var sample bytes.Buffer
		if err := pprof.Lookup("goroutine").WriteTo(&sample, 1); err != nil {
			t.Fatal(err)
		}
		if !orderedSetWorkerFrame(sample.Bytes(), t.Name(), frame) {
			t.Fatal("goroutine profile lost owned label or live callback frame")
		}
		if orderedSetWorkerFrame(sample.Bytes(), t.Name(), orderedSetSortFrame) {
			t.Fatal("profile callback supplied a sort witness")
		}
	})
}

func TestOrderedSetObservationWait(t *testing.T) {
	for _, tc := range []struct{ cost, want time.Duration }{
		{-1, 5 * time.Millisecond}, {0, 5 * time.Millisecond}, {time.Millisecond, 5 * time.Millisecond},
		{2500 * time.Microsecond, 5 * time.Millisecond}, {4 * time.Millisecond, 8 * time.Millisecond},
		{12 * time.Millisecond, 24 * time.Millisecond}, {12500 * time.Microsecond, 25 * time.Millisecond},
		{100 * time.Millisecond, 200 * time.Millisecond},
		{3354342562 * time.Nanosecond, 6708685124 * time.Nanosecond},
		{7500*time.Millisecond - time.Nanosecond, 15*time.Second - 2*time.Nanosecond},
		{7500 * time.Millisecond, 15 * time.Second},
		{time.Duration(1<<63 - 1), 15 * time.Second},
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
	if orderedSetObservationFailure(errors.Join(context.DeadlineExceeded, errors.New("wrapped"))) != orderedSetObservationFailure(context.DeadlineExceeded) {
		t.Fatal("wrapped worker deadline lost its category")
	}
	for _, tc := range []struct{ frame, want string }{
		{orderedSetSortFrame, "sort"},
		{"stuntgres/internal/engine/exec.(*Stage).Drain", "source"},
		{"stuntgres/internal/engine/exec.OpenJoinNode", "source"},
		{"stuntgres/internal/engine/exec.openAggregateNode", "unknown"},
		{"stuntgres/internal/engine/exec.OpenSortNode", "unknown"},
		{"", "unknown"},
	} {
		record := []byte("# 0x123 " + tc.frame + "+0x10 /src/query.go:1\n")
		if got := orderedSetObservationPhase(record); got != tc.want {
			t.Errorf("frame=%q phase=%s want=%s", tc.frame, got, tc.want)
		}
	}
}

