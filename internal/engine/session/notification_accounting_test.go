package session

import (
	"strings"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestNotificationQueueAccountingPacking(t *testing.T) {
	for _, tc := range []struct {
		name   string
		start  NotificationQueuePosition
		events []Notification
		want   NotificationQueuePosition
	}{
		{"empty", NotificationQueuePosition{}, nil, NotificationQueuePosition{}},
		{"minimum", NotificationQueuePosition{}, []Notification{{Channel: "a"}}, NotificationQueuePosition{Offset: 20}},
		{"utf8", NotificationQueuePosition{}, []Notification{{Channel: "雪", Payload: "🙂"}}, NotificationQueuePosition{Offset: 28}},
		{"one", NotificationQueuePosition{}, []Notification{{Channel: "a", Payload: strings.Repeat("x", 7900)}}, NotificationQueuePosition{Offset: 7920}},
		{"padding", NotificationQueuePosition{Offset: 7920}, []Notification{{Channel: "a", Payload: strings.Repeat("x", 7900)}}, NotificationQueuePosition{Page: 1, Offset: 7920}},
		{"short-remainder", NotificationQueuePosition{Offset: 8160}, []Notification{{Channel: "a"}}, NotificationQueuePosition{Page: 1}},
		{"exact-page", NotificationQueuePosition{Offset: 8172}, []Notification{{Channel: "a"}}, NotificationQueuePosition{Page: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.start.Advance(tc.events); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	events := make([]Notification, 6)
	for i := range events {
		events[i] = Notification{Channel: "a", Payload: strings.Repeat("x", 7900)}
	}
	want := NotificationQueuePosition{Page: 5, Offset: 7920}
	if got := (NotificationQueuePosition{}).Advance(events); got != want {
		t.Fatal(got, want)
	}
	head := (NotificationQueuePosition{}).Advance(events[:2]).Advance(events[2:])
	if head != want {
		t.Fatal("batch split changed layout", head)
	}
}

func TestNotificationQueueAccountingCursors(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "local-unmatched"}[local], func(t *testing.T) {
			domain := &ids.Domain{}
			a := notificationTestListening(domain, 1, "a", "b")
			b := notificationTestListening(domain, 2, "a")
			a.Busy, b.Busy = true, true
			r := notificationTestRegistry(a, b)
			original := a.State
			start := NotificationQueuePosition{Page: 7, Offset: 40}
			events := []Notification{{Channel: "unmatched", Payload: "event"}}
			p := &NotificationPublication{}
			r.CaptureLocked(p, a)
			p.Prepare(nil, events, start, local)
			if a.State != original || original.Unread {
				t.Fatal("prepare mutated published snapshot")
			}
			if tail := p.OldestPrepared(NotificationQueuePosition{Page: 9}); tail != start {
				t.Fatal("shared oldest boundary", tail)
			}
			r.InstallLocked(p)
			if !a.State.Unread || a.State.Position != start || !a.Used.Load() || (a.State.Pending != nil) != local {
				t.Fatal("cursor-only/local ownership", a.State)
			}
			p = &NotificationPublication{}
			r.CaptureLocked(p, a)
			p.Prepare([]NotificationAction{{Channel: "a"}}, events, NotificationQueuePosition{Page: 8}, local)
			r.InstallLocked(p)
			if !a.State.Unread || a.State.Position != start || len(a.State.Channels) != 1 {
				t.Fatal("partial UNLISTEN lost oldest hold")
			}
			p = &NotificationPublication{}
			r.CaptureLocked(p, a)
			p.Prepare([]NotificationAction{{All: true}}, nil, NotificationQueuePosition{Page: 9}, local)
			r.InstallLocked(p)
			if a.State.Unread || a.State.Pending != nil || a.State.Position != (NotificationQueuePosition{}) {
				t.Fatal("last UNLISTEN retained hold")
			}
			if !b.State.Unread || b.State.Position != start {
				t.Fatal("peer hold changed")
			}
			r.EndLocked(a)
			if r[b.Session.SessionToken] != b || !b.State.Unread {
				t.Fatal("end cleared peer")
			}
		})
	}
}
