package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func notificationAccountingBatch() []session.Notification {
	return []session.Notification{{Channel: "unit", Payload: strings.Repeat("x", 7900)}, {Channel: "unit", Payload: strings.Repeat("y", 7900)}}
}

func TestNotificationQueueAccountingPublication(t *testing.T) {
	t.Run("global-roots", func(t *testing.T) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE DATABASE queue_foreign")
		foreign, err := e.SelectDatabase(context.Background(), "queue_foreign")
		if err != nil {
			t.Fatal(err)
		}
		local, remote, sender := sessionStart(t, e, nil), sessionStart(t, foreign, nil), sessionStart(t, e, nil)
		notificationUnitListen(t, local)
		notificationUnitListen(t, remote)
		e.Mu.Lock()
		core.PayloadNotificationsRef(core.PayloadOf(local)).Busy = true
		core.PayloadNotificationsRef(core.PayloadOf(remote)).Busy = true
		e.Mu.Unlock()
		events := notificationAccountingBatch()
		for _, change := range []string{"head", "target", "remote-state", "remote-busy", "remote-ended", "remote-registry"} {
			p := core.EnginePrepareNotificationPublication(e, sender, nil, events)
			e.Mu.Lock()
			oldHead := core.DatabaseClusterNotificationHead(e.Cluster)
			oldState := core.PayloadNotifications(core.PayloadOf(remote)).State
			switch change {
			case "head":
				core.SetDatabaseClusterNotificationHead(e.Cluster, &session.NotificationQueuePosition{})
			case "target":
				e.Cluster.Targets["queue_foreign"] = e
			case "remote-state":
				next := *oldState
				core.PayloadNotificationsRef(core.PayloadOf(remote)).State = &next
			case "remote-busy":
				core.PayloadNotificationsRef(core.PayloadOf(remote)).Busy = false
			case "remote-ended":
				core.PayloadNotificationsRef(core.PayloadOf(remote)).Ended = true
			case "remote-registry":
				delete(core.EngineNotifications(foreign), remote.SessionToken)
			}
			unchanged := core.EngineNotificationPublicationUnchangedLocked(e, p)
			core.SetDatabaseClusterNotificationHead(e.Cluster, oldHead)
			e.Cluster.Targets["queue_foreign"] = foreign
			core.PayloadNotificationsRef(core.PayloadOf(remote)).State = oldState
			core.PayloadNotificationsRef(core.PayloadOf(remote)).Busy = true
			core.PayloadNotificationsRef(core.PayloadOf(remote)).Ended = false
			core.EngineNotifications(foreign)[remote.SessionToken] = core.PayloadNotificationsRef(core.PayloadOf(remote))
			e.Mu.Unlock()
			if unchanged {
				t.Fatal("accepted stale cluster root", change)
			}
		}
		p := core.EnginePrepareNotificationPublication(e, sender, nil, events)
		e.Mu.Lock()
		if !core.EngineNotificationPublicationUnchangedLocked(e, p) || core.NotificationPublicationError(p) != nil {
			e.Mu.Unlock()
			t.Fatal("fresh candidate invalid")
		}
		core.EngineInstallNotificationPublicationLocked(e, p)
		allocations := testing.AllocsPerRun(100, func() {
			core.EngineInstallNotificationPublicationLocked(e, p)
			_ = core.EngineNotificationQueueUsageLocked(e)
		})
		if allocations != 0 {
			e.Mu.Unlock()
			t.Fatal("installation/usage allocated", allocations)
		}
		wantHead := session.NotificationQueuePosition{Page: 1, Offset: 7924}
		if core.DatabaseClusterNotificationHead(e.Cluster) == nil || *core.DatabaseClusterNotificationHead(e.Cluster) != wantHead {
			e.Mu.Unlock()
			t.Fatal("batch multiplied by recipients", core.DatabaseClusterNotificationHead(e.Cluster))
		}
		if got := core.EngineNotificationQueueUsageLocked(e); got != 1.0/1048576 {
			e.Mu.Unlock()
			t.Fatal("page fraction", got)
		}
		localState, remoteState := core.PayloadNotifications(core.PayloadOf(local)).State, core.PayloadNotifications(core.PayloadOf(remote)).State
		if localState.Pending == nil || remoteState.Pending != nil || remoteState.Ready.Back != nil || !remoteState.Unread || !core.PayloadNotificationsRef(core.PayloadOf(remote)).Used.Load() {
			e.Mu.Unlock()
			t.Fatal("foreign payload or missing cursor")
		}
		e.Mu.Unlock()
		// Cursor-only outer rollback releases a foreign hold even without Pending.
		core.FinishNotificationBoundary(remote.Transactions)
		e.Mu.Lock()
		if core.PayloadNotifications(core.PayloadOf(remote)).State.Unread || core.EngineNotificationQueueUsageLocked(e) != 1.0/1048576 {
			e.Mu.Unlock()
			t.Fatal("one release cleared another hold")
		}
		e.Mu.Unlock()
		core.FinishNotificationBoundary(local.Transactions)
		e.Mu.Lock()
		if core.EngineNotificationQueueUsageLocked(e) != 0 || core.PayloadNotifications(core.PayloadOf(local)).State.Ready.Back == nil {
			e.Mu.Unlock()
			t.Fatal("Ready counted as backend pending")
		}
		e.Mu.Unlock()
		// Polling Ready while a later transaction is busy preserves its new hold.
		e.Mu.Lock()
		core.PayloadNotificationsRef(core.PayloadOf(local)).Busy = true
		e.Mu.Unlock()
		p = core.EnginePrepareNotificationPublication(e, sender, nil, events)
		e.Mu.Lock()
		core.EngineInstallNotificationPublicationLocked(e, p)
		held := core.PayloadNotifications(core.PayloadOf(local)).State.Position
		usage := core.EngineNotificationQueueUsageLocked(e)
		e.Mu.Unlock()
		if _, ok, _, err := core.EnginePollNotification(e, local, nil); err != nil || !ok {
			t.Fatal(ok, err)
		}
		e.Mu.Lock()
		if !core.PayloadNotifications(core.PayloadOf(local)).State.Unread || core.PayloadNotifications(core.PayloadOf(local)).State.Position != held || core.EngineNotificationQueueUsageLocked(e) != usage {
			e.Mu.Unlock()
			t.Fatal("Ready dequeue changed hold")
		}
		e.Mu.Unlock()
	})

	for _, catalog := range []bool{false, true} {
		t.Run(map[bool]string{false: "capacity-empty", true: "capacity-catalog"}[catalog], func(t *testing.T) {
			e := engine.New()
			receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
			notificationUnitListen(t, receiver)
			e.Mu.Lock()
			head := &session.NotificationQueuePosition{Page: session.NotificationQueuePages - 1, Offset: 7924}
			core.SetDatabaseClusterNotificationHead(e.Cluster, head)
			n := core.PayloadNotificationsRef(core.PayloadOf(receiver))
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
			err := core.EnginePublishRowsPrepared(e, op, ref, nil, nil, changes, nil, nil, nil)
			var sqlerr *sqlstate.Error
			if !errors.As(err, &sqlerr) || sqlerr.Code != "54000" || sqlerr.Message != "too many notifications in the NOTIFY queue" {
				t.Fatal("capacity diagnostic", err)
			}
			if core.DatabaseClusterNotificationHead(e.Cluster) != head || n.State != root || e.Catalog.State != state || e.Visibility.CommitSeq() != frontier {
				t.Fatal("capacity published partial effects")
			}
			// The same capacity decision becomes stale when a hold is released.
			p := core.EnginePrepareNotificationPublication(e, sender, nil, notificationAccountingBatch()[:1])
			if core.NotificationPublicationError(p) == nil {
				t.Fatal("missing prepared capacity error")
			}
			e.Mu.Lock()
			released := *n.State
			released.Unread = false
			n.State = &released
			unchanged := core.EngineNotificationPublicationUnchangedLocked(e, p)
			e.Mu.Unlock()
			if unchanged {
				t.Fatal("stale capacity veto accepted")
			}
			fresh := core.EnginePrepareNotificationPublication(e, sender, nil, notificationAccountingBatch()[:1])
			if err := core.NotificationPublicationError(fresh); err != nil {
				t.Fatal("fresh head cursor inherited stale full decision", err)
			}
		})
	}
}

