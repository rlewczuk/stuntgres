package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func notificationAccountingBatch() []session.Notification {
	return []session.Notification{{Channel: "unit", Payload: strings.Repeat("x", 7900)}, {Channel: "unit", Payload: strings.Repeat("y", 7900)}}
}

func TestNotificationQueueAccountingPublication(t *testing.T) {
	t.Run("global-roots", func(t *testing.T) {
		e := New()
		foreign := databaseCreateUnitInstall(t, e, New(), "queue_foreign")
		local, remote, sender := sessionStart(t, e, nil), sessionStart(t, foreign, nil), sessionStart(t, e, nil)
		notificationUnitListen(t, local)
		notificationUnitListen(t, remote)
		e.mu.Lock()
		payloadOf(local).notifications.Busy = true
		payloadOf(remote).notifications.Busy = true
		e.mu.Unlock()
		events := notificationAccountingBatch()
		for _, change := range []string{"head", "target", "remote-state", "remote-busy", "remote-ended", "remote-registry"} {
			p := e.prepareNotificationPublication(sender, nil, events)
			e.mu.Lock()
			oldHead := e.cluster.notificationHead
			oldState := payloadOf(remote).notifications.State
			switch change {
			case "head":
				e.cluster.notificationHead = &session.NotificationQueuePosition{}
			case "target":
				e.cluster.targets["queue_foreign"] = e
			case "remote-state":
				next := *oldState
				payloadOf(remote).notifications.State = &next
			case "remote-busy":
				payloadOf(remote).notifications.Busy = false
			case "remote-ended":
				payloadOf(remote).notifications.Ended = true
			case "remote-registry":
				delete(foreign.notifications, remote.SessionToken)
			}
			unchanged := e.notificationPublicationUnchangedLocked(p)
			e.cluster.notificationHead = oldHead
			e.cluster.targets["queue_foreign"] = foreign
			payloadOf(remote).notifications.State = oldState
			payloadOf(remote).notifications.Busy = true
			payloadOf(remote).notifications.Ended = false
			foreign.notifications[remote.SessionToken] = &payloadOf(remote).notifications
			e.mu.Unlock()
			if unchanged {
				t.Fatal("accepted stale cluster root", change)
			}
		}
		p := e.prepareNotificationPublication(sender, nil, events)
		e.mu.Lock()
		if !e.notificationPublicationUnchangedLocked(p) || notificationPublicationError(p) != nil {
			e.mu.Unlock()
			t.Fatal("fresh candidate invalid")
		}
		e.installNotificationPublicationLocked(p)
		allocations := testing.AllocsPerRun(100, func() {
			e.installNotificationPublicationLocked(p)
			_ = e.notificationQueueUsageLocked()
		})
		if allocations != 0 {
			e.mu.Unlock()
			t.Fatal("installation/usage allocated", allocations)
		}
		wantHead := session.NotificationQueuePosition{Page: 1, Offset: 7924}
		if e.cluster.notificationHead == nil || *e.cluster.notificationHead != wantHead {
			e.mu.Unlock()
			t.Fatal("batch multiplied by recipients", e.cluster.notificationHead)
		}
		if got := e.notificationQueueUsageLocked(); got != 1.0/1048576 {
			e.mu.Unlock()
			t.Fatal("page fraction", got)
		}
		localState, remoteState := payloadOf(local).notifications.State, payloadOf(remote).notifications.State
		if localState.Pending == nil || remoteState.Pending != nil || remoteState.Ready.Back != nil || !remoteState.Unread || !payloadOf(remote).notifications.Used.Load() {
			e.mu.Unlock()
			t.Fatal("foreign payload or missing cursor")
		}
		e.mu.Unlock()
		// Cursor-only outer rollback releases a foreign hold even without Pending.
		finishNotificationBoundary(remote.Transactions)
		e.mu.Lock()
		if payloadOf(remote).notifications.State.Unread || e.notificationQueueUsageLocked() != 1.0/1048576 {
			e.mu.Unlock()
			t.Fatal("one release cleared another hold")
		}
		e.mu.Unlock()
		finishNotificationBoundary(local.Transactions)
		e.mu.Lock()
		if e.notificationQueueUsageLocked() != 0 || payloadOf(local).notifications.State.Ready.Back == nil {
			e.mu.Unlock()
			t.Fatal("Ready counted as backend pending")
		}
		e.mu.Unlock()
		// Polling Ready while a later transaction is busy preserves its new hold.
		e.mu.Lock()
		payloadOf(local).notifications.Busy = true
		e.mu.Unlock()
		p = e.prepareNotificationPublication(sender, nil, events)
		e.mu.Lock()
		e.installNotificationPublicationLocked(p)
		held := payloadOf(local).notifications.State.Position
		usage := e.notificationQueueUsageLocked()
		e.mu.Unlock()
		if _, ok, _, err := e.pollNotification(local, nil); err != nil || !ok {
			t.Fatal(ok, err)
		}
		e.mu.Lock()
		if !payloadOf(local).notifications.State.Unread || payloadOf(local).notifications.State.Position != held || e.notificationQueueUsageLocked() != usage {
			e.mu.Unlock()
			t.Fatal("Ready dequeue changed hold")
		}
		e.mu.Unlock()
	})

	for _, catalog := range []bool{false, true} {
		t.Run(map[bool]string{false: "capacity-empty", true: "capacity-catalog"}[catalog], func(t *testing.T) {
			e := New()
			receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
			notificationUnitListen(t, receiver)
			e.mu.Lock()
			head := &session.NotificationQueuePosition{Page: session.NotificationQueuePages - 1, Offset: 7924}
			e.cluster.notificationHead = head
			n := &payloadOf(receiver).notifications
			next := *n.State
			next.Unread = true
			next.Position = session.NotificationQueuePosition{}
			n.State, n.Busy = &next, true
			root, state, frontier := n.State, e.catalog.state, e.visibility.CommitSeq()
			e.mu.Unlock()
			ref := transactionBegin(t, sender)
			sender.Transactions.NotificationEvents = notificationAccountingBatch()[:1]
			op := transactionOperation(t, sender)
			var changes *session.Journal
			if catalog {
				changes = session.NewJournal()
			}
			err := e.publishRowsPrepared(op, ref, nil, nil, changes, nil, nil, nil)
			var sqlerr *sqlstate.Error
			if !errors.As(err, &sqlerr) || sqlerr.Code != "54000" || sqlerr.Message != "too many notifications in the NOTIFY queue" {
				t.Fatal("capacity diagnostic", err)
			}
			if e.cluster.notificationHead != head || n.State != root || e.catalog.state != state || e.visibility.CommitSeq() != frontier {
				t.Fatal("capacity published partial effects")
			}
			// The same capacity decision becomes stale when a hold is released.
			p := e.prepareNotificationPublication(sender, nil, notificationAccountingBatch()[:1])
			if notificationPublicationError(p) == nil {
				t.Fatal("missing prepared capacity error")
			}
			e.mu.Lock()
			released := *n.State
			released.Unread = false
			n.State = &released
			unchanged := e.notificationPublicationUnchangedLocked(p)
			e.mu.Unlock()
			if unchanged {
				t.Fatal("stale capacity veto accepted")
			}
			fresh := e.prepareNotificationPublication(sender, nil, notificationAccountingBatch()[:1])
			if err := notificationPublicationError(fresh); err != nil {
				t.Fatal("fresh head cursor inherited stale full decision", err)
			}
		})
	}
}

func TestNotificationQueueAccountingControls(t *testing.T) {
	e := New()
	if e.cluster.notificationHead != nil || e.notificationQueueUsageLocked() != 0 {
		t.Fatal("fresh accounting allocated or retained state")
	}
	receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, receiver)
	e.mu.Lock()
	payloadOf(receiver).notifications.Busy = true
	e.mu.Unlock()
	ref := transactionBegin(t, sender)
	sender.Transactions.NotificationEvents = notificationAccountingBatch()
	transactionEnd(t, sender, ref, true)
	head, root := e.cluster.notificationHead, payloadOf(receiver).notifications.State
	if head == nil || root == nil || !root.Unread {
		t.Fatal("missing committed roots")
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.cluster.notificationHead != head || payloadOf(receiver).notifications.State != root || e.notificationQueueUsageLocked() != 1.0/1048576 {
		t.Fatal("catalog replacement changed live ephemeral roots")
	}
	fresh := New()
	if fresh.cluster.notificationHead != nil || fresh.notificationQueueUsageLocked() != 0 || len(fresh.notifications) != 0 {
		t.Fatal("independent cluster imported queue")
	}
}
