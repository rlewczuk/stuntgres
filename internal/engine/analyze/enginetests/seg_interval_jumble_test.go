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

var _ expr.StatementJumbler = functions.SegInterval{}
var _ expr.StatementJumbler = plan.InstalledSegIntervalExpr{}

type segIntervalJumbleFields struct{ fields []any }

func (s *segIntervalJumbleFields) JumbleText(v string)      { s.fields = append(s.fields, v) }
func (s *segIntervalJumbleFields) JumbleUint64(v uint64)    { s.fields = append(s.fields, v) }
func (s *segIntervalJumbleFields) JumbleBool(v bool)        { s.fields = append(s.fields, v) }
func (s *segIntervalJumbleFields) JumbleConst(v sqltype.ID) { s.fields = append(s.fields, v) }

// Observe authentic analyzed carriers before folding, including strict NULL
// calls. Execution cannot stand in for the statement-identity projection.
func TestSegIntervalStatementJumble(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE EXTENSION seg")
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"})).Installation
	for _, arguments := range []struct {
		left, right string
		parameters  bool
	}{
		{"NULL::seg", "'1'::seg", false},
		{"'0 .. 1'::seg", "'1 .. 2'::seg", false},
		{"$1::seg", "$2::seg", true},
	} {
		for _, operator := range []bool{false, true} {
			seen := make(map[int64]bool)
			for i, name := range []string{"seg_left", "seg_right", "seg_over_left", "seg_over_right", "seg_same", "seg_overlap", "seg_contains", "seg_contained"} {
				t.Run(fmt.Sprintf("%s/operator_%t/%s", name, operator, arguments.left), func(t *testing.T) {
					source := "SELECT public." + name + "(" + arguments.left + "," + arguments.right + ")"
					if operator {
						spelling := []string{"<<", ">>", "&<", "&>", "=", "&&", "@>", "<@"}[i]
						source = "SELECT " + arguments.left + " OPERATOR(public." + spelling + ") " + arguments.right
					}
					ast, binding := pgssAnalyzedBinding(t, e, source)
					project, ok := binding.Query.Root.(*plan.ProjectNode)
					if !ok || len(project.Expressions) != 1 {
						t.Fatal("missing actual selected scalar projection")
					}
					root := project.Expressions[0]
					provider := root
					if operator {
						selected, ok := root.(plan.SelectedOperatorExpr)
						if !ok || selected.Ref.Address != installation.Members[11+i] {
							t.Fatal("lost selected operator identity")
						}
						provider = selected.Provider
					}
					call, ok := provider.(plan.InstalledSegIntervalExpr)
					if !ok || call.Procedure == nil || call.CallRoutineReference() != call.Procedure || call.Ref.Address != installation.Members[3+i] || call.Ref.Address != call.Procedure.Address() || call.Plan.Operation().Routine() != name {
						t.Fatal("lost authentic installed SEG carrier")
					}
					for _, projection := range []struct {
						node expr.StatementJumbler
						tag  string
					}{
						{call.SegInterval, "functions.SegInterval"},
						{call, "plan.InstalledSegIntervalExpr"},
					} {
						var fields segIntervalJumbleFields
						projection.node.JumbleFields(&fields)
						if !reflect.DeepEqual(fields.fields, []any{projection.tag}) {
							t.Fatal("concrete carrier did not own exactly its static tag", fields.fields)
						}
					}
					jumble := func(node expr.Expr) analyze.StatementJumble {
						if selected, ok := root.(plan.SelectedOperatorExpr); ok {
							selected.Provider = node
							node = selected
						}
						projectCopy, queryCopy, bindingCopy := *project, *binding.Query, *binding
						projectCopy.Expressions = []expr.Expr{node}
						queryCopy.Root = &projectCopy
						bindingCopy.Query = &queryCopy
						return analyze.JumbleStatement(ast, &bindingCopy)
					}
					before := jumble(call)
					if before.QueryID() == 0 || seen[before.QueryID()] {
						t.Fatal("selected SEG routine/operator identities collapsed")
					}
					seen[before.QueryID()] = true
					highest := 0
					var locations []int
					expected := source
					if arguments.parameters {
						highest = 2
					} else {
						locations = []int{strings.Index(source, arguments.left), strings.LastIndex(source, arguments.right)}
						expected = strings.Replace(expected, strings.TrimSuffix(arguments.left, "::seg"), "$1", 1)
						expected = strings.Replace(expected, strings.TrimSuffix(arguments.right, "::seg"), "$2", 1)
					}
					if before.HighestParameter != highest || !reflect.DeepEqual(before.Locations, locations) || len(before.Lists) != 0 || len(before.UtilityLocations) != 0 {
						t.Fatal("lost lexical constants or parameter identity", before)
					}
					normalized, raw := parser.NormalizeStatementText(source, 0, before.HighestParameter, before.Locations, before.Lists, before.UtilityLocations, parser.Options{StandardConformingStrings: true})
					if raw != source || normalized != expected {
						t.Fatal("lost lexical normalization", normalized, expected, raw)
					}
					if !arguments.parameters && arguments.left != "NULL::seg" {
						other := strings.Replace(source, "'0 .. 1'", "'2 .. 3'", 1)
						other = strings.Replace(other, "'1 .. 2'", "'4 .. 5'", 1)
						if before.QueryID() != pgssAnalyzedJumble(t, e, other).QueryID() {
							t.Fatal("literal values leaked into selected statement identity")
						}
					}
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
					preparedRoot := prepared.Query.Root.(*plan.ProjectNode).Expressions[0]
					if selected, ok := preparedRoot.(plan.SelectedOperatorExpr); ok {
						if selected.Ref != root.(plan.SelectedOperatorExpr).Ref {
							t.Fatal("prepared reanalysis changed selected operator authority")
						}
						preparedRoot = selected.Provider
					}
					preparedCall := preparedRoot.(plan.InstalledSegIntervalExpr)
					if preparedCall.Procedure != call.Procedure || preparedCall.Ref != call.Ref || !reflect.DeepEqual(before, analyze.JumbleStatement(ast, &prepared)) {
						t.Fatal("prepared reanalysis changed captured authority or statement identity")
					}
					for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
						rebuilt, err := reconstruct(expr.InfoOf(call), call.Children())
						if err != nil {
							t.Fatal(err)
						}
						copy := rebuilt.(plan.InstalledSegIntervalExpr)
						if copy.Procedure != call.Procedure || copy.Ref != call.Ref || !reflect.DeepEqual(before, jumble(copy)) {
							t.Fatal("reconstruction changed captured authority or statement identity")
						}
					}
					if before.QueryID() == jumble(call.SegInterval).QueryID() {
						t.Fatal("installed wrapper inherited its kernel discriminator")
					}
					withoutRoutine := call
					withoutRoutine.RoutineReference = nil
					if before.QueryID() == jumble(withoutRoutine).QueryID() {
						t.Fatal("static tag replaced selected routine identity")
					}
					if arguments.parameters {
						swapped := call
						swapped.Args = [2]expr.Expr{call.Args[1], call.Args[0]}
						if before.QueryID() == jumble(swapped).QueryID() {
							t.Fatal("ordered lexical parameter identity was omitted")
						}
					}
				})
			}
		}
	}
}
