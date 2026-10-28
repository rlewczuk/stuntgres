package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterStorageCandidateAndRecursion(t *testing.T) {
	t.Run("candidate", func(t *testing.T) {
		p := fixture.NewScenario(t)
		p.Step(t, fixture.Exec("CREATE TABLE storage_case(i integer,t text,n numeric)", 0))
		p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER added SET STORAGE MAIN, ADD COLUMN added text", 0)))
		p.Step(t, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "x", ""}, []any{"n", "m", ""}, []any{"added", "m", ""}))
		p.Step(t, fixture.Exec("ALTER TABLE storage_case DROP COLUMN added", 0))
		p.Step(t, fixture.Exec("ALTER TABLE storage_case ALTER i SET STORAGE EXTENDED, ALTER i TYPE text USING i::text", 0))
		p.Step(t, storageMetadata("storage_case", []any{"i", "x", ""}, []any{"t", "x", ""}, []any{"n", "m", ""}))
		p.Step(t, fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0))
		p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER t TYPE varchar", 0)))
		p.Step(t, storageMetadata("storage_case", []any{"i", "x", ""}, []any{"t", "x", ""}, []any{"n", "m", ""}))
		p.Step(t, fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE MAIN, ALTER t TYPE text", 0))
		p.Step(t, storageMetadata("storage_case", []any{"i", "x", ""}, []any{"t", "m", ""}, []any{"n", "m", ""}))
		p.Step(t, fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE PLAIN, ALTER t SET COMPRESSION pglz, ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION default", 0))
		for _, q := range []string{
			"ALTER TABLE storage_case ALTER t SET STORAGE MAIN, ALTER t TYPE integer USING t::integer",
			"ALTER TABLE storage_case ALTER n SET STORAGE EXTERNAL, ALTER n TYPE integer USING n::integer",
		} {
			p.Step(t, fixture.Prepared(storageError(q, "0A000", "column data type integer can only have storage PLAIN")))
			p.Step(t, storageMetadata("storage_case", []any{"i", "x", ""}, []any{"t", "e", ""}, []any{"n", "m", ""}))
		}
		p.Step(t, fixture.Select("SELECT attname,atttypid::regtype::text FROM pg_attribute WHERE attrelid='storage_case'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "atttypid"}, []string{"NAME", "TEXT"}, [][]any{{"i", "text"}, {"t", "text"}, {"n", "numeric"}}))
	})
	t.Run("physical_failure", func(t *testing.T) {
		p := fixture.NewScenario(t)
		p.Step(t, fixture.Exec("CREATE TABLE bad(i integer,t text)", 0))
		p.Step(t, fixture.Exec("INSERT INTO bad VALUES(-1,'payload')", 1))
		p.Step(t, fixture.Exec("CREATE INDEX bad_idx ON bad(t)", 0))
		d := fixture.ControlsReferenceDiagnostic("23514", `check constraint "storage_positive" of relation "bad" is violated by some row`)
		d.Schema, d.Table, d.Constraint = "public", "bad", "storage_positive"
		p.Step(t, fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE bad ALTER t SET STORAGE EXTERNAL, ADD CONSTRAINT storage_positive CHECK(i>0)", d)))
		p.Step(t, storageMetadata("bad", []any{"i", "p", ""}, []any{"t", "x", ""}))
		p.Step(t, storageMetadata("bad_idx", []any{"t", "x", ""}))
		p.Step(t, fixture.Select("SELECT i,t FROM bad ORDER BY i", []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), "payload"}}))
	})
	t.Run("inheritance", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, q := range []string{"CREATE TABLE p(t text)", "CREATE TABLE c() INHERITS(p)", "CREATE TABLE g() INHERITS(c)", "CREATE INDEX c_idx ON c(t)"} { p.Step(t, fixture.Exec(q, 0)) }
		check := func(root, child, rootCompression string) {
			p.Step(t, storageMetadata("p", []any{"t", root, rootCompression}))
			for _, name := range []string{"c", "g", "c_idx"} { p.Step(t, storageMetadata(name, []any{"t", child, ""})) }
		}
		check("x", "x", "")
		p.Step(t, fixture.Exec("ALTER TABLE p ALTER t SET STORAGE EXTERNAL", 0)); check("e", "e", "")
		p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE ONLY p ALTER t SET STORAGE MAIN", 0))); check("m", "e", "")
		p.Step(t, fixture.Exec("ALTER TABLE p ALTER t SET COMPRESSION pglz", 0)); check("m", "e", "p")
		p.Step(t, fixture.Exec("ALTER TABLE p ALTER t SET STORAGE PLAIN, ALTER t SET COMPRESSION lz4", 0)); check("p", "p", "l")
		p.Step(t, fixture.Exec("ALTER TABLE ONLY p ALTER t SET STORAGE DEFAULT", 0)); check("x", "p", "l")
		p.Step(t, fixture.Prepared(storageError("ALTER TABLE p ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION bogus", "22023", `invalid compression method "bogus"`))); check("x", "p", "l")
		p.Step(t, fixture.Exec("CREATE TABLE inherited() INHERITS(p)", 0))
		p.Step(t, storageMetadata("inherited", []any{"t", "x", "l"}))
	})
	t.Run("partition", func(t *testing.T) {
		p := fixture.NewScenario(t)
		p.Step(t, fixture.Exec("CREATE TABLE p(i integer,t text) PARTITION BY RANGE(i)", 0))
		p.Step(t, fixture.Exec("CREATE TABLE c PARTITION OF p FOR VALUES FROM(0) TO(10)", 0))
		p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE p ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0)))
		p.Step(t, storageMetadata("p", []any{"i", "p", ""}, []any{"t", "e", "p"}))
		p.Step(t, storageMetadata("c", []any{"i", "p", ""}, []any{"t", "e", ""}))
		p.Step(t, fixture.Exec("ALTER TABLE ONLY p ALTER t SET STORAGE MAIN", 0))
		p.Step(t, storageMetadata("c", []any{"i", "p", ""}, []any{"t", "e", ""}))
	})
	t.Run("transactions", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE storage_case(i integer,t text,n numeric)", 0))
			run(fixture.Exec("INSERT INTO storage_case VALUES(7,'payload',12)", 1))
			peer, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer peer.Close()
			observe := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0))
			run(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "e", "p"}, []any{"n", "m", ""}))
			observe(storageInitial())
			run(fixture.Exec("ROLLBACK", 0)); run(storageInitial()); run(storageRows())
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0)))
			run(fixture.Exec("COMMIT", 0))
			observe(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "e", "p"}, []any{"n", "m", ""}))
			run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE DEFAULT, ALTER t SET COMPRESSION default", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL", 0))
			run(fixture.Exec("SAVEPOINT saved", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER t SET COMPRESSION pglz", 0)))
			run(fixture.Exec("ROLLBACK TO SAVEPOINT saved", 0))
			run(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "e", ""}, []any{"n", "m", ""}))
			run(fixture.Exec("COMMIT", 0))
			observe(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "e", ""}, []any{"n", "m", ""}))
			run(fixture.Exec("BEGIN", 0))
			run(storageError("ALTER TABLE storage_case ALTER t SET COMPRESSION pglz, ALTER t SET STORAGE bogus", "22023", `invalid storage type "bogus"`))
			run(storageError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			run(fixture.Exec("ROLLBACK", 0))
			run(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "e", ""}, []any{"n", "m", ""})); run(storageRows())
		})
	})
	t.Run("like_properties", func(t *testing.T) {
		p := fixture.NewScenario(t)
		p.Step(t, fixture.Exec("CREATE TABLE storage_source(t text)", 0))
		p.Step(t, fixture.Exec("ALTER TABLE storage_source ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0))
		for _, tc := range []struct{ name, options, storage, compression string }{
			{"storage_like_none", "", "x", ""},
			{"storage_like_storage", " INCLUDING STORAGE", "e", ""},
			{"storage_like_compression", " INCLUDING COMPRESSION", "x", "p"},
			{"storage_like_both", " INCLUDING STORAGE INCLUDING COMPRESSION", "e", "p"},
		} {
			p.Step(t, fixture.Exec("CREATE TABLE "+tc.name+"(LIKE storage_source"+tc.options+")", 0))
			p.Step(t, storageMetadata(tc.name, []any{"t", tc.storage, tc.compression}))
		}
	})
}

func TestReferenceAlterStorageIndexMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{"CREATE TABLE storage_case(i integer,t text,n numeric)", "CREATE INDEX storage_simple ON storage_case(t)", "CREATE INDEX storage_expression ON storage_case(lower(t))", "CREATE INDEX storage_include ON storage_case(i) INCLUDE(t)", "CREATE INDEX storage_dup ON storage_case(t,t)"} { p.Step(t, fixture.Exec(q, 0)) }
	p.Step(t, fixture.Exec("INSERT INTO storage_case VALUES(7,'payload',12)", 1))
	check := func(first, compression string, newIndex bool) {
		p.Step(t, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", first, compression}, []any{"n", "m", ""}))
		p.Step(t, storageMetadata("storage_simple", []any{"t", first, compression}))
		p.Step(t, storageMetadata("storage_include", []any{"i", "p", ""}, []any{"t", first, compression}))
		p.Step(t, storageMetadata("storage_expression", []any{"lower", "x", ""}))
		p.Step(t, storageMetadata("storage_dup", []any{"t", first, compression}, []any{"t1", "x", ""}))
		if newIndex { p.Step(t, storageMetadata("storage_new_dup", []any{"t", first, compression}, []any{"t1", "e", "p"})) }
		p.Step(t, storageRows())
	}
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz", 0))); check("e", "p", false)
	p.Step(t, fixture.Exec("CREATE INDEX storage_new_dup ON storage_case(t,t)", 0)); check("e", "p", true)
	p.Step(t, fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE MAIN, ALTER t SET COMPRESSION default", 0)); check("m", "", true)
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE MAIN, ALTER t SET COMPRESSION default", 0))); check("m", "", true)
	for _, q := range []string{"ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER n SET COMPRESSION bogus", "ALTER TABLE storage_case ALTER t SET COMPRESSION pglz, ALTER n SET STORAGE bogus"} {
		message := `invalid compression method "bogus"`; if q == "ALTER TABLE storage_case ALTER t SET COMPRESSION pglz, ALTER n SET STORAGE bogus" { message = `invalid storage type "bogus"` }
		p.Step(t, fixture.Prepared(storageError(q, "22023", message))); check("m", "", true)
	}
	before := storageDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil { t.Fatal(err) }
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE storage_case CASCADE"); err != nil { t.Fatal(err) }
		if _, err := p.Postgres.ExecContext(p.Ctx, string(before)); err != nil { t.Fatal("PostgreSQL index replay", err) }
	}
	check("m", "", true)
	if !bytes.Equal(storageDump(t, p), before) { t.Fatal("index metadata redump changed") }
}