func TestNotificationQueueAccountingControls(t *testing.T) {
	e := engine.New()
	if core.DatabaseClusterNotificationHead(e.Cluster) != nil || core.EngineNotificationQueueUsageLocked(e) != 0 {
		t.Fatal("fresh accounting allocated or retained state")
	}
	receiver, sender := sessionStart(t, e, nil), sessionStart(t, e, nil)
	notificationUnitListen(t, receiver)
	e.Mu.Lock()
	core.PayloadNotificationsRef(core.PayloadOf(receiver)).Busy = true
	e.Mu.Unlock()
	ref := transactionBegin(t, sender)
	sender.Transactions.NotificationEvents = notificationAccountingBatch()
	transactionEnd(t, sender, ref, true)
	head, root := core.DatabaseClusterNotificationHead(e.Cluster), core.PayloadNotifications(core.PayloadOf(receiver)).State
	if head == nil || root == nil || !root.Unread {
		t.Fatal("missing committed roots")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if core.DatabaseClusterNotificationHead(e.Cluster) != head || core.PayloadNotifications(core.PayloadOf(receiver)).State != root || core.EngineNotificationQueueUsageLocked(e) != 1.0/1048576 {
		t.Fatal("catalog replacement changed live ephemeral roots")
	}
	fresh := engine.New()
	if core.DatabaseClusterNotificationHead(fresh.Cluster) != nil || core.EngineNotificationQueueUsageLocked(fresh) != 0 || len(core.EngineNotifications(fresh)) != 0 {
		t.Fatal("independent cluster imported queue")
	}
}
