package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectACLLifecycle(t *testing.T) {
	objectACLScenario(t, []string{"acl_obj_data_role"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE acl_obj_recipe(a INT,b INT)", 0))
		run(fixture.Exec("CREATE VIEW acl_obj_recipe_view AS SELECT pg_get_acl(1259,'acl_obj_recipe'::regclass,1) AS acl", 0))
		run(fixture.Exec("CREATE VIEW acl_obj_named_recipe AS SELECT pg_get_acl(objsubid => 1,objid => 'acl_obj_recipe'::regclass,classid => 1259) AS acl", 0))
		run(fixture.Exec("CREATE TABLE acl_obj_data(id INT,acl ACLITEM[])", 0))
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_recipe TO PUBLIC", 0))
		run(fixture.Exec("INSERT INTO acl_obj_data SELECT 1,pg_get_acl(1259,'acl_obj_recipe'::regclass,1)", 1))
		run(fixture.Exec("REVOKE SELECT(a) ON acl_obj_recipe FROM PUBLIC", 0))
		run(fixture.Exec("GRANT UPDATE(b) ON acl_obj_recipe TO acl_obj_data_role", 0))
		run(fixture.Exec("INSERT INTO acl_obj_data SELECT 2,pg_get_acl(1259,'acl_obj_recipe'::regclass,2)", 1))
		run(fixture.Exec("REVOKE UPDATE(b) ON acl_obj_recipe FROM acl_obj_data_role", 0))
		run(fixture.Exec("INSERT INTO acl_obj_data VALUES (3,NULL),(4,'{}'::aclitem[])", 2))
		check := fixture.Select("SELECT id,acl FROM acl_obj_data ORDER BY id", []string{"id", "acl"}, []string{"INT4", "_ACLITEM"}, [][]any{{int64(1), "{=r/" + objectACLOwner(b) + "}"}, {int64(2), "{acl_obj_data_role=w/" + objectACLOwner(b) + "}"}, {int64(3), nil}, {int64(4), "{}"}})
		checks := []fixture.Step{
			check,
			objectACLSelect("SELECT acl FROM acl_obj_recipe_view", nil),
			objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_recipe'::regclass,2) AS acl", nil),
			fixture.Select("SELECT unnest(acl) AS acl FROM acl_obj_data WHERE id=1", []string{"acl"}, []string{"ACLITEM"}, [][]any{{"=r/" + objectACLOwner(b)}}),
			objectACLSelect("SELECT acl FROM acl_obj_named_recipe", nil),
		}
		checks[1].CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, "_ACLITEM") }
		for _, s := range checks {
			run(s)
		}
		if b.PG {
			return // Native controls are not emulated by PostgreSQL.
		}
		old, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
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
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.ScalarRows(t, false, old, check.Columns, check.Types, check.Rows, false)
		fixture.ScalarAssertDescription(t, metadata, check.Columns, check.Types)
		for _, s := range checks {
			run(s)
		}
		// A destination role has a different identity but the same SQL name.
		// Returned ACL data must remap principals, not retain source identities.
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector(t.Name() + "/acl-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		for _, q := range []string{"CREATE ROLE acl_obj_destination_spacer", "CREATE ROLE acl_obj_data_role"} {
			if _, err := destination.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range checks {
			rows, err := destination.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			if s.CheckMetadata != nil {
				s.CheckMetadata(t, rows)
			}
			fixture.ScalarRows(t, false, rows, s.Columns, s.Types, s.Rows, s.Bag)
		}
	})
}

type objectACLWriter struct {
	bytes.Buffer
	calls int
}

func (w *objectACLWriter) Write(p []byte) (int, error) {
	w.calls++
	return w.Buffer.Write(p)
}

func objectACLRoundtripControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
	checks := []fixture.Step{
		objectACLSelect("SELECT pg_get_acl(1259,'"+table+"'::regclass,1) AS acl", "{=r/"+objectACLOwner(b)+"}"),
		objectACLSelect("SELECT pg_get_acl(1259,'"+table+"'::regclass,0) AS acl", nil),
		objectACLSelect("SELECT pg_get_acl(1259,'"+table+"'::regclass,2) AS acl", nil),
		fixture.Select("SELECT a FROM "+table, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}),
	}
	for _, s := range checks {
		run(s)
	}
	if b.PG {
		return
	}
	var out objectACLWriter
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
		t.Fatal("Dump column ACL", err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(out.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, s := range checks {
		run(s)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if redump.String() != out.String() {
		t.Fatal("column ACL redump changed")
	}
	err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE acl_obj_replacement(a INT); GRANT SELECT ON acl_obj_replacement TO PUBLIC"))
	want := fixture.ControlsReferenceDiagnostic("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
	fixture.AssertDiagnostic(t, err, &want)
	for _, s := range checks {
		run(s)
	}
	run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='acl_obj_replacement' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func TestReferenceObjectACLControls(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		objectACLRun(t, p, b, c, fixture.Exec("CREATE TABLE acl_obj_controls(a INT,b INT)", 0))
		objectACLRun(t, p, b, c, fixture.Exec("INSERT INTO acl_obj_controls VALUES (7,9)", 1))
		objectACLRun(t, p, b, c, fixture.Exec("GRANT SELECT(a) ON acl_obj_controls TO PUBLIC", 0))
		objectACLRoundtripControls(t, p, b, c, "acl_obj_controls")
	})
}

func TestReferenceObjectACLCancellation(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT pg_get_acl($1,$2,$3) AS acl"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if b.PG {
			// Existing admission helper observes pg_sleep on this exact owner,
			// cancels through the server and joins before the inquiry resumes.
			fixture.SessionCancel(t, p.Ctx, c, false, true)
			fixture.SessionCancel(t, p.Ctx, c, true, true)
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, held := range []bool{false, true} {
				var rows *sql.Rows
				if held {
					rows, err = stmt.QueryContext(ctx, int64(1259), int64(0), int64(0))
				} else {
					rows, err = c.QueryContext(ctx, q, int64(1259), int64(0), int64(0))
				}
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled query published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("precanceled query", err)
				}
			}
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := objectACLSelect(q, nil)
		s.Args = []any{int64(1259), int64(0), int64(0)}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		objectACLRun(t, p, b, c, objectACLSelect("SELECT pg_get_acl(1259,0,0) AS acl", nil))
	})
}
