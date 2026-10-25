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
		e.Mu.Lock()
		PayloadOf(local).notifications.Busy = true
		PayloadOf(remote).notifications.Busy = true
		e.Mu.Unlock()
		events := notificationAccountingBatch()
		for _, change := range []string{"head", "target", "remote-state", "remote-busy", "remote-ended", "remote-registry"} {
			p := e.prepareNotificationPublication(sender, nil, events)
			e.Mu.Lock()
			oldHead := e.Cluster.notificationHead
			oldState := PayloadOf(remote).notifications.State
			switch change {
			case "head":
				e.Cluster.notificationHead = &session.NotificationQueuePosition{}
			case "target":
				e.Cluster.Targets["queue_foreign"] = e
			case "remote-state":
				next := *oldState
				PayloadOf(remote).notifications.State = &next
			case "remote-busy":
				PayloadOf(remote).notifications.Busy = false
			case "remote-ended":
				PayloadOf(remote).notifications.Ended = true
			case "remote-registry":
				delete(foreign.notifications, remote.SessionToken)
			}
			unchanged := e.notificationPublicationUnchangedLocked(p)
			e.Cluster.notificationHead = oldHead
			e.Cluster.Targets["queue_foreign"] = foreign
			PayloadOf(remote).notifications.State = oldState
			PayloadOf(remote).notifications.Busy = true
			PayloadOf(remote).notifications.Ended = false
			foreign.notifications[remote.SessionToken] = &PayloadOf(remote).notifications
			e.Mu.Unlock()
			if unchanged {
				t.Fatal("accepted stale cluster root", change)
			}
		}
		p := e.prepareNotificationPublication(sender, nil, events)
		e.Mu.Lock()
		if !e.notificationPublicationUnchangedLocked(p) || notificationPublicationError(p) != nil {
			e.Mu.Unlock()
			t.Fatal("fresh candidate invalid")
		}
		e.installNotificationPublicationLocked(p)
		allocations := testing.AllocsPerRun(100, func() {
			e.installNotificationPublicationLocked(p)
			_ = e.notificationQueueUsageLocked()
		})
		if allocations != 0 {
			e.Mu.Unlock()
			t.Fatal("installation/usage allocated", allocations)
		}
		wantHead := session.NotificationQueuePosition{Page: 1, Offset: 7924}
		if e.Cluster.notificationHead == nil || *e.Cluster.notificationHead != wantHead {
			e.Mu.Unlock()
			t.Fatal("batch multiplied by recipients", e.Cluster.notificationHead)
		}
		if got := e.notificationQueueUsageLocked(); got != 1.0/1048576 {
			e.Mu.Unlock()
			t.Fatal("page fraction", got)
		}
		localState, remoteState := PayloadOf(local).notifications.State, PayloadOf(remote).notifications.State
		if localState.Pending == nil || remoteState.Pending != nil || remoteState.Ready.Back != nil || !remoteState.Unread || !PayloadOf(remote).notifications.Used.Load() {
			e.Mu.Unlock()
			t.Fatal("foreign payload or missing cursor")
		}
		e.Mu.Unlock()
		// Cursor-only outer rollback releases a foreign hold even without Pending.
		finishNotificationBoundary(remote.Transactions)
		e.Mu.Lock()
		if PayloadOf(remote).notifications.State.Unread || e.notificationQueueUsageLocked() != 1.0/1048576 {
			e.Mu.Unlock()
			t.Fatal("one release cleared another hold")
		}
		e.Mu.Unlock()
		finishNotificationBoundary(local.Transactions)
		e.Mu.Lock()
		if e.notificationQueueUsageLocked() != 0 || PayloadOf(local).notifications.State.Ready.Back == nil {
			e.Mu.Unlock()
			t.Fatal("Ready counted as backend pending")
		}
		e.Mu.Unlock()
		// Polling Ready while a later transaction is busy preserves its new hold.
		e.Mu.Lock()
		PayloadOf(local).notifications.Busy = true
		e.Mu.Unlock()
		p = e.prepareNotificationPublication(sender, nil, events)
		e.Mu.Lock()
		e.installNotificationPublicationLocked(p)
		held := PayloadOf(local).notifications.State.Position
		usage := e.notificationQueueUsageLocked()
		e.Mu.Unlock()
		if _, ok, _, err := e.pollNotification(local, nil); err != nil || !ok {
			t.Fatal(ok, err)
		}
		e.Mu.Lock()
		if !PayloadOf(local).notifications.State.Unread || PayloadOf(local).notifications.State.Position != held || e.notificationQueueUsageLocked() != usage {
			e.Mu.Unlock()
			t.Fatal("Ready dequeue changed hold")
		}
		e.Mu.Unlock()
	})

	for _, catalog := range []bool{false, true} {
		t.Run(map[bool]string{false: "capacity-empty", true: "capacity-catalog"}[catalog], func(t *testing.T) {
			e := New()
			receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
			notificationUnitListen(t, receiver)
			e.Mu.Lock()
			head := &session.NotificationQueuePosition{Page: session.NotificationQueuePages - 1, Offset: 7924}
			e.Cluster.notificationHead = head
			n := &PayloadOf(receiver).notifications
			next := *n.State
			next.Unread = true
			next.Position = session.NotificationQueuePosition{}
			n.State, n.Busy = &next, true
			root, state, frontier := n.State, e.Catalog.State, e.Visibility.CommitSeq()
			e.Mu.Unlock()
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
			if e.Cluster.notificationHead != head || n.State != root || e.Catalog.State != state || e.Visibility.CommitSeq() != frontier {
				t.Fatal("capacity published partial effects")
			}
			// The same capacity decision becomes stale when a hold is released.
			p := e.prepareNotificationPublication(sender, nil, notificationAccountingBatch()[:1])
			if notificationPublicationError(p) == nil {
				t.Fatal("missing prepared capacity error")
			}
			e.Mu.Lock()
			released := *n.State
			released.Unread = false
			n.State = &released
			unchanged := e.notificationPublicationUnchangedLocked(p)
			e.Mu.Unlock()
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
	if e.Cluster.notificationHead != nil || e.notificationQueueUsageLocked() != 0 {
		t.Fatal("fresh accounting allocated or retained state")
	}
	receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, receiver)
	e.Mu.Lock()
	PayloadOf(receiver).notifications.Busy = true
	e.Mu.Unlock()
	ref := transactionBegin(t, sender)
	sender.Transactions.NotificationEvents = notificationAccountingBatch()
	transactionEnd(t, sender, ref, true)
	head, root := e.Cluster.notificationHead, PayloadOf(receiver).notifications.State
	if head == nil || root == nil || !root.Unread {
		t.Fatal("missing committed roots")
	}
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if e.Cluster.notificationHead != head || PayloadOf(receiver).notifications.State != root || e.notificationQueueUsageLocked() != 1.0/1048576 {
		t.Fatal("catalog replacement changed live ephemeral roots")
	}
	fresh := New()
	if fresh.Cluster.notificationHead != nil || fresh.notificationQueueUsageLocked() != 0 || len(fresh.notifications) != 0 {
		t.Fatal("independent cluster imported queue")
	}
}
