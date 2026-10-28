package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbEachStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jbeach_store(id int,j jsonb)", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO jbeach_store VALUES($1,$2),($3,$4),($5,$6)", 3, 1, `{"a":1,"a":null,"b":"x"}`, 2, `{}`, 3, nil)))
	for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
		run(fixture.Exec("CREATE VIEW jbeach_view_"+f+" AS SELECT s.id,f.key,f.value,n FROM jbeach_store s,"+f+"(s.j) WITH ORDINALITY AS f(key,value,n)", 0))
		run(fixture.Exec("CREATE VIEW jbeach_target_"+f+" AS SELECT ("+f+"('{\"constant\":1}'::jsonb)).key AS k", 0))
		run(fixture.Exec("CREATE VIEW jbeach_target_source_"+f+" AS SELECT id,("+f+"(j)).key AS k FROM jbeach_store", 0))
	}
	run(fixture.Exec("ALTER TABLE jbeach_store RENAME COLUMN j TO document", 0))
	run(fixture.Exec("ALTER TABLE jbeach_store RENAME TO jbeach_documents", 0))
	var checks []fixture.Step
	for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
		n, x := any("null"), any(`"x"`)
		if f == "jsonb_each_text" {
			n, x = nil, "x"
		}
		checks = append(checks,
			fixture.Select("SELECT id,key,value,n FROM jbeach_view_"+f+" ORDER BY id,n", []string{"id", "key", "value", "n"}, []string{"INT4", "TEXT", jsonbEachType(f), "INT8"}, [][]any{{int64(1), "a", n, int64(1)}, {int64(1), "b", x, int64(2)}}),
			fixture.Select("SELECT k FROM jbeach_target_"+f, []string{"k"}, []string{"TEXT"}, [][]any{{"constant"}}),
			fixture.Select("SELECT id,k FROM jbeach_target_source_"+f+" ORDER BY id,k", []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}}),
		)
		run(fixture.Exec("CREATE TABLE jbeach_materialized_"+f+" AS SELECT key,value FROM jbeach_view_"+f, 2))
		s := fixture.Select("SELECT key,value FROM jbeach_materialized_"+f, []string{"key", "value"}, []string{"TEXT", jsonbEachType(f)}, [][]any{{"a", n}, {"b", x}})
		s.Bag = true
		checks = append(checks, s)
		// Check deparse symbolically, without pinning formatting or generated IDs.
		rows, err := c.QueryContext(p.Ctx, "SELECT pg_get_viewdef('jbeach_view_"+f+"'::regclass)")
		if err != nil {
			t.Fatal(err)
		}
		var definition string
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing view definition")
		}
		if err := rows.Scan(&definition); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if rows.Next() {
			rows.Close()
			t.Fatal("extra view definitions")
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(definition, f+"(") || !strings.Contains(definition, "document") {
			t.Fatalf("lost symbolic JSON call: %s", definition)
		}
	}
	for _, s := range checks {
		fixture.JSONEachBoth(t, p, b, c, s)
	}
	return checks
}

func jsonbEachControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, checks []fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
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
	for _, s := range checks {
		fixture.JSONEachBoth(t, p, b, c, s)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("JSON each control redump changed")
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

func TestReferenceJSONBEachAuthorityAndControls(t *testing.T) {
	const role = "jbeach_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE TABLE jbeach_acl(j jsonb,hidden jsonb)", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			for _, prepared := range []bool{false, true} {
				fixture.JSONKeysError(t, p, b, c, "SELECT * FROM jbeach_acl s,"+f+"(s.j) f", prepared, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbeach_acl"), "execute")
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec(`INSERT INTO jbeach_acl VALUES('{"a":1}','{}')`, 1))
		run(fixture.Exec("GRANT SELECT(j) ON jbeach_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			fixture.JSONEachBoth(t, p, b, c, jsonbEachSelect(f, "SELECT f.key,f.value FROM jbeach_acl s,"+f+"(s.j) f", [][]any{{"a", "1"}}))
			q := "SELECT f.key,f.value FROM jbeach_acl s," + f + "(s.j) f"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbEachSelect(f, q, [][]any{{"a", "1"}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE SELECT(j) ON jbeach_acl FROM "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			fixture.JSONKeysError(t, p, b, held, q, false, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbeach_acl"), "execute")
			for _, prepared := range []bool{false, true} {
				fixture.JSONKeysError(t, p, b, c, q+" WHERE false", prepared, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbeach_acl"), "execute")
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT(j) ON jbeach_acl TO "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbEachSelect(f, q, [][]any{{"a", "1"}}))
			stmt.Close()
			for _, prepared := range []bool{false, true} {
				fixture.JSONKeysError(t, p, b, c, "SELECT * FROM jbeach_acl s,"+f+"(s.hidden) f WHERE false", prepared, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbeach_acl"), "execute")
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("DROP TABLE jbeach_acl", 0))
		checks := jsonbEachStored(t, p, b, c)
		jsonbEachControls(t, p, b, c, checks)
	})
}
