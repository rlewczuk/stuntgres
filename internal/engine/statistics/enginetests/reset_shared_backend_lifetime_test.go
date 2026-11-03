package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

func backendRegistrationCount(p *statistics.Provider) int {
	return reflect.ValueOf(p).Elem().FieldByName("liveBackends").Len()
}

func statsProviderBackendNoParticipants(t *testing.T) {
	var p statistics.Provider
	domain := new(ids.Domain)
	owner := new(struct {
		ids.SessionOwnerMarker
		payload [4096]byte
	})
	invalid := []ids.SessionToken{{}, domain.Session(0), domain.DetachedSession(1), domain.PrivateSession(owner)}
	var writer statistics.Writer
	for _, token := range invalid {
		if p.BeginBackend(token) {
			t.Fatal("invalid or private identity received backend authority", token)
		}
		writer.Backend(&p, token, statistics.Fact{Operations: 1}, statistics.Fact{Operations: 2})
		p.End(token)
	}
	for i := ids.SessionID(1); i <= 128; i++ {
		// A valid-shaped token without positive authority is not a participant.
		token := domain.Session(i)
		writer.Backend(&p, token, statistics.Fact{Operations: 1}, statistics.Fact{Operations: 2})
		p.End(token)
	}
	writer.Flush(&p, time.Unix(100, 0), true)
	state := reflect.ValueOf(&p).Elem()
	if !state.FieldByName("liveBackends").IsNil() || !state.FieldByName("backends").IsNil() || writer.Pending() {
		t.Fatal("nonparticipant/private lifetime allocated backend history")
	}
	p.Replace(time.Unix(101, 0), nil)
	for _, token := range invalid {
		writer.Backend(&p, token, statistics.Fact{Operations: 3}, statistics.Fact{})
		p.End(token)
	}
	if !state.FieldByName("liveBackends").IsNil() || writer.Pending() {
		t.Fatal("replacement admitted invalid/private history")
	}
}

func statsProviderBackendRegistrationChurn(t *testing.T) {
	var p statistics.Provider
	domain := new(ids.Domain)
	peer := domain.Session(1)
	at := time.Unix(100, 0)
	if !p.BeginBackend(peer) {
		t.Fatal("live peer registration rejected")
	}
	var seed statistics.Writer
	seed.Backend(&p, peer, statistics.Fact{Operations: 17}, statistics.Fact{Operations: 19})
	seed.Flush(&p, at, true)
	before, _ := p.ReadBackend(peer)
	var retained statistics.Reader
	retained.Configure("snapshot", 1)
	retained.ReadBackend(&p, peer, at)
	for i := ids.SessionID(2); i <= 129; i++ {
		token := domain.Session(i)
		p.BeginBackend(token)
		var writer statistics.Writer
		writer.Backend(&p, token, statistics.Fact{Operations: 11}, statistics.Fact{Operations: 13})
		writer.Flush(&p, at, true)
		writer.Backend(&p, token, statistics.Fact{Operations: 3}, statistics.Fact{Operations: 4})
		if !writer.Pending() {
			t.Fatal("positive pre-end report was not pending")
		}
		p.End(token)
		p.End(token)
		if backendRegistrationCount(&p) != 1 {
			t.Fatal("public churn retained historical registrations", i, backendRegistrationCount(&p))
		}
		writer.Flush(&p, at.Add(time.Second), false)
		writer.Backend(&p, token, statistics.Fact{Operations: 7}, statistics.Fact{Operations: 9})
		if writer.Pending() {
			t.Fatal("report after end registered itself")
		}
		if _, exists := p.ReadBackend(token); exists {
			t.Fatal("pending end report resurrected token", i)
		}
		if current, _ := p.ReadBackend(peer); current != before {
			t.Fatal("end changed active peer", current, before)
		}
		if cached, _ := retained.ReadBackend(&p, peer, at); cached != before {
			t.Fatal("end changed retained peer capture")
		}
	}
	ended := domain.Session(130)
	p.BeginBackend(ended)
	var late statistics.Writer
	late.Backend(&p, ended, statistics.Fact{Operations: 5}, statistics.Fact{Operations: 6})
	var peerPending statistics.Writer
	peerPending.Backend(&p, peer, statistics.Fact{Operations: 23}, statistics.Fact{Operations: 29})
	p.End(ended)
	p.Replace(at, nil)
	late.Flush(&p, at, true)
	peerPending.Flush(&p, at, true)
	if _, exists := p.ReadBackend(peer); exists {
		t.Fatal("old live pending generation crossed replacement")
	}
	late.Backend(&p, ended, statistics.Fact{Operations: 7}, statistics.Fact{Operations: 8})
	if late.Pending() || backendRegistrationCount(&p) != 1 {
		t.Fatal("replacement restored ended authority")
	}
	// Replacement invalidates old pending generations but retains genuinely live
	// registration. A new actual peer report remains admissible.
	seed.Backend(&p, peer, statistics.Fact{Operations: 5}, statistics.Fact{Operations: 6})
	seed.Flush(&p, at, true)
	if current, ok := p.ReadBackend(peer); !ok || current.IO.Operations != 5 || current.WAL.Operations != 6 {
		t.Fatal("replacement lost live authority", current, ok)
	}
	p.Reset(at)
	late.Backend(&p, ended, statistics.Fact{Operations: 9}, statistics.Fact{})
	late.Flush(&p, at, true)
	if _, exists := p.ReadBackend(ended); exists {
		t.Fatal("reset restored ended authority")
	}
	if cached, _ := retained.ReadBackend(&p, peer, at); cached != before {
		t.Fatal("replacement mutated retained snapshot")
	}
	p.End(peer)
	state := reflect.ValueOf(&p).Elem()
	if !state.FieldByName("liveBackends").IsNil() || !state.FieldByName("backends").IsNil() {
		t.Fatal("final end retained backend ownership")
	}
}

func statsProviderBackendRegistrationInheritance(t *testing.T) {
	var source, destination statistics.Provider
	token := new(ids.Domain).Session(1)
	source.BeginBackend(token)
	var writer statistics.Writer
	at := time.Unix(100, 0)
	writer.Backend(&source, token, statistics.Fact{Operations: 11}, statistics.Fact{Operations: 13})
	writer.Flush(&source, at, true)
	source.ResetBackend(token, at)
	destination.InheritBackendRegistrations(&source)
	if backendRegistrationCount(&destination) != 1 {
		t.Fatal("new target missed active registration")
	}
	if _, exists := destination.ReadBackend(token); exists {
		t.Fatal("new target inherited monitoring history")
	}
	var targetWriter statistics.Writer
	targetWriter.Backend(&destination, token, statistics.Fact{Operations: 17}, statistics.Fact{Operations: 19})
	targetWriter.Flush(&destination, at, true)
	if f, ok := destination.ReadBackend(token); !ok || f.IO.Operations != 17 || f.WAL.Operations != 19 || !f.ResetAt.IsZero() {
		t.Fatal("inherited authority/report history", f, ok)
	}
	source.End(token)
	if backendRegistrationCount(&destination) != 1 {
		t.Fatal("target membership map aliases source")
	}
	destination.End(token)
	if backendRegistrationCount(&source) != 0 || backendRegistrationCount(&destination) != 0 {
		t.Fatal("partition end retained authority")
	}
}
