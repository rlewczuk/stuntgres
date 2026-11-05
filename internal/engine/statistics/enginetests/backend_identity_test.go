package statistics_test

import (
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"sync"
	"testing"
	"time"
)

func TestBackendIdentityProviderSlots(t *testing.T) {
	var p statistics.Provider
	domain := new(ids.Domain)
	login := statsLogin()
	birth := statistics.Activity{Principal: login, Database: 99, BackendStart: time.Unix(100, 0)}
	privateOwner := new(struct{ ids.SessionOwnerMarker })
	for _, token := range []ids.SessionToken{{}, domain.Session(0), domain.DetachedSession(90), domain.PrivateSession(privateOwner)} {
		if p.RegisterActivity(token, birth) != 0 {
			t.Fatal("invalid registration admitted")
		}
		p.End(token)
	}
	p.RegisterActivity(domain.Session(90), statistics.Activity{})
	p.End(domain.Session(91))
	a, b, c := domain.Session(101), domain.Session(103), domain.Session(107)
	p.RegisterActivity(a, birth)
	first, _ := p.LiveActivity(a)
	if first.BackendID != 0 || first.Incarnation == 0 || int64(first.BackendID) == int64(a.ID()) {
		t.Fatal("slot zero independent of PID", first)
	}
	p.RegisterActivity(b, birth)
	p.RegisterActivity(c, birth)
	if got := p.CaptureActivities(); got[b].BackendID != 1 || got[c].BackendID != 2 {
		t.Fatal("physical slots", got)
	}
	relabeled := birth
	relabeled.Database, relabeled.Principal, relabeled.BackendID = 111, statsLogin(), 72
	if p.RegisterActivity(a, relabeled) != first.Incarnation {
		t.Fatal("duplicate registration reminted birth")
	}
	if got, _ := p.LiveActivity(a); got != first {
		t.Fatal("duplicate registration relabeled identity", got)
	}
	var reader statistics.Reader
	reader.Configure("none", 1)
	old := reader.Activities(&p)
	p.End(b)
	p.End(b) // cannot recycle the same live slot twice
	p.Reset(time.Unix(200, 0))
	p.Replace(time.Unix(300, 0), nil)
	if got, _ := p.LiveActivity(a); got != first {
		t.Fatal("reset/replacement reminted surviving birth", got)
	}
	if got, _ := p.LiveActivity(c); got.BackendID != 2 {
		t.Fatal("unrelated retirement compacted slots", got)
	}
	d, e := domain.Session(109), domain.Session(113)
	p.RegisterActivity(d, birth)
	p.RegisterActivity(e, birth)
	current := p.CaptureActivities()
	if current[d].BackendID != 1 || current[e].BackendID != 3 || current[d].Incarnation == old[b].Incarnation {
		t.Fatal("exactly-once recycling", current)
	}
	if got := reader.Activities(&p); !reflect.DeepEqual(got, old) {
		t.Fatal("retirement/reset mutated detached cut", got, old)
	}
	reader.Clear()
	if got := reader.Activities(&p); !reflect.DeepEqual(got, current) || !reader.Timestamp().IsZero() {
		t.Fatal("clear or activity timestamp contract", got)
	}
	p.Command(b, ids.CommandIdentity{Command: 17}, "late", time.Now(), true)
	p.ReportIdle(b, "late", "idle", ids.TxRef{}, time.Time{}, time.Now())
	if _, ok := p.LiveActivity(b); ok {
		t.Fatal("late command resurrected retired token")
	}
	// Real concurrent registrations must retain uniqueness under the provider's
	// one leaf mutex. No fixed portable public slot sequence is implied.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p.RegisterActivity(domain.Session(ids.SessionID(1000+i)), birth)
		}(i)
	}
	close(start)
	wg.Wait()
	seen := map[int32]bool{}
	for _, activity := range p.CaptureActivities() {
		if activity.BackendID < 0 || seen[activity.BackendID] {
			t.Fatal("concurrent duplicate live slots", activity)
		}
		seen[activity.BackendID] = true
	}
}
