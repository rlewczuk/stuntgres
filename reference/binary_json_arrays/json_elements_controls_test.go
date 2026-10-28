package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonElementsStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jelements_store(id int,j json)", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO jelements_store VALUES($1,$2),($3,$4),($5,$6)", 3, 1, `[1,null,"x"]`, 2, `[]`, 3, nil)))
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		run(fixture.Exec("CREATE VIEW jelements_view_"+f+" AS SELECT s.id,e.value,e.n FROM jelements_store s,pg_catalog."+f+"(from_json => s.j) WITH ORDINALITY AS e(value,n)", 0))
		run(fixture.Exec("CREATE VIEW jelements_target_"+f+" AS SELECT id,"+f+"(VARIADIC j) AS v FROM jelements_store", 0))
	}
	run(fixture.Exec("ALTER TABLE jelements_store RENAME COLUMN j TO document", 0))
	run(fixture.Exec("ALTER TABLE jelements_store RENAME TO jelements_documents", 0))
	var checks []fixture.Step
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		n, x := any("null"), any(`"x"`)
		if f == "json_array_elements_text" {
			n, x = nil, "x"
		}
		view := "jelements_view_" + f
		s := fixture.Select("SELECT id,value,n FROM "+view+" ORDER BY id,n", []string{"id", "value", "n"}, []string{"INT4", jsonElementsType(f), "INT8"}, [][]any{{int64(1), "1", int64(1)}, {int64(1), n, int64(2)}, {int64(1), x, int64(3)}})
		checks = append(checks, s)
		s = fixture.Select("SELECT id,v FROM jelements_target_"+f, []string{"id", "v"}, []string{"INT4", jsonElementsType(f)}, [][]any{{int64(1), "1"}, {int64(1), n}, {int64(1), x}})
		s.Bag = true
		checks = append(checks, s)
		run(fixture.Exec("CREATE TABLE jelements_materialized_"+f+" AS SELECT value,n FROM "+view, 3))
		checks = append(checks, fixture.Select("SELECT value,n FROM jelements_materialized_"+f+" ORDER BY n", []string{"value", "n"}, []string{jsonElementsType(f), "INT8"}, [][]any{{"1", int64(1)}, {n, int64(2)}, {x, int64(3)}}))
		var definition string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('"+view+"'::regclass)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, f+"(") || !strings.Contains(definition, "document") || !strings.Contains(definition, "jelements_documents") {
			t.Fatalf("lost symbolic call: %s", definition)
		}
	}
	for _, s := range checks {
		fixture.JSONKeysBoth(t, p, b, c, s)
		stmt, err := c.PrepareContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
		stmt.Close()
	}
	return checks
}

func jsonElementsControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, checks []fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	heldStmt, err := c.PrepareContext(p.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer heldStmt.Close()
	old, err := c.QueryContext(p.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, old, nil, checks[0].Columns, checks[0].Types, checks[0].Rows, false)
	for i, ct := range meta {
		if ct.Name() != checks[0].Columns[i] || ct.DatabaseTypeName() != checks[0].Types[i] {
			t.Fatal("retained metadata changed", ct)
		}
	}
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: heldStmt, SQL: checks[0].Query}, b.Recorder, checks[0])
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE jelements_broken(")); err == nil {
		t.Fatal("malformed replacement succeeded")
	}
	for _, s := range checks {
		fixture.JSONKeysBoth(t, p, b, c, s)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("elements redump changed")
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual dump replay", err)
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONElementsAuthorityAndControls(t *testing.T) {
	const role = "jelements_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	jsonElementsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE TABLE jelements_acl(j json,hidden json)", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
			jsonElementsError(t, p, b, c, "SELECT e.value FROM jelements_acl s,"+f+"(s.j) e", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jelements_acl"), false)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec(`INSERT INTO jelements_acl VALUES('[1]','[]')`, 1))
		run(fixture.Exec("GRANT SELECT(j) ON jelements_acl TO "+role, 0))
		for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
			q := "SELECT e.value FROM jelements_acl s," + f + "(s.j) e"
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonElementsSelect(f, q, [][]any{{"1"}}))
			jsonElementsError(t, p, b, c, "SELECT e.value FROM jelements_acl s,"+f+"(s.hidden) e WHERE false", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jelements_acl"), false)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE SELECT(j) ON jelements_acl FROM "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			fixture.JSONKeysError(t, p, b, held, q, false, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jelements_acl"), "execute")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT(j) ON jelements_acl TO "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonElementsSelect(f, q, [][]any{{"1"}}))
			stmt.Close()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("DROP TABLE jelements_acl", 0))
		checks := jsonElementsStored(t, p, b, c)
		jsonElementsControls(t, p, b, c, checks)
	})
}
