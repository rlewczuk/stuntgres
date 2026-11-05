package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendIdentityFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bi_smoke_target", "bi_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		admin := statActivityConn(t, p, b)
		survivor := statActivityConn(t, p, b)
		activityExec(t, p, admin, "CREATE ROLE bi_smoke_target LOGIN PASSWORD 'role-attributes-fixture'")
		activityExec(t, p, admin, "CREATE ROLE bi_smoke_reader LOGIN PASSWORD 'role-attributes-fixture'")
		pool := fixture.RoleAttributesPool(t, p, b, "bi_smoke_target")
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		before := identityMapped(t, p, b, o, peer)
		stable := identityMapped(t, p, b, o, survivor)
		if before[0] == stable[0] || before[1] == stable[1] {
			t.Fatal("concurrent physical identities alias", before, stable)
		}
		// Real source alias and ordinal metadata, without inventing slot order.
		r, err := o.QueryContext(p.Ctx, "SELECT slot,n FROM pg_stat_get_backend_idset() WITH ORDINALITY AS b(slot,n) ORDER BY n")
		if err != nil {
			t.Fatal(err)
		}
		ordinals := identityRows(t, r, b.PG, []string{"slot", "n"}, []string{"INT4", "INT8"})
		found := false
		for i, row := range ordinals {
			if row[1] != int64(i+1) {
				t.Fatal("source ordinal", ordinals)
			}
			found = found || row[0] == before[0]
		}
		if !found {
			t.Fatal("mapped physical slot not enumerated", ordinals, before)
		}
		s, err := o.PrepareContext(p.Ctx, identityLookupSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		e := fixture.HeldStatement{Executor: o, Statement: s, SQL: identityLookupSQL}
		if got := identityLookup(t, p, b, e, nil); !reflect.DeepEqual(got, []any{nil, nil, nil, nil, nil, nil, nil}) {
			t.Fatal("strict held NULL", got)
		}
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		readerPool := fixture.RoleAttributesPool(t, p, b, "bi_smoke_reader")
		reader, err := readerPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		rs, err := reader.PrepareContext(p.Ctx, identityLookupSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		re := fixture.HeldStatement{Executor: reader, Statement: rs, SQL: identityLookupSQL}
		activityExec(t, p, reader, "BEGIN")
		defer reader.ExecContext(context.Background(), "ROLLBACK")
		identityMasked(t, identityLookup(t, p, b, re, before[0]), before, false)
		activityExec(t, p, admin, "GRANT bi_smoke_target TO bi_smoke_reader")
		var inherited bool
		if err := reader.QueryRowContext(p.Ctx, "SELECT pg_has_role('bi_smoke_target','USAGE') FROM pg_roles WHERE rolname=current_user").Scan(&inherited); err != nil || !inherited {
			t.Fatal("published inherited authority", inherited, err)
		}
		identityMasked(t, identityLookup(t, p, b, re, before[0]), before, true)
		activityExec(t, p, reader, "ROLLBACK")
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		newcomer := statActivityConn(t, p, b)
		newPID := statActivityPID(t, p, newcomer)
		identityPIDAbsent(t, identitySet(t, p, b, o), newPID)
		identityActivityCount(t, p, o, newPID, 0)
		statActivitySignal(t, p, admin, before[1].(int64), true)
		statActivityRetired(t, p, admin, before[1].(int64))
		identityEqual(t, identityLookup(t, p, b, e, before[0]), before)
		identityEqual(t, identityForPID(t, identitySet(t, p, b, o), before[1].(int64)), before)
		identityActivityCount(t, p, o, before[1].(int64), 1)
		statActivityClear(t, p, o)
		identityRetiredFresh(t, p, b, o, before)
		identityForPID(t, identitySet(t, p, b, o), newPID)
		identityActivityCount(t, p, o, before[1].(int64), 0)
		activityExec(t, p, o, "COMMIT")
		backendReset(t, p, admin, stable[1].(int64))
		activityExec(t, p, admin, "SELECT pg_stat_reset()")
		statActivityClear(t, p, o)
		identityEqual(t, identityLookup(t, p, b, e, stable[0]), stable)
		// Full 42883 contract confirmed by pinned PostgreSQL18.6 discovery.
		q := "/*é🙂*/ SELECT pg_stat_get_backend_pid(true)"
		step := fixture.ErrorDiagnostic(q, identityDiagnostic(q, "pg_stat_get_backend_pid", "42883", "function pg_stat_get_backend_pid(boolean) does not exist", identityNoMatchHint))
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		identityEqual(t, identityLookup(t, p, b, e, stable[0]), stable)
	})
}
