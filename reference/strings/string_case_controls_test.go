package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCaseConsumers(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stringCaseSetup(t, p, b, c)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sc_stored(id INT,s TEXT COLLATE pg_c_utf8 DEFAULT (casefold('İ' COLLATE pg_c_utf8)),f TEXT COLLATE pg_unicode_fast DEFAULT (initcap('ßSS' COLLATE pg_unicode_fast)),CHECK(lower(s)=lower(s)),CHECK(upper(f)=upper(f)))", 0),
			fixture.Exec("INSERT INTO sc_stored(id) VALUES(1)", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		stored := fixture.Select("SELECT s,f,upper(s) AS su,casefold(f) AS ff FROM sc_stored", []string{"s", "f", "su", "ff"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"İ", "Ssss", "İ", "ssss"}})
		stringCaseBoth(t, p, b, c, stored)
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,lower(upper(c)) AS v FROM sc WHERE upper(v)='ABC  ' ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "abc"}}),
			fixture.Select("SELECT casefold(t COLLATE pg_unicode_fast) AS v,count(*) AS n FROM sc GROUP BY casefold(t COLLATE pg_unicode_fast) ORDER BY v NULLS LAST", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"", int64(1)}, {"i\u0307σ", int64(1)}, {"strasse", int64(1)}, {nil, int64(1)}}),
		} {
			stringCaseBoth(t, p, b, c, s)
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sc_dest(id INT,v TEXT COLLATE pg_unicode_fast DEFAULT (upper('ß' COLLATE pg_unicode_fast)),CHECK(v=upper(v)))", 0),
			fixture.Exec("INSERT INTO sc_dest(id) VALUES(0)", 1),
			fixture.Prepared(fixture.Exec("INSERT INTO sc_dest SELECT id,upper(t COLLATE pg_unicode_fast) FROM sc WHERE id=1", 1)),
			fixture.Select("UPDATE sc_dest SET v=upper('ﬃ' COLLATE pg_unicode_fast) WHERE id=1 RETURNING id,lower(v) AS lo,casefold(v) AS fo,initcap(v) AS ic", []string{"id", "lo", "fo", "ic"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "ffi", "ffi", "Ffi"}}),
			fixture.Exec("CREATE VIEW sc_view AS SELECT id,lower(v) AS lo,upper(v) AS up,casefold(v) AS fo,initcap(v) AS ic FROM sc_dest", 0),
			fixture.Exec("ALTER TABLE sc_dest RENAME COLUMN v TO renamed", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT id,lo,up,fo,ic FROM sc_view ORDER BY id", []string{"id", "lo", "up", "fo", "ic"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(0), "ss", "SS", "ss", "Ss"}, {int64(1), "ffi", "FFI", "ffi", "Ffi"}})
		stringCaseBoth(t, p, b, c, want)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			old, err := c.QueryContext(p.Ctx, want.Query)
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
			if !old.Next() {
				t.Fatal("replacement lost held rows", old.Err())
			}
			var id int64
			var lo, up, fo, ic string
			if err := old.Scan(&id, &lo, &up, &fo, &ic); err != nil {
				t.Fatal(err)
			}
			if id != 0 || lo != "ss" || up != "SS" || fo != "ss" || ic != "Ss" || meta[1].DatabaseTypeName() != "TEXT" {
				t.Fatal(id, lo, up, fo, ic, meta)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			stringCaseBoth(t, p, b, c, want)
			stringCaseBoth(t, p, b, c, stored)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, redump.String()); err != nil {
					t.Fatal("dump SQL replay", err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, stored)
				// Leave the oracle empty for its later shared backend callback.
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
			// Every selected function is admitted in parameter-free Restore INSERTs.
			valid := `CREATE TABLE restored(v TEXT COLLATE pg_unicode_fast); INSERT INTO restored VALUES(lower('ABC')), (upper('ß' COLLATE pg_unicode_fast)), (casefold('ẞ' COLLATE pg_unicode_fast)), (initcap('ﬃOO' COLLATE pg_unicode_fast));`
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(valid)); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM restored ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"Ffioo"}, {"SS"}, {"abc"}, {"ss"}}))
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			invalid := `CREATE TABLE bad(v VARCHAR(1)); INSERT INTO bad VALUES('z'); INSERT INTO bad VALUES(upper('ß' COLLATE pg_unicode_fast));`
			d := fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(1)")
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid)), &d)
			for _, bad := range []string{`CREATE TABLE bad(v TEXT); INSERT INTO bad VALUES(lower($1));`, `CREATE TABLE bad(v TEXT); INSERT INTO bad VALUES(lower(random()));`} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
					t.Fatalf("unsupported Restore producer succeeded: %s", bad)
				}
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("failed Restore changed committed catalog")
			}
		}
	})
}

func TestReferenceStringCaseAtomicityCancellation(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sc_source(id INT,v VARCHAR(1) COLLATE pg_unicode_fast)", 0),
			fixture.Exec("INSERT INTO sc_source VALUES(1,'a'),(2,'ß')", 2),
			fixture.Exec("CREATE TABLE sc_narrow(v VARCHAR(1))", 0),
			fixture.Exec("INSERT INTO sc_narrow VALUES('z')", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q := "INSERT INTO sc_narrow SELECT upper(v) FROM sc_source ORDER BY id"
		d := fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(1)")
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM sc_narrow", []string{"v"}, []string{"VARCHAR"}, [][]any{{"z"}}))
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		s := fixture.ErrorDiagnostic(q, d)
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		// Pool-backed calls enforce pre-cancellation before driver dispatch.
		// Keep this SQL valid: the expansion failure above is a separate witness.
		cancelQuery := "INSERT INTO sc_narrow SELECT upper(v) FROM sc_source WHERE id=1"
		cancelStmt, err := b.DB.PrepareContext(p.Ctx, cancelQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer cancelStmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, exec := range []func() (sql.Result, error){
			func() (sql.Result, error) { return b.DB.ExecContext(ctx, cancelQuery) },
			func() (sql.Result, error) { return cancelStmt.ExecContext(ctx) },
		} {
			result, err := exec()
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation result=%v error=%v", result, err)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM sc_narrow", []string{"v"}, []string{"VARCHAR"}, [][]any{{"z"}}))
		if !b.PG {
			var before, after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE cancelled(v TEXT DEFAULT (upper('ß' COLLATE pg_unicode_fast)));")); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("canceled Restore changed catalog")
			}
		}
	})
}
