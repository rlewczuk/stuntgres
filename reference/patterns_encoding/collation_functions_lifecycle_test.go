package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationFunctionsLifecycle(t *testing.T) {
	collationFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.CollationExec(t, p, b, `CREATE TABLE cf_values(id int)`, `CREATE VIEW cf_versions AS SELECT id,pg_collation_actual_version(6411::oid) AS actual FROM cf_values`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO cf_values VALUES(1),(1),(2)`, 3))
		want := fixture.Select(`SELECT id,actual FROM cf_versions GROUP BY id,actual ORDER BY id`, []string{"id", "actual"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1"}, {int64(2), "1"}})
		fixture.CollationRoutes(t, p, b, want)
		fixture.CollationExec(t, p, b, `CREATE TABLE cf_check(v oid CHECK(pg_collation_actual_version(v) IS NULL))`, `CREATE VIEW cf_database_versions AS SELECT pg_database_collation_actual_version(oid) AS actual FROM pg_database WHERE datname=current_database()`)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO cf_check VALUES(950)`, 1))
		databaseView := fixture.Select(`SELECT actual FROM cf_database_versions`, []string{"actual"}, []string{"TEXT"}, [][]any{{nil}})
		fixture.CollationRoutes(t, p, b, databaseView)
		var definition string
		if err := b.DB.QueryRowContext(p.Ctx, `SELECT pg_get_viewdef('cf_versions'::regclass,true)`).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "pg_collation_actual_version") {
			t.Fatal("stored call replaced with value", definition)
		}
		fixture.CollationExec(t, p, b, `CREATE COLLATION cf_held (PROVIDER=builtin,LOCALE='C')`)
		q := `SELECT pg_collation_actual_version($1) AS actual`
		h := collationFunctionsHeld(t, p, b, b.DB, q)
		oid := fixture.CollationOID(t, b.DB, "cf_held")
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"actual"}, []string{"TEXT"}, [][]any{{"1"}}, oid))
		fixture.CollationExec(t, p, b, `DROP COLLATION cf_held`)
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: fmt.Sprintf("collation with OID %d does not exist", oid)}, oid)
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		fixture.CollationExec(t, p, b, `CREATE COLLATION cf_held (PROVIDER=builtin,LOCALE='C')`)
		newOID := fixture.CollationOID(t, b.DB, "cf_held")
		if newOID == oid {
			t.Fatal("recreated identity reused")
		}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"actual"}, []string{"TEXT"}, [][]any{{"1"}}, newOID))
		h.Statement.Close()
		for _, ddl := range []string{
			`CREATE TABLE cf_generated(v oid,a text GENERATED ALWAYS AS (pg_collation_actual_version(v)) STORED)`,
			`CREATE TABLE cf_generated_db(v oid,a text GENERATED ALWAYS AS (pg_database_collation_actual_version(v)) STORED)`,
			`CREATE INDEX cf_volatile_idx ON cf_values ((pg_collation_actual_version(id::oid)))`,
		} {
			_, err := b.DB.ExecContext(p.Ctx, ddl)
			message := "generation expression is not immutable"
			if strings.HasPrefix(ddl, "CREATE INDEX") {
				message = "functions in index expression must be marked IMMUTABLE"
			}
			want := fixture.ControlsReferenceDiagnostic("42P17", message)
			fixture.AssertDiagnostic(t, err, &want)
		}
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := b.DB.QueryContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.DeleteReferenceConsume(t, false, retained, nil, want)
		fixture.CollationRoutes(t, p, b, want)
		if p.Postgres != nil {
			if _, err = p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
			if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("replay native emitted SQL", err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
			if _, err = p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		r, err := b.DB.QueryContext(ctx, `SELECT pg_database_collation_actual_version(NULL::oid)`)
		if r != nil {
			r.Close()
			t.Fatal("canceled query returned rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled query", err)
		}
		canceledHeld := collationFunctionsHeld(t, p, b, b.DB, `SELECT pg_import_system_collations($1::regnamespace) AS n`)
		r, err = canceledHeld.Statement.QueryContext(ctx, "public")
		if r != nil {
			r.Close()
			t.Fatal("canceled held import returned rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled held import", err)
		}
		fixture.RunStep(t, p.Ctx, canceledHeld, b.Recorder, fixture.Select(canceledHeld.SQL, []string{"n"}, []string{"INT4"}, [][]any{{nil}}, nil))
		fixture.CollationRoutes(t, p, b, want)
	})
}
