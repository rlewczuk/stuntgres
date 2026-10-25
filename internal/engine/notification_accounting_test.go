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
		run(t, e, "CREATE DATABASE queue_foreign")
		foreign, err := e.SelectDatabase(context.Background(), "queue_foreign")
		if err != nil {
			t.Fatal(err)
		}
		local, remote, sender := sessionStart(t, e, nil), sessionStart(t, foreign, nil), sessionStart(t, e, nil)
		notificationUnitListen(t, local)
		notificationUnitListen(t, remote)
		e.Mu.Lock()
		PayloadNotificationsRef(PayloadOf(local)).Busy = true
		PayloadNotificationsRef(PayloadOf(remote)).Busy = true
		e.Mu.Unlock()
		events := notificationAccountingBatch()
		for _, change := range []string{"head", "target", "remote-state", "remote-busy", "remote-ended", "remote-registry"} {
			p := EnginePrepareNotificationPublication(e, sender, nil, events)
			e.Mu.Lock()
			oldHead := DatabaseClusterNotificationHead(e.Cluster)
			oldState := PayloadNotifications(PayloadOf(remote)).State
			switch change {
			case "head":
				SetDatabaseClusterNotificationHead(e.Cluster, &session.NotificationQueuePosition{})
			case "target":
				e.Cluster.Targets["queue_foreign"] = e
			case "remote-state":
				next := *oldState
				PayloadNotificationsRef(PayloadOf(remote)).State = &next
			case "remote-busy":
				PayloadNotificationsRef(PayloadOf(remote)).Busy = false
			case "remote-ended":
				PayloadNotificationsRef(PayloadOf(remote)).Ended = true
			case "remote-registry":
				delete(EngineNotifications(foreign), remote.SessionToken)
			}
			unchanged := EngineNotificationPublicationUnchangedLocked(e, p)
			SetDatabaseClusterNotificationHead(e.Cluster, oldHead)
			e.Cluster.Targets["queue_foreign"] = foreign
			PayloadNotificationsRef(PayloadOf(remote)).State = oldState
			PayloadNotificationsRef(PayloadOf(remote)).Busy = true
			PayloadNotificationsRef(PayloadOf(remote)).Ended = false
			EngineNotifications(foreign)[remote.SessionToken] = PayloadNotificationsRef(PayloadOf(remote))
			e.Mu.Unlock()
			if unchanged {
				t.Fatal("accepted stale cluster root", change)
			}
		}
		p := EnginePrepareNotificationPublication(e, sender, nil, events)
		e.Mu.Lock()
		if !EngineNotificationPublicationUnchangedLocked(e, p) || NotificationPublicationError(p) != nil {
			e.Mu.Unlock()
			t.Fatal("fresh candidate invalid")
		}
		EngineInstallNotificationPublicationLocked(e, p)
		allocations := testing.AllocsPerRun(100, func() {
			EngineInstallNotificationPublicationLocked(e, p)
			_ = EngineNotificationQueueUsageLocked(e)
		})
		if allocations != 0 {
			e.Mu.Unlock()
			t.Fatal("installation/usage allocated", allocations)
		}
		wantHead := session.NotificationQueuePosition{Page: 1, Offset: 7924}
		if DatabaseClusterNotificationHead(e.Cluster) == nil || *DatabaseClusterNotificationHead(e.Cluster) != wantHead {
			e.Mu.Unlock()
			t.Fatal("batch multiplied by recipients", DatabaseClusterNotificationHead(e.Cluster))
		}
		if got := EngineNotificationQueueUsageLocked(e); got != 1.0/1048576 {
			e.Mu.Unlock()
			t.Fatal("page fraction", got)
		}
		localState, remoteState := PayloadNotifications(PayloadOf(local)).State, PayloadNotifications(PayloadOf(remote)).State
		if localState.Pending == nil || remoteState.Pending != nil || remoteState.Ready.Back != nil || !remoteState.Unread || !PayloadNotificationsRef(PayloadOf(remote)).Used.Load() {
			e.Mu.Unlock()
			t.Fatal("foreign payload or missing cursor")
		}
		e.Mu.Unlock()
		// Cursor-only outer rollback releases a foreign hold even without Pending.
		FinishNotificationBoundary(remote.Transactions)
		e.Mu.Lock()
		if PayloadNotifications(PayloadOf(remote)).State.Unread || EngineNotificationQueueUsageLocked(e) != 1.0/1048576 {
			e.Mu.Unlock()
			t.Fatal("one release cleared another hold")
		}
		e.Mu.Unlock()
		FinishNotificationBoundary(local.Transactions)
		e.Mu.Lock()
		if EngineNotificationQueueUsageLocked(e) != 0 || PayloadNotifications(PayloadOf(local)).State.Ready.Back == nil {
			e.Mu.Unlock()
			t.Fatal("Ready counted as backend pending")
		}
		e.Mu.Unlock()
		// Polling Ready while a later transaction is busy preserves its new hold.
		e.Mu.Lock()
		PayloadNotificationsRef(PayloadOf(local)).Busy = true
		e.Mu.Unlock()
		p = EnginePrepareNotificationPublication(e, sender, nil, events)
		e.Mu.Lock()
		EngineInstallNotificationPublicationLocked(e, p)
		held := PayloadNotifications(PayloadOf(local)).State.Position
		usage := EngineNotificationQueueUsageLocked(e)
		e.Mu.Unlock()
		if _, ok, _, err := EnginePollNotification(e, local, nil); err != nil || !ok {
			t.Fatal(ok, err)
		}
		e.Mu.Lock()
		if !PayloadNotifications(PayloadOf(local)).State.Unread || PayloadNotifications(PayloadOf(local)).State.Position != held || EngineNotificationQueueUsageLocked(e) != usage {
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
			SetDatabaseClusterNotificationHead(e.Cluster, head)
			n := PayloadNotificationsRef(PayloadOf(receiver))
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
			err := EnginePublishRowsPrepared(e, op, ref, nil, nil, changes, nil, nil, nil)
			var sqlerr *sqlstate.Error
			if !errors.As(err, &sqlerr) || sqlerr.Code != "54000" || sqlerr.Message != "too many notifications in the NOTIFY queue" {
				t.Fatal("capacity diagnostic", err)
			}
			if DatabaseClusterNotificationHead(e.Cluster) != head || n.State != root || e.Catalog.State != state || e.Visibility.CommitSeq() != frontier {
				t.Fatal("capacity published partial effects")
			}
			// The same capacity decision becomes stale when a hold is released.
			p := EnginePrepareNotificationPublication(e, sender, nil, notificationAccountingBatch()[:1])
			if NotificationPublicationError(p) == nil {
				t.Fatal("missing prepared capacity error")
			}
			e.Mu.Lock()
			released := *n.State
			released.Unread = false
			n.State = &released
			unchanged := EngineNotificationPublicationUnchangedLocked(e, p)
			e.Mu.Unlock()
			if unchanged {
				t.Fatal("stale capacity veto accepted")
			}
			fresh := EnginePrepareNotificationPublication(e, sender, nil, notificationAccountingBatch()[:1])
			if err := NotificationPublicationError(fresh); err != nil {
				t.Fatal("fresh head cursor inherited stale full decision", err)
			}
		})
	}
}

