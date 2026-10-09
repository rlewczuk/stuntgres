package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsCoreSourceConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					step := func(s referenceStep) {
						s.nonContext = true
						if s.selectRows && s.diagnostic == nil {
							previous := s.checkMetadata
							s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
								deparseReviewMetadata(t, b.pg, rows)
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					}
					run := func(s referenceStep) { s.prepared = prepared; s.nonContext = true; step(s) }
					run(referenceExec("CREATE TABLE t(a INT,b BIGINT DEFAULT 7,CONSTRAINT ck CHECK(a::bigint>0))", 0))
					for _, tc := range []struct{ suffix, constraint, expr string }{
						{"", "CHECK (((a)::bigint > 0))", "((a)::bigint > 0)"},
						{",false", "CHECK (((a)::bigint > 0))", "((a)::bigint > 0)"},
						{",true", "CHECK (a::bigint > 0)", "a::bigint > 0"},
					} {
						run(referenceSelect("SELECT pg_get_constraintdef(oid"+tc.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"d"}, []string{"TEXT"}, [][]any{{tc.constraint}}))
						run(referenceSelect("SELECT pg_get_expr(conbin,conrelid"+tc.suffix+") AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"d"}, []string{"TEXT"}, [][]any{{tc.expr}}))
					}
					run(referenceSelect("SELECT pg_get_expr(d.adbin,0) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='t'", []string{"d"}, []string{"TEXT"}, [][]any{{"7"}}))
					for _, q := range []string{"SELECT pg_get_constraintdef(NULL) AS d", "SELECT pg_get_expr(conbin,NULL) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", "SELECT pg_get_expr(conbin,4294967295::oid) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", "SELECT pg_get_constraintdef(0) AS d", "SELECT pg_get_constraintdef(NULL,true) AS d", "SELECT pg_get_constraintdef(0,NULL) AS d", "SELECT pg_get_constraintdef(4294967295::oid) AS d", "SELECT pg_get_expr(NULL,0) AS d", "SELECT pg_get_expr(NULL,NULL) AS d", "SELECT pg_get_expr(NULL,0,NULL) AS d", "SELECT pg_get_expr(NULL,0,true) AS d", "SELECT pg_get_expr(conbin,conrelid,NULL) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", "SELECT pg_get_expr(d.adbin,4294967295::oid) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='t'"} {
						run(referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{nil}}))
					}
					sourceQueryFailure1 := referenceImplicitError("SELECT pg_get_expr(conbin,0) FROM pg_catalog.pg_constraint WHERE conname='ck'", referenceDiagnostic{Code: "22023", Message: "expression contains variables"})
					sourceQueryFailure1.selectRows = true
					run(sourceQueryFailure1)
					run(referenceExec("CREATE TABLE other(z TEXT)", 0))
					run(referenceSelect("SELECT pg_get_expr(conbin,'other'::regclass) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"d"}, []string{"TEXT"}, [][]any{{"((z)::bigint > 0)"}}))
					run(referenceExec(`ALTER TABLE t RENAME a TO "A renamed"`, 0))
					run(referenceSelect("SELECT pg_get_constraintdef(oid) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"d"}, []string{"TEXT"}, [][]any{{`CHECK ((("A renamed")::bigint > 0))`}}))
					run(referenceSelect("SELECT pg_get_expr(conbin,conrelid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"d"}, []string{"TEXT"}, [][]any{{`"A renamed"::bigint > 0`}}))
					run(referenceExec("CREATE TABLE modifiers(a INTEGER CONSTRAINT nn NOT NULL,b INTEGER,CONSTRAINT ck_no CHECK(b>0) NO INHERIT)", 0))
					run(referenceSelect("SELECT pg_get_constraintdef(oid) AS d FROM pg_catalog.pg_constraint WHERE conname='nn'", []string{"d"}, []string{"TEXT"}, [][]any{{"NOT NULL a"}}))
					run(referenceSelect("SELECT pg_get_constraintdef(oid,false) AS d FROM pg_catalog.pg_constraint WHERE conname='ck_no'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK ((b > 0)) NO INHERIT"}}))
					run(referenceSelect("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='ck_no'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (b > 0) NO INHERIT"}}))
					run(referenceSelect("SELECT pg_get_expr(conbin,conrelid) AS d FROM pg_catalog.pg_constraint WHERE conname='nn'", []string{"d"}, []string{"TEXT"}, [][]any{{nil}}))
				})
			}
		})
	}
}

