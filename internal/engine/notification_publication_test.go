package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Internal setup uses actual statement/transaction owners, not public SQL or a
// replacement publisher. Assertions below concern retained roots and leases.
func notificationUnitUtility(t *testing.T, s *Session, stmt parser.Statement) {
	t.Helper()
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := engineOf(s).StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Execute(stmt, 0, nil)
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
}
func notificationUnitReceive(t *testing.T, s *Session) Notification {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	op, err := s.AcquireActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	event, err := engineOf(s).WaitForNotification(op)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func notificationUnitListen(t *testing.T, s *Session) {
	notificationUnitUtility(t, s, &parser.Listen{Channel: parser.Identifier{Text: "unit"}})
}
func notificationUnitNotify(t *testing.T, s *Session, payload string) {
	notificationUnitUtility(t, s, &parser.Notify{Channel: parser.Identifier{Text: "unit"}, Payload: payload})
}

func TestNotificationPublicationAtomicity(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		for _, failure := range []string{"success", "cancel", "epoch", "xid"} {
			name := failure
			if catalog {
				name += "-catalog"
			}
			t.Run(name, func(t *testing.T) {
				e := New()
				receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
				notificationUnitListen(t, receiver)
				ref := transactionBegin(t, sender)
				sender.Transactions.notificationEvents = []Notification{{PID: uint64(sender.SessionToken.ID()), Channel: "unit", Payload: "published"}}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := sender.AcquireActivity(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				var changes *catalogChanges
				if catalog {
					changes = session.NewJournal()
				}
				original, frontier, root := e.catalog.state, e.visibility.CommitSeq(), payloadOf(receiver).notifications.state
				switch failure {
				case "cancel":
					cancel()
				case "epoch":
					next := *e.catalog.state
					next.SetEpoch(next.Epoch() + 1)
					e.catalog.state = &next
					original = e.catalog.state
				case "xid":
					e.visibility.SetLastAssignedXIDLocked(^uint64(0))
				}
				err = e.publishRowsPrepared(op, ref, nil, nil, changes, nil, nil, nil)
				if failure != "success" {
					if err == nil {
						t.Fatal("failed preflight published")
					}
					if payloadOf(receiver).notifications.state != root || e.catalog.state != original || e.visibility.CommitSeq() != frontier {
						t.Fatal("partial catalog/notification installation")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if e.visibility.LastAssignedXID() == 0 {
					t.Fatal("notification-only publication did not assign XID")
				}
				wantFrontier := frontier
				if catalog {
					wantFrontier++
				}
				if e.visibility.CommitSeq() != wantFrontier {
					t.Fatal("notification publication invented row commit sequence", e.visibility.CommitSeq(), wantFrontier)
				}
				if got := notificationUnitReceive(t, receiver); got.Payload != "published" {
					t.Fatal(got)
				}
			})
		}
	}
}

// Context.Err is the existing cooperative final preflight seam. No other
// worker owns Engine.mu in this unit: a failed TryLock identifies the publisher's
// own lock, so this one root replacement occurs at that lock-owned boundary.
type notificationRootContext struct {
	context.Context
	engine      *Engine
	resource    *sessionNotifications
	replacement *notificationState
	changed     bool
}

func (c *notificationRootContext) Err() error {
	if !c.changed {
		if c.engine.mu.TryLock() {
			c.engine.mu.Unlock()
		} else {
			c.resource.state = c.replacement
			c.changed = true
		}
	}
	return c.Context.Err()
}
func TestNotificationPublicationRoots(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "catalog"}[catalog], func(t *testing.T) {
			e := New()
			r, s := sessionStart(t, e, nil), sessionStart(t, e, nil)
			notificationUnitListen(t, r)
			ref := transactionBegin(t, s)
			s.Transactions.notificationEvents = []Notification{{PID: uint64(s.SessionToken.ID()), Channel: "unit", Payload: "after"}}
			replacement := *payloadOf(r).notifications.state
			replacement.ready = replacement.ready.append([]Notification{{PID: 999, Channel: "unit", Payload: "before"}})
			ctx := &notificationRootContext{Context: context.Background(), engine: e, resource: &payloadOf(r).notifications, replacement: &replacement}
			op := transactionOperation(t, s)
			op.Ctx = ctx
			var changes *catalogChanges
			if catalog {
				changes = session.NewJournal()
			}
			if err := e.publishRowsPrepared(op, ref, nil, nil, changes, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if !ctx.changed {
				t.Fatal("stale-root boundary not exercised")
			}
			for _, want := range []string{"before", "after"} {
				if got := notificationUnitReceive(t, r); got.Payload != want {
					t.Fatal("retry lost or reordered root", got, want)
				}
			}
			e.mu.Lock()
			queue := payloadOf(r).notifications.state.ready
			e.mu.Unlock()
			if queue.front != nil || queue.back != nil {
				t.Fatal("retry duplicated delivery")
			}
		})
	}
	e := New()
	r, s := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, r)
	event := []Notification{{PID: uint64(s.SessionToken.ID()), Channel: "unit", Payload: "candidate"}}
	for _, change := range []string{"busy", "dequeue", "registration", "retire"} {
		t.Run(change, func(t *testing.T) {
			candidate := e.prepareNotificationPublication(s, nil, event)
			e.mu.Lock()
			switch change {
			case "busy":
				payloadOf(r).notifications.busy = !payloadOf(r).notifications.busy
			case "dequeue":
				next := *payloadOf(r).notifications.state
				payloadOf(r).notifications.state = &next
			case "registration":
				delete(e.notifications, r.SessionToken)
			case "retire":
				payloadOf(r).notifications.ended = true
			}
			unchanged := e.notificationPublicationUnchangedLocked(candidate)
			if change == "registration" {
				e.notifications[r.SessionToken] = &payloadOf(r).notifications
			}
			if change == "retire" {
				payloadOf(r).notifications.ended = false
			}
			e.mu.Unlock()
			if unchanged {
				t.Fatal("accepted stale recipient capture")
			}
		})
	}
}

