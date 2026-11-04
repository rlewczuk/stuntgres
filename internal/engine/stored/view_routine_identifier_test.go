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

// Isolate frozen-query spelling from scalar Render; the public stored scenario
// separately exercises authenticated capture and actual Dump/Restore.
func TestPGLSNDiffFrozenCallRendering(t *testing.T) {
	read, _, _, _ := routineCaptureFixture(t)
	var usage sqltype.Usage
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.PgLSN {
			usage = typ.Usage()
			break
		}
	}
	if usage.Type == nil {
		t.Fatal("missing pg_lsn fixture type")
	}
	statements, err := parser.Parse("SELECT pg_wal_lsn_diff($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*parser.Select).Targets[0]
	view := &View{routineInputs: []sqltype.Usage{usage, usage}}
	for _, mode := range []struct {
		name   string
		policy Presentation
		want   string
	}{
		{"replay", Presentation{}, "pg_catalog.pg_wal_lsn_diff($1, $2)"},
		{"deparse", Presentation{Deparse: true}, "pg_wal_lsn_diff($1, $2)"},
		{"quoted", Presentation{Deparse: true, Output: sqltype.OutputEnvironment{QuoteAllIdentifiers: true}}, `"pg_wal_lsn_diff"($1, $2)`},
	} {
		t.Run(mode.name, func(t *testing.T) {
			renderer := NewQueryRenderer(view)
			renderer.Presentation = mode.policy
			got, err := renderer.ExpressionSQL(context.Background(), read, raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
			if err != nil || got != mode.want {
				t.Fatalf("frozen call = %q, %v; want %q", got, err, mode.want)
			}
		})
	}
}
