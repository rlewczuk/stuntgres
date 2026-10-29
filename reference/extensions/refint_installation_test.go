package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRefintInstallation(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"refint_installer"})
	refintCase(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec(`CREATE SCHEMA "Refint Q"`, 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec(`CREATE EXTENSION refint SCHEMA "Refint Q"`, 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='refint'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// Untrusted installation must fail before publishing any member.
		run(fixture.Exec("CREATE ROLE refint_installer", 0))
		defer func() {
			for _, q := range []string{"RESET ROLE", "DROP OWNED BY refint_installer", "DROP ROLE refint_installer"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error(q, err) }
			}
		}()
		run(fixture.Exec("SET ROLE refint_installer", 0))
		untrusted := refintError("42501", `permission denied to create extension "refint"`)
		untrusted.Hint = "Must be superuser to create this extension."
		run(fixture.ErrorDiagnostic(`CREATE EXTENSION refint SCHEMA "Refint Q"`, untrusted))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='refint'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// A collision on the second member must roll back the first member.
		run(fixture.Exec(`CREATE FUNCTION "Refint Q".check_foreign_key() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN OLD; END $$`, 0))
		var decoy int64
		if err := c.QueryRowContext(p.Ctx, `SELECT '"Refint Q".check_foreign_key()'::regprocedure::oid`).Scan(&decoy); err != nil { t.Fatal(err) }
		collision := refintError("42723", `function "check_foreign_key" already exists with same argument types`)
		collision.Context = "SQL statement \"CREATE FUNCTION check_foreign_key()\nRETURNS trigger\nAS '$libdir/refint'\nLANGUAGE C\"\nextension script file \"refint--1.0.sql\", near line 11"
		run(fixture.ErrorDiagnostic(`CREATE EXTENSION refint SCHEMA "Refint Q"`, collision))
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='refint'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE nspname='Refint Q' AND proname='check_primary_key'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select(`SELECT '"Refint Q".check_foreign_key()'::regprocedure::oid::bigint AS oid`, []string{"oid"}, []string{"INT8"}, [][]any{{decoy}}))
		run(fixture.Exec(`DROP FUNCTION "Refint Q".check_foreign_key()`, 0))
		run(fixture.Exec(`CREATE EXTENSION refint SCHEMA "Refint Q"`, 0))
		run(fixture.Select("SELECT extversion,extrelocatable,nspname,obj_description(e.oid,'pg_extension') AS comment FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE extname='refint'", []string{"extversion", "extrelocatable", "nspname", "comment"}, []string{"TEXT", "BOOL", "NAME", "TEXT"}, [][]any{{"1.0", true, "Refint Q", "functions for implementing referential integrity (obsolete)"}}))
		// Every portable exposed stock attribute has an independent release/default
		// expectation. Failure displays actual defaults for the root to reconcile.
		run(fixture.Select(`SELECT proname,pronargs,prorettype::regtype::text AS result,l.lanname,provolatile,proisstrict,proparallel,prosecdef,procost,prosrc,probin FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE nspname='Refint Q' ORDER BY proname`, []string{"proname", "pronargs", "result", "lanname", "provolatile", "proisstrict", "proparallel", "prosecdef", "procost", "prosrc", "probin"}, []string{"NAME", "INT2", "TEXT", "NAME", "CHAR", "BOOL", "CHAR", "BOOL", "FLOAT4", "TEXT", "TEXT"}, [][]any{
			{"check_foreign_key", int64(0), "trigger", "c", "v", false, "u", false, float64(1), "check_foreign_key", "$libdir/refint"},
			{"check_primary_key", int64(0), "trigger", "c", "v", false, "u", false, float64(1), "check_primary_key", "$libdir/refint"},
		}))
		var ext int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='refint'").Scan(&ext); err != nil || ext <= 0 { t.Fatal("extension identity", ext, err) }
		rows, err := c.QueryContext(p.Ctx, "SELECT objid FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND classid=1255 AND deptype='e' ORDER BY objid", ext)
		if err != nil { t.Fatal(err) }
		var ids []int64
		for rows.Next() { var id int64; if err := rows.Scan(&id); err != nil { t.Fatal(err) }; ids = append(ids, id) }
		if err := rows.Err(); err != nil { t.Fatal(err) }
		rows.Close()
		if len(ids) != 2 || ids[0] <= 0 || ids[0] == ids[1] { t.Fatal("distinct members", ids) }
		for _, name := range []string{"check_primary_key", "check_foreign_key"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(`SELECT "Refint Q".`+name+`()`, refintError("XX000", name+": not fired by trigger manager"))
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				run(s)
			}
		}
		run(fixture.ErrorDiagnostic("CREATE EXTENSION refint", refintError("42710", `extension "refint" already exists`)))
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS refint", 0))
		wantNotice := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "refint" already exists, skipping`}}
		if !reflect.DeepEqual(*notices, wantNotice) { t.Fatalf("notices got %#v want %#v", *notices, wantNotice) }
		*notices = nil
	})
}
