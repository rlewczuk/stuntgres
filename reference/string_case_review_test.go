package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceStringCaseReviewSigma(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// Independently derived from REL_18_0's original-input prefix scan and
		// word boundaries; confirmed on pinned PostgreSQL18.0 by root discovery.
		cases := []struct{ input, lower, initcap string }{
			{"\u0345Σ", "\u0345ς", "Ις"},
			{"\u0301Σ", "\u0301ς", "\u0301Σ"},
			{"\u0301\u0345Σ", "\u0301\u0345ς", "\u0301Ις"},
			{"\u0345Σ\u0345", "\u0345ς\u0345", "Ις\u0345"},
			{"Σ", "σ", "Σ"},
			{"1Σ", "1σ", "1σ"},
			{"1\u0345Σ", "1\u0345σ", "1\u0345σ"},
			{"\u0345ΣΑ", "\u0345σα", "Ισα"},
			{"\u0301ΣΑ", "\u0301σα", "\u0301Σα"},
			{"ΑΣ", "ας", "Ας"},
		}
		q := "SELECT lower($1 COLLATE pg_unicode_fast) AS lo,initcap($1 COLLATE pg_unicode_fast) AS ic"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%U", []rune(tc.input)), func(t *testing.T) {
				want := [][]any{{tc.lower, tc.initcap}}
				literal := "SELECT lower('" + tc.input + "' COLLATE pg_unicode_fast) AS lo,initcap('" + tc.input + "' COLLATE pg_unicode_fast) AS ic"
				stringCaseBoth(t, p, b, c, referenceSelect(literal, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, want))
				stringCaseBoth(t, p, b, c, referenceSelect(q, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, want, tc.input))
				referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, want, tc.input))
			})
		}
		for _, value := range []any{"", nil, "\u0345Σ"} {
			row := []any{"", ""}
			if value == nil {
				row = []any{nil, nil}
			} else if value != "" {
				row = []any{"\u0345ς", "Ις"}
			}
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, [][]any{row}, value))
		}
	})
}

func TestReferenceStringCaseReviewTypedCollate(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE sc_review_typed(id INT)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO sc_review_typed VALUES(1)", 1))
		for _, op := range []string{"lower", "upper", "casefold", "initcap"} {
			for _, arg := range []string{"1", "NULL::integer"} {
				q := "/*é🙂*/ SELECT " + op + "(" + arg + " COLLATE pg_unicode_fast) FROM sc_review_typed"
				d := controlsReferenceDiagnostic("42804", "collations are not supported by type integer")
				d.Position = referencePosition(len([]rune(q[:strings.Index(q, "COLLATE")])) + 1)
				stringCaseFailure(t, p, b, c, q, d, true)
			}
			q := "/*é🙂*/ SELECT $1::integer AS n," + op + "($1 COLLATE pg_unicode_fast) AS v FROM sc_review_typed"
			d := controlsReferenceDiagnostic("42804", "collations are not supported by type integer")
			d.Position = referencePosition(len([]rune(q[:strings.Index(q, "COLLATE")])) + 1)
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/established/prepared=%v", op, prepared), func(t *testing.T) {
					s := referenceErrorDiagnostic(q, d, 1)
					s.selectRows, s.errorStage = true, "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				})
			}
		}
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT lower($1::char(4) COLLATE pg_unicode_fast) AS lo,upper($1::char(4) COLLATE pg_unicode_fast) AS up,casefold($1::char(4) COLLATE pg_unicode_fast) AS fo,initcap($1::char(4) COLLATE pg_unicode_fast) AS ic FROM sc_review_typed", []string{"lo", "up", "fo", "ic"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ß", "SS", "ss", "Ss"}}, "ß"))
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT lower(NULL::char(4) COLLATE pg_unicode_fast) AS v FROM sc_review_typed", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
	})
}
