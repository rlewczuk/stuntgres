package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatParameterWidths(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, widthType := range []string{"text", "integer"} {
			t.Run(widthType, func(t *testing.T) {
				q := "SELECT format('|%*s|',$1::" + widthType + ",$2::text) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("quiet width Prepare", err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				if widthType == "integer" {
					d.Context = "unnamed portal parameter $1 = '...'"
				}
				for _, executor := range []fixture.Executor{c, held} {
					s := fixture.ErrorDiagnostic(q, d, "bad", "x")
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"|    x|"}}, "5", "x"))
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"|x|"}}, nil, "x"))
				}
			})
		}
	})
}

func TestReferenceFormatAnalysis(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE format_analysis(id int,v text)", 0))
		for _, q := range []string{
			"SELECT format(NULL,absent) FROM format_analysis",
			"SELECT format('%s',absent) FROM format_analysis WHERE false",
			"UPDATE format_analysis SET v=format(NULL,absent) WHERE false RETURNING v",
		} {
			t.Run(q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "absent")])) + 1)
				if strings.HasPrefix(q, "SELECT") {
					formatReferenceFailure(t, p, b, c, q, d, true)
					return
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		t.Run("limit-child", func(t *testing.T) {
			q := "SELECT id FROM format_analysis LIMIT format('%s',count(*))::integer"
			d := fixture.ControlsReferenceDiagnostic("42803", "aggregate functions are not allowed in LIMIT")
			d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "count")])) + 1)
			formatReferenceFailure(t, p, b, c, q, d, true)
		})
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT format('%s',id) AS v FROM format_analysis WHERE false", []string{"v"}, []string{"TEXT"}, [][]any{}))
	})
}
