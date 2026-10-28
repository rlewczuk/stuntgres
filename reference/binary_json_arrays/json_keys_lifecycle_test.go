package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonKeysStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	jsonKeysSources(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW jk_view AS SELECT id,k,n FROM jk_source,json_object_keys(j) WITH ORDINALITY AS f(k,n)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE jk_source RENAME COLUMN j TO document", 0))
	check := fixture.Select("SELECT id,k,n FROM jk_view ORDER BY id,n", []string{"id", "k", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {int64(1), "b", int64(2)}, {int64(4), "a", int64(1)}, {int64(4), "a", int64(2)}})
	fixture.JSONKeysBoth(t, p, b, c, check)
	return check
}

func jsonKeysControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	q := "SELECT json_object_keys($1)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, x := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
		rows, err := x.QueryContext(ctx, q, `{"a":1}`)
		if rows != nil {
			rows.Close()
			t.Fatal("precanceled query published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("precanceled query: %v", err)
		}
		fixture.RunStep(t, p.Ctx, x, b.Recorder, jsonKeysSelect(q, []string{"a"}, `{"a":1}`))
	}
	stmt.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
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
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	for i, ct := range meta {
		if ct.Name() != check.Columns[i] || ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal("retained metadata changed", ct)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.JSONKeysBoth(t, p, b, c, check)
	storage := fixture.Select("SELECT document FROM jk_source ORDER BY id", []string{"document"}, []string{"JSON"}, [][]any{{`{"a":1,"b":2}`}, {`{}`}, {nil}, {`{"a":1,"a":2}`}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, storage)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("restored JSON/view redump changed")
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, storage)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONKeysAuthorityAndControls(t *testing.T) {
	const role = "jk_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE TABLE jk_acl(j json,hidden json)", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, q := range []string{"SELECT json_object_keys(j) FROM jk_acl", "SELECT json_object_keys(hidden) FROM jk_acl WHERE false"} {
			for _, prepared := range []bool{false, true} {
				fixture.JSONKeysError(t, p, b, c, q, prepared, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jk_acl"), "execute")
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec(`INSERT INTO jk_acl VALUES('{"a":1}','{}')`, 1))
		run(fixture.Exec("GRANT SELECT(j) ON jk_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		fixture.JSONKeysBoth(t, p, b, c, jsonKeysSelect("SELECT json_object_keys(j) FROM jk_acl", []string{"a"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Explicit ACLs are intentionally outside native Dump's representation.
		// Finish authorization witnesses before exporting the independent source/view.
		run(fixture.Exec("DROP TABLE jk_acl", 0))
		check := jsonKeysStored(t, p, b, c)
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table jk_source because other objects depend on it")
		d.Detail = "view jk_view depends on table jk_source"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP TABLE jk_source", d))
		jsonKeysControls(t, p, b, c, check)
	})
}
