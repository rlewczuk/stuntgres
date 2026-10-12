package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprConstructors(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct {
			q            string
			names, types []string
			want         [][]any
		}{
			{`SELECT ROW()`, []string{"row"}, []string{"RECORD"}, [][]any{{"()"}}},
			{`SELECT ROW(1)`, []string{"row"}, []string{"RECORD"}, [][]any{{"(1)"}}},
			{`SELECT ROW(1,'x'),(1,'x'),(1) AS scalar`, []string{"row", "row", "scalar"}, []string{"RECORD", "RECORD", "INT4"}, [][]any{{"(1,x)", "(1,x)", int64(1)}}},
			{`SELECT ROW(ROW(2,'y'),3)`, []string{"row"}, []string{"RECORD"}, [][]any{{`("(2,y)",3)`}}},
			{`SELECT ROW(NULL::integer,NULL::text)`, []string{"row"}, []string{"RECORD"}, [][]any{{"(,)"}}},
			{`SELECT ROW(''::text,NULL::text,'a,b'::text)`, []string{"row"}, []string{"RECORD"}, [][]any{{`("",,"a,b")`}}},
		}
		for _, x := range cases {
			t.Run(x.q, func(t *testing.T) { compositeExprBoth(t, p, b, c, x.q, x.names, x.types, x.want) })
		}
		q := `SELECT ROW(` + strings.TrimSuffix(strings.Repeat("1,", 1664), ",") + `)::text AS v`
		compositeExprBoth(t, p, b, c, q, []string{"v"}, []string{"TEXT"}, [][]any{{"(" + strings.TrimSuffix(strings.Repeat("1,", 1664), ",") + ")"}})
		q = `SELECT ROW(` + strings.TrimSuffix(strings.Repeat("1,", 1665), ",") + `)`
		compositeExprError(t, p, c, q, "54011", "ROW expressions can have at most 1664 entries", "ROW")
	})
}