func TestNotificationSavepointRoots(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	transactionBegin(t, s)
	owner := s.Transactions
	owner.mu.Lock()
	defer owner.mu.Unlock()
	parent := Notification{PID: uint64(s.SessionToken.ID()), Channel: "unit", Payload: "parent"}
	owner.notificationActions = make([]notificationAction, 1, 8)
	owner.notificationActions[0] = notificationAction{channel: "unit", listen: true}
	owner.notificationEvents = make([]Notification, 1, 8)
	owner.notificationEvents[0] = parent
	mark := captureSavepoint(owner, "root")
	if cap(mark.notificationActions) != len(mark.notificationActions) || cap(mark.notificationEvents) != len(mark.notificationEvents) {
		t.Fatal("savepoint retained mutable append capacity")
	}
	child := Notification{PID: parent.PID, Channel: "unit", Payload: "child"}
	owner.notificationEvents = appendNotificationEvents(owner.notificationEvents, []Notification{parent, child})
	owner.notificationActions = append(owner.notificationActions, notificationAction{all: true})
	restoreSavepoint(owner, mark, mark.settings.clone())
	owner.notificationEvents = appendNotificationEvents(owner.notificationEvents, []Notification{parent, child})
	if !reflect.DeepEqual(owner.notificationEvents, []Notification{parent, child}) || len(owner.notificationActions) != 1 {
		t.Fatal("rollback roots/dedup ancestry corrupted")
	}
	if len(mark.notificationEvents) != 1 || mark.notificationEvents[0] != parent {
		t.Fatal("append overwrote retained parent")
	}
}

func TestNotificationIdentityAndAssignment(t *testing.T) {
	e := New()
	s, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(1)<<48 | 17})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if e.notifications != nil || payloadOf(s).notifications.state != nil || payloadOf(s).notifications.wake != nil {
		t.Fatal("unused feature allocated queue state")
	}
	before := e.visibility.LastAssignedXID()
	notificationUnitListen(t, s)
	if e.visibility.LastAssignedXID() != before {
		t.Fatal("subscription-only commit assigned XID")
	}
	notificationUnitNotify(t, s, "wide")
	if got := notificationUnitReceive(t, s); got.PID != uint64(s.SessionToken.ID()) || got.PID <= uint64(^uint32(0)) {
		t.Fatal("sender identity narrowed", got)
	}
	if e.visibility.LastAssignedXID() != before+1 {
		t.Fatal("outgoing-only commit missing XID")
	}
}

