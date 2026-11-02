package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnUdtUsageFeatureSmoke(t *testing.T) {
	cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		cuOwnedSeed(run, "cu_smoke")
		run(fixture.Exec("INSERT INTO cu_smoke.t VALUES('x','x','x')", 1))
		where := "table_schema='cu_smoke'"
		original := cuOwnedRows(database, "cu_smoke")
		s := cuProjection(b.PG, where, original)
		held := cduHold(t, p, c, s.Query)
		cuCheck(t, p, c, r, held, s)
		run(fixture.Exec("SET SESSION AUTHORIZATION cu_set", 0))
		cuCheck(t, p, c, r, held, cuProjection(b.PG, where, nil))
		run(fixture.Exec("SET ROLE cu_type_owner", 0))
		cuCheck(t, p, c, r, held, cuProjection(b.PG, where, original[:2]))
		run(fixture.Exec("RESET ROLE", 0))
		cuCheck(t, p, c, r, held, cuProjection(b.PG, where, nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		cuCheck(t, p, c, r, held, s)
		retained, err := b.DB.QueryContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		cuMetadata(t, columns, b.PG)
		if b.PG {
			cuRetained(t, retained, original)
		}
		run(fixture.Exec("ALTER TABLE cu_smoke.t RENAME COLUMN x TO renamed", 0))
		run(fixture.Exec("ALTER TABLE cu_smoke.t ALTER COLUMN y TYPE text USING y::text", 0))
		want := [][]any{cuRow(database, "cu_smoke", "e", "cu_smoke", "t", "renamed"), cuRow(database, "pg_catalog", "text", "cu_smoke", "t", "y"), original[2]}
		cuCheck(t, p, c, r, held, cuProjection(b.PG, where, want))
		run(fixture.Exec("ALTER TABLE cu_smoke.t DROP COLUMN z", 0))
		want = want[:2]
		s = cuProjection(b.PG, where, want)
		check := func() {
			cuCheck(t, p, c, r, held, s)
			run(fixture.Select("SELECT renamed::text AS renamed,y FROM cu_smoke.t", []string{"renamed", "y"}, []string{"TEXT", "TEXT"}, [][]any{{"x", "x"}}))
		}
		check()
		cuMissing(func(s fixture.Step) { run(s); check() })
		cuReadOnly(func(s fixture.Step) { run(s); check() }, []string{"INSERT"})
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(cuProjection(false, where, nil))
			run(cuProjection(false, "table_schema='information_schema' AND table_name='column_udt_usage'", cuSelfRows(database)))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			held = cduHold(t, p, c, s.Query)
			check()
			cuIdentity(t, p, b, c, r)
			cuRetained(t, retained, original)
		}
		cuMetadata(t, columns, b.PG)
	})
}