func TestReferenceCompositeExprConstructorCoercion(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		compositeExprBoth(t, p, b, c, `SELECT ROW(1,'x')::pair AS v,ROW(ROW(2,'y'),3)::outer_pair AS w`, []string{"v", "w"}, []string{"", ""}, [][]any{{"(1,x)", `("(2,y)",3)`}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder VALUES ($1,ROW($2,$3),ROW(ROW($4,$5),$6))`, 1, 1, 7, "z", 8, "nested", 9)))
		compositeExprBoth(t, p, b, c, `SELECT v,w FROM holder`, []string{"v", "w"}, []string{"", ""}, [][]any{{"(7,z)", `("(8,nested)",9)`}})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE mood AS ENUM ('ok','sad')`, `CREATE DOMAIN short_text AS varchar(3)`, `CREATE TYPE constrained_pair AS (s short_text,m mood,t varchar(2) COLLATE "C")`)
		compositeExprBoth(t, p, b, c, `SELECT ROW('abc','ok','xy')::constrained_pair AS v`, []string{"v"}, []string{""}, [][]any{{"(abc,ok,xy)"}})
		compositeExprBoth(t, p, b, c, `SELECT ROW(true,'x')::pair AS v`, []string{"v"}, []string{""}, [][]any{{"(1,x)"}})
		for _, x := range []struct{ q, detail string }{
			{`SELECT ROW(1)::pair`, `Input has too few columns.`},
			{`SELECT ROW(1,'x',3)::pair`, `Input has too many columns.`},
			{`SELECT ROW(ARRAY[1],'x')::pair`, `Cannot cast type integer[] to integer in column 1.`},
		} {
			t.Run(x.detail, func(t *testing.T) {
				d := fixture.CompositeReferenceDiagnostic("42846", "cannot cast type record to pair")
				d.Detail = x.detail
				d.Position = compositeExprPosition(x.q, "::pair")
				fixture.CompositeReferenceError(t, p, c, x.q, "prepare", d)
			})
		}
		q := `SELECT ROW('bad','x',3)::pair`
		d := fixture.CompositeReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = compositeExprPosition(q, "'bad'")
		fixture.CompositeReferenceError(t, p, c, q, "prepare", d)
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE other_pair AS (i integer,t text)`)
		q = `SELECT ROW(1,'x')::other_pair::pair`
		compositeExprError(t, p, c, q, "42846", "cannot cast type other_pair to pair", "::pair")
	})
}

func TestReferenceCompositeExprConstructorDemand(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE SEQUENCE skipped`, `CREATE SEQUENCE expanded`, `CREATE SEQUENCE held`)
		compositeExprSelect(t, p, b, c, fixture.Select(`SELECT (ROW(7,nextval('skipped'))).f1 AS v`, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		compositeExprSelect(t, p, b, c, fixture.Select(`SELECT nextval('skipped') AS v`, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		compositeExprSelect(t, p, b, c, fixture.Select(`SELECT (ROW(nextval('expanded'),nextval('expanded'))).*`, []string{"f1", "f2"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(2)}}))
		compositeExprSelect(t, p, b, c, fixture.Select(`SELECT nextval('expanded') AS v`, []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
		stmt, err := c.PrepareContext(p.Ctx, `SELECT (ROW(7,nextval('held'))).f1`)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for range 2 {
			var n int64
			if err := stmt.QueryRowContext(p.Ctx).Scan(&n); err != nil || n != 7 {
				t.Fatalf("held projection %d %v", n, err)
			}
		}
		compositeExprSelect(t, p, b, c, fixture.Select(`SELECT nextval('held') AS v`, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE demand_pair AS (i bigint,t text)`, `CREATE SEQUENCE computed`)
		compositeExprSelect(t, p, b, c, fixture.Select(`SELECT (COALESCE(ROW(nextval('computed'),nextval('computed')::text)::demand_pair,NULL::demand_pair)).*`, []string{"i", "t"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "4"}}))
		compositeExprSelect(t, p, b, c, fixture.Select(`SELECT nextval('computed') AS v`, []string{"v"}, []string{"INT8"}, [][]any{{int64(5)}}))
		compositeExprError(t, p, c, `SELECT (ROW(7,missing)).f1`, "42703", `column "missing" does not exist`, "missing")
		fixture.CompositeReferenceError(t, p, c, `SELECT (ROW(7,1/0)).f1`, "execute", fixture.CompositeReferenceDiagnostic("22012", "division by zero"))
	})
}

func TestReferenceCompositeExprMetadata(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{`SELECT ROW(1,'x') AS v`, `SELECT ROW(NULL::integer,NULL::text) AS v`, `SELECT ROW(1,'x') AS v WHERE false`} {
			want := [][]any{{"(1,x)"}}
			if strings.Contains(q, "NULL") {
				want = [][]any{{"(,)"}}
			}
			if strings.Contains(q, "false") {
				want = [][]any{}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"v"}, []string{"RECORD"}, want)
				s.Prepared = prepared
				s.CheckMetadata = compositeExprRecordMetadata(b.PG, []string{"v"})
				compositeExprSelect(t, p, b, c, s)
			}
		}
		compositeExprSetup(t, p, b, c)
		compositeExprBoth(t, p, b, c, `SELECT (ROW(1,'x'::text)).*`, []string{"f1", "f2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}})
		compositeExprBoth(t, p, b, c, `SELECT (NULL::pair).* WHERE false`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE limited_pair AS (s varchar(3),n numeric(5,2))`)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(`SELECT (ROW('abc',12.34)::limited_pair).*`, []string{"s", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"abc", "12.34"}})
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				ts, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				l, ok := ts[0].Length()
				p, scale, known := ts[1].DecimalSize()
				if l != 3 || !ok || p != 5 || scale != 2 || !known {
					t.Errorf("member typmods length=%d/%t decimal=%d,%d/%t", l, ok, p, scale, known)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceCompositeExprNullSemantics(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		for _, x := range []struct {
			row             string
			isNull, notNull bool
		}{
			{`ROW(NULL,NULL)`, true, false}, {`ROW(1,NULL)`, false, false}, {`ROW(1,2)`, false, true}, {`NULL::pair`, true, false}, {`ROW()`, true, true}, {`ROW(ROW(NULL,NULL))`, false, true},
		} {
			compositeExprBoth(t, p, b, c, `SELECT `+x.row+` IS NULL AS n,`+x.row+` IS NOT NULL AS nn`, []string{"n", "nn"}, []string{"BOOL", "BOOL"}, [][]any{{x.isNull, x.notNull}})
		}
		compositeExprBoth(t, p, b, c, `SELECT num_nulls(ROW(NULL,NULL)) AS n,num_nulls(NULL::pair) AS nn`, []string{"n", "nn"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}})
	})
}