func TestReferenceDeparseConstraintsRendererGoldensSourceConsumerRoutes(t *testing.T) {
	for _, tc := range []struct{ expr, compact, pretty string }{
		{"a>0 AND (b<5 OR flag)", "((a > 0) AND ((b < 5) OR flag))", "a > 0 AND (b < 5 OR flag)"},
		{"NULLIF(a,1)>0", "(NULLIF(a, 1) > 0)", "NULLIF(a, 1) > 0"},
		{"GREATEST(a,1)>0", "(GREATEST(a, 1) > 0)", "GREATEST(a, 1) > 0"},
		{"LEAST(a,3)>0", "(LEAST(a, 3) > 0)", "LEAST(a, 3) > 0"},
		{"COALESCE(a,1)>0", "(COALESCE(a, 1) > 0)", "COALESCE(a, 1) > 0"},
		{"flag IS NOT TRUE", "(flag IS NOT TRUE)", "flag IS NOT TRUE"},
		{"NOT flag", "(NOT flag)", "NOT flag"},
		{"a BETWEEN 1 AND 5", "((a >= 1) AND (a <= 5))", "a >= 1 AND a <= 5"},
		{"a BETWEEN SYMMETRIC 1 AND 5", "(((a >= 1) AND (a <= 5)) OR ((a >= 5) AND (a <= 1)))", "a >= 1 AND a <= 5 OR a >= 5 AND a <= 1"},
		{"a NOT BETWEEN 1 AND 5", "((a < 1) OR (a > 5))", "a < 1 OR a > 5"},
		{"a IS DISTINCT FROM 1", "(a IS DISTINCT FROM 1)", "a IS DISTINCT FROM 1"},
		{"a IS NOT NULL", "(a IS NOT NULL)", "a IS NOT NULL"},
		{"CASE WHEN flag THEN a ELSE 2 END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0"},
		{"CASE a WHEN 1 THEN true ELSE false END", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND"},
		{"abs(a)>0", "(abs(a) > 0)", "abs(a) > 0"},
		{"(a+1)*2>0", "(((a + 1) * 2) > 0)", "((a + 1) * 2) > 0"},
		{"(a & 3)=1", "((a & 3) = 1)", "(a & 3) = 1"},
		{"round(n,1)>0", "(round(n, 1) > (0)::numeric)", "round(n, 1) > 0::numeric"},
		{"random()>0", "(random() > (0)::double precision)", "random() > 0::double precision"},
		{"random_normal() IS NOT NULL", "(random_normal() IS NOT NULL)", "random_normal() IS NOT NULL"},
		{"a::bigint=b", "((a)::bigint = b)", "a::bigint = b"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) {
						s.nonContext = true
						if s.selectRows && s.diagnostic == nil {
							s.checkMetadata = func(t *testing.T, rows *sql.Rows) { deparseReviewMetadata(t, b.pg, rows) }
						}
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					}
					run(referenceExec("CREATE TABLE render(a INT,b BIGINT,flag BOOL,n NUMERIC,CONSTRAINT c CHECK("+tc.expr+"))", 0))
					for _, prepared := range []bool{false, true} {
						for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
							s := referenceSelect("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}})
							s.prepared = prepared
							run(s)
						}
					}
				})
			}
		})
	}
}

func TestReferenceDeparseConstraintsAssignmentGoldensSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				if s.selectRows && s.diagnostic == nil {
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) { deparseReviewMetadata(t, b.pg, rows) }
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
			run(referenceExec("CREATE TABLE assigned(a DOUBLE PRECISION DEFAULT 0,b REAL DEFAULT 1,c DOUBLE PRECISION DEFAULT 0.5,n NUMERIC DEFAULT 0.0,f BOOL DEFAULT false)", 0))
			for _, prepared := range []bool{false, true} {
				for _, pretty := range []string{"false", "true"} {
					s := referenceSelect("SELECT adnum,pg_get_expr(adbin,0,"+pretty+") AS d FROM pg_catalog.pg_attrdef ORDER BY adnum", []string{"adnum", "d"}, []string{"INT2", "TEXT"}, [][]any{{int64(1), "0"}, {int64(2), "1"}, {int64(3), "0.5"}, {int64(4), "0.0"}, {int64(5), "false"}})
					s.prepared = prepared
					run(s)
				}
			}
		})
	}
}
