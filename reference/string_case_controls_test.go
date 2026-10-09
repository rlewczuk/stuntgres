package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceStringCaseConsumers(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		stringCaseSetup(t, p, b, c)
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sc_stored(id INT,s TEXT COLLATE pg_c_utf8 DEFAULT (casefold('İ' COLLATE pg_c_utf8)),f TEXT COLLATE pg_unicode_fast DEFAULT (initcap('ßSS' COLLATE pg_unicode_fast)),CHECK(lower(s)=lower(s)),CHECK(upper(f)=upper(f)))", 0),
			referenceExec("INSERT INTO sc_stored(id) VALUES(1)", 1),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		stored := referenceSelect("SELECT s,f,upper(s) AS su,casefold(f) AS ff FROM sc_stored", []string{"s", "f", "su", "ff"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"İ", "Ssss", "İ", "ssss"}})
		stringCaseBoth(t, p, b, c, stored)
		for _, s := range []referenceStep{
			referenceSelect("SELECT id,lower(upper(c)) AS v FROM sc WHERE upper(v)='ABC  ' ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "abc"}}),
			referenceSelect("SELECT casefold(t COLLATE pg_unicode_fast) AS v,count(*) AS n FROM sc GROUP BY casefold(t COLLATE pg_unicode_fast) ORDER BY v NULLS LAST", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"", int64(1)}, {"i\u0307σ", int64(1)}, {"strasse", int64(1)}, {nil, int64(1)}}),
		} {
			stringCaseBoth(t, p, b, c, s)
		}
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sc_dest(id INT,v TEXT COLLATE pg_unicode_fast DEFAULT (upper('ß' COLLATE pg_unicode_fast)),CHECK(v=upper(v)))", 0),
			referenceExec("INSERT INTO sc_dest(id) VALUES(0)", 1),
			referencePrepared(referenceExec("INSERT INTO sc_dest SELECT id,upper(t COLLATE pg_unicode_fast) FROM sc WHERE id=1", 1)),
			referenceSelect("UPDATE sc_dest SET v=upper('ﬃ' COLLATE pg_unicode_fast) WHERE id=1 RETURNING id,lower(v) AS lo,casefold(v) AS fo,initcap(v) AS ic", []string{"id", "lo", "fo", "ic"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "ffi", "ffi", "Ffi"}}),
			referenceExec("CREATE VIEW sc_view AS SELECT id,lower(v) AS lo,upper(v) AS up,casefold(v) AS fo,initcap(v) AS ic FROM sc_dest", 0),
			referenceExec("ALTER TABLE sc_dest RENAME COLUMN v TO renamed", 0),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		want := referenceSelect("SELECT id,lo,up,fo,ic FROM sc_view ORDER BY id", []string{"id", "lo", "up", "fo", "ic"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(0), "ss", "SS", "ss", "Ss"}, {int64(1), "ffi", "FFI", "ffi", "Ffi"}})
		stringCaseBoth(t, p, b, c, want)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			old, err := c.QueryContext(p.ctx, want.query)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			meta, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
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
			if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
				t.Fatal(err)
			}
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.postgres.ExecContext(p.ctx, redump.String()); err != nil {
					t.Fatal("dump SQL replay", err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, stored)
				// Leave the oracle empty for its later shared backend callback.
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
			// Every selected function is admitted in parameter-free Restore INSERTs.
			valid := `CREATE TABLE restored(v TEXT COLLATE pg_unicode_fast); INSERT INTO restored VALUES(lower('ABC')), (upper('ß' COLLATE pg_unicode_fast)), (casefold('ẞ' COLLATE pg_unicode_fast)), (initcap('ﬃOO' COLLATE pg_unicode_fast));`
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(valid)); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM restored ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"Ffioo"}, {"SS"}, {"abc"}, {"ss"}}))
			var before bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
				t.Fatal(err)
			}
			invalid := `CREATE TABLE bad(v VARCHAR(1)); INSERT INTO bad VALUES('z'); INSERT INTO bad VALUES(upper('ß' COLLATE pg_unicode_fast));`
			d := controlsReferenceDiagnostic("22001", "value too long for type character varying(1)")
			referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(invalid)), &d)
			for _, bad := range []string{`CREATE TABLE bad(v TEXT); INSERT INTO bad VALUES(lower($1));`, `CREATE TABLE bad(v TEXT); INSERT INTO bad VALUES(lower(random()));`} {
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)); err == nil {
					t.Fatalf("unsupported Restore producer succeeded: %s", bad)
				}
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("failed Restore changed committed catalog")
			}
		}
	})
}

func TestReferenceStringCaseAtomicityCancellation(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sc_source(id INT,v VARCHAR(1) COLLATE pg_unicode_fast)", 0),
			referenceExec("INSERT INTO sc_source VALUES(1,'a'),(2,'ß')", 2),
			referenceExec("CREATE TABLE sc_narrow(v VARCHAR(1))", 0),
			referenceExec("INSERT INTO sc_narrow VALUES('z')", 1),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		q := "INSERT INTO sc_narrow SELECT upper(v) FROM sc_source ORDER BY id"
		d := controlsReferenceDiagnostic("22001", "value too long for type character varying(1)")
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(q, d)
			s.errorStage = "execute"
			if prepared {
				s = referencePrepared(s)
			}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM sc_narrow", []string{"v"}, []string{"VARCHAR"}, [][]any{{"z"}}))
		}
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		s := referenceErrorDiagnostic(q, d)
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, s)
		// Pool-backed calls enforce pre-cancellation before driver dispatch.
		// Keep this SQL valid: the expansion failure above is a separate witness.
		cancelQuery := "INSERT INTO sc_narrow SELECT upper(v) FROM sc_source WHERE id=1"
		cancelStmt, err := b.db.PrepareContext(p.ctx, cancelQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer cancelStmt.Close()
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		for _, exec := range []func() (sql.Result, error){
			func() (sql.Result, error) { return b.db.ExecContext(ctx, cancelQuery) },
			func() (sql.Result, error) { return cancelStmt.ExecContext(ctx) },
		} {
			result, err := exec()
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation result=%v error=%v", result, err)
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM sc_narrow", []string{"v"}, []string{"VARCHAR"}, [][]any{{"z"}}))
		if !b.pg {
			var before, after bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE cancelled(v TEXT DEFAULT (upper('ß' COLLATE pg_unicode_fast)));")); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("canceled Restore changed catalog")
			}
		}
	})
}
