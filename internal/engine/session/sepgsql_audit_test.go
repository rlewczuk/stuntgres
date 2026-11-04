package session

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqlstate"
)

func auditDoneOperation(t *testing.T) (*Session, *Operation) {
	t.Helper()
	s := &Session{State: Live}
	s.Cond = sync.NewCond(&s.Mu)
	op, err := s.acquire(context.Background(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return s, op
}

func auditDonePolicy(t *testing.T, permissive bool, audit func(sepgsql.AuditEvent)) *sepgsql.Runtime {
	t.Helper()
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{
		Policy: sepgsql.Policy{
			ID: "audit-done", Version: "1", Contexts: []string{"subject", "target"},
			InitialContext: "subject", UnlabeledContext: "target",
		},
		Permissive: permissive, Audit: audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func auditDoneEvent(name string, permissive bool) sepgsql.AuditEvent {
	suffix := " permissive=0"
	if permissive {
		suffix = " permissive=1"
	}
	return sepgsql.AuditEvent{
		PolicyID: "audit-done", PolicyVersion: "1", Subject: "subject", Target: "target",
		Class: "db_table", Name: name, Required: []string{"select"}, Denied: []string{"select"}, Audited: []string{"select"},
		Permissive: permissive,
		Message: "SELinux: denied { select } scontext=subject tcontext=target tclass=db_table name=\"" + name + "\"" + suffix,
	}
}

func auditDoneWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("audit Done worker failed bounded join")
	}
}

func TestSepgsqlSimulationAuditDoneConcurrent(t *testing.T) {
	for _, mode := range []string{"no-provider", "enforcing", "permissive"} {
		t.Run(mode, func(t *testing.T) {
			s, op := auditDoneOperation(t)
			timerCalls := 0
			op.TimerCancel = func() { timerCalls++ }
			op.NoticeThreshold = LevelError
			var mu sync.Mutex
			var got, want []sepgsql.AuditEvent
			if mode != "no-provider" {
				permissive := mode == "permissive"
				policy := auditDonePolicy(t, permissive, func(event sepgsql.AuditEvent) {
					mu.Lock()
					got = append(got, event)
					mu.Unlock()
				})
				for _, name := range []string{"first", "second"} {
					allowed, event := policy.Decide("subject", "target", "db_table", name, sepgsql.Permission("db_table", "select"), false)
					if allowed != permissive || event == nil {
						t.Fatal("default-deny decision lost enforcing/permissive semantics", allowed, event)
					}
					want = append(want, auditDoneEvent(name, permissive))
					op.SepgsqlAudit(policy, *event)
				}
			}
			if len(op.Notices) != 0 || len(got) != 0 {
				t.Fatal("audit callback ran before Done or LOG ignored client filtering")
			}

			// Every caller starts from the same barrier, without ordering their
			// returns. Even an empty queue must not be drained by losing Done calls.
			const callers = 8
			start, done := make(chan struct{}), make(chan struct{})
			var ready, workers sync.WaitGroup
			ready.Add(callers)
			workers.Add(callers)
			for range callers {
				go func() {
					defer workers.Done()
					ready.Done()
					<-start
					op.Done()
				}()
			}
			ready.Wait()
			close(start)
			go func() { workers.Wait(); close(done) }()
			t.Cleanup(func() { auditDoneWait(t, done) })
			auditDoneWait(t, done)
			op.Done()
			if !op.Released || s.Leases != 0 || s.Timeouts.SQLLeases != 0 || len(s.Operations) != 0 || op.sepgsqlAudit != nil || timerCalls != 1 {
				t.Fatal("Done did not release operation and audit ownership once", op.Released, s.Leases, s.Timeouts.SQLLeases, len(s.Operations), len(op.sepgsqlAudit), timerCalls)
			}
			if op.Ctx.Err() != context.Canceled || len(op.Notices) != 0 {
				t.Fatal("Done lost cancellation or changed client LOG filtering", op.Ctx.Err(), op.Notices)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("audit batch lost order, exact fields or once-only delivery: got %#v want %#v", got, want)
			}
		})
	}
}

func TestSepgsqlSimulationAuditDoneDetached(t *testing.T) {
	s, op := auditDoneOperation(t)
	op.NoticeThreshold = LevelLog
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	delivered := make(chan sepgsql.AuditEvent, 2)
	policy := auditDonePolicy(t, false, func(event sepgsql.AuditEvent) {
		delivered <- event
		if event.Name != "first" {
			return
		}
		// No competitor has started yet: this directly checks that the
		// callback itself holds neither the session lock nor its old lease.
		if !s.Mu.TryLock() {
			t.Error("audit callback ran under Session.Mu")
		} else {
			if !op.Released || s.Leases != 0 || s.Timeouts.SQLLeases != 0 || len(s.Operations) != 0 || op.sepgsqlAudit != nil || op.Ctx.Err() != context.Canceled {
				t.Error("audit callback retained live operation ownership")
			}
			s.Mu.Unlock()
			// A sync.Once around callbacks would deadlock this valid reentry.
			op.Done()
		}
		close(entered)
		<-resume
	})
	var wantNotices []sqlstate.Error
	for _, name := range []string{"first", "second"} {
		allowed, event := policy.Decide("subject", "target", "db_table", name, sepgsql.Permission("db_table", "select"), false)
		if allowed || event == nil {
			t.Fatal("default-deny event missing", allowed, event)
		}
		op.SepgsqlAudit(policy, *event)
		wantNotices = append(wantNotices, sqlstate.Error{Severity: "LOG", SeverityUnlocalized: "LOG", Code: "00000", Message: auditDoneEvent(name, false).Message})
	}
	t.Cleanup(func() {
		release()
		auditDoneWait(t, done)
	})
	go func() {
		defer close(done)
		op.Done()
	}()
	auditDoneWait(t, entered)
	select {
	case <-done:
		t.Fatal("winning Done returned before its synchronous audit callback")
	default:
	}
	if len(delivered) != 1 {
		t.Fatal("audit batch advanced past blocked first callback", len(delivered))
	}

	// The batch now belongs only to the winning stack frame. Another Done
	// must return without waiting on that callback or touching its remaining event.
	otherDone := make(chan struct{})
	t.Cleanup(func() {
		release()
		auditDoneWait(t, otherDone)
	})
	go func() { defer close(otherDone); op.Done() }()
	auditDoneWait(t, otherDone)
	next, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal("detached audit prevented fresh operation admission", err)
	}
	next.Done()
	if !reflect.DeepEqual(op.DetachNotices(), wantNotices) || op.DetachNotices() != nil {
		t.Fatal("host audit delivery changed independent owned client LOG notices")
	}
	release()
	auditDoneWait(t, done)
	for _, name := range []string{"first", "second"} {
		select {
		case event := <-delivered:
			if !reflect.DeepEqual(event, auditDoneEvent(name, false)) {
				t.Fatalf("detached batch event %q changed: %#v", name, event)
			}
		default:
			t.Fatalf("completed Done lost detached batch event %q", name)
		}
	}
	if len(delivered) != 0 || s.Leases != 0 || s.Timeouts.SQLLeases != 0 || len(s.Operations) != 0 {
		t.Fatal("detached audit duplicated events or retained a lease")
	}
}
