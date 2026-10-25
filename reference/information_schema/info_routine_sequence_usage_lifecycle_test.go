package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutineSequenceUsageLifecycle(t *testing.T) {
	rsuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE SCHEMA rsu_moved", "CREATE SEQUENCE rsu_life_a", "CREATE SEQUENCE rsu_life_b",
			"CREATE FUNCTION rsu_life_r() RETURNS bigint RETURN nextval('public.rsu_life_a')",
			"CREATE FUNCTION rsu_life_other() RETURNS bigint RETURN nextval('public.rsu_life_b')",
		} {
			run(fixture.Exec(q, 0))
		}
		id := rsuIdentity(t, p, c, "public", "rsu_life_r", "")
		otherID := rsuIdentity(t, p, c, "public", "rsu_life_other", "")
		other := rsuRow(database, "public", "rsu_life_other", otherID, "public", "rsu_life_b")
		where := "routine_name IN ('rsu_life_r','rsu_life_other')"
		step := func(schema, sequence string) fixture.Step {
			return rsuProjection(b.PG, where, [][]any{rsuRow(database, "public", "rsu_life_r", id, schema, sequence), other})
		}
		held := rsuHeld(t, p, c, r, step("public", "rsu_life_a").Query)
		held(step("public", "rsu_life_a"))
		run(fixture.Exec("ALTER SEQUENCE rsu_life_a RENAME TO rsu_life_renamed", 0))
		held(step("public", "rsu_life_renamed"))
		run(fixture.Exec("ALTER SEQUENCE rsu_life_renamed SET SCHEMA rsu_moved", 0))
		held(step("rsu_moved", "rsu_life_renamed"))
		run(fixture.Exec("DROP FUNCTION rsu_life_r()", 0))
		run(fixture.Exec("CREATE FUNCTION rsu_life_r() RETURNS bigint RETURN nextval('public.rsu_life_b')", 0))
		newID := rsuIdentity(t, p, c, "public", "rsu_life_r", "")
		if newID == id {
			t.Fatal("recreated routine retained dropped identity", id)
		}
		id = newID
		held(step("public", "rsu_life_b"))
		run(fixture.Exec("DROP SEQUENCE rsu_moved.rsu_life_renamed", 0))
		held(step("public", "rsu_life_b"))
		// Only the two actual dependents of b disappear; an independent pair
		// on a newly created sequence must survive the cascade unchanged.
		run(fixture.Exec("CREATE SEQUENCE rsu_life_keep", 0))
		run(fixture.Exec("CREATE FUNCTION rsu_life_keep_r() RETURNS bigint RETURN nextval('public.rsu_life_keep')", 0))
		keepID := rsuIdentity(t, p, c, "public", "rsu_life_keep_r", "")
		*notices = nil
		run(fixture.Exec("DROP SEQUENCE rsu_life_b CASCADE", 0))
		if b.PG {
			wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to function rsu_life_other()\ndrop cascades to function rsu_life_r()"}
			fixture.RegtypeAssertNotices(t, *notices, []fixture.Diagnostic{wantNotice})
		}
		held(rsuProjection(b.PG, where, nil))
		run(rsuProjection(b.PG, "routine_name='rsu_life_keep_r'", [][]any{rsuRow(database, "public", "rsu_life_keep_r", keepID, "public", "rsu_life_keep")}))
		run(fixture.Select("SELECT proname FROM pg_catalog.pg_proc WHERE proname IN ('rsu_life_r','rsu_life_other')", []string{"proname"}, []string{"NAME"}, nil))
	})
}

func TestReferenceInfoRoutineSequenceUsageControls(t *testing.T) {
	rsuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE ROLE rsu_owner", "CREATE ROLE rsu_reader",
			"CREATE SEQUENCE rsu_control_a", "CREATE SEQUENCE rsu_control_b",
			"CREATE FUNCTION rsu_control_r() RETURNS bigint BEGIN ATOMIC SELECT nextval('public.rsu_control_a'); SELECT nextval('public.rsu_control_a')+nextval('public.rsu_control_b'); END",
			"CREATE FUNCTION rsu_control_text() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('public.rsu_control_a')$$",
			"ALTER SEQUENCE rsu_control_a OWNER TO rsu_owner",
		} {
			run(fixture.Exec(q, 0))
		}
		where := "routine_name IN ('rsu_control_r','rsu_control_text')"
		want := func() [][]any {
			id := rsuIdentity(t, p, c, "public", "rsu_control_r", "")
			return [][]any{rsuRow(database, "public", "rsu_control_r", id, "public", "rsu_control_a"), rsuRow(database, "public", "rsu_control_r", id, "public", "rsu_control_b")}
		}
		check := func() {
			id := rsuIdentity(t, p, c, "public", "rsu_control_r", "")
			run(rsuProjection(b.PG, where, want()))
			run(fixture.Exec("SET SESSION AUTHORIZATION rsu_reader", 0))
			run(rsuProjection(b.PG, where, nil))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rsu_owner", 0))
			run(rsuProjection(b.PG, where, [][]any{rsuRow(database, "public", "rsu_control_r", id, "public", "rsu_control_a")}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		check()
		if b.PG {
			return
		} // Dump/Reset/Restore are native public APIs only.
		original := want()
		rows, err := c.QueryContext(p.Ctx, rsuProjection(false, where, original).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(rsuProjection(false, where, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var retained [][]any
		for rows.Next() {
			values := make([]any, 9)
			dest := make([]any, 9)
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			retained = append(retained, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		// Order is unspecified; retain exact multiplicity without sorting a
		// provider-dependent traversal order into the expected contract.
		if len(retained) != len(original) {
			t.Fatalf("retained=%v want=%v", retained, original)
		}
		for _, expected := range original {
			found := 0
			for _, actual := range retained {
				if reflect.DeepEqual(actual, expected) {
					found++
				}
			}
			if found != 1 {
				t.Fatalf("retained=%v missing/duplicate=%v", retained, expected)
			}
		}
		rsuMetadata(t, columns, false)
		check()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE FUNCTION rsu_bad() RETURNS bigint RETURN nextval('rsu_absent');")); err == nil {
			t.Fatal("invalid restore succeeded")
		}
		check()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err == nil {
			t.Fatal("canceled restore succeeded")
		}
		check()
	})
}