func TestNotificationQueueAccountingControls(t *testing.T) {
	e := New()
	if DatabaseClusterNotificationHead(e.Cluster) != nil || EngineNotificationQueueUsageLocked(e) != 0 {
		t.Fatal("fresh accounting allocated or retained state")
	}
	receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, receiver)
	e.Mu.Lock()
	PayloadNotificationsRef(PayloadOf(receiver)).Busy = true
	e.Mu.Unlock()
	ref := transactionBegin(t, sender)
	sender.Transactions.NotificationEvents = notificationAccountingBatch()
	transactionEnd(t, sender, ref, true)
	head, root := DatabaseClusterNotificationHead(e.Cluster), PayloadNotifications(PayloadOf(receiver)).State
	if head == nil || root == nil || !root.Unread {
		t.Fatal("missing committed roots")
	}
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if DatabaseClusterNotificationHead(e.Cluster) != head || PayloadNotifications(PayloadOf(receiver)).State != root || EngineNotificationQueueUsageLocked(e) != 1.0/1048576 {
		t.Fatal("catalog replacement changed live ephemeral roots")
	}
	fresh := New()
	if DatabaseClusterNotificationHead(fresh.Cluster) != nil || EngineNotificationQueueUsageLocked(fresh) != 0 || len(EngineNotifications(fresh)) != 0 {
		t.Fatal("independent cluster imported queue")
	}
}
