package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func numericFormattingMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range columns {
		scan := reflect.TypeFor[string]()
		l, lk := col.Length()
		precision, scale, dk := col.DecimalSize()
		nullable, nk := col.Nullable()
		switch col.DatabaseTypeName() {
		case "TEXT":
			if l != math.MaxInt64 || !lk || precision != 0 || scale != 0 || dk {
				t.Fatalf("TEXT length/decimal: %d/%t %d,%d/%t", l, lk, precision, scale, dk)
			}
		case "NUMERIC":
			wp, ws, wk := int64(0), int64(0), false
			if pg {
				scan = reflect.TypeFor[any]()
				wp, ws, wk = 65535, 65531, true
			}
			if l != 0 || lk || precision != wp || scale != ws || dk != wk {
				t.Fatalf("NUMERIC length/decimal: %d/%t %d,%d/%t", l, lk, precision, scale, dk)
			}
		default:
			t.Fatalf("unexpected formatting result type %q", col.DatabaseTypeName())
		}
		if col.ScanType() != scan {
			t.Fatalf("%s ScanType got %v want %v", col.Name(), col.ScanType(), scan)
		}
		if nullable || nk {
			t.Fatalf("computed nullability got %t/%t want false/false", nullable, nk)
		}
	}
}

func TestReferenceNumericFormattingMetadata(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT to_char(12,'FM99'),to_number('12.50','99.99')", []string{"to_char", "to_number"}, []string{"TEXT", "NUMERIC"}, [][]any{{"12", "12.50"}}),
			fixture.Select("SELECT to_char(NULL::numeric,'FM99'),to_number(NULL::text,'99.99')", []string{"to_char", "to_number"}, []string{"TEXT", "NUMERIC"}, [][]any{{nil, nil}}),
			fixture.Select("SELECT to_char(12,'FM99') AS rendered,to_number('12.50','99.99') AS parsed WHERE false", []string{"rendered", "parsed"}, []string{"TEXT", "NUMERIC"}, nil),
			fixture.Select("SELECT to_char($1::numeric,$2::text) AS rendered,to_number($3::text,$4::text) AS parsed", []string{"rendered", "parsed"}, []string{"TEXT", "NUMERIC"}, [][]any{{"12.50", "12.50"}}, "12.50", "FM99.00", "12.50", "99.99"),
		} {
			t.Run(s.Query, func(t *testing.T) {
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { numericFormattingMetadata(t, r, b.PG) }
				s.CheckRaw = func(t *testing.T, i, j int, v any) bool { fixture.AssertRawValue(t, b.PG, s.Types[i], v); return true }
				numericFormattingBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceNumericFormattingParametersAndCoercions(t *testing.T) {
	numericFormattingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN nf_decimal AS numeric", 0))
		run(fixture.Exec("CREATE DOMAIN nf_label AS text", 0))
		for _, v := range []numericFormattingCase{
			{"domain-source", "to_char(12.50::nf_decimal,'FM99.00')", "", "12.50"},
			{"domain-input", "to_number('12.50'::nf_label,'99.99'::nf_label)", "NUMERIC", "12.50"},
			{"varchar", "to_char(12,'FM99'::varchar(8))", "", "12"},
			{"bpchar-template", "to_char(12,'FM99'::char(8))", "", "12"},
			{"bpchar-source", "to_number('12  '::char(6),'99')", "NUMERIC", "12"},
			{"name-source", "to_number('12'::name,'99'::name)", "NUMERIC", "12"},
			{"internal-char-source", "to_number('1'::\"char\",'9')", "NUMERIC", "1"},
			{"known-text", "to_number('12'::text,'99'::text)", "NUMERIC", "12"},
		} {
			t.Run(v.name, func(t *testing.T) {
				typ := v.typ
				if typ == "" {
					typ = "TEXT"
				}
				numericFormattingBoth(t, p, b, c, fixture.Select("SELECT "+v.expression+" AS result", []string{"result"}, []string{typ}, [][]any{{v.want}}))
			})
		}
		// NAME clipping at 63 bytes is distinguished by a quoted literal's
		// closing quote being clipped; the unmatched numeric quote is legal.
		nameMask := `"` + strings.Repeat("x", 64) + `"FM99`
		t.Run("name-clipping", func(t *testing.T) {
			numericFormattingBoth(t, p, b, c, fixture.Select("SELECT to_char(12,$1::name) AS result", []string{"result"}, []string{"TEXT"}, [][]any{{strings.Repeat("x", 62)}}, nameMask))
		})
		for _, typ := range []string{"integer", "bigint", "numeric", "real", "double precision", "smallint"} {
			t.Run(typ, func(t *testing.T) {
				q := "SELECT to_char($1::" + typ + ",$2::text) AS result"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, v := range []struct {
					n    any
					mask string
					want any
				}{{int64(12), "FM99", "12"}, {"13", "FM99", "13"}, {nil, "9.9.9", nil}, {"12", "FM000", "012"}, {"12", "FMRN", "XII"}} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"result"}, []string{"TEXT"}, [][]any{{v.want}}, v.n, v.mask))
				}
			})
		}
		for _, q := range []string{"SELECT to_number($1,$2) AS result", "SELECT to_char($1::numeric,$2) AS result"} {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				typ := "TEXT"
				if strings.Contains(q, "to_number") {
					typ = "NUMERIC"
				}
				for _, args := range [][]any{{"12", "FM99"}, {"13", "FM99"}, {nil, "9.9.9"}, {"12", nil}, {"12", "FM99"}} {
					want := any(args[0])
					if args[1] == nil {
						want = nil
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"result"}, []string{typ}, [][]any{{want}}, args...))
				}
			})
		}
		run(fixture.Exec("CREATE TABLE nf_vary(id integer,n numeric,s text,m text)", 0))
		run(fixture.Exec("INSERT INTO nf_vary VALUES(1,12,'12','FM99'),(2,13,'13','FM000'),(3,NULL,NULL,'9.9.9')", 3))
		numericFormattingBoth(t, p, b, c, fixture.Select("SELECT id,to_char(n,m) AS rendered,to_number(s,m) AS parsed FROM nf_vary ORDER BY id", []string{"id", "rendered", "parsed"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{{int64(1), "12", "12"}, {int64(2), "013", "13"}, {int64(3), nil, nil}}))
		for _, q := range []string{
			"SELECT to_char($1::nf_decimal,$2::nf_label) AS result",
			"SELECT to_number($1::\"char\",$2::text) AS result",
			"SELECT to_char($1::numeric,$1::text) AS result",
			"SELECT to_number($1::text,$1::text) AS result",
		} {
			t.Run("descriptor/"+q, func(t *testing.T) {
				args := []any{"12", "FM99"}
				if !strings.Contains(q, "$2") {
					args = args[:1]
				}
				if strings.Contains(q, "to_number($1::text,$1::text)") {
					s := numericFormattingError(q, numericFormattingDiagnostic("22P02", `invalid input syntax for type numeric: " "`, "", "", ""), args...)
					s.Prepared = true
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				} else {
					typ, want := "TEXT", "12"
					if strings.Contains(q, `"char"`) {
						typ, want = "NUMERIC", "1"
					}
					s := fixture.Select(q, []string{"result"}, []string{typ}, [][]any{{want}}, args...)
					s.Prepared = true
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) { numericFormattingMetadata(t, r, b.PG) }
					s.CheckRaw = func(t *testing.T, i, j int, v any) bool { fixture.AssertRawValue(t, b.PG, typ, v); return true }
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		// Malformed numeric input is rejected before mask execution, on the
		// same held handle that subsequently succeeds. Both drivers preserve
		// the prerequisite parameter-input Context.
		q := "SELECT to_char($1::numeric,$2::text) AS result"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		d := numericFormattingDiagnostic("22P02", `invalid input syntax for type numeric: "bad"`, "", "", "")
		d.Context = "unnamed portal parameter $1 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, numericFormattingError(q, d, "bad", "9.9.9"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"result"}, []string{"TEXT"}, [][]any{{"12"}}, "12", "FM99"))
	})
}
