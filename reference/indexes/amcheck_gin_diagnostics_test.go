package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckGINDiagnostics(t *testing.T) {
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE EXTENSION amcheck",
			"CREATE TABLE gin_probe(a integer[],n integer)",
			"CREATE INDEX gin_probe_i ON gin_probe USING gin(a)",
			"CREATE INDEX gin_am_btree ON gin_probe(n)",
			"CREATE INDEX gin_am_hash ON gin_probe USING hash(n)",
			"CREATE VIEW gin_am_view AS SELECT a FROM gin_probe",
			"CREATE SEQUENCE gin_am_sequence",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ query, message string }{
			{"SELECT gin_index_check()", "function gin_index_check() does not exist"},
			{"SELECT gin_index_check('gin_probe_i',true)", "function gin_index_check(unknown, boolean) does not exist"},
			{"SELECT gin_index_check(wrong => 'gin_probe_i')", "function gin_index_check(wrong => unknown) does not exist"},
		} {
			d := heapDiagnostic("42883", tc.message)
			d.Hint, d.Position = heapMissingHint, "8"
			for _, prepared := range []bool{false, true} {
				s := heapError(tc.query, d)
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		for _, prepared := range []bool{false, true} {
			d := heapDiagnostic("42P01", `relation "am_absent" does not exist`)
			d.Position = "24"
			s := heapError("SELECT gin_index_check('am_absent')", d)
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		for _, tc := range []struct{ target, code, message, detail string }{
			{"'gin_probe'", "42809", `"gin_probe" is not an index`, ""},
			{"'gin_am_view'", "42809", `"gin_am_view" is not an index`, ""},
			{"'gin_am_sequence'", "42809", `"gin_am_sequence" is not an index`, ""},
			{"17::regclass", "XX000", "could not open relation with OID 17", ""},
			{"'gin_am_btree'", "0A000", `expected "gin" index as targets for verification`, `Relation "gin_am_btree" is a btree index.`},
			{"'gin_am_hash'", "0A000", `expected "gin" index as targets for verification`, `Relation "gin_am_hash" is a hash index.`},
		} {
			d := heapDiagnostic(tc.code, tc.message)
			d.Detail = tc.detail
			s := heapError("SELECT gin_index_check("+tc.target+")", d)
			s.ErrorStage = "execute"
			heapBoth(t, p, b, c, s)
		}
		// NULL is strict; a non-NULL missing OID is not silently treated as NULL.
		heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check($1::regclass) AS ok", true, nil))
		s := heapError("SELECT gin_index_check($1::regclass) AS ok", heapDiagnostic("XX000", "could not open relation with OID 17"), int64(17))
		s.ErrorStage = "execute"
		heapBoth(t, p, b, c, s)
		// Only PostgreSQL's physical concurrent-build lifecycle can retain this
		// failed partial build. Native retained-invalid descriptor admission is
		// exercised by TestAmcheckGINTargetDiagnostics in the owning package.
		run(fixture.Exec("CREATE TABLE gin_am_invalid_heap(n integer,a integer[])", 0))
		run(fixture.Exec("INSERT INTO gin_am_invalid_heap VALUES(0,'{1}')", 1))
		if b.PG {
			t.Run("physical_failed_concurrent_build", func(t *testing.T) {
				s := fixture.ErrorDiagnostic("CREATE INDEX CONCURRENTLY gin_am_invalid ON gin_am_invalid_heap USING gin(a) WHERE 10/n>0", heapDiagnostic("22012", "division by zero"))
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				heapBoth(t, p, b, c, fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='gin_am_invalid'::regclass", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}}))
				d := heapDiagnostic("0A000", `cannot check index "gin_am_invalid"`)
				d.Detail = "Index is not valid."
				s = heapError("SELECT gin_index_check('gin_am_invalid')", d)
				s.ErrorStage = "execute"
				heapBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP INDEX gin_am_invalid", 0))
			})
		}
		run(fixture.Exec("UPDATE gin_am_invalid_heap SET n=1", 1))
		run(fixture.Exec("CREATE INDEX gin_am_invalid ON gin_am_invalid_heap USING gin(a) WHERE 10/n>0", 0))
		heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check('gin_am_invalid') AS ok", false))
		run(fixture.Exec("CREATE TEMP TABLE gin_am_temp(a integer[])", 0))
		run(fixture.Exec("CREATE INDEX gin_am_temp_i ON gin_am_temp USING gin(a)", 0))
		run(fixture.Exec("INSERT INTO gin_am_temp VALUES('{1,1}'),('{}'),(NULL)", 3))
		heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check('gin_am_temp_i') AS ok", false))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'gin_am_temp_i'::regclass::oid::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		foreign, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer foreign.Close()
		d := heapDiagnostic("0A000", "cannot access temporary tables of other sessions")
		d.Detail = `Index "gin_am_temp_i" is associated with temporary relation.`
		s = heapError("SELECT gin_index_check($1::regclass)", d, oid)
		s.ErrorStage = "execute"
		heapBoth(t, p, b, foreign, s)
		heapBoth(t, p, b, foreign, amcheckSelect("SELECT gin_index_check('gin_probe_i') AS ok", false))
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, heapError("SELECT gin_index_check('gin_probe')", heapDiagnostic("42809", `"gin_probe" is not an index`)))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, heapError("SELECT gin_index_check('gin_probe_i')", heapDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check('gin_probe_i') AS ok", false))
	})
}