func TestNotificationPreparedOriginProgress(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rows     bool
		progress bool
	}{
		{name: "empty-held"},
		{name: "rows-held", rows: true},
		{name: "empty-progress", progress: true},
		{name: "rows-progress", rows: true, progress: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := tc.rows
			e := New()
			origin, finisher, receiver := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			notificationUnitListen(t, finisher)
			notificationUnitListen(t, receiver)
			if rows {
				sessionSQL(t, origin, "CREATE TABLE prepared_notification_rows(v INT)")
			}
			transactionBegin(t, origin)
			if rows {
				sessionSQL(t, origin, "INSERT INTO prepared_notification_rows VALUES (7)")
			}
			if err := preparedUnitControl(t, e, origin, parser.KindPrepareTransaction, "notification-progress"); err != nil {
				t.Fatal(err)
			}
			live := transactionBegin(t, origin)
			notificationUnitNotify(t, origin, "uncommitted")
			notificationUnitUtility(t, origin, &parser.Listen{Channel: parser.Identifier{Text: "origin-only"}})
			notificationUnitUtility(t, origin, &parser.Unlisten{Channel: parser.Identifier{Text: "unit"}})

			// Held cases retain origin admission until completion returns;
			// progress cases merge a statement into the live owner concurrently.
			originOp := transactionOperation(t, origin)
			scope, err := e.StartStatement(originOp)
			if err != nil {
				t.Fatal(err)
			}
			defer originOp.Done()
			defer scope.Finish(nil)
			finishOp := transactionOperation(t, finisher)
			defer finishOp.Done()
			start, completed := make(chan struct{}), make(chan error, 1)
			go func() {
				<-start
				completed <- e.completePrepared(finishOp, "notification-progress", true)
			}()
			close(start)
			// Progress the actual origin statement concurrently with detached
			// completion; its private event must remain with this incarnation.
			_, executeErr := scope.Execute(&parser.Notify{Channel: parser.Identifier{Text: "unit"}, Payload: "in-flight"}, 0, nil)
			if tc.progress {
				if err := scope.Finish(executeErr); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-completed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("prepared completion waited for live origin admission")
			}
			if err := scope.Finish(executeErr); err != nil {
				t.Fatal(err)
			}
			originOp.Done()
			finishOp.Done()
			if rows {
				transactionRows(t, finisher, "SELECT v FROM prepared_notification_rows", 7)
			}
			// A committed positive sentinel checks absence without a timeout.
			notificationUnitNotify(t, finisher, "before-origin-end")
			for _, recipient := range []*Session{receiver, finisher} {
				if got := notificationUnitReceive(t, recipient); got.Payload != "before-origin-end" || got.PID != uint64(finisher.SessionToken.ID()) {
					t.Fatal("detached completion published live-origin effects", got)
				}
			}
			e.mu.Lock()
			_, kept := payloadOf(finisher).notifications.state.channels["unit"]
			_, stolen := payloadOf(finisher).notifications.state.channels["origin-only"]
			e.mu.Unlock()
			if !kept || stolen {
				t.Fatal("detached completion changed finisher subscriptions")
			}
			transactionEnd(t, origin, live, false)
			notificationUnitNotify(t, finisher, "after-origin-rollback")
			for _, recipient := range []*Session{receiver, finisher} {
				if got := notificationUnitReceive(t, recipient); got.Payload != "after-origin-rollback" {
					t.Fatal("origin rollback leaked an event", got)
				}
			}
		})
	}
}

func TestNotificationEffectsOwnership(t *testing.T) {
	e := New()
	origin, other := sessionStart(t, e, nil), sessionStart(t, e, nil)
	old := transactionBegin(t, origin)
	transactionEnd(t, origin, old, false)
	live := transactionBegin(t, origin)
	notificationUnitNotify(t, origin, "live")
	op := transactionOperation(t, origin)
	defer op.Done()
	for _, ref := range []ids.TxRef{old, e.catalog.Domain.Tx(other.SessionToken, live.ID(), 0, other.Transactions, 0)} {
		actions, events, err := notificationEffects(op, ref, nil, nil)
		if err == nil || len(actions) != 0 || len(events) != 0 {
			t.Fatal("foreign or stale transaction exposed live effects", err, actions, events)
		}
	}
	_, events, err := notificationEffects(op, live, nil, nil)
	if err != nil || len(events) != 1 || events[0].Payload != "live" {
		t.Fatal("exact live owner lost its effects", err, events)
	}
}
