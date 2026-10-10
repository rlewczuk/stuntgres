package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceFormatEncodingControls(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE fe_controls(id integer, r real, d double precision)", 0))
		run(referenceExec("INSERT INTO fe_controls VALUES(1,'1.23456789','1.2345678901234567'),(2,'-0','-0'),(3,'1e-45','5e-324'),(4,'33554448','1.8014398509481992e+16')", 4))
		run(referenceExec("SET extra_float_digits TO -15", 0))
		run(referenceExec("SET lc_numeric TO 'POSIX'", 0))
		rounded := referenceSelect("SELECT r::text AS r,d::text AS d FROM fe_controls ORDER BY id", []string{"r", "d"}, []string{"TEXT", "TEXT"}, [][]any{{"1", "1"}, {"-0", "-0"}, {"1e-45", "5e-324"}, {"3e+07", "2e+16"}})
		run(rounded)
		if !b.pg {
			cursor, err := c.QueryContext(p.ctx, "SELECT d::text AS d FROM fe_controls WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
			// Restore must retain current settings, not replay the settings
			// effective when Dump emitted its lossless SQL.
			run(referenceExec("SET extra_float_digits TO 0", 0))
			run(referenceExec("SET lc_numeric TO 'C'", 0))
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "0"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "C"))
			run(referenceExec("SET extra_float_digits TO -15", 0))
			run(referenceExec("SET lc_numeric TO 'POSIX'", 0))
			run(rounded)
			if !cursor.Next() {
				t.Fatal("retained controls cursor", cursor.Err())
			}
			var old string
			if err := cursor.Scan(&old); err != nil || old != "1" {
				t.Fatal("retained text", old, err)
			}
			if cursor.Next() || cursor.Err() != nil {
				t.Fatal("retained controls EOF", cursor.Err())
			}
			cursor.Close()
			bad := "CREATE TABLE fe_replacement(v double precision); INSERT INTO fe_replacement VALUES('1'),('bad')"
			err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad))
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type double precision: "bad"`, Position: "90"})
			run(rounded)
			run(referenceSelect("SELECT count(*) AS n FROM pg_tables WHERE schemaname='public' AND tablename='fe_replacement'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
			rawValues := [][]any{
				{float64(float32(1.23456789)), 1.2345678901234567},
				{math.Copysign(0, -1), math.Copysign(0, -1)},
				{float64(math.Float32frombits(1)), math.Float64frombits(1)},
				{float64(33554448), 1.8014398509481992e+16},
			}
			raw := referenceSelect("SELECT r,d FROM fe_controls ORDER BY id", []string{"r", "d"}, []string{"FLOAT4", "FLOAT8"}, rawValues)
			raw.checkRaw = func(t *testing.T, col, row int, v any) bool {
				want := rawValues[row][col].(float64)
				value, ok := v.(float64)
				if !ok || math.Float64bits(value) != math.Float64bits(want) {
					t.Fatalf("restored binary col%d=%T(%v), want bits %#x", col, v, v, math.Float64bits(want))
				}
				return true
			}
			run(raw)
			// Replay the actual unchanged emitted SQL, not a manually reconstructed
			// equivalent. Use the existing paired pinned backend when enabled.
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatalf("actual canonical SQL replay: %v\n%s", err, dump.String())
				}
				pg, err := p.postgres.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, pg, p.postgresRecord, referenceExec("SET extra_float_digits TO 3", 0))
				referenceRunStep(t, p.ctx, pg, p.postgresRecord, formatEncodingText("SELECT d::text AS v FROM fe_controls WHERE id=1", "v", "1.2345678901234567"))
				pg.Close()
				if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE fe_controls"); err != nil {
					t.Fatal(err)
				}
			}
		}
		run(referenceExec("SET extra_float_digits TO 3", 0))
		run(formatEncodingText("SELECT d::text AS v FROM fe_controls WHERE id=1", "v", "1.2345678901234567"))
	})
}

func TestReferenceFormatEncodingFeatureSmoke(t *testing.T) {
	roleReferenceCleanup(t, []string{"fe_smoke_role"})
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("SET NAMES 'utf-8'", 0))
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
		run(referenceExec("CREATE TABLE fe_smoke(v text, r real, d double precision)", 0))
		run(referencePrepared(referenceExec("INSERT INTO fe_smoke VALUES($1,'1.23456789','1.2345678901234567')", 1, "é🙂")))
		run(formatEncodingText("SELECT v FROM fe_smoke", "v", "é🙂"))
		for _, name := range []string{"lc_numeric", "lc_monetary", "lc_time"} {
			run(referenceExec("SET "+name+" TO 'POSIX'", 0))
		}
		run(referenceSelect("SELECT to_char(-1234.5::numeric,'FMS9G999D00') AS n,to_char(12::numeric,'FML99') AS m,to_char(TIMESTAMP '2006-10-19','TMMonth TMDay') AS t", []string{"n", "m", "t"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"-1,234.50", " 12", "October Thursday"}}))
		const q = "SELECT r::text AS r,d::text AS d FROM fe_smoke"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{statement: stmt, query: q}
		for _, tc := range []struct{ digits, r, d string }{{"1", "1.2345679", "1.2345678901234567"}, {"0", "1.23457", "1.23456789012346"}, {"-15", "1", "1"}, {"3", "1.2345679", "1.2345678901234567"}} {
			run(referenceExec("SET extra_float_digits TO "+tc.digits, 0))
			step := referenceSelect(q, []string{"r", "d"}, []string{"TEXT", "TEXT"}, [][]any{{tc.r, tc.d}})
			run(step)
			referenceRunStep(t, p.ctx, held, b.recorder, step)
		}
		run(referenceExec("SET extra_float_digits TO 0", 0))
		cursor, err := stmt.QueryContext(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer cursor.Close()
		if b.pg {
			other, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, other, b.recorder, referenceExec("SET extra_float_digits TO 3", 0))
			other.Close()
		} else {
			run(referenceExec("SET extra_float_digits TO 3", 0))
		}
		if !cursor.Next() {
			t.Fatal("smoke retained row", cursor.Err())
		}
		var r, d string
		if err := cursor.Scan(&r, &d); err != nil || r != "1.23457" || d != "1.23456789012346" {
			t.Fatal("smoke retained precision", r, d, err)
		}
		if cursor.Next() || cursor.Err() != nil {
			t.Fatal("smoke retained EOF", cursor.Err())
		}
		cursor.Close()
		run(referenceExec("SET extra_float_digits TO 3", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"r", "d"}, []string{"TEXT", "TEXT"}, [][]any{{"1.2345679", "1.2345678901234567"}}))
		run(referenceExec("CREATE ROLE fe_smoke_role", 0))
		run(referenceExec("SET ROLE fe_smoke_role", 0))
		defer c.ExecContext(p.ctx, "RESET ROLE")
		run(referenceImplicitError("SET lc_messages TO 'bad'", formatEncodingPermission()))
		run(referenceExec("RESET ROLE", 0))
		run(referenceExec("SET lc_messages TO 'C'", 0))
		localeDetail := "Only C and POSIX locales are supported by stuntgres."
		if b.pg {
			localeDetail = ""
		}
		for _, step := range []referenceStep{
			referenceImplicitError("SET client_encoding TO 'bad'", formatEncodingInvalid("client_encoding", "bad", "")),
			referenceImplicitError("SET lc_numeric TO 'stuntgres_missing_locale_18'", formatEncodingInvalid("lc_numeric", "stuntgres_missing_locale_18", localeDetail)),
			referenceImplicitError("SET extra_float_digits TO 4", referenceDiagnostic{Code: "22023", Message: `4 is outside the valid range for parameter "extra_float_digits" (-15 .. 3)`}),
		} {
			run(step)
		}
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
		run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "3"))
		if !b.pg {
			run(referenceExec("SET extra_float_digits TO -15", 0))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "1.2345678901234567") {
				t.Fatal("rounded canonical smoke Dump", dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(referenceExec("SET extra_float_digits TO 3", 0))
			run(formatEncodingText("SELECT d::text AS v FROM fe_smoke", "v", "1.2345678901234567"))
		}
	})
}
