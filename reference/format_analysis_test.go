package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceFormatParameterWidths(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, widthType := range []string{"text", "integer"} {
			t.Run(widthType, func(t *testing.T) {
				q := "SELECT format('|%*s|',$1::" + widthType + ",$2::text) AS v"
				stmt, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal("quiet width Prepare", err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				d := controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				if widthType == "integer" {
					d.Context = "unnamed portal parameter $1 = '...'"
				}
				for _, executor := range []referenceExecutor{c, held} {
					s := referenceErrorDiagnostic(q, d, "bad", "x")
					s.selectRows, s.errorStage = true, "execute"
					referenceRunStep(t, p.ctx, executor, b.recorder, s)
					referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"|    x|"}}, "5", "x"))
					referenceRunStep(t, p.ctx, executor, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"|x|"}}, nil, "x"))
				}
			})
		}
	})
}

func TestReferenceFormatAnalysis(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format_analysis(id int,v text)", 0))
		for _, q := range []string{
			"SELECT format(NULL,absent) FROM format_analysis",
			"SELECT format('%s',absent) FROM format_analysis WHERE false",
			"UPDATE format_analysis SET v=format(NULL,absent) WHERE false RETURNING v",
		} {
			t.Run(q, func(t *testing.T) {
				d := controlsReferenceDiagnostic("42703", `column "absent" does not exist`)
				d.Position = referencePosition(len([]rune(q[:strings.Index(q, "absent")])) + 1)
				if strings.HasPrefix(q, "SELECT") {
					formatReferenceFailure(t, p, b, c, q, d, true)
					return
				}
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic(q, d)
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				}
			})
		}
		t.Run("limit-child", func(t *testing.T) {
			q := "SELECT id FROM format_analysis LIMIT format('%s',count(*))::integer"
			d := controlsReferenceDiagnostic("42803", "aggregate functions are not allowed in LIMIT")
			d.Position = referencePosition(len([]rune(q[:strings.Index(q, "count")])) + 1)
			formatReferenceFailure(t, p, b, c, q, d, true)
		})
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT format('%s',id) AS v FROM format_analysis WHERE false", []string{"v"}, []string{"TEXT"}, [][]any{}))
	})
}
