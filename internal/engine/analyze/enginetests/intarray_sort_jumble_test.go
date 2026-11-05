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

var _ expr.StatementJumbler = functions.IntarraySort{}

type intarraySortJumbleFields struct{ fields []any }

func (s *intarraySortJumbleFields) JumbleText(v string)      { s.fields = append(s.fields, v) }
func (s *intarraySortJumbleFields) JumbleUint64(v uint64)    { s.fields = append(s.fields, v) }
func (s *intarraySortJumbleFields) JumbleBool(v bool)        { s.fields = append(s.fields, v) }
func (s *intarraySortJumbleFields) JumbleConst(v sqltype.ID) { s.fields = append(s.fields, v) }

func TestIntarraySortStatementJumble(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE EXTENSION intarray")
	t.Run("literal_values", func(t *testing.T) {
		for _, pair := range [][2]string{
			{"SELECT public.sort('{3,1}'::integer[])", "SELECT public.sort('{9,2}'::integer[])"},
			{"SELECT public.sort('{3,1}'::integer[],'ASC'::text)", "SELECT public.sort('{9,2}'::integer[],'DESC'::text)"},
			{"SELECT public.uniq('{3,3}'::integer[])", "SELECT public.uniq('{9,2}'::integer[])"},
		} {
			firstAST, first := pgssAnalyzedBinding(t, e, pair[0])
			secondAST, second := pgssAnalyzedBinding(t, e, pair[1])
			a, b := analyze.JumbleStatement(firstAST, first), analyze.JumbleStatement(secondAST, second)
			if a.QueryID() == 0 || a.QueryID() != b.QueryID() {
				t.Fatal("literal values leaked into selected sort identity", pair, a.QueryID(), b.QueryID())
			}
		}
	})
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
	for _, parameters := range []bool{false, true} {
		seen := make(map[int64]bool)
		for _, tc := range []struct {
			name, format string
			operation    sqltype.IntarraySortOperation
		}{
			{"sort_unary", "public.sort(%s)", sqltype.IntarraySort},
			{"sort_binary", "public.sort(%s,%s)", sqltype.IntarraySortDirection},
			{"sort_asc", "public.sort_asc(%s)", sqltype.IntarraySortAsc},
			{"sort_desc", "public.sort_desc(%s)", sqltype.IntarraySortDesc},
			{"uniq", "public.uniq(%s)", sqltype.IntarrayUniq},
		} {
			t.Run(fmt.Sprintf("%s/parameters_%t", tc.name, parameters), func(t *testing.T) {
				arguments := make([]any, tc.operation.Arity())
				for i := range arguments {
					typ := "text"
					if i == 0 {
						typ = "integer[]"
					}
					value := "NULL"
					if parameters {
						value = fmt.Sprintf("$%d", i+1)
					}
					arguments[i] = value + "::" + typ
				}
				source := "SELECT " + fmt.Sprintf(tc.format, arguments...) + " AS v"
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
				var locations []int
				normalized := source
				if !parameters {
					for start := 0; ; {
						offset := strings.Index(source[start:], "NULL")
						if offset < 0 {
							break
						}
						location := start + offset
						locations = append(locations, location)
						start = location + len("NULL")
					}
					for i := range arguments {
						normalized = strings.Replace(normalized, "NULL", fmt.Sprintf("$%d", i+1), 1)
					}
				}
				var identity int64
				for _, current := range []*analyze.Binding{binding, &prepared} {
					project, ok := current.Query.Root.(*plan.ProjectNode)
					if !ok || len(project.Expressions) != 1 {
						t.Fatal("missing selected scalar projection")
					}
					root := project.Expressions[0]
					provider := root
					call, ok := provider.(functions.IntarraySort)
					if !ok || call.Operation != tc.operation || call.Ref.Address != installation.Members[39+int(tc.operation)] || call.Validate() != nil {
						t.Fatal("lost selected sort overload")
					}
					var fields intarraySortJumbleFields
					call.JumbleFields(&fields)
					if !reflect.DeepEqual(fields.fields, []any{"functions.IntarraySort"}) {
						t.Fatal("node projected more than its owned static discriminator", fields.fields)
					}
					before := analyze.JumbleStatement(ast, current)
					highest := 0
					if parameters {
						highest = tc.operation.Arity()
					}
					if before.QueryID() == 0 || before.HighestParameter != highest || !reflect.DeepEqual(before.Locations, locations) || len(before.Lists) != 0 || len(before.UtilityLocations) != 0 {
						t.Fatal("lost lexical child/parameter provenance", source, before)
					}
					text, raw := parser.NormalizeStatementText(source, 0, before.HighestParameter, before.Locations, before.Lists, before.UtilityLocations, parser.Options{StandardConformingStrings: true})
					if text != normalized || raw != source {
						t.Fatal("normalization lost operand spans", text, normalized, raw)
					}
					rebuilt, err := call.Rebuild(call.Info, call.Children())
					if err != nil {
						t.Fatal(err)
					}
					projectCopy, queryCopy, bindingCopy := *project, *current.Query, *current
					projectCopy.Expressions = []expr.Expr{rebuilt}
					queryCopy.Root = &projectCopy
					bindingCopy.Query = &queryCopy
					after := analyze.JumbleStatement(ast, &bindingCopy)
					if after.QueryID() != before.QueryID() || !reflect.DeepEqual(after.Locations, before.Locations) || after.HighestParameter != before.HighestParameter {
						t.Fatal("reconstruction changed selected statement identity")
					}
					if identity != 0 && identity != before.QueryID() {
						t.Fatal("prepared reanalysis changed selected identity")
					}
					identity = before.QueryID()
				}
				if seen[identity] {
					t.Fatal("distinct overload/operator identities collapsed")
				}
				seen[identity] = true
			})
		}
	}
}
