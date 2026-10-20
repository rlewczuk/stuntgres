package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONBSetViewReplayCastBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		query, want string
		domainError bool
	}{
		{query: `SELECT jsonb_set((('{}'::jsonb)::jsonb)::jsonb,'{a}','2')`, want: `{"a": 2}`},
		{query: `SELECT jsonb_set('{}',ARRAY[('ab'::varchar(1))::text],((('12.340'::numeric(4,1))::float8)::text)::jsonb)`, want: `{"a": 12.3}`},
		{query: `SELECT jsonb_set('{}','{a}',('{"z":1.00}'::json)::jsonb)`, want: `{"a": {"z": 1.00}}`},
		{query: `SELECT jsonb_set_lax((('{"a":1}'::jsonb)::jbs_unit_doc)::jsonb,'{a}',NULL,true,'return_target')`, want: `{"a": 1}`},
		{query: `SELECT jsonb_set_lax((('[]'::jsonb)::jbs_unit_doc)::jsonb,'{a}',NULL,true,'return_target')`, domainError: true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			engine := New()
			op, done := roleOperation(t, engine, "postgres")
			defer done()
			domainUnitPublish(t, engine, domainUnitStage(t, engine, op, `CREATE DOMAIN jbs_unit_doc AS jsonb CONSTRAINT jbs_unit_object CHECK (jsonb_typeof(VALUE) = 'object')`))
			query := tc.query
			var previous string
			for round := range 3 {
				b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
				raw := statement(t, query).(*parser.Select).Targets[0]
				node, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(round, err)
				}
				eval := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: engine.CatalogRead()}}
				eval.Input.DomainChecks = &eval
				value, err := node.Eval(&eval)
				if tc.domainError {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Schema != "public" || diagnostic.DataType != "jbs_unit_doc" || diagnostic.Constraint != "jbs_unit_object" || value != (sqltype.Value{}) {
						t.Fatal("replay discarded meaningful domain check", round, value, err)
					}
				} else {
					if err != nil || value.Type().ID() != sqltype.JSONB {
						t.Fatal(round, value, err)
					}
					text, err := value.Type().Output(ctx, value)
					if err != nil || text != tc.want {
						t.Fatal("cast replay changed value", round, text, err)
					}
				}
				if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
					t.Fatal(round, err)
				}
				renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.Capture.Literals, castUsages: b.Capture.CastUsages}, frame: b.FrameID()}
				emitted, err := renderer.expressionSQL(ctx, engine.CatalogRead(), raw, viewLiteralPath{Clause: analyze.ViewTargetLiteral})
				if err != nil {
					t.Fatal(round, err)
				}
				if round != 0 && emitted != previous {
					t.Fatalf("cast boundary replay drifted in round %d\nbefore:\n%s\nafter:\n%s", round, previous, emitted)
				}
				previous, query = emitted, "SELECT "+emitted
			}
		})
	}
}
