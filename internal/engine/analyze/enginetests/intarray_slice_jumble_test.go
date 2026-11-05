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

var _ expr.StatementJumbler = functions.IntarraySlice{}

type intarraySliceJumbleFields struct{ fields []any }

func (s *intarraySliceJumbleFields) JumbleText(v string)      { s.fields = append(s.fields, v) }
func (s *intarraySliceJumbleFields) JumbleUint64(v uint64)    { s.fields = append(s.fields, v) }
func (s *intarraySliceJumbleFields) JumbleBool(v bool)        { s.fields = append(s.fields, v) }
func (s *intarraySliceJumbleFields) JumbleConst(v sqltype.ID) { s.fields = append(s.fields, v) }

func TestIntarraySliceStatementJumble(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE EXTENSION intarray")
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
	for _, parameters := range []bool{false, true} {
		seen := make(map[int64]bool)
		for _, tc := range []struct {
			name, format string
			operation sqltype.IntarraySliceOperation
			operator int
		}{
			{"icount", "public.icount(%s)", sqltype.IntarrayIcount, 0},
			{"idx", "public.idx(%s,%s)", sqltype.IntarrayIdx, 0},
			{"subarray", "public.subarray(%s,%s)", sqltype.IntarraySubarray, 0},
			{"subarray_length", "public.subarray(%s,%s,%s)", sqltype.IntarraySubarrayLength, 0},
			{"intset", "public.intset(%s)", sqltype.IntarrayIntset, 0},
			{"prefix", "OPERATOR(public.#) %s", sqltype.IntarrayIcount, 37},
			{"binary", "%s OPERATOR(public.#) %s", sqltype.IntarrayIdx, 38},
		} {
			t.Run(fmt.Sprintf("%s/parameters_%t", tc.name, parameters), func(t *testing.T) {
				arguments := make([]any, tc.operation.Arity())
				for i := range arguments {
					typ := "integer"
					if i == 0 && tc.operation != sqltype.IntarrayIntset { typ = "integer[]" }
					value := "NULL"
					if parameters { value = fmt.Sprintf("$%d", i+1) }
					arguments[i] = value + "::" + typ
				}
				source := "SELECT " + fmt.Sprintf(tc.format, arguments...) + " AS v"
				ast, binding := pgssAnalyzedBinding(t, e, source)
				_, description := enginetest.PreparedDescription(t, e, session, source)
				op, err := session.Acquire(t.Context())
				if err != nil { t.Fatal(err) }
				prepared, err := func() (analyze.Binding, error) {
					defer op.Done()
					e.Mu.Lock()
					defer e.Mu.Unlock()
					auth, err := e.AuthorizationLocked(op)
					if err != nil { return analyze.Binding{}, err }
					return core.Describe(t.Context(), e.AnalysisHost(op), ast, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, &description, nil)
				}()
				if err != nil { t.Fatal(err) }
				var locations []int
				normalized := source
				if !parameters {
					for start := 0; ; {
						offset := strings.Index(source[start:], "NULL")
						if offset < 0 { break }
						location := start + offset
						locations = append(locations, location)
						start = location + len("NULL")
					}
					for i := range arguments { normalized = strings.Replace(normalized, "NULL", fmt.Sprintf("$%d", i+1), 1) }
				}
				var identity int64
				for _, current := range []*analyze.Binding{binding, &prepared} {
					project, ok := current.Query.Root.(*plan.ProjectNode)
					if !ok || len(project.Expressions) != 1 { t.Fatal("missing selected scalar projection") }
					root := project.Expressions[0]
					provider := root
					if tc.operator != 0 {
						selected, ok := root.(plan.SelectedOperatorExpr)
						if !ok || selected.RoutineReference != nil || selected.Ref.Address != installation.Members[tc.operator] { t.Fatal("lost authentic operator identity") }
						provider = selected.Provider
					}
					call, ok := provider.(functions.IntarraySlice)
					if !ok || call.Operation != tc.operation || call.Ref.Address != installation.Members[32+int(tc.operation)] || call.Validate() != nil { t.Fatal("lost selected slice overload") }
					var fields intarraySliceJumbleFields
					call.JumbleFields(&fields)
					if !reflect.DeepEqual(fields.fields, []any{"functions.IntarraySlice"}) { t.Fatal("node projected more than its owned static discriminator", fields.fields) }
					before := analyze.JumbleStatement(ast, current)
					highest := 0
					if parameters { highest = tc.operation.Arity() }
					if before.QueryID() == 0 || before.HighestParameter != highest || !reflect.DeepEqual(before.Locations, locations) || len(before.Lists) != 0 || len(before.UtilityLocations) != 0 { t.Fatal("lost lexical child/parameter provenance", source, before) }
					text, raw := parser.NormalizeStatementText(source, 0, before.HighestParameter, before.Locations, before.Lists, before.UtilityLocations, parser.Options{StandardConformingStrings: true})
					if text != normalized || raw != source { t.Fatal("normalization lost operand spans", text, normalized, raw) }
					rebuilt, err := call.Rebuild(call.Info, call.Children())
					if err != nil { t.Fatal(err) }
					if selected, ok := root.(plan.SelectedOperatorExpr); ok { selected.Provider = rebuilt; rebuilt = selected }
					projectCopy, queryCopy, bindingCopy := *project, *current.Query, *current
					projectCopy.Expressions = []expr.Expr{rebuilt}
					queryCopy.Root = &projectCopy
					bindingCopy.Query = &queryCopy
					after := analyze.JumbleStatement(ast, &bindingCopy)
					if after.QueryID() != before.QueryID() || !reflect.DeepEqual(after.Locations, before.Locations) || after.HighestParameter != before.HighestParameter { t.Fatal("reconstruction changed selected statement identity") }
					if identity != 0 && identity != before.QueryID() { t.Fatal("prepared reanalysis changed selected identity") }
					identity = before.QueryID()
				}
				if seen[identity] { t.Fatal("distinct overload/operator identities collapsed") }
				seen[identity] = true
			})
		}
	}
}
