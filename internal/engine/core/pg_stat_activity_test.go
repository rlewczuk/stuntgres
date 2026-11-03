package core_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func activityOwnerExecute(e *core.Engine, s *session.Session, ctx context.Context, q string) error {
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	op.OriginalSQL, op.SQLAvailable = q, true
	statements, err := parser.Parse(q)
	if err != nil {
		return err
	}
	for _, statement := range statements {
		result, err := enginetest.ExecuteFrame(e, op, statement, nil)
		if result.Rows != nil {
			values := make([]sqltype.Value, len(result.Rows.Schema()))
			if err == nil {
				for {
					present, nextErr := result.Rows.Next(ctx, values)
					if nextErr != nil {
						err = nextErr
						break
					}
					if !present {
						break
					}
				}
			}
			if closeErr := result.Rows.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func activityOwnerRun(t *testing.T, e *core.Engine, s *session.Session, commands ...string) {
	t.Helper()
	for _, q := range commands {
		if err := activityOwnerExecute(e, s, t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
}

// This cursor helper retains the actual typed producer output, rather than
// proving only that a monitoring SELECT happened to execute successfully.
func activityOwnerValues(t *testing.T, e *core.Engine, s *session.Session, q string) [][]sqltype.Value {
	t.Helper()
	op, _, err := s.AcquireCommand(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	op.OriginalSQL, op.SQLAvailable = q, true
	statements, err := parser.Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal("one monitoring statement", err)
	}
	result, err := enginetest.ExecuteFrame(e, op, statements[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows == nil {
		t.Fatal("missing monitoring rows")
	}
	defer result.Rows.Close()
	var rows [][]sqltype.Value
	for {
		values := make([]sqltype.Value, len(result.Rows.Schema()))
		present, err := result.Rows.Next(t.Context(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		rows = append(rows, values)
	}
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPGStatActivityPublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	token := s.SessionToken
	first, ok := e.Statistics.CaptureActivities()[token]
	if !ok || first.BackendStart.IsZero() || first.QueryAvailable || first.Principal != s.CountedAuthenticated {
		t.Fatal("registration publication", first)
	}
	activityOwnerRun(t, e, s, "SET application_name='owned'", "BEGIN", "SAVEPOINT keep", "SET LOCAL application_name='local'")
	if a := e.Statistics.CaptureActivities()[token]; a.Application != "local" || a.State != "idle in transaction" || a.TransactionStart.IsZero() {
		t.Fatal("local publication", a)
	}
	if err := activityOwnerExecute(e, s, t.Context(), "SELECT 1/0"); err == nil {
		t.Fatal("missing real failure")
	}
	activityOwnerRun(t, e, s, "ROLLBACK TO keep")
	if a := e.Statistics.CaptureActivities()[token]; a.TransactionStart.IsZero() {
		t.Fatal("savepoint cleared live parent start", a)
	}
	activityOwnerRun(t, e, s, "ROLLBACK")
	if a := e.Statistics.CaptureActivities()[token]; a.Application != "owned" || !a.TransactionStart.IsZero() {
		t.Fatal("rollback restoration publication", a)
	}
	activityOwnerRun(t, e, s, "CREATE FUNCTION activity_owner_nested() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT 1; SELECT 7'", "SELECT activity_owner_nested() /* top-level */")
	before := e.Statistics.CaptureActivities()[token]
	if before.Query != "SELECT activity_owner_nested() /* top-level */" || before.State != "idle" || before.QueryID == 0 {
		t.Fatal("nested source/ID publication", before)
	}
	e.Statistics.Reset(time.Now())
	e.Statistics.Replace(time.Now(), nil)
	if after := e.Statistics.CaptureActivities()[token]; after != before || !after.BackendStart.Equal(first.BackendStart) {
		t.Fatal("cumulative reset replaced live activity", before, after)
	}
	detached := e.Statistics.CaptureActivities()
	delete(detached, token)
	if _, ok := e.Statistics.CaptureActivities()[token]; !ok {
		t.Fatal("capture aliases provider")
	}
	// A leaf-level exact-command witness cannot clear the next admitted wait.
	current := ids.CommandIdentity{Transaction: before.Command.Transaction, Command: before.Command.Command + 1}
	e.Statistics.Command(token, current, "SELECT pg_sleep(1)", time.Now(), true)
	e.Statistics.ActivityQueryID(token, current, -9223372036854775807)
	e.Statistics.ActivityQueryID(token, before.Command, 99)
	e.Statistics.ActivityWait(token, current, true)
	e.Statistics.ActivityWait(token, before.Command, false)
	if a := e.Statistics.CaptureActivities()[token]; a.Wait != "PgSleep" || a.QueryID != -9223372036854775807 {
		t.Fatal("stale command replaced wait/signed query ID", a)
	}
	e.Statistics.Command(token, current, "hidden source", time.Now(), false)
	e.Statistics.ActivityWait(token, current, true)
	e.Statistics.ActivityQueryID(token, current, 17)
	if a := e.Statistics.CaptureActivities()[token]; !a.Disabled || !a.Active || a.Query != "" || !a.Start.IsZero() || a.QueryID != 0 || a.WaitType != "Timeout" || a.Wait != "PgSleep" {
		t.Fatal("disabled activity lost real wait or leaked query facts", a)
	}
	e.Statistics.ActivityWait(token, current, false)
	if a := e.Statistics.CaptureActivities()[token]; a.Wait != "" || a.WaitType != "" {
		t.Fatal("disabled wait cleanup", a)
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.Statistics.Command(token, current, "late", time.Now(), true)
	if _, ok := e.Statistics.CaptureActivities()[token]; ok {
		t.Fatal("late publication resurrected retired identity")
	}
	t.Run("principal_provenance", activityRoleAuthority)
}

func TestPGStatActivityXIDAuthority(t *testing.T) {
	e := engine.New()
	writer := enginetest.StartSession(t, e, nil)
	reader := enginetest.StartSession(t, e, nil)
	monitor := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, writer, "CREATE TABLE activity_authority(v integer)")
	e.Mu.Lock()
	e.Visibility.SetLastAssignedXIDLocked(1<<32 | 40)
	e.Mu.Unlock()
	activityOwnerRun(t, e, writer, "BEGIN")
	beforeAssignment := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT backend_xid,backend_xmin FROM pg_stat_activity WHERE pid=%d", writer.SessionToken.ID()))
	if len(beforeAssignment) != 1 || !beforeAssignment[0][0].IsNull() || !beforeAssignment[0][1].IsNull() {
		t.Fatal("read-only high-epoch BEGIN invented public identity", beforeAssignment)
	}
	activityOwnerRun(t, e, writer, "INSERT INTO activity_authority VALUES(7)")
	ref := writer.Transactions.Ref
	e.Mu.Lock()
	entry := e.Visibility.Entries[ref]
	assigned := e.Visibility.AssignedXIDLocked(ref)
	e.Mu.Unlock()
	if assigned != 41 || entry.FullXID != 1<<32|41 || entry.XID == uint64(assigned) {
		t.Fatal("canonical/writer-order distinction", assigned, entry)
	}
	renderedWriter := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT backend_xid,backend_xmin FROM pg_stat_activity WHERE pid=%d", writer.SessionToken.ID()))
	if len(renderedWriter) != 1 || renderedWriter[0][0].IsNull() || renderedWriter[0][0].Type().ID() != sqltype.Xid || !renderedWriter[0][1].IsNull() {
		t.Fatal("rendered canonical assigned XID/withdrawn RC horizon", renderedWriter)
	}
	if xid, err := renderedWriter[0][0].XID(); err != nil || xid != 41 {
		t.Fatal("rendered high-epoch low32 XID", xid, err)
	}
	activityOwnerRun(t, e, writer, "COMMIT")
	afterCommit := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT backend_xid,backend_xmin FROM pg_stat_activity WHERE pid=%d", writer.SessionToken.ID()))
	if len(afterCommit) != 1 || !afterCommit[0][0].IsNull() || !afterCommit[0][1].IsNull() {
		t.Fatal("completed high-epoch writer retained public identity", afterCommit)
	}
	activityOwnerRun(t, e, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ", "SELECT v FROM activity_authority")
	rr := reader.Transactions.Ref
	e.Mu.Lock()
	retained := e.Visibility.Entries[rr]
	snapshot := reader.Transactions.CurrentSnapshot
	frontier := e.Visibility.LastAssignedXID()
	e.Mu.Unlock()
	if snapshot == nil || retained.ActivityFullXmin != snapshot.Xmin() || retained.ActivityFullXmin>>32 != 1 || retained.AssignedXID != 0 || retained.ActivityFullXmin == retained.Xmin {
		t.Fatal("advertised public horizon authority", retained, snapshot)
	}
	renderedReader := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT backend_xid,backend_xmin FROM pg_stat_activity WHERE pid=%d", reader.SessionToken.ID()))
	if len(renderedReader) != 1 || !renderedReader[0][0].IsNull() || renderedReader[0][1].IsNull() || renderedReader[0][1].Type().ID() != sqltype.Xid {
		t.Fatal("rendered unassigned reader/accepted horizon", renderedReader)
	}
	if xmin, err := renderedReader[0][1].XID(); err != nil || xmin != uint32(retained.ActivityFullXmin) {
		t.Fatal("rendered high-epoch low32 xmin", xmin, retained.ActivityFullXmin, err)
	}
	e.Mu.Lock()
	after := e.Visibility.Entries[rr]
	now := e.Visibility.LastAssignedXID()
	e.Mu.Unlock()
	if after != retained || now != frontier {
		t.Fatal("monitoring acquired/assigned visibility", retained, after, frontier, now)
	}
	activityOwnerRun(t, e, reader, "ROLLBACK", "BEGIN", "SELECT v FROM activity_authority")
	e.Mu.Lock()
	transient := e.Visibility.Entries[reader.Transactions.Ref]
	e.Mu.Unlock()
	if transient.ActivityFullXmin != 0 {
		t.Fatal("RC completion retained transient horizon", transient)
	}
	activityOwnerRun(t, e, reader, "ROLLBACK")
	afterReaderEnd := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT backend_xid,backend_xmin FROM pg_stat_activity WHERE pid=%d", reader.SessionToken.ID()))
	if len(afterReaderEnd) != 1 || !afterReaderEnd[0][0].IsNull() || !afterReaderEnd[0][1].IsNull() {
		t.Fatal("completed reader retained public identity", afterReaderEnd)
	}
	// Clipping is byte capacity, independent of original source/current_query.
	source := "SELECT 1 /*" + strings.Repeat("é", 600) + "*/"
	activityOwnerRun(t, e, writer, source)
	if a := e.Statistics.CaptureActivities()[writer.SessionToken]; len(a.Query) > 1023 || !strings.HasPrefix(source, a.Query) {
		t.Fatal("activity clipping", len(a.Query))
	}
	t.Run("safe_candidates", activitySafeCandidateAuthority)
	t.Run("nested_horizons", activityNestedHorizonAuthority)
}

func TestPGStatActivityConcurrentCapture(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	waiter := enginetest.StartSession(t, e, nil)
	viewer := enginetest.StartSession(t, e, nil)
	monitor := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, holder, "CREATE TABLE activity_concurrent(v integer)", "BEGIN", "LOCK TABLE activity_concurrent IN ACCESS EXCLUSIVE MODE")
	activityOwnerRun(t, e, waiter, "BEGIN")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	q := "LOCK TABLE activity_concurrent IN ACCESS EXCLUSIVE MODE"
	go func() { done <- activityOwnerExecute(e, waiter, ctx, q) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	deadline := time.Now().Add(enginetest.Liveness)
	for {
		if _, ok := e.Locks.ActivityWaits()[waiter.SessionToken]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manager wait barrier")
		}
		runtime.Gosched()
	}
	a := e.Statistics.CaptureActivities()[waiter.SessionToken]
	if !a.Active || a.Query != q || a.QueryID == 0 {
		t.Fatal("query publication did not precede actual wait", a)
	}
	var reader statistics.Reader
	cut := reader.Activities(&e.Statistics)
	stop := make(chan struct{})
	captured := make(chan struct{})
	viewCaptured := make(chan struct{})
	var captureErr error
	captureSQL := fmt.Sprintf("SELECT pid,wait_event FROM pg_stat_activity WHERE pid=%d", waiter.SessionToken.ID())
	go func() {
		defer close(captured)
		firstView := true
		for {
			select {
			case <-stop:
				return
			default:
				e.Statistics.CaptureActivities()
				e.Locks.ActivityWaits()
				if err := activityOwnerExecute(e, viewer, t.Context(), captureSQL); err != nil {
					captureErr = err
					return
				}
				if firstView {
					close(viewCaptured)
					firstView = false
				}
			}
		}
	}()
	stopped := false
	stopCapture := func() {
		if !stopped {
			close(stop)
			<-captured
			stopped = true
		}
	}
	defer stopCapture()
	select {
	case <-viewCaptured:
	case <-captured:
		t.Fatal("initial actual concurrent view capture", captureErr)
	}
	e.Statistics.Reset(time.Now())
	e.Statistics.Replace(time.Now(), nil)
	cancel()
	err := <-done
	joined = true
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled owner result", err)
	}
	if _, ok := e.Locks.ActivityWaits()[waiter.SessionToken]; ok {
		t.Fatal("canceled request still live")
	}
	if got := reader.Activities(&e.Statistics)[waiter.SessionToken]; got != cut[waiter.SessionToken] || !got.Active {
		t.Fatal("reader lost independent cut", got)
	}
	if live := e.Statistics.CaptureActivities()[waiter.SessionToken]; live.Active || live.Wait != "" || live.State != "idle in transaction (aborted)" {
		t.Fatal("terminal publication", live)
	}
	activityOwnerRun(t, e, waiter, "ROLLBACK")
	nextCtx, nextCancel := context.WithCancel(t.Context())
	defer nextCancel()
	nextDone := make(chan error, 1)
	nextJoined := false
	nextSQL := "SELECT pg_sleep(600) /* actual successor wait */"
	go func() { nextDone <- activityOwnerExecute(e, waiter, nextCtx, nextSQL) }()
	defer func() {
		nextCancel()
		if !nextJoined {
			<-nextDone
		}
	}()
	deadline = time.Now().Add(enginetest.Liveness)
	var successor statistics.Activity
	for {
		successor = e.Statistics.CaptureActivities()[waiter.SessionToken]
		if successor.WaitType == "Timeout" && successor.Wait == "PgSleep" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual successor sleep barrier", successor)
		}
		runtime.Gosched()
	}
	if successor.Command == a.Command || successor.Command.Transaction == a.Command.Transaction || successor.Query != nextSQL || successor.QueryID == 0 {
		t.Fatal("successor did not own a fresh actual command", a, successor)
	}
	e.Statistics.ActivityWait(waiter.SessionToken, a.Command, false)
	e.Statistics.ActivityQueryID(waiter.SessionToken, a.Command, 99)
	if guarded := e.Statistics.CaptureActivities()[waiter.SessionToken]; guarded.Wait != "PgSleep" || guarded.QueryID != successor.QueryID {
		t.Fatal("stale completion cleared actual successor", guarded, successor)
	}
	nextRows := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT state,wait_event_type,wait_event,query FROM pg_stat_activity WHERE pid=%d", waiter.SessionToken.ID()))
	if len(nextRows) != 1 {
		t.Fatal("actual successor view membership", nextRows)
	}
	for i, want := range []string{"active", "Timeout", "PgSleep", nextSQL} {
		if text, err := nextRows[0][i].Text(); err != nil || text != want {
			t.Fatal("actual successor view ownership", i, text, want, err)
		}
	}
	// Drain the physical owner while real source captures are still running.
	waiter.RequestEnd(session.CleanClose)
	if err := waiter.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
	nextErr := <-nextDone
	nextJoined = true
	if nextErr == nil {
		t.Fatal("retired successor wait succeeded")
	}
	e.Statistics.ActivityWait(waiter.SessionToken, successor.Command, false)
	e.Statistics.Command(waiter.SessionToken, a.Command, "late retired callback", time.Now(), true)
	if _, ok := e.Statistics.CaptureActivities()[waiter.SessionToken]; ok {
		t.Fatal("retired identity resurrected")
	}
	if rows := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT pid FROM pg_stat_activity WHERE pid=%d", waiter.SessionToken.ID())); len(rows) != 0 {
		t.Fatal("retired identity in fresh view", rows)
	}
	if got := reader.Activities(&e.Statistics)[waiter.SessionToken]; got != cut[waiter.SessionToken] || !got.Active {
		t.Fatal("retirement mutated detached activity cut", got)
	}
	fresh := enginetest.StartSession(t, e, nil)
	activityOwnerRun(t, e, fresh, "SELECT 7 /* new physical successor */")
	if fresh.SessionToken == waiter.SessionToken {
		t.Fatal("physical successor reused retired token")
	}
	freshRows := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT pid,query FROM pg_stat_activity WHERE pid=%d", fresh.SessionToken.ID()))
	if len(freshRows) != 1 {
		t.Fatal("fresh physical successor membership", freshRows)
	}
	if source, err := freshRows[0][1].Text(); err != nil || source != "SELECT 7 /* new physical successor */" {
		t.Fatal("fresh physical successor source", source, err)
	}
	activityOwnerRun(t, e, fresh, "BEGIN")
	grantCtx, grantCancel := context.WithCancel(t.Context())
	defer grantCancel()
	grantDone := make(chan error, 1)
	grantJoined := false
	go func() { grantDone <- activityOwnerExecute(e, fresh, grantCtx, q+" /* physical successor grant */") }()
	defer func() {
		grantCancel()
		if !grantJoined {
			<-grantDone
		}
	}()
	deadline = time.Now().Add(enginetest.Liveness)
	for {
		if _, ok := e.Locks.ActivityWaits()[fresh.SessionToken]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical successor pending barrier")
		}
		runtime.Gosched()
	}
	grantRows := activityOwnerValues(t, e, monitor, fmt.Sprintf("SELECT state,wait_event_type,wait_event FROM pg_stat_activity WHERE pid=%d", fresh.SessionToken.ID()))
	if len(grantRows) != 1 {
		t.Fatal("physical successor actual wait membership", grantRows)
	}
	for i, want := range []string{"active", "Lock", "relation"} {
		if text, err := grantRows[0][i].Text(); err != nil || text != want {
			t.Fatal("physical successor actual queued wait", i, text, want, err)
		}
	}
	activityOwnerRun(t, e, holder, "ROLLBACK")
	grantErr := <-grantDone
	grantJoined = true
	if grantErr != nil {
		t.Fatal("concurrent physical successor grant", grantErr)
	}
	if _, ok := e.Locks.ActivityWaits()[fresh.SessionToken]; ok {
		t.Fatal("successful grant retained queue ownership")
	}
	activityOwnerRun(t, e, fresh, "ROLLBACK")
	stopCapture()
	if captureErr != nil {
		t.Fatal("concurrent actual view capture", captureErr)
	}
}
