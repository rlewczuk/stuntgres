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
	// Routes are serial and each owns one operation. The test's full name gives
	// its worker a distinct identity even when other packages are being tested.
	worker := t.Name()
	joined := false
	start := time.Now()
	var out fixture.RelationOutcome
	var samples int
	var captureTotal, captureMax time.Duration
	var joinOwner orderedSetCancellationJoin
	join := func() {
		if joined || joinOwner.attempted {
			return
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		out, joined = joinOwner.receive(done, timer.C)
	}
	// Cleanup precedes outer statement/connection cleanup even when an assertion
	// calls Fatal. Keep one terminal join allowance and the eventual real outcome.
	defer func() {
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
			t.Logf("ordered observation: worker=%q samples=%d capture_total=%s capture_max=%s elapsed=%s joined=%t parent_err=%v child_err=%v actual_error=%T %v rows=%t result=%t stmt=%t",
				worker, samples, captureTotal, captureMax, time.Since(start), joined, p.Ctx.Err(), ctx.Err(), out.Err, out.Err, out.Rows != nil, out.Result != nil, out.Stmt != nil)
		}
	}()
	go pprof.Do(ctx, pprof.Labels(orderedSetWorkerLabel, worker), func(ctx context.Context) {
		done <- operation(ctx)
	})
	early := func(r fixture.RelationOutcome) {
		out = r
		joined = true
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
			t.Fatal("observer deadline without observed sort", ctx.Err())
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
		if orderedSetWorkerFrame(sample.Bytes(), worker, orderedSetSortFrame) {
			if ctx.Err() != nil || p.Ctx.Err() != nil {
				t.Fatal("witness without live cancellation budget")
			}
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
		poll.Reset(orderedSetObservationWait(cost))
		select {
		case <-ctx.Done():
		case r := <-done:
			early(r)
		case <-poll.C:
		}
	}
}

// A goroutine profile groups live stacks by PCs AND labels. Require the owned
// operation's exact label and the actual comparator in the same record, never a
// launcher name or a sort frame belonging to another operation. Profile labels
// avoid depending on a deep wrapper frame surviving the profiler's stack limit.
func orderedSetWorkerFrame(sample []byte, worker, wantFrame string) bool {
	if worker == "" {
		return false
	}
	for len(sample) > 0 {
		block, rest, found := bytes.Cut(sample, []byte("\n\n"))
		var owned, sorting bool
		for _, line := range bytes.Split(block, []byte("\n")) {
			if labels, ok := bytes.CutPrefix(line, []byte("# labels: ")); ok {
				var values map[string]string
				if json.Unmarshal(labels, &values) == nil {
					owned = values[orderedSetWorkerLabel] == worker
				}
				continue
			}
			fields := bytes.Fields(line)
			if len(fields) < 3 || !bytes.Equal(fields[0], []byte("#")) {
				continue
			}
			function, _, _ := bytes.Cut(fields[2], []byte("+"))
			if bytes.Equal(function, []byte(wantFrame)) {
				sorting = true
			}
		}
		if owned && sorting {
			return true
		}
		if !found {
			break
		}
		sample = rest
	}
	return false
}

// Keep the existing bounded, cost-sensitive observation pacing.
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
	if orderedSetObservationFailure(errors.Join(context.DeadlineExceeded, errors.New("wrapped"))) != orderedSetObservationFailure(context.DeadlineExceeded) {
		t.Fatal("wrapped worker deadline lost its category")
	}
}

