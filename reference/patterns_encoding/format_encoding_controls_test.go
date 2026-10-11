package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatEncodingControls(t *testing.T) {
	formatEncodingBackends(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fe_controls(id integer, r real, d double precision)", 0))
		run(fixture.Exec("INSERT INTO fe_controls VALUES(1,'1.23456789','1.2345678901234567'),(2,'-0','-0'),(3,'1e-45','5e-324'),(4,'33554448','1.8014398509481992e+16')", 4))
		run(fixture.Exec("SET extra_float_digits TO -15", 0))
		run(fixture.Exec("SET lc_numeric TO 'POSIX'", 0))
		rounded := fixture.Select("SELECT r::text AS r,d::text AS d FROM fe_controls ORDER BY id", []string{"r", "d"}, []string{"TEXT", "TEXT"}, [][]any{{"1", "1"}, {"-0", "-0"}, {"1e-45", "5e-324"}, {"3e+07", "2e+16"}})
		run(rounded)
		if !b.PG {
			cursor, err := c.QueryContext(p.Ctx, "SELECT d::text AS d FROM fe_controls WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
			// Restore must retain current settings, not replay the settings
			// effective when Dump emitted its lossless SQL.
			run(fixture.Exec("SET extra_float_digits TO 0", 0))
			run(fixture.Exec("SET lc_numeric TO 'C'", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "0"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "C"))
			run(fixture.Exec("SET extra_float_digits TO -15", 0))
			run(fixture.Exec("SET lc_numeric TO 'POSIX'", 0))
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
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type double precision: "bad"`, Position: "90"})
			run(rounded)
			run(fixture.Select("SELECT count(*) AS n FROM pg_tables WHERE schemaname='public' AND tablename='fe_replacement'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
			rawValues := [][]any{
				{float64(float32(1.23456789)), 1.2345678901234567},
				{math.Copysign(0, -1), math.Copysign(0, -1)},
				{float64(math.Float32frombits(1)), math.Float64frombits(1)},
				{float64(33554448), 1.8014398509481992e+16},
			}
			raw := fixture.Select("SELECT r,d FROM fe_controls ORDER BY id", []string{"r", "d"}, []string{"FLOAT4", "FLOAT8"}, rawValues)
			raw.CheckRaw = func(t *testing.T, col, row int, v any) bool {
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
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatalf("actual canonical SQL replay: %v\n%s", err, dump.String())
				}
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, fixture.Exec("SET extra_float_digits TO 3", 0))
				fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, formatEncodingText("SELECT d::text AS v FROM fe_controls WHERE id=1", "v", "1.2345678901234567"))
				pg.Close()
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE fe_controls"); err != nil {
					t.Fatal(err)
				}
			}
		}
		run(fixture.Exec("SET extra_float_digits TO 3", 0))
		run(formatEncodingText("SELECT d::text AS v FROM fe_controls WHERE id=1", "v", "1.2345678901234567"))
	})
}

func TestReferenceFormatEncodingFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fe_smoke_role"})
	formatEncodingBackends(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET NAMES 'utf-8'", 0))
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
		run(fixture.Exec("CREATE TABLE fe_smoke(v text, r real, d double precision)", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO fe_smoke VALUES($1,'1.23456789','1.2345678901234567')", 1, "é🙂")))
		run(formatEncodingText("SELECT v FROM fe_smoke", "v", "é🙂"))
		for _, name := range []string{"lc_numeric", "lc_monetary", "lc_time"} {
			run(fixture.Exec("SET "+name+" TO 'POSIX'", 0))
		}
		run(fixture.Select("SELECT to_char(-1234.5::numeric,'FMS9G999D00') AS n,to_char(12::numeric,'FML99') AS m,to_char(TIMESTAMP '2006-10-19','TMMonth TMDay') AS t", []string{"n", "m", "t"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"-1,234.50", " 12", "October Thursday"}}))
		const q = "SELECT r::text AS r,d::text AS d FROM fe_smoke"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Statement: stmt, SQL: q}
		for _, tc := range []struct{ digits, r, d string }{{"1", "1.2345679", "1.2345678901234567"}, {"0", "1.23457", "1.23456789012346"}, {"-15", "1", "1"}, {"3", "1.2345679", "1.2345678901234567"}} {
			run(fixture.Exec("SET extra_float_digits TO "+tc.digits, 0))
			step := fixture.Select(q, []string{"r", "d"}, []string{"TEXT", "TEXT"}, [][]any{{tc.r, tc.d}})
			run(step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		run(fixture.Exec("SET extra_float_digits TO 0", 0))
		cursor, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer cursor.Close()
		if b.PG {
			other, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, other, b.Recorder, fixture.Exec("SET extra_float_digits TO 3", 0))
			other.Close()
		} else {
			run(fixture.Exec("SET extra_float_digits TO 3", 0))
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
		run(fixture.Exec("SET extra_float_digits TO 3", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"r", "d"}, []string{"TEXT", "TEXT"}, [][]any{{"1.2345679", "1.2345678901234567"}}))
		run(fixture.Exec("CREATE ROLE fe_smoke_role", 0))
		run(fixture.Exec("SET ROLE fe_smoke_role", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		run(fixture.ImplicitError("SET lc_messages TO 'bad'", formatEncodingPermission()))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET lc_messages TO 'C'", 0))
		localeDetail := "Only C and POSIX locales are supported by stuntgres."
		if b.PG {
			localeDetail = ""
		}
		for _, step := range []fixture.Step{
			fixture.ImplicitError("SET client_encoding TO 'bad'", formatEncodingInvalid("client_encoding", "bad", "")),
			fixture.ImplicitError("SET lc_numeric TO 'stuntgres_missing_locale_18'", formatEncodingInvalid("lc_numeric", "stuntgres_missing_locale_18", localeDetail)),
			fixture.ImplicitError("SET extra_float_digits TO 4", fixture.Diagnostic{Code: "22023", Message: `4 is outside the valid range for parameter "extra_float_digits" (-15 .. 3)`}),
		} {
			run(step)
		}
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
		run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "POSIX"))
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "3"))
		if !b.PG {
			run(fixture.Exec("SET extra_float_digits TO -15", 0))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "1.2345678901234567") {
				t.Fatal("rounded canonical smoke Dump", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-15"))
			run(fixture.Exec("SET extra_float_digits TO 3", 0))
			run(formatEncodingText("SELECT d::text AS v FROM fe_smoke", "v", "1.2345678901234567"))
		}
	})
}
