package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGProcLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pp_life_owner", "pp_life_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer b.DB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS pp_life_schema CASCADE")
			pgProcExec(t, p, b, c, "CREATE ROLE pp_life_owner", "CREATE ROLE pp_life_reader", "CREATE SCHEMA pp_life_schema", "GRANT ALL ON SCHEMA pp_life_schema TO pp_life_owner")
			const q = "SELECT proname,prosrc,pg_get_expr(proargdefaults,0) AS defaults,prosqlbody IS NOT NULL AS body FROM pg_proc WHERE oid=$1::oid"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			pgProcExec(t, p, b, c, "CREATE FUNCTION pp_life(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL RETURN a")
			var id int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'pp_life(integer)'::regprocedure::oid").Scan(&id); err != nil || id <= 0 {
				t.Fatal(id, err)
			}
			probe := func(name, defaults string, present bool) {
				t.Helper()
				var values [][]any
				if present {
					values = [][]any{{name, "", defaults, true}}
				}
				s := fixture.Select(q, []string{"proname", "prosrc", "defaults", "body"}, []string{"NAME", "TEXT", "TEXT", "BOOL"}, values, id)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			probe("pp_life", "7", true)
			old, err := b.DB.QueryContext(p.Ctx, q, id)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			pgProcExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pp_life(integer) TO pp_life_reader", "CREATE OR REPLACE FUNCTION pp_life(a integer DEFAULT 9) RETURNS integer LANGUAGE SQL RETURN a")
			probe("pp_life", "9", true)
			pgProcBoth(t, p, b, c, "SELECT oid=$1::oid AS same,has_function_privilege('pp_life_reader',oid,'EXECUTE') AS granted FROM pg_proc WHERE oid='pp_life(integer)'::regprocedure", []string{"same", "granted"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, id)
			pgProcExec(t, p, b, c, "ALTER FUNCTION pp_life(integer) RENAME TO pp_life_renamed", "ALTER FUNCTION pp_life_renamed(integer) SET SCHEMA pp_life_schema", "ALTER FUNCTION pp_life_schema.pp_life_renamed(integer) OWNER TO pp_life_owner")
			probe("pp_life_renamed", "9", true)
			pgProcBoth(t, p, b, c, "SELECT pronamespace='pp_life_schema'::regnamespace::oid AS located,proowner='pp_life_owner'::regrole::oid AS owned FROM pg_proc WHERE oid=$1::oid", []string{"located", "owned"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, id)
			pgProcExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pp_life_schema.pp_life_renamed(integer) FROM pp_life_reader", "REVOKE EXECUTE ON FUNCTION pp_life_schema.pp_life_renamed(integer) FROM PUBLIC")
			pgProcBoth(t, p, b, c, "SELECT has_function_privilege('pp_life_reader',$1::oid,'EXECUTE') AS granted", []string{"granted"}, []string{"BOOL"}, [][]any{{false}}, id)
			pgProcExec(t, p, b, c, "BEGIN", "DROP FUNCTION pp_life_schema.pp_life_renamed(integer)")
			probe("", "", false)
			pgProcExec(t, p, b, c, "ROLLBACK")
			probe("pp_life_renamed", "9", true)
			// A failed same-signature replacement cannot publish new defaults/source.
			d := fixture.ControlsReferenceDiagnostic("42P13", "cannot change return type of existing function")
			d.Hint = "Use DROP FUNCTION pp_life_schema.pp_life_renamed(integer) first."
			s := fixture.ImplicitError("CREATE OR REPLACE FUNCTION pp_life_schema.pp_life_renamed(a integer DEFAULT 11) RETURNS text LANGUAGE SQL RETURN 'bad'::text", d)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			probe("pp_life_renamed", "9", true)
			pgProcExec(t, p, b, c, "DROP FUNCTION pp_life_schema.pp_life_renamed(integer)")
			probe("", "", false)
			pgProcRetained(t, old, metadata, "pp_life", "7")
			pgProcExec(t, p, b, c, "DROP SCHEMA pp_life_schema")
		})
	}
}

func pgProcRetained(t *testing.T, rows *sql.Rows, metadata []*sql.ColumnType, name, defaults string) {
	t.Helper()
	if !rows.Next() {
		t.Fatal("retained snapshot missing", rows.Err())
	}
	var gotName, source, gotDefaults string
	var body bool
	if err := rows.Scan(&gotName, &source, &gotDefaults, &body); err != nil {
		t.Fatal(err)
	}
	if gotName != name || source != "" || gotDefaults != defaults || !body {
		t.Fatalf("retained snapshot %q %q %q %v", gotName, source, gotDefaults, body)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained snapshot cardinality", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 4 || metadata[0].Name() != "proname" || metadata[0].DatabaseTypeName() != "NAME" || metadata[2].DatabaseTypeName() != "TEXT" {
		t.Fatal("retained descriptor", metadata)
	}
}
