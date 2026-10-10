package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceCompositeTextIO(t *testing.T) {
	cases := []struct{ definition, input, output string }{
		{`a text,b text`, `(,)`, `(,)`},
		{`a text,b text`, `("",)`, `("",)`},
		{`a text,b text`, `(NULL,"NULL")`, `(NULL,NULL)`},
		{`a text,b text`, `( ,)`, `(" ",)`},
		{`a text,b text`, " \t(a,b)\r\n", "(a,b)"},
		{`a text,b text`, `("a,b","c(d)")`, `("a,b","c(d)")`},
		{`a text,b text`, `("a""b",c)`, `("a""b",c)`},
		{`a text,b text`, `(a\,b,c)`, `("a,b",c)`},
		{`a text,b text`, `(a\(b,c)`, `("a(b",c)`},
		{`a text,b text`, `(a(b,c)`, `("a(b",c)`},
		{`a text,b text`, `(a" b "c,d)`, `("a b c",d)`},
		{`a text,b text`, `("a\\b",c)`, `("a\\b",c)`},
		{`i integer,t text`, `( +0007 ,)`, `(7,)`},
		{`t text`, `()`, `()`},
		{``, `()`, `()`},
		{`p inner_record,t text`, `("(1,foo)",bar)`, `("(1,foo)",bar)`},
		{`p inner_record,t text`, `("(,)",)`, `("(,)",)`},
		{`p inner_record,t text`, `(,)`, `(,)`},
		{`a integer[],t text`, `("{1,2}",x)`, `("{1,2}",x)`},
		{`b boolean,c char(3),n numeric(4,1)`, `(true,x,12.34)`, `(t,"x  ",12.3)`},
		{`u uuid,j jsonb`, `(A0EEBC999C0B4EF8BB6D6BB9BD380A11,"{""a"":1}")`, `(a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11,"{""a"": 1}")`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				compositeReferenceSetup(t, p, b, c, `CREATE TYPE inner_record AS (i integer,t text)`, "CREATE TYPE tested AS ("+tc.definition+")", `CREATE TABLE holder(k integer,v tested)`)
				literal := "'" + strings.ReplaceAll(tc.input, "'", "''") + "'"
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO holder VALUES (1,"+literal+")", 1))
				buf := []byte(tc.input)
				referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(referenceExec(`INSERT INTO holder VALUES ($1,$2)`, 1, 2, buf)))
				for i := range buf {
					buf[i] = 'x'
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(referenceExec(`INSERT INTO holder VALUES ($1,$2)`, 1, 3, nil)))
				for _, prepared := range []bool{false, true} {
					compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{tc.output}, {tc.output}, {nil}}, prepared)
					compositeReferenceRows(t, p, b, c, `SELECT v FROM holder WHERE k=0`, [][]any{}, prepared)
				}
			})
		})
	}
}

func TestReferenceCompositeStandaloneConstraints(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TABLE constrained_source(id integer NOT NULL CHECK(id>0),note text)`, `CREATE TYPE wrapper AS (r constrained_source)`, `CREATE TABLE standalone_rows(k integer,v constrained_source NOT NULL,w wrapper)`)
		for _, prepared := range []bool{false, true} {
			s := referenceExec(`INSERT INTO standalone_rows VALUES (1,'(,free)','("(-1,nested)")'),(2,'(-1,negative)','("(,)")'),(3,'(,)','()')`, 3)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			enumReferenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(`SELECT k,v,w FROM standalone_rows ORDER BY k`, []string{"k", "v", "w"}, []string{"INT4", "", ""}, [][]any{{int64(1), "(,free)", `("(-1,nested)")`}, {int64(2), "(-1,negative)", `("(,)")`}, {int64(3), "(,)", "()"}}))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`DELETE FROM standalone_rows`, 3))
		}
		for _, tc := range []struct{ q, code, message, detail, table, column, constraint string }{
			{`INSERT INTO constrained_source VALUES (NULL,'x')`, "23502", `null value in column "id" of relation "constrained_source" violates not-null constraint`, `Failing row contains (null, x).`, "constrained_source", "id", ""},
			{`INSERT INTO constrained_source VALUES (-1,'x')`, "23514", `new row for relation "constrained_source" violates check constraint "constrained_source_id_check"`, `Failing row contains (-1, x).`, "constrained_source", "", "constrained_source_id_check"},
			{`INSERT INTO standalone_rows VALUES (4,NULL,NULL)`, "23502", `null value in column "v" of relation "standalone_rows" violates not-null constraint`, `Failing row contains (4, null, null).`, "standalone_rows", "v", ""},
		} {
			want := compositeReferenceDiagnostic(tc.code, tc.message)
			want.Detail = tc.detail
			want.Schema = "public"
			want.Table = tc.table
			want.Column = tc.column
			want.Constraint = tc.constraint
			compositeReferenceError(t, p, c, tc.q, "execute", want)
		}
	})
}
