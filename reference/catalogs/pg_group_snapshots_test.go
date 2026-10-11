package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGGroupFreshness(t *testing.T) {
	pgGroupScenario(t, []string{"pgg_fresh", "pgg_fresh_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "SELECT * FROM pg_catalog.pg_group WHERE groname=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		check := func(want [][]any) {
			pgGroupQuery(t, p, b, held, q, false, want, "pgg_fresh")
			pgGroupQuery(t, p, b, b.DB, q, false, want, "pgg_fresh")
		}
		check(nil)
		id := pgGroupCreate(t, p, b, "pgg_fresh", "NOLOGIN")
		member := pgGroupCreate(t, p, b, "pgg_fresh_member", "LOGIN")
		check([][]any{pgGroupRow("pgg_fresh", id)})
		idQuery := "SELECT * FROM pg_group WHERE grosysid=$1"
		byID, err := b.DB.PrepareContext(p.Ctx, idQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer byID.Close()
		heldID := fixture.HeldStatement{Executor: b.DB, Statement: byID, SQL: idQuery}
		checkID := func(want [][]any) {
			pgGroupQuery(t, p, b, heldID, idQuery, false, want, id)
			pgGroupQuery(t, p, b, b.DB, idQuery, false, want, id)
		}
		for _, tc := range []struct {
			q       string
			members []int64
			present bool
		}{
			{"GRANT pgg_fresh TO pgg_fresh_member", []int64{member}, true},
			{"REVOKE pgg_fresh FROM pgg_fresh_member", nil, true},
			{"ALTER ROLE pgg_fresh LOGIN", nil, false},
			{"ALTER ROLE pgg_fresh NOLOGIN", nil, true},
		} {
			pgRolesExec(t, p, b, tc.q)
			var want [][]any
			if tc.present {
				want = [][]any{pgGroupRow("pgg_fresh", id, tc.members...)}
			}
			check(want)
			checkID(want)
		}
		pgRolesExec(t, p, b, "DROP ROLE pgg_fresh")
		check(nil)
		checkID(nil)
		replacement := pgGroupCreate(t, p, b, "pgg_fresh", "NOLOGIN")
		if replacement == id {
			t.Fatal("dropped group identity reused")
		}
		check([][]any{pgGroupRow("pgg_fresh", replacement)})
		checkID(nil)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if rows, err := stmt.QueryContext(canceled, "pgg_fresh"); rows != nil || !errors.Is(err, context.Canceled) {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("canceled held query", err)
		}
		if rows, err := b.DB.QueryContext(canceled, q, "pgg_fresh"); rows != nil || !errors.Is(err, context.Canceled) {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("canceled direct query", err)
		}
		if s, err := b.DB.PrepareContext(canceled, q); s != nil || !errors.Is(err, context.Canceled) {
			if s != nil {
				s.Close()
			}
			t.Fatal("canceled prepare", err)
		}
		check([][]any{pgGroupRow("pgg_fresh", replacement)})
	})
}

// Retain the exact middleware operation before later queries reuse the SQL.
func pgGroupOpenRetained(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, args ...any) (*sql.Rows, *fixture.Operation, []*sql.ColumnType) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	rows, err := b.DB.QueryContext(ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	meta := pgGroupMetadata(t, rows, false)
	b.Recorder.Mu.Lock()
	var op *fixture.Operation
	for _, candidate := range b.Recorder.Operations {
		if candidate.SQL == q && candidate.Columns != nil && candidate.Err == nil {
			op = candidate
		}
	}
	b.Recorder.Mu.Unlock()
	if op == nil {
		rows.Close()
		t.Fatal("retained record unavailable")
	}
	return rows, op, meta
}

func TestReferencePGGroupSnapshots(t *testing.T) {
	pgGroupScenario(t, []string{"pgg_snapshot", "pgg_snapshot_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgGroupCreate(t, p, b, "pgg_snapshot", "NOLOGIN")
		member := pgGroupCreate(t, p, b, "pgg_snapshot_member", "LOGIN")
		pgRolesExec(t, p, b, "GRANT pgg_snapshot TO pgg_snapshot_member")
		for _, q := range []string{"CREATE VIEW pgg_snapshot_view AS SELECT * FROM pg_catalog.pg_group WHERE groname='pgg_snapshot'", "CREATE VIEW pgg_snapshot_nested AS SELECT * FROM pgg_snapshot_view"} {
			pgRolesExec(t, p, b, q)
		}
		queries := []string{
			"SELECT * FROM pgg_snapshot_view", "SELECT * FROM pgg_snapshot_nested",
			"SELECT * FROM (SELECT * FROM pg_group) d WHERE groname='pgg_snapshot'",
			"SELECT * FROM (SELECT * FROM (SELECT * FROM pg_group) n) d WHERE groname='pgg_snapshot'",
		}
		joined := []string{
			"SELECT a.groname,a.grosysid,a.grolist,b.groname,b.grosysid,b.grolist FROM pg_group a JOIN pg_group b ON a.grosysid=b.grosysid WHERE a.groname='pgg_snapshot'",
			"SELECT a.groname,a.grosysid,a.grolist,b.groname,b.grosysid,b.grolist FROM pgg_snapshot_nested a JOIN pg_group b ON a.grosysid=b.grosysid WHERE a.groname='pgg_snapshot'",
		}
		check := func(members ...int64) {
			for _, q := range queries {
				pgGroupBoth(t, p, b, q, [][]any{pgGroupRow("pgg_snapshot", id, members...)})
			}
			// Inspect both captured arrays in the same statement without invoking
			// excluded SQL array equality. These fixtures have zero or one member.
			row := pgGroupRow("pgg_snapshot", id, members...)
			paired := []any{row[0], row[1], row[2], row[0], row[1], row[2]}
			for _, q := range joined {
				pgRolesBoth(t, p, b, fixture.Select(q, []string{"groname", "grosysid", "grolist", "groname", "grosysid", "grolist"}, []string{"NAME", "OID", "_OID", "NAME", "OID", "_OID"}, [][]any{paired}))
			}
		}
		check(member)
		var old *sql.Rows
		var record *fixture.Operation
		var saved []*sql.ColumnType
		q := "SELECT * FROM pg_group WHERE grosysid=$1"
		if !b.PG {
			old, record, saved = pgGroupOpenRetained(t, p, b, q, id)
			defer old.Close()
		}
		pgRolesExec(t, p, b, "REVOKE pgg_snapshot FROM pgg_snapshot_member")
		check()
		pgRolesExec(t, p, b, "GRANT pgg_snapshot TO pgg_snapshot_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		check(member)
		pgRolesExec(t, p, b, "DROP ROLE pgg_snapshot")
		pgGroupBoth(t, p, b, "SELECT * FROM pgg_snapshot_nested", nil)
		replacement := pgGroupCreate(t, p, b, "pgg_snapshot", "NOLOGIN")
		if replacement == id {
			t.Fatal("replacement aliases old identity")
		}
		pgGroupBoth(t, p, b, "SELECT * FROM pgg_snapshot_nested", [][]any{pgGroupRow("pgg_snapshot", replacement)})
		if old != nil {
			pgGroupConsume(t, old, b, q, [][]any{pgGroupRow("pgg_snapshot", id, member)}, record)
			pgGroupSavedMetadata(t, saved, false)
		}
	})
}
