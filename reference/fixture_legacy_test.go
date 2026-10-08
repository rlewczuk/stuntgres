//go:build reference

package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceFixtureSessionAuthorizationReset(t *testing.T) {
	t.Run("replay", func(t *testing.T) {
		p := newReferencePair(t)
		conn, err := referenceAdmin.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		// Model a replay returning its physical session without resetting auth.
		// Pair cleanup must discard it rather than create public as postgres.
		if _, err := conn.ExecContext(p.ctx, "SET SESSION AUTHORIZATION postgres; CREATE TABLE fixture_replay_owner(v INT)"); err != nil {
			t.Fatal(err)
		}
		var owner string
		if err := conn.QueryRowContext(p.ctx, "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='fixture_replay_owner'::regclass").Scan(&owner); err != nil || owner != "postgres" {
			t.Fatalf("replay owner: got %q, want postgres, error %v", owner, err)
		}
	})
	checkReset := func(t *testing.T) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var sessionUser, currentUser, owner string
		var empty bool
		if err := referenceAdmin.QueryRowContext(ctx, `SELECT session_user, current_user, pg_get_userbyid(nspowner), NOT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=pg_namespace.oid) FROM pg_namespace WHERE nspname='public'`).Scan(&sessionUser, &currentUser, &owner, &empty); err != nil {
			t.Fatal(err)
		}
		if sessionUser != "stuntgres" || currentUser != "stuntgres" || owner != "stuntgres" || !empty {
			t.Fatalf("schema reset: session=%q current=%q owner=%q empty=%t", sessionUser, currentUser, owner, empty)
		}
	}
	// Check the after-case reset separately from the next case's before reset.
	checkReset(t)
	t.Run("next_case", func(t *testing.T) {
		newReferencePair(t)
		checkReset(t)
	})
}
