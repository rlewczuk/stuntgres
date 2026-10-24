package session

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

func preparedRegistryCode(err error) string {
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return ""
	}
	return diagnostic.Code
}

func TestPreparedRegistryClaimOrder(t *testing.T) {
	domain := &authorization.Domain{}
	owner, other := domain.Principal(1), domain.Principal(2)
	var r PreparedRegistry
	if r.Len() != 0 || r.Lookup("g") != nil {
		t.Fatal("zero registry is not empty")
	}
	if _, err := r.ClaimLocked("g", authorization.Read{}, owner); preparedRegistryCode(err) != "42704" {
		t.Fatal("missing entry", err)
	}
	entry := &PreparedTransaction{GID: "g", Owner: owner}
	r.ReserveLocked()
	r.InstallLocked(entry)
	if r.Len() != 1 || r.Lookup("g") != entry {
		t.Fatal("install", r.Len())
	}
	if _, err := r.ClaimLocked("g", authorization.Read{}, other); preparedRegistryCode(err) != "42501" || entry.Claimed {
		t.Fatal("foreign finisher", err)
	}
	claimed, err := r.ClaimLocked("g", authorization.Read{}, owner)
	if err != nil || claimed != entry || !entry.Claimed {
		t.Fatal("claim", err)
	}
	// Permission precedes the busy check.
	if _, err := r.ClaimLocked("g", authorization.Read{}, other); preparedRegistryCode(err) != "42501" {
		t.Fatal("foreign finisher of a busy entry", err)
	}
	if _, err := r.ClaimLocked("g", authorization.Read{}, owner); preparedRegistryCode(err) != "55000" {
		t.Fatal("busy", err)
	}
	r.ReleaseLocked(entry)
	if entry.Claimed {
		t.Fatal("release")
	}
	r.RemoveLocked("g")
	if r.Len() != 0 || r.Lookup("g") != nil {
		t.Fatal("remove")
	}
}

func TestPreparedRegistryPublicationPreflight(t *testing.T) {
	var r PreparedRegistry
	r.ReserveLocked()
	entry := &PreparedTransaction{GID: "g"}
	finishing := &PreparedPublication{Entry: entry}
	if err := r.PublicationPreflightLocked(finishing); err == nil || preparedRegistryCode(err) != "" {
		t.Fatal("unregistered finishing entry is not stale", err)
	}
	r.InstallLocked(entry)
	if err := r.PublicationPreflightLocked(finishing); err == nil {
		t.Fatal("unclaimed finishing entry is not stale")
	}
	entry.Claimed = true
	if err := r.PublicationPreflightLocked(finishing); err != nil {
		t.Fatal("claimed finishing entry", err)
	}
	origin := &TransactionOwner{State: TxActive}
	preparing := &PreparedPublication{Entry: &PreparedTransaction{GID: "g", Ref: origin.Ref}, Preparing: true, Origin: origin}
	if err := r.PublicationPreflightLocked(preparing); preparedRegistryCode(err) != "42710" {
		t.Fatal("GID in use", err)
	}
	origin.State = TxIdle
	if err := r.PublicationPreflightLocked(preparing); err == nil || preparedRegistryCode(err) != "" {
		t.Fatal("ended origin is not stale", err)
	}
}
