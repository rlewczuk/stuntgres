package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pvc_control_owner", "pvc_control_reader"})
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE pvc_control_owner", "CREATE ROLE pvc_control_reader",
			"GRANT USAGE ON SCHEMA pv_part TO pvc_control_owner,pvc_control_reader",
			"GRANT pg_stat_scan_tables TO pvc_control_reader",
			"ALTER FUNCTION pv_part.pg_check_visible(regclass) OWNER TO pvc_control_owner",
			"CREATE VIEW pv_part.visible_view AS SELECT * FROM pv_part.pg_check_visible('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.frozen_view AS SELECT * FROM pv_part.pg_check_frozen('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.mutator_view AS SELECT pv_part.pg_truncate_visibility_map('pv_part.heap'::regclass) IS NULL AS was_null",
		} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
		pvFreeze(t, p, b, c)
		pvOneFrozen(t, p, b, c)
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			run := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, target, rec, s)
				fixture.RunStep(t, p.Ctx, target, rec, fixture.Prepared(s))
			}
			for _, view := range []string{"visible_view", "frozen_view"} {
				run(fixture.Select("SELECT * FROM pv_part."+view+" ORDER BY t_ctid", []string{"t_ctid"}, []string{"TID"}, nil))
			}
			run(fixture.Select("SELECT pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid='pv_part.pg_check_visible(regclass)'::regprocedure", []string{"owner"}, []string{"NAME"}, [][]any{{"pvc_control_owner"}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='pg_visibility' AND d.classid='pg_proc'::regclass AND d.deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
			for _, fn := range pvcFunctions {
				result := int64(27)
				if fn == pvcFunctions[2] {
					result = 2278
				}
				run(fixture.Select("SELECT prorettype::bigint AS result,has_function_privilege('pvc_control_reader',oid,'EXECUTE') AS allowed FROM pg_proc WHERE oid=$1::regprocedure", []string{"result", "allowed"}, []string{"INT8", "BOOL"}, [][]any{{result, fn != pvcFunctions[2]}}, "pv_part."+fn+"(regclass)"))
			}
		}
		post(c, b.Recorder)
		var definition string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pv_part.mutator_view'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "pg_truncate_visibility_map") || !strings.Contains(strings.ToLower(definition), "is null") {
			t.Fatal("lost stored VOID child", definition)
		}
		pvOneFrozen(t, p, b, c)
		if b.PG {
			pvBoth(t, p, b, c, fixture.Select("SELECT was_null FROM pv_part.mutator_view", []string{"was_null"}, []string{"BOOL"}, [][]any{{false}}))
			pvcSuppressed(t, p, b, c)
			return
		}
		q := "SELECT * FROM pv_part.pg_check_visible($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		readHeld := func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"t_ctid"}, []string{"TID"}, nil, "pv_part.heap"))
		}
		readHeld()
		old, err := c.QueryContext(p.Ctx, "SELECT pv_part.pg_truncate_visibility_map(NULL::regclass) AS result")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		pvOneFrozen(t, p, b, c) // Dump/deparse must not invoke a stored mutator.
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if !old.Next() {
			t.Fatal("detached VOID row lost", old.Err())
		}
		var value any
		if err := old.Scan(&value); err != nil || value != nil {
			t.Fatal("detached VOID NULL", value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("detached tail", old.Err())
		}
		old.Close()
		if len(metadata) != 1 || metadata[0].Name() != "result" || metadata[0].DatabaseTypeName() != "VOID" {
			t.Fatal("detached metadata", metadata)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(c, b.Recorder)
		readHeld()
		pvBoth(t, p, b, c, fixture.Select("SELECT was_null FROM pv_part.mutator_view", []string{"was_null"}, []string{"BOOL"}, [][]any{{false}}))
		pvcSuppressed(t, p, b, c)
		pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
		pvOneFrozen(t, p, b, c)
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore published prefix")
		}
		post(c, b.Recorder)
		readHeld()
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore published prefix")
		}
		post(c, b.Recorder)
		readHeld()
		other, dest, rec := pgcryptoControlDestination(t, p, "pvc_destination")
		for _, q := range []string{"CREATE ROLE pvc_control_owner", "CREATE ROLE pvc_control_reader", "GRANT pg_stat_scan_tables TO pvc_control_reader", "CREATE TEMP TABLE pvc_keep(k integer)"} {
			fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO pvc_keep VALUES(19)", 1))
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(dest, rec)
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k FROM pvc_keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(19)}}))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT was_null FROM pv_part.mutator_view", []string{"was_null"}, []string{"BOOL"}, [][]any{{false}}))
		pvOneFrozen(t, p, b, c)
		post(c, b.Recorder)
		readHeld()
	})
}
