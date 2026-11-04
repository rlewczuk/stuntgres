package enginetests

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type mcvItemsJumbleFields struct{ fields []any }

func (s *mcvItemsJumbleFields) JumbleText(v string)      { s.fields = append(s.fields, v) }
func (s *mcvItemsJumbleFields) JumbleUint64(v uint64)    { s.fields = append(s.fields, v) }
func (s *mcvItemsJumbleFields) JumbleBool(v bool)        { s.fields = append(s.fields, v) }
func (s *mcvItemsJumbleFields) JumbleConst(v sqltype.ID) { s.fields = append(s.fields, v) }

// Observe real analyzed providers and prepared reanalysis, never SQL rows or
// source evaluation. The visitor, not the provider's static tag, owns operands.
func TestStatisticsMCVItemsStatementJumble(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, layout := range []struct{ name, query string }{
		{"target", "SELECT %s"},
		{"from", "SELECT * FROM %s"},
	} {
		for _, operand := range []string{"NULL::pg_mcv_list", "$1::pg_mcv_list"} {
			t.Run(layout.name+"/"+operand, func(t *testing.T) {
				var identity int64
				for _, call := range []string{
					"pg_mcv_list_items(%s)",
					"pg_catalog.pg_mcv_list_items(%s)",
					"pg_mcv_list_items(mcv_list => %s)",
					"pg_catalog.pg_mcv_list_items(mcv_list => %s)",
				} {
					source := fmt.Sprintf(layout.query, fmt.Sprintf(call, operand))
					ast, binding := pgssAnalyzedBinding(t, e, source)
					_, description := enginetest.PreparedDescription(t, e, session, source)
					op, err := session.Acquire(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					prepared, err := func() (analyze.Binding, error) {
						defer op.Done()
						e.Mu.Lock()
						defer e.Mu.Unlock()
						auth, err := e.AuthorizationLocked(op)
						if err != nil {
							return analyze.Binding{}, err
						}
						return core.Describe(t.Context(), e.AnalysisHost(op), ast, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, &description, nil)
					}()
					if err != nil {
						t.Fatal(err)
					}
					for _, current := range []*analyze.Binding{binding, &prepared} {
						found := false
						var visit func(plan.Node)
						visit = func(node plan.Node) {
							if node == nil {
								return
							}
							plan.QueryExpressions(node, func(expression expr.Expr) {
								plan.WalkExpression(expression, func(child expr.Expr) {
									call, ok := child.(functions.StatisticsMCVItems)
									if !ok {
										return
									}
									found = true
									if call.CallRoutineReference() == nil || call.CallRoutineReference().RoutineOID() != 3427 || call.Arg.ResultUsage().Type.ID() != sqltype.PgMCVList || call.ValidateStoredProvider() != nil {
										t.Fatal("lost actual MCV provider or selected formal", call)
									}
									var fields mcvItemsJumbleFields
									call.JumbleFields(&fields)
									if !reflect.DeepEqual(fields.fields, []any{"functions.StatisticsMCVItems"}) {
										t.Fatal("provider projected more than its static discriminator", fields.fields)
									}
								})
							})
							left, right := plan.QueryInputs(node)
							visit(left)
							visit(right)
						}
						visit(current.Query.Root)
						if !found {
							t.Fatal("missing actual analyzed MCV source", source)
						}
						jumble := analyze.JumbleStatement(ast, current)
						highest := 1
						literal := strings.HasPrefix(operand, "NULL")
						if literal {
							highest = 0
						}
						// Target-set stages retain the written call in their template
						// as well as their executable slots; every visit must still
						// point to the same real operand, never a synthetic literal.
						for _, location := range jumble.Locations {
							if !literal || location != strings.Index(source, "NULL") {
								t.Fatal("lost original literal location", source, jumble.Locations)
							}
						}
						if jumble.QueryID() == 0 || jumble.HighestParameter != highest || literal != (len(jumble.Locations) != 0) || len(jumble.Lists) != 0 || len(jumble.UtilityLocations) != 0 {
							t.Fatal("lost lexical literal/parameter provenance", source, jumble)
						}
						if identity != 0 && identity != jumble.QueryID() {
							t.Fatal("qualification, labels or prepared reanalysis changed selected identity", source)
						}
						identity = jumble.QueryID()
						normalized, raw := parser.NormalizeStatementText(source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
						want := strings.Replace(source, "NULL", "$1", 1)
						if normalized != want || raw != source {
							t.Fatal("normalization lost written operand spans", normalized, want, raw, source)
						}
					}
				}
			})
		}
	}
}
