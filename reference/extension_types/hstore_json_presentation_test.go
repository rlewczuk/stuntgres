package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hstoreJSONPresentationVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	q := "SELECT hs_json_namespace.strict_current(v) AS strict_json,hs_json_namespace.loose_current(v) AS loose_json FROM hs_json_controls ORDER BY id"
	for _, s := range []fixture.Step{
		hstoreJSONPair(b, q, hstoreJSONControlWant()),
		hstoreJSONSelect(b, "SELECT strict_json,loose_json FROM hs_json_view ORDER BY id", []string{"strict_json", "loose_json"}, hstoreJSONControlWant(), true),
	} {
		hstoreRun(t, p, b, db, s)
		hstoreRun(t, p, b, db, fixture.Prepared(s))
	}
	hstoreRun(t, p, b, db, fixture.Exec("INSERT INTO hs_json_defaults(id) VALUES (1)", 1))
	hstoreRun(t, p, b, db, hstoreJSONSelect(b, "SELECT strict_json,loose_json FROM hs_json_defaults", []string{"strict_json", "loose_json"}, [][]any{{`{"a": "1.00"}`, `{"a": true}`}}, true))
	hstoreRun(t, p, b, db, fixture.Exec("DELETE FROM hs_json_defaults", 1))
}

func TestReferenceHstoreJSONPresentation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hs_json_namespace")
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreJSONControlSeed(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA hs_json_namespace", 0))
		for _, tc := range []struct{ name, current string }{{"hstore_to_json", "strict_current"}, {"hstore_to_json_loose", "loose_current"}} {
			q := "SELECT " + tc.name + "(v) AS j FROM hs_json_controls ORDER BY id"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			var before, after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT '"+tc.name+"(hstore)'::regprocedure::oid").Scan(&before); err != nil {
				t.Fatal(err)
			}
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION "+tc.name+"(hstore) RENAME TO "+tc.current, 0))
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION "+tc.current+"(hstore) SET SCHEMA hs_json_namespace", 0))
			if err := c.QueryRowContext(p.Ctx, "SELECT 'hs_json_namespace."+tc.current+"(hstore)'::regprocedure::oid").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("function identity changed %d -> %d", before, after)
			}
			want := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.name+"(hstore) does not exist")
			want.Position = "8"
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, q, prepared, true, true, want)
			}
			hstoreError(t, p, b, held, q, false, true, false, want)
		}
		hstoreJSONPresentationVerify(t, p, b, c)
		if b.PG {
			return
		}
		dump := hstoreDump(t, p, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		hstoreJSONPresentationVerify(t, p, b, c)
		destination := fixture.NewFixture(t, false, true)
		target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
		hstoreRun(t, p, target, destination.Memory, fixture.Exec("CREATE EXTENSION hstore", 0))
		if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		hstoreJSONPresentationVerify(t, p, target, destination.Memory)
		if p.Postgres != nil {
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			clean := "DROP SCHEMA public CASCADE; DROP SCHEMA IF EXISTS hs_json_namespace CASCADE; CREATE SCHEMA public"
			if _, err := p.Postgres.ExecContext(p.Ctx, clean); err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := p.Postgres.ExecContext(ctx, clean); err != nil {
					t.Error(err)
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
				t.Fatalf("actual renamed dump replay: %v", err)
			}
			hstoreJSONPresentationVerify(t, p, pg, p.Postgres)
		}
	})
}
