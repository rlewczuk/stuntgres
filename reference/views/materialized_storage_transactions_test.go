package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func materializedStorageDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(out.Bytes())
}

func TestReferenceMaterializedStorageAtomicity(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		run(fixture.Exec("CREATE ACCESS METHOD ms_heap2 TYPE TABLE HANDLER heap_tableam_handler", 0))
		var before []byte
		if !b.PG {
			before = materializedStorageDump(t, p)
		}
		var definition string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('ms_mv'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		checkUnchanged := func() {
			materializedStorageBaseline(run)
			run(fixture.Select("SELECT pg_get_viewdef('ms_mv'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
			fixture.MaterializedViewPopulation(run, "ms_empty", true)
			fixture.MaterializedViewPopulation(run, "ms_no", false)
			if !b.PG && !bytes.Equal(before, materializedStorageDump(t, p)) {
				t.Fatal("failed mixed ALTER changed persistent state")
			}
		}
		for _, tc := range []struct{ actions, code, message string }{
			{"ALTER id SET STATISTICS 7, SET (fillfactor=70), CLUSTER ON ms_a, ALTER absent SET STORAGE PLAIN", "42703", `column "absent" of relation "ms_mv" does not exist`},
			{"ALTER id SET STATISTICS 7, CLUSTER ON ms_a, SET (bogus=1)", "22023", `unrecognized parameter "bogus"`},
			{"SET (fillfactor=70), CLUSTER ON ms_a, ALTER id SET STORAGE EXTENDED", "0A000", "column data type integer can only have storage PLAIN"},
			{"SET ACCESS METHOD ms_heap2, ALTER id SET STATISTICS 7, CLUSTER ON absent_idx", "42704", `index "absent_idx" for table "ms_mv" does not exist`},
			{"ALTER id SET STATISTICS 7, SET ACCESS METHOD absent_am", "42704", `access method "absent_am" does not exist`},
			{"SET (bogus=1), SET ACCESS METHOD absent_am", "42704", `access method "absent_am" does not exist`},
			{"SET ACCESS METHOD absent_am, SET (bogus=1)", "42704", `access method "absent_am" does not exist`},
		} {
			run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv "+tc.actions, fixture.ControlsReferenceDiagnostic(tc.code, tc.message)))
			checkUnchanged()
		}
		// Actual target kind, not the ALTER spelling, owns the property order.
		for _, prefix := range []string{"ALTER MATERIALIZED VIEW ms_mv ", "ALTER TABLE ms_mv "} {
			for _, tc := range []struct{ actions, message string }{
				{"ALTER id SET STATISTICS -2, SET (bogus=1)", "statistics target -2 is too low"},
				{"SET (bogus=1), ALTER id SET STATISTICS -2", `unrecognized parameter "bogus"`},
			} {
				run(fixture.ErrorDiagnostic(prefix+tc.actions, fixture.ControlsReferenceDiagnostic("22023", tc.message)))
				checkUnchanged()
			}
		}
		// An original-state no-op is not a second genuine method change.
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv SET ACCESS METHOD heap, SET ACCESS METHOD ms_heap2", 0))
		run(materializedStorageMethod("ms_heap2"))
		run(materializedStorageRows())
	})
}

func TestReferenceMaterializedStorageTransactions(t *testing.T) {
	materializedStorageRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		materializedStorageSeed(run)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		other := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
		// Reuse the same held consumer before, within, and after transactions.
		run(materializedStorageColumns(nil, nil, "x", ""))
		run(materializedStorageRows())
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 7, SET (fillfactor=70), CLUSTER ON ms_a", 0))
		run(materializedStorageColumns(int64(7), nil, "x", ""))
		run(materializedStorageOptions("{fillfactor=70}"))
		run(materializedStorageCluster(true, false))
		other(materializedStorageColumns(nil, nil, "x", ""))
		other(materializedStorageOptions(nil))
		other(materializedStorageCluster(false, false))
		run(fixture.Exec("COMMIT", 0))
		other(materializedStorageColumns(int64(7), nil, "x", ""))
		other(materializedStorageOptions("{fillfactor=70}"))
		other(materializedStorageCluster(true, false))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 9, SET (fillfactor=80), CLUSTER ON ms_b", 0))
		run(materializedStorageColumns(int64(9), nil, "x", ""))
		run(fixture.Exec("SAVEPOINT ms_inner", 0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER note SET STORAGE EXTERNAL, ALTER id SET STATISTICS 11, SET WITHOUT CLUSTER", 0))
		run(materializedStorageColumns(int64(11), nil, "e", ""))
		run(materializedStorageCluster(false, false))
		run(fixture.Exec("ROLLBACK TO ms_inner", 0))
		run(materializedStorageColumns(int64(9), nil, "x", ""))
		run(materializedStorageOptions("{fillfactor=80}"))
		run(materializedStorageCluster(false, true))
		run(fixture.Exec("ROLLBACK", 0))
		run(materializedStorageColumns(int64(7), nil, "x", ""))
		run(materializedStorageOptions("{fillfactor=70}"))
		run(materializedStorageCluster(true, false))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.ErrorDiagnostic("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 9, SET (bogus=1)", fixture.ControlsReferenceDiagnostic("22023", `unrecognized parameter "bogus"`)))
		run(fixture.ErrorDiagnostic("SELECT id,note FROM ms_mv", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		run(fixture.Exec("ROLLBACK", 0))
		run(materializedStorageColumns(int64(7), nil, "x", ""))
		run(materializedStorageRows())
		other(materializedStorageRows())
		// Go's Tx owner has the same real publication/rollback contract.
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ALTER MATERIALIZED VIEW ms_mv ALTER id SET STATISTICS 13", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedStorageColumns(int64(13), nil, "x", ""))
		other(materializedStorageColumns(int64(7), nil, "x", ""))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(materializedStorageColumns(int64(7), nil, "x", ""))
	})
}
