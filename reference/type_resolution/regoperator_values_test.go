package reference_test

import (
	"database/sql"
	"fmt"
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
func TestReferenceRegOperatorValuesMetadata(t *testing.T) {
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		raw := fixture.Select("SELECT '597'::regoper AS r,'551'::regoperator AS s,NULL::regoper AS n", []string{"r", "s", "n"}, []string{"REGOPER", "REGOPERATOR", "REGOPER"}, [][]any{{"||/", "+(integer,integer)", nil}})
		raw.CheckRaw = func(t *testing.T, col, row int, v any) bool { fixture.RegtypeRaw(t, b.PG, v); return true }
		raw.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.RegOperatorMetadata(t, r, b.PG, raw.Columns, raw.Types) }
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
			q := fmt.Sprintf("SELECT %s::%s AS v,%s::%s::oid AS o", fixture.RegOperatorLiteral(tc.input), tc.alias, fixture.RegOperatorLiteral(tc.input), tc.alias)
			s := fixture.RegOperatorRows(q, tc.alias, tc.text, tc.oid, b.PG)
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
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RegOperatorRows(q, "regoper", text, oid, b.PG))
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
			q := "SELECT " + fixture.RegOperatorLiteral(tc.input) + "::regoperator AS v," + fixture.RegOperatorLiteral(tc.input) + "::regoperator::oid AS o"
			s := fixture.RegOperatorRows(q, "regoperator", tc.text, tc.oid, b.PG)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		var db string
		if e := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); e != nil {
			t.Fatal(e)
		}
		input := fixture.RegtypeIdentifier(db) + ".pg_catalog.+(int,int)"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RegOperatorRows("SELECT $1::regoperator AS v,$1::regoperator::oid AS o", "regoperator", "+(integer,integer)", 551, b.PG, input))
	})
}
