package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRuleRoutineIdentifiers(t *testing.T) {
	read, _, _, _ := routineCaptureFixture(t)
	for _, test := range []struct {
		query, plain, quoted, replay string
	}{
		{"SELECT row_number() OVER w WINDOW w AS ()", "row_number() OVER w", `"row_number"() OVER "w"`, `row_number() OVER "w"`},
		{"SELECT rank() OVER ()", "rank() OVER ()", `"rank"() OVER ()`, "rank() OVER ()"},
		{"SELECT count(*)", "count(*)", `"count"(*)`, "count(*)"},
	} {
		t.Run(test.query, func(t *testing.T) {
			statements, err := parser.Parse(test.query)
			if err != nil {
				t.Fatal(err)
			}
			raw := statements[0].(*parser.Select).Targets[0]
			view := &View{}
			for _, mode := range []struct {
				name   string
				policy Presentation
				want   string
			}{
				{"plain", Presentation{Deparse: true, Rule: true}, test.plain},
				{"quoted", Presentation{Deparse: true, Rule: true, Output: sqltype.OutputEnvironment{QuoteAllIdentifiers: true}}, test.quoted},
				{"replay", Presentation{Output: sqltype.OutputEnvironment{QuoteAllIdentifiers: true}}, test.replay},
			} {
				t.Run(mode.name, func(t *testing.T) {
					renderer := NewQueryRenderer(view)
					renderer.Presentation = mode.policy
					got, err := renderer.ExpressionSQL(context.Background(), read, raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
					if err != nil || got != mode.want {
						t.Fatalf("expression = %q, %v; want %q", got, err, mode.want)
					}
				})
			}
		})
	}
}
