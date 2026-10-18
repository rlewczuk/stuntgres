package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCaseReviewSigma(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// A capital sigma is final only when the nearest preceding character that
		// is not Case_Ignorable is cased (REL_18_6; 18.0 also accepted an entirely
		// ignorable prefix such as U+0345 or U+0301). Observed on pinned 18.6.
		cases := []struct{ input, lower, initcap string }{
			{"\u0345Σ", "\u0345σ", "Ισ"},
			{"\u0301Σ", "\u0301σ", "\u0301Σ"},
			{"\u0301\u0345Σ", "\u0301\u0345σ", "\u0301Ισ"},
			{"\u0345Σ\u0345", "\u0345σ\u0345", "Ισ\u0345"},
			{"Σ", "σ", "Σ"},
			{"1Σ", "1σ", "1σ"},
			{"1\u0345Σ", "1\u0345σ", "1\u0345σ"},
			{"\u0345ΣΑ", "\u0345σα", "Ισα"},
			{"\u0301ΣΑ", "\u0301σα", "\u0301Σα"},
			{"ΑΣ", "ας", "Ας"},
		}
		q := "SELECT lower($1 COLLATE pg_unicode_fast) AS lo,initcap($1 COLLATE pg_unicode_fast) AS ic"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%U", []rune(tc.input)), func(t *testing.T) {
				want := [][]any{{tc.lower, tc.initcap}}
				literal := "SELECT lower('" + tc.input + "' COLLATE pg_unicode_fast) AS lo,initcap('" + tc.input + "' COLLATE pg_unicode_fast) AS ic"
				stringCaseBoth(t, p, b, c, fixture.Select(literal, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, want))
				stringCaseBoth(t, p, b, c, fixture.Select(q, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, want, tc.input))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, want, tc.input))
			})
		}
		for _, value := range []any{"", nil, "\u0345Σ"} {
			row := []any{"", ""}
			if value == nil {
				row = []any{nil, nil}
			} else if value != "" {
				row = []any{"\u0345σ", "Ισ"}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"lo", "ic"}, []string{"TEXT", "TEXT"}, [][]any{row}, value))
		}
	})
}

func TestReferenceStringCaseReviewTypedCollate(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sc_review_typed(id INT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sc_review_typed VALUES(1)", 1))
		for _, op := range []string{"lower", "upper", "casefold", "initcap"} {
			for _, arg := range []string{"1", "NULL::integer"} {
				q := "/*é🙂*/ SELECT " + op + "(" + arg + " COLLATE pg_unicode_fast) FROM sc_review_typed"
				d := fixture.ControlsReferenceDiagnostic("42804", "collations are not supported by type integer")
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "COLLATE")])) + 1)
				stringCaseFailure(t, p, b, c, q, d, true)
			}
			q := "/*é🙂*/ SELECT $1::integer AS n," + op + "($1 COLLATE pg_unicode_fast) AS v FROM sc_review_typed"
			d := fixture.ControlsReferenceDiagnostic("42804", "collations are not supported by type integer")
			d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "COLLATE")])) + 1)
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/established/prepared=%v", op, prepared), func(t *testing.T) {
					s := fixture.ErrorDiagnostic(q, d, 1)
					s.SelectRows, s.ErrorStage = true, "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		}
		stringCaseBoth(t, p, b, c, fixture.Select("SELECT lower($1::char(4) COLLATE pg_unicode_fast) AS lo,upper($1::char(4) COLLATE pg_unicode_fast) AS up,casefold($1::char(4) COLLATE pg_unicode_fast) AS fo,initcap($1::char(4) COLLATE pg_unicode_fast) AS ic FROM sc_review_typed", []string{"lo", "up", "fo", "ic"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ß", "SS", "ss", "Ss"}}, "ß"))
		stringCaseBoth(t, p, b, c, fixture.Select("SELECT lower(NULL::char(4) COLLATE pg_unicode_fast) AS v FROM sc_review_typed", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
	})
}
