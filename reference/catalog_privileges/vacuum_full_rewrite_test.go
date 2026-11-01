package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumFullRewrite(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TABLE full_rows(id INT PRIMARY KEY, v TEXT, n BIGINT, flag BOOL)", 0))
		f.run(fixture.Exec("INSERT INTO full_rows VALUES(1,'gone',10,true),(2,NULL,20,false),(3,'middle',30,true),(4,'four',40,NULL),(5,'five',50,false)", 5))
		f.run(fixture.Exec("CREATE INDEX full_rows_hash ON full_rows USING hash(n)", 0))
		f.run(fixture.Exec("CREATE INDEX full_rows_brin ON full_rows USING brin(n)", 0))
		f.run(fixture.Exec("CREATE INDEX full_rows_expr ON full_rows(lower(v)) INCLUDE(n) WHERE v IS NOT NULL", 0))
		f.run(fixture.Exec("DELETE FROM full_rows WHERE id=1 OR id=3", 2))
		f.run(fixture.Exec("UPDATE full_rows SET v='changed' WHERE id=5", 1))
		q := "SELECT id,v,n,flag FROM full_rows ORDER BY id"
		want := fixture.Select(q, []string{"id", "v", "n", "flag"}, []string{"INT4", "TEXT", "INT8", "BOOL"}, [][]any{{int64(2), nil, int64(20), false}, {int64(4), "four", int64(40), nil}, {int64(5), "changed", int64(50), false}})
		selectHeld := vacuumHeld(f.t, f.p, f.c, q)
		vacuumRun(f.t, f.p, f.b, selectHeld, want)
		before := make(map[string]fullLocator)
		for _, name := range []string{"full_rows", "full_rows_pkey", "full_rows_hash", "full_rows_brin", "full_rows_expr"} {
			before[name] = f.locator(name)
		}
		for range 2 {
			f.command(fixture.Exec("VACUUM (FULL, INDEX_CLEANUP OFF, TRUNCATE false, PARALLEL 0) full_rows", 0))
			for name, locator := range before {
				before[name] = f.rewritten(name, locator)
			}
			f.run(want)
			vacuumRun(f.t, f.p, f.b, selectHeld, want)
			f.run(fixture.Select("SELECT id FROM full_rows WHERE n=40", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
			f.run(fixture.Select("SELECT id FROM full_rows WHERE lower(v)='changed'", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}))
		}
		duplicate := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "full_rows_pkey"`)
		duplicate.Detail = "Key (id)=(2) already exists."
		duplicate.Schema, duplicate.Table, duplicate.Constraint = "public", "full_rows", "full_rows_pkey"
		f.run(fixture.ErrorDiagnostic("INSERT INTO full_rows VALUES(2,'bad',99,true)", duplicate))
		f.run(want)
		f.run(fixture.Exec("CREATE TABLE full_empty(id INT PRIMARY KEY)", 0))
		empty := f.locator("full_empty")
		for range 2 {
			f.command(fixture.Exec("VACUUM FULL full_empty", 0))
			empty = f.rewritten("full_empty", empty)
		}
		f.run(fixture.Exec("CREATE SEQUENCE full_birth_calls", 0))
		f.run(fixture.Exec("CREATE TABLE full_birth(id INT DEFAULT nextval('full_birth_calls'), doubled INT GENERATED ALWAYS AS (id*2) STORED)", 0))
		f.run(fixture.Exec("CREATE FUNCTION full_birth_trigger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('full_birth_calls'); RETURN NEW; END $$", 0))
		f.run(fixture.Exec("CREATE TRIGGER full_birth_insert AFTER INSERT ON full_birth FOR EACH ROW EXECUTE FUNCTION full_birth_trigger()", 0))
		f.run(fixture.Exec("INSERT INTO full_birth DEFAULT VALUES", 1))
		birth := f.locator("full_birth")
		f.command(fixture.Exec("VACUUM FULL full_birth", 0))
		f.rewritten("full_birth", birth)
		f.run(fixture.Select("SELECT id,doubled FROM full_birth", []string{"id", "doubled"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		f.run(fixture.Select("SELECT last_value FROM full_birth_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		f.run(fixture.Exec("CREATE TABLE full_ranges(id INT,span INT4RANGE,word TEXT,EXCLUDE USING gist(span WITH &&))", 0))
		f.run(fixture.Exec("CREATE INDEX full_ranges_spgist ON full_ranges USING spgist(word)", 0))
		f.run(fixture.Exec("INSERT INTO full_ranges VALUES(1,'[1,3)','one'),(2,'[4,6)','two')", 2))
		rangeHeap, rangeExcl, rangeSpgist := f.locator("full_ranges"), f.locator("full_ranges_span_excl"), f.locator("full_ranges_spgist")
		f.command(fixture.Exec("VACUUM (FULL,INDEX_CLEANUP OFF) full_ranges", 0))
		f.rewritten("full_ranges", rangeHeap)
		f.rewritten("full_ranges_span_excl", rangeExcl)
		f.rewritten("full_ranges_spgist", rangeSpgist)
		f.run(fixture.Select("SELECT id,span,word FROM full_ranges ORDER BY id", []string{"id", "span", "word"}, []string{"INT4", "INT4RANGE", "TEXT"}, [][]any{{int64(1), "[1,3)", "one"}, {int64(2), "[4,6)", "two"}}))
		exclusion := fixture.ControlsReferenceDiagnostic("23P01", `conflicting key value violates exclusion constraint "full_ranges_span_excl"`)
		exclusion.Detail = "Key (span)=([2,4)) conflicts with existing key (span)=([1,3))."
		exclusion.Schema, exclusion.Table, exclusion.Constraint = "public", "full_ranges", "full_ranges_span_excl"
		f.run(fixture.ErrorDiagnostic("INSERT INTO full_ranges VALUES(3,'[2,4)','bad')", exclusion))
		f.emptyProgress()
		// Native controls have no PostgreSQL counterpart. Their public SQL
		// witnesses remain inside this shared scenario, not private duplicates.
		if !f.b.PG {
			beforeControl := f.locator("full_rows")
			if err := stuntgres.Restore(f.p.Ctx, f.p.Native, bytes.NewBufferString("CREATE TABLE partial_full(id INT); INSERT INTO missing_full VALUES(1);")); err == nil {
				f.t.Fatal("invalid Restore unexpectedly published")
			}
			f.unchanged("full_rows", beforeControl)
			f.run(want)
			var dump bytes.Buffer
			if err := stuntgres.Dump(f.p.Ctx, f.p.Native, &dump); err != nil {
				f.t.Fatal(err)
			}
			if err := stuntgres.Restore(f.p.Ctx, f.p.Native, &dump); err != nil {
				f.t.Fatal(err)
			}
			f.run(want)
			restored := f.locator("full_rows")
			f.command(fixture.Exec("VACUUM FULL full_rows", 0))
			f.rewritten("full_rows", restored)
			f.run(want)
			f.emptyProgress()
		}
	})
}

func TestReferenceVacuumFullOptionsDiagnostics(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TABLE full_options(id INT)", 0))
		f.run(fixture.Exec("INSERT INTO full_options VALUES(1)", 1))
		for _, q := range []string{"VACUUM FULL full_options", "VACUUM FULL FREEZE full_options", "VACUUM (FULL true,FULL false,FULL true,PARALLEL 0,DISABLE_PAGE_SKIPPING false,INDEX_CLEANUP OFF,TRUNCATE false) full_options", "VACUUM (FULL,FREEZE,ANALYZE,BUFFER_USAGE_LIMIT 0) full_options(id)"} {
			before := f.locator("full_options")
			f.command(fixture.Exec(q, 0))
			f.rewritten("full_options", before)
		}
		for _, tc := range []struct{ q, code, message string }{
			{"VACUUM (FULL,PARALLEL 1) full_options", "0A000", "VACUUM FULL cannot be performed in parallel"},
			{"VACUUM (FULL,BUFFER_USAGE_LIMIT 0) full_options", "0A000", "BUFFER_USAGE_LIMIT cannot be specified for VACUUM FULL"},
			{"VACUUM FULL full_options(id)", "0A000", "ANALYZE option must be specified when a column list is provided"},
			{"VACUUM (FULL,DISABLE_PAGE_SKIPPING) full_options", "0A000", "VACUUM option DISABLE_PAGE_SKIPPING cannot be used with FULL"},
			{"VACUUM (FULL,PROCESS_MAIN false,PROCESS_TOAST false) full_options", "0A000", "PROCESS_TOAST required with VACUUM FULL"},
			{"VACUUM (FULL,ONLY_DATABASE_STATS)", "0A000", "ONLY_DATABASE_STATS cannot be specified with other VACUUM options"},
			{"VACUUM (FULL,INDEX_CLEANUP bogus) full_options", "42601", "index_cleanup requires a Boolean value"},
			{"VACUUM (FULL,TRUNCATE bogus) full_options", "42601", "truncate requires a Boolean value"},
			{"VACUUM (FULL 'bad',FULL true) full_options", "42601", "full requires a Boolean value"},
		} {
			before := f.locator("full_options")
			f.command(fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message)))
			f.unchanged("full_options", before)
		}
		before := f.locator("full_options")
		f.command(fixture.ErrorDiagnostic("VACUUM FULL full_options,full_absent", fixture.ControlsReferenceDiagnostic("42P01", `relation "full_absent" does not exist`)))
		f.unchanged("full_options", before)
		for _, q := range []string{"VACUUM (FULL false) full_options", "VACUUM (FULL,PROCESS_MAIN false) full_options"} {
			f.command(fixture.Exec(q, 0))
			f.unchanged("full_options", before)
		}
		for _, begin := range []string{"BEGIN", "BEGIN READ ONLY"} {
			f.run(fixture.Exec(begin, 0))
			f.command(fixture.ErrorDiagnostic("VACUUM FULL full_missing", fixture.ControlsReferenceDiagnostic("25001", "VACUUM cannot run inside a transaction block")))
			f.command(fixture.ErrorDiagnostic("VACUUM FULL full_options", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			f.run(fixture.Exec("ROLLBACK", 0))
			f.unchanged("full_options", before)
		}
		// Metadata-only Prepare of a missing object succeeds; execution binds now.
		h := vacuumHeld(f.t, f.p, f.c, "VACUUM FULL full_late")
		f.run(fixture.Exec("CREATE TABLE full_late(id INT)", 0))
		late := f.locator("full_late")
		vacuumRun(f.t, f.p, f.b, h, fixture.Exec(h.SQL, 0))
		f.rewritten("full_late", late)
		f.run(fixture.ErrorDiagnostic("PREPARE full_sql AS VACUUM FULL full_options", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "VACUUM"`, Position: "21"}))
		vacuumRows(f.t, f.p, f.b, f.c, "full_options", 1)
		fullVerbose(f)
		f.emptyProgress()
	})
}
