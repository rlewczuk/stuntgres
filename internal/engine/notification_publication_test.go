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
func notificationUnitUtility(t *testing.T, s *session.Session, stmt parser.Statement) {
	t.Helper()
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := EngineOf(s).StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Execute(stmt, 0, nil)
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
}
func notificationUnitReceive(t *testing.T, s *session.Session) session.Notification {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	op, err := s.AcquireActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	event, err := EngineOf(s).WaitForNotification(op)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func notificationUnitListen(t *testing.T, s *session.Session) {
	notificationUnitUtility(t, s, &parser.Listen{Channel: parser.Identifier{Text: "unit"}})
}
func notificationUnitNotify(t *testing.T, s *session.Session, payload string) {
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
				sender.Transactions.NotificationEvents = []session.Notification{{PID: uint64(sender.SessionToken.ID()), Channel: "unit", Payload: "published"}}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := sender.AcquireActivity(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				var changes *session.Journal
				if catalog {
					changes = session.NewJournal()
				}
				original, frontier, root := e.Catalog.State, e.Visibility.CommitSeq(), PayloadOf(receiver).notifications.State
				head := e.Cluster.notificationHead
				switch failure {
				case "cancel":
					cancel()
				case "epoch":
					next := *e.Catalog.State
					next.SetEpoch(next.Epoch() + 1)
					e.Catalog.PublishLocked(&next)
					original = e.Catalog.State
				case "xid":
					e.Visibility.SetLastAssignedXIDLocked(^uint64(0))
				}
				err = e.publishRowsPrepared(op, ref, nil, nil, changes, nil, nil, nil)
				if failure != "success" {
					if err == nil {
						t.Fatal("failed preflight published")
					}
					if PayloadOf(receiver).notifications.State != root || e.Catalog.State != original || e.Visibility.CommitSeq() != frontier || e.Cluster.notificationHead != head {
						t.Fatal("partial catalog/notification installation")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if e.Cluster.notificationHead == head || *e.Cluster.notificationHead != (session.NotificationQueuePosition{Offset: 32}) {
					t.Fatal("successful publication did not advance the sole head exactly once", e.Cluster.notificationHead)
				}
				if e.Visibility.LastAssignedXID() == 0 {
					t.Fatal("notification-only publication did not assign XID")
				}
				wantFrontier := frontier
				if catalog {
					wantFrontier++
				}
				if e.Visibility.CommitSeq() != wantFrontier {
					t.Fatal("notification publication invented row commit sequence", e.Visibility.CommitSeq(), wantFrontier)
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
	resource    *session.NotificationListener
	replacement *session.NotificationState
	changed     bool
}

func (c *notificationRootContext) Err() error {
	if !c.changed {
		if c.engine.Mu.TryLock() {
			c.engine.Mu.Unlock()
		} else {
			c.resource.State = c.replacement
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
			s.Transactions.NotificationEvents = []session.Notification{{PID: uint64(s.SessionToken.ID()), Channel: "unit", Payload: "after"}}
			replacement := *PayloadOf(r).notifications.State
			replacement.Ready = replacement.Ready.Append([]session.Notification{{PID: 999, Channel: "unit", Payload: "before"}})
			ctx := &notificationRootContext{Context: context.Background(), engine: e, resource: &PayloadOf(r).notifications, replacement: &replacement}
			op := transactionOperation(t, s)
			op.Ctx = ctx
			var changes *session.Journal
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
			e.Mu.Lock()
			queue := PayloadOf(r).notifications.State.Ready
			e.Mu.Unlock()
			if queue.Front != nil || queue.Back != nil {
				t.Fatal("retry duplicated delivery")
			}
		})
	}
	e := New()
	r, s := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, r)
	event := []session.Notification{{PID: uint64(s.SessionToken.ID()), Channel: "unit", Payload: "candidate"}}
	for _, change := range []string{"busy", "dequeue", "registration", "retire"} {
		t.Run(change, func(t *testing.T) {
			candidate := e.prepareNotificationPublication(s, nil, event)
			e.Mu.Lock()
			switch change {
			case "busy":
				PayloadOf(r).notifications.Busy = !PayloadOf(r).notifications.Busy
			case "dequeue":
				next := *PayloadOf(r).notifications.State
				PayloadOf(r).notifications.State = &next
			case "registration":
				delete(e.notifications, r.SessionToken)
			case "retire":
				PayloadOf(r).notifications.Ended = true
			}
			unchanged := e.notificationPublicationUnchangedLocked(candidate)
			if change == "registration" {
				e.notifications[r.SessionToken] = &PayloadOf(r).notifications
			}
			if change == "retire" {
				PayloadOf(r).notifications.Ended = false
			}
			e.Mu.Unlock()
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
	owner.Mu.Lock()
	defer owner.Mu.Unlock()
	parent := session.Notification{PID: uint64(s.SessionToken.ID()), Channel: "unit", Payload: "parent"}
	owner.NotificationActions = make([]session.NotificationAction, 1, 8)
	owner.NotificationActions[0] = session.NotificationAction{Channel: "unit", Listen: true}
	owner.NotificationEvents = make([]session.Notification, 1, 8)
	owner.NotificationEvents[0] = parent
	mark := captureSavepoint(owner, "root")
	if cap(mark.NotificationActions) != len(mark.NotificationActions) || cap(mark.NotificationEvents) != len(mark.NotificationEvents) {
		t.Fatal("savepoint retained mutable append capacity")
	}
	child := session.Notification{PID: parent.PID, Channel: "unit", Payload: "child"}
	owner.NotificationEvents = session.AppendNotificationEvents(owner.NotificationEvents, []session.Notification{parent, child})
	owner.NotificationActions = append(owner.NotificationActions, session.NotificationAction{All: true})
	restoreSavepoint(owner, mark, mark.Settings.Clone())
	owner.NotificationEvents = session.AppendNotificationEvents(owner.NotificationEvents, []session.Notification{parent, child})
	if !reflect.DeepEqual(owner.NotificationEvents, []session.Notification{parent, child}) || len(owner.NotificationActions) != 1 {
		t.Fatal("rollback roots/dedup ancestry corrupted")
	}
	if len(mark.NotificationEvents) != 1 || mark.NotificationEvents[0] != parent {
		t.Fatal("append overwrote retained parent")
	}
}

func TestNotificationIdentityAndAssignment(t *testing.T) {
	e := New()
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(1)<<48 | 17})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if e.notifications != nil || PayloadOf(s).notifications.State != nil || PayloadOf(s).notifications.Wake != nil {
		t.Fatal("unused feature allocated queue state")
	}
	before := e.Visibility.LastAssignedXID()
	notificationUnitListen(t, s)
	if e.Visibility.LastAssignedXID() != before {
		t.Fatal("subscription-only commit assigned XID")
	}
	notificationUnitNotify(t, s, "wide")
	if got := notificationUnitReceive(t, s); got.PID != uint64(s.SessionToken.ID()) || got.PID <= uint64(^uint32(0)) {
		t.Fatal("sender identity narrowed", got)
	}
	if e.Visibility.LastAssignedXID() != before+1 {
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
			for _, recipient := range []*session.Session{receiver, finisher} {
				if got := notificationUnitReceive(t, recipient); got.Payload != "before-origin-end" || got.PID != uint64(finisher.SessionToken.ID()) {
					t.Fatal("detached completion published live-origin effects", got)
				}
			}
			e.Mu.Lock()
			_, kept := PayloadOf(finisher).notifications.State.Channels["unit"]
			_, stolen := PayloadOf(finisher).notifications.State.Channels["origin-only"]
			e.Mu.Unlock()
			if !kept || stolen {
				t.Fatal("detached completion changed finisher subscriptions")
			}
			transactionEnd(t, origin, live, false)
			notificationUnitNotify(t, finisher, "after-origin-rollback")
			for _, recipient := range []*session.Session{receiver, finisher} {
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
	for _, ref := range []ids.TxRef{old, e.Catalog.Domain.Tx(other.SessionToken, live.ID(), 0, other.Transactions, 0)} {
		actions, events, err := notificationEffects(op, ref, nil, nil)
		if err == nil || len(actions) != 0 || len(events) != 0 {
			t.Fatal("foreign or stale transaction exposed live effects", err, actions, events)
		}
	}
	_, events, err := notificationEffects(op, live, nil, nil)
	if err != nil || len(events) != 1 || events[0].Payload != "live" {
		t.Fatal("exact live owner lost its effects", err, events)
	}

	// A statement of another operation or owner is rejected after the owner
	// checks; the exact statement's effects follow the owner's, deduplicated.
	peerOp := transactionOperation(t, origin)
	for _, scope := range []*StatementScope{{Op: peerOp, Owner: origin.Transactions}, {Op: op, Owner: &session.TransactionOwner{}}} {
		actions, events, err := notificationEffects(op, live, nil, scope)
		if err == nil || err.Error() != "stuntgres: foreign notification statement" || len(actions) != 0 || len(events) != 0 {
			t.Fatal("foreign statement exposed effects", err, actions, events)
		}
	}
	if _, _, err := notificationEffects(op, old, nil, &StatementScope{Op: peerOp, Owner: origin.Transactions}); err == nil || err.Error() == "stuntgres: foreign notification statement" {
		t.Fatal("statement check preceded the owner check", err)
	}
	listen := session.NotificationAction{Channel: "unit", Listen: true}
	statement := &StatementScope{Op: op, Owner: origin.Transactions, notificationActions: []session.NotificationAction{listen},
		notificationEvents: []session.Notification{events[0], {PID: events[0].PID, Channel: "unit", Payload: "statement"}}}
	actions, merged, err := notificationEffects(op, live, nil, statement)
	if err != nil || !reflect.DeepEqual(actions, []session.NotificationAction{listen}) || len(merged) != 2 || merged[0] != events[0] || merged[1].Payload != "statement" {
		t.Fatal("statement effects not merged after the owner's", err, actions, merged)
	}
	if actions, events, err := notificationEffects(op, live, &session.PreparedPublication{}, statement); err != nil || actions != nil || events != nil {
		t.Fatal("prepared completion read live effects", err, actions, events)
	}
}
