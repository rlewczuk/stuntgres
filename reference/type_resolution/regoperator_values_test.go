package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func regoperatorScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}
func regoperatorLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func regoperatorRows(q, alias, text string, oid int64, pg bool, args ...any) fixture.Step {
	var expectedOID any = oid
	if pg {
		expectedOID = fmt.Sprint(oid)
	}
	s := fixture.Select(q, []string{"v", "o"}, []string{strings.ToUpper(alias), "OID"}, [][]any{{text, expectedOID}}, args...)
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if col == 0 {
			fixture.RegtypeRaw(t, pg, v)
		} else {
			fixture.OidReferenceValue(t, pg, "OID", v)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		regoperatorMetadata(t, r, pg, []string{"v", "o"}, []string{strings.ToUpper(alias), "OID"})
	}
	return s
}
func regoperatorMetadata(t *testing.T, r *sql.Rows, pg bool, names, families []string) {
	t.Helper()
	ts, e := r.ColumnTypes()
	if e != nil {
		t.Fatal(e)
	}
	if len(ts) != len(names) {
		t.Fatal("metadata width", len(ts), len(names))
	}
	for i, c := range ts {
		scan := reflect.TypeFor[string]()
		if families[i] == "OID" {
			scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		n, nk := c.Nullable()
		l, lk := c.Length()
		pr, sc, dk := c.DecimalSize()
		if c.Name() != names[i] || c.DatabaseTypeName() != families[i] || c.ScanType() != scan || n || nk || l != 0 || lk || pr != 0 || sc != 0 || dk {
			t.Fatalf("metadata %d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), n, nk, l, lk, pr, sc, dk)
		}
	}
}
func TestReferenceRegOperatorValuesMetadata(t *testing.T) {
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		raw := fixture.Select("SELECT '597'::regoper AS r,'551'::regoperator AS s,NULL::regoper AS n", []string{"r", "s", "n"}, []string{"REGOPER", "REGOPERATOR", "REGOPER"}, [][]any{{"||/", "+(integer,integer)", nil}})
		raw.CheckRaw = func(t *testing.T, col, row int, v any) bool { fixture.RegtypeRaw(t, b.PG, v); return true }
		raw.CheckMetadata = func(t *testing.T, r *sql.Rows) { regoperatorMetadata(t, r, b.PG, raw.Columns, raw.Types) }
		fixture.RunStep(t, p.Ctx, c, b.Recorder, raw)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(raw))
		for _, tc := range []struct {
			alias, input, text string
			oid                int64
		}{
			{"regoper", "||/", "||/", 597}, {"regoper", "|/", "|/", 596}, {"regoper", "^@", "^@", 3877}, {"regoper", `"pg_catalog"."||/"`, "||/", 597},
			{"regoper", "551", "pg_catalog.+", 551}, {"regoperator", "551", "+(integer,integer)", 551},
			{"regoper", "0", "0", 0}, {"regoperator", "0", "0", 0}, {"regoper", "4294967295", "4294967295", 4294967295}, {"regoperator", "4294967295", "4294967295", 4294967295},
		} {
			q := fmt.Sprintf("SELECT %s::%s AS v,%s::%s::oid AS o", regoperatorLiteral(tc.input), tc.alias, regoperatorLiteral(tc.input), tc.alias)
			s := regoperatorRows(q, tc.alias, tc.text, tc.oid, b.PG)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, alias := range []string{"regoper", "regoperator"} {
			for _, suffix := range []string{"", " WHERE false"} {
				s := fixture.Select("SELECT NULL::"+alias+" AS v"+suffix, []string{"v"}, []string{strings.ToUpper(alias)}, [][]any{{nil}})
				if suffix != "" {
					s.Rows = nil
				}
				s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
					fixture.RegtypeRaw(t, b.PG, v)
					return true
				}
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					fixture.RegtypeCursorMetadata(t, r, b.PG, []string{"v"}, []string{strings.ToUpper(alias)}, nil)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		}
		for _, q := range []string{"SELECT (-1)::integer::regoper AS v,(-1)::integer::regoper::oid AS o", "SELECT '551'::regoperator::regoper AS v,'551'::regoperator::regoper::oid AS o"} {
			text, oid := "4294967295", int64(4294967295)
			if strings.Contains(q, "551") {
				text, oid = "pg_catalog.+", 551
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorRows(q, "regoper", text, oid, b.PG))
		}
	})
}
func TestReferenceRegOperatorSignatures(t *testing.T) {
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			input, text string
			oid         int64
		}{
			{"+(int4,int4)", "+(integer,integer)", 551}, {"pg_catalog.+(smallint,integer)", "+(smallint,integer)", 552}, {"pg_catalog.+(integer,smallint)", "+(integer,smallint)", 553},
			{` "pg_catalog" . "+" ( pg_catalog.int4 , integer ) `, "+(integer,integer)", 551}, {"-(none,int)", "-(NONE,integer)", 558}, {"|/(NONE,float8)", "|/(NONE,double precision)", 596}, {"!!(NONE,tsquery)", "!!(NONE,tsquery)", 3682},
		} {
			q := "SELECT " + regoperatorLiteral(tc.input) + "::regoperator AS v," + regoperatorLiteral(tc.input) + "::regoperator::oid AS o"
			s := regoperatorRows(q, "regoperator", tc.text, tc.oid, b.PG)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		var db string
		if e := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); e != nil {
			t.Fatal(e)
		}
		input := fixture.RegtypeIdentifier(db) + ".pg_catalog.+(int,int)"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorRows("SELECT $1::regoperator AS v,$1::regoperator::oid AS o", "regoperator", "+(integer,integer)", 551, b.PG, input))
	})
}
func regoperatorContext(p *fixture.Pair, b fixture.Backend) context.Context {
	return context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
}
