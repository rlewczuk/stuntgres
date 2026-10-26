package enginetests_test

import (
	"context"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPreparedFoldStoredDefaultRulePurpose(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TYPE fold_default_operand AS (o regoperator)")
	enginetest.SessionSQL(t, s, "CREATE FUNCTION fold_default_provider(integer) RETURNS integer LANGUAGE SQL AS $$SELECT abs($1)$$")
	enginetest.SessionSQL(t, s, "CREATE FUNCTION fold_default_operator(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$")
	enginetest.SessionSQL(t, s, "CREATE OPERATOR public.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=fold_default_operator)")
	enginetest.SessionSQL(t, s, "CREATE FUNCTION fold_default_call(p regprocedure DEFAULT 'fold_default_provider(integer)'::regprocedure) RETURNS regprocedure LANGUAGE SQL AS $$SELECT $1$$")
	for _, input := range []string{
		"'fold_default_provider(integer)'::regprocedure",
		`'("#~#(integer,integer)")'::fold_default_operand`,
		`'{integer}'::regtype[]`,
		`'{"fold_default_provider(integer)"}'::regprocedure[]`,
	} {
		t.Run(input, func(t *testing.T) {
			_, description := preparedFoldStoredDescription(t, e, s, "SELECT "+input)
			if description.Regtypes == nil || len(description.Regtypes.Constants) != 1 {
				t.Fatal("missing typed identifier capture")
			}
			value := description.Regtypes.Constants[0].Value
			literal := plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
			defaults, err := stored.NewRoutineDefaults(context.Background(), e.CatalogRead(), nil, []expr.Expr{literal})
			if err != nil {
				t.Fatal(err)
			}
			// The datum itself supplies the expected address independently of
			// the direct analyzer's optional operand-owner manifest.
			address := ids.ObjectAddress{}
			switch input {
			case "'fold_default_provider(integer)'::regprocedure":
				oid, err := value.ProcedureOID()
				if err != nil {
					t.Fatal(err)
				}
				address = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(oid)}
			case `'{"fold_default_provider(integer)"}'::regprocedure[]`:
				oid, err := value.ArrayElement(0).ProcedureOID()
				if err != nil {
					t.Fatal(err)
				}
				address = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(oid)}
			case `'("#~#(integer,integer)")'::fold_default_operand`:
				oid, err := value.CompositeField(0).OperatorOID()
				if err != nil {
					t.Fatal(err)
				}
				address = ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: ids.OID(oid)}
			case `'{integer}'::regtype[]`:
				address = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
			}
			ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), address)
			requireOperand := func(dependencies []catalog.ExpressionDependency, consumer string) {
				t.Helper()
				for _, dependency := range dependencies {
					if dependency.Ref == ref {
						if dependency.Purpose != catalog.OperandDependency {
							t.Fatal(consumer+" promoted identifier operand", dependency)
						}
						return
					}
				}
				t.Fatal(consumer+" lost identifier operand", ref)
			}
			requireOperand(defaults.Dependencies(), "default collector")
			if input == "'fold_default_provider(integer)'::regprocedure" {
				_, call := preparedFoldStoredDescription(t, e, s, "SELECT fold_default_call()")
				requireOperand(call.Analysis.Dependencies, "selected default consumer")
			}
			if err := defaults.Validate(e.CatalogRead()); err != nil {
				t.Fatal("purpose-qualified default failed its ordinary manifest checks", err)
			}
			enginetest.SessionSQL(t, s, "CREATE VIEW fold_default_frozen AS SELECT "+input+" AS operand")
			frozen := stored.ViewOf(preparedFoldStoredRelation(t, e, "fold_default_frozen"))
			requireOperand(frozen.Dependencies(), "frozen rule presentation")
			presentation := stored.NewRuleAction(stored.RuleActionParts{Presentation: frozen})
			requireOperand(stored.NewRoutineProgram([]stored.RuleAction{presentation}).Dependencies(), "rule/program presentation")
			for _, executableFirst := range []bool{false, true} {
				// Use the real frozen presentation as the operand side of the
				// rule union, not a fabricated operand-purpose manifest.
				executable := catalog.ExpressionDependency{Ref: ref, Revision: e.CatalogRead().Get(address).Revision(), Purpose: catalog.DefinitionDependency}
				invocation := stored.NewRuleAction(stored.RuleActionParts{Binding: analyze.Binding{Dependencies: []catalog.ExpressionDependency{executable}}})
				actions := []stored.RuleAction{presentation, invocation}
				if executableFirst {
					actions[0], actions[1] = invocation, presentation
				}
				seen := false
				for _, dependency := range stored.NewRoutineProgram(actions).Dependencies() {
					if dependency.Ref == ref {
						seen = true
						if dependency.Purpose != catalog.DefinitionDependency {
							t.Fatal("rule/program union lost executable purpose", executableFirst)
						}
					}
				}
				if !seen {
					t.Fatal("rule/program union lost dependency", executableFirst)
				}
			}
			enginetest.SessionSQL(t, s, "DROP VIEW fold_default_frozen")
		})
	}
}

func preparedFoldStoredDescription(t *testing.T, e *core.Engine, s *session.Session, query string) (parser.Statement, core.Description) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	ast := enginetest.Statement(t, query)
	d, err := e.Describe(op, ast)
	if err != nil { t.Fatal(err) }
	return ast, d
}
