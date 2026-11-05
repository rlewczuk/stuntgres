package statistics_test

import (
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"
)

func TestBackendCurrentProviderAndCache(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			var owner, mirror statistics.Provider
			domain := new(ids.Domain)
			token := domain.Session(41)
			unknown := domain.Session(42)
			at, next := time.Unix(100, 0), time.Unix(200, 0)
			identity := statistics.BackendIdentity{Token: token, Incarnation: 1}
			otherIdentity := statistics.BackendIdentity{Token: unknown, Incarnation: 2}
			owner.BeginBackend(token, 1)
			mirror.InheritBackendRegistrations(&owner)
			state := reflect.ValueOf(&owner).Elem().FieldByName("backends")
			for range 3 {
				if f, ok := owner.ReadBackend(token); !ok || f != (statistics.BackendFact{}) {
					t.Fatal("registration is logical zero", f, ok)
				}
			}
			if state.Len() != 0 {
				t.Fatal("reads allocated stored history")
			}
			var negative statistics.Reader
			negative.Configure(mode, 1)
			if _, ok := negative.ReadBackend(&owner, otherIdentity, at); ok {
				t.Fatal("unknown cumulative entry present")
			}
			owner.BeginBackend(unknown, 2)
			if _, ok := negative.ReadBackend(&owner, otherIdentity, next); ok != (mode == "none") {
				t.Fatal("negative cumulative cache changed membership", mode, ok)
			}
			negative.Clear()
			if f, ok := negative.ReadBackend(&owner, otherIdentity, next); !ok || f != (statistics.BackendFact{}) {
				t.Fatal("clear retained negative", f, ok)
			}
			owner.End(unknown)
			if owner.ResetBackend(unknown, at) {
				t.Fatal("unknown reset creates entry")
			}
			if !owner.ResetBackend(token, at) {
				t.Fatal("reset before first report")
			}
			owner.BeginBackend(token, 1)
			f, _ := owner.ReadBackend(token)
			if f.ResetAt != at || f.IO.ResetAt != at || f.WAL.ResetAt != at {
				t.Fatal("one backend reset", f)
			}
			var pending statistics.Writer
			pending.Backend(&owner, token, statistics.Fact{Operations: 31, TotalTime: time.Second}, statistics.Fact{Operations: 37, TotalTime: 2 * time.Second})
			var reader statistics.Reader
			reader.Configure(mode, 1)
			before, ok := reader.ReadBackend(&owner, identity, at)
			if !ok {
				t.Fatal("missing live")
			}
			owner.ResetBackend(token, next)
			pending.Flush(&owner, next, true)
			after, _ := reader.ReadBackend(&owner, identity, next)
			if mode == "none" {
				if after.ResetAt != next || after.IO.Operations != 31 || after.WAL.Operations != 37 {
					t.Fatal("published reset canceled valid pending", after)
				}
			} else if after != before {
				t.Fatal("whole entry cache split", after, before)
			}
			reader.Clear()
			current, _ := reader.ReadBackend(&owner, identity, next)
			if current.ResetAt != next || current.IO.Operations != 31 || current.WAL.Operations != 37 {
				t.Fatal("clear failed", current)
			}
			// Snapshot cuts select actual owners in either ordering. Local mirror zeros
			// must not overwrite positive peer facts, and logical live zeros are kept.
			for _, reverse := range []bool{false, true} {
				var snapshot statistics.Reader
				snapshot.Configure("snapshot", 1)
				sources := []statistics.CaptureSource{{Provider: &owner, Owners: []statistics.BackendIdentity{identity}}, {Provider: &mirror, All: true}}
				if reverse {
					sources[0], sources[1] = sources[1], sources[0]
				}
				snapshot.Capture(sources, next)
				got, ok := snapshot.ReadBackend(&owner, identity, next)
				if !ok || got != current {
					t.Fatal("mirror replaced owning fact", got, current)
				}
			}
			owner.BeginBackend(unknown, 2)
			var snapshot statistics.Reader
			snapshot.Configure("snapshot", 1)
			snapshot.Capture([]statistics.CaptureSource{{Provider: &owner, All: true, Owners: []statistics.BackendIdentity{identity, otherIdentity}}}, next)
			if zero, ok := snapshot.ReadBackend(&owner, otherIdentity, next); !ok || zero != (statistics.BackendFact{}) {
				t.Fatal("full capture omitted registered zero", zero, ok)
			}
			if state.Len() != 1 {
				t.Fatal("capture allocated logical zero history")
			}
			owner.Replace(next, nil)
			if zero, ok := owner.ReadBackend(token); !ok || zero != (statistics.BackendFact{}) {
				t.Fatal("replace lost live logical presence", zero, ok)
			}
			if retained, ok := snapshot.ReadBackend(&owner, identity, next); !ok || retained != current {
				t.Fatal("replace mutated retained value", retained, ok)
			}
			owner.End(token)
			if _, ok := owner.ReadBackend(token); ok {
				t.Fatal("ended registration present")
			}
			if state.Len() != 0 {
				t.Fatal("retired stored history")
			}
			// Reused token: late pending work and old cumulative entries cannot be
			// adopted even if the SQL-visible birth timestamp happens to collide.
			var late statistics.Writer
			late.Backend(&owner, unknown, statistics.Fact{Operations: 91}, statistics.Fact{Operations: 97})
			owner.End(unknown)
			owner.BeginBackend(unknown, 3)
			late.Flush(&owner, next, true)
			if f, _ := owner.ReadBackend(unknown); f != (statistics.BackendFact{}) {
				t.Fatal("old pending crossed recycled lifetime", f)
			}
			replacement := statistics.BackendIdentity{Token: unknown, Incarnation: 3}
			if _, ok := snapshot.ReadBackend(&owner, replacement, next); ok {
				t.Fatal("new incarnation adopted old full cut")
			}
			snapshot.Clear()
			if f, ok := snapshot.ReadBackend(&owner, replacement, next); !ok || f != (statistics.BackendFact{}) {
				t.Fatal("clear did not admit new incarnation", f, ok)
			}
		})
	}
	t.Run("BirthAllocator", func(t *testing.T) {
		var p statistics.Provider
		token := new(ids.Domain).Session(7)
		a := statistics.Activity{Principal: statsLogin(), BackendStart: time.Unix(1, 0)}
		first := p.RegisterActivity(token, a)
		if first == 0 || p.RegisterActivity(token, a) != first {
			t.Fatal("duplicate birth changed incarnation")
		}
		p.BeginBackend(token, first)
		p.Reset(time.Now())
		p.Replace(time.Now(), nil)
		if current, ok := p.LiveActivity(token); !ok || current.Incarnation != first {
			t.Fatal("replacement reminted physical birth")
		}
		p.End(token)
		second := p.RegisterActivity(token, a)
		if second <= first {
			t.Fatal("equal timestamp/token reused incarnation", first, second)
		}
		current, _ := p.LiveActivity(token)
		if current.BackendStart != a.BackendStart {
			t.Fatal("timestamp was used as invented identity")
		}
		if p.BeginBackend(token, 0) {
			t.Fatal("zero birth authorized")
		}
		p.BeginBackend(token, second)
		if p.BeginBackend(token, first) {
			t.Fatal("old birth replaced current registration")
		}
	})
}
