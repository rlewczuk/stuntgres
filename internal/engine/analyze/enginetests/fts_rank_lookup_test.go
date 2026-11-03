package enginetests

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Count the target provider's requests while delegating every actual analysis
// and coercion to the real engine binder; this supplies no synthetic SQL result.
type ftsLookupTrackedBinder struct {
	functions.Binder
	calls     []parser.Expr
	arguments []functions.Argument
}

func (b *ftsLookupTrackedBinder) AnalyzeArgument(raw parser.Expr, request functions.AnalysisRequest) (functions.Argument, error) {
	b.calls = append(b.calls, raw)
	argument, err := b.Binder.AnalyzeArgument(raw, request)
	b.arguments = append(b.arguments, argument)
	return argument, err
}

func TestFTSRankCallableLookup(t *testing.T) {
	ftsRankHeadlineCallableLookup(t, []string{"ts_rank", "ts_rank_cd"}, "'cat'::tsvector", "tsvector")
}
func TestFTSHeadlineCallableLookup(t *testing.T) {
	ftsRankHeadlineCallableLookup(t, []string{"ts_headline"}, "'Cat'::text", "text")
}
func ftsRankHeadlineCallableLookup(t *testing.T, names []string, left, leftType string) {
	t.Helper()
	for _, name := range names {
		for _, array := range []string{"ARRAY['cat'::tsquery]", "ARRAY[ARRAY['cat'::tsquery]]"} {
			for _, variadic := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/variadic_%t", name, array, variadic), func(t *testing.T) {
					binder := enginetest.ExpressionBinder(t, t.Context(), engine.New(), sqltype.InputEnvironment{}, nil)
					tracked := &ftsLookupTrackedBinder{Binder: binder}
					decoration := ""
					if variadic {
						decoration = "VARIADIC "
					}
					raw := enginetest.Statement(t, "SELECT "+name+"("+left+","+decoration+array+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
					definition, ok := functions.Lookup(name)
					if !ok {
						t.Fatal(name)
					}
					_, err := definition.Bind(tracked, raw)
					var diagnostic *sqlstate.Error
					want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function " + name + "(" + leftType + ", tsquery[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}
					if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
						t.Fatal("lookup demanded unsupported array execution or lost exact resolution", err, want)
					}
					if len(tracked.calls) != len(raw.Args) || !reflect.DeepEqual(tracked.calls, raw.Args) {
						t.Fatal("written arguments reanalyzed or reordered", tracked.calls, raw.Args)
					}
					argument := tracked.arguments[1]
					companion := binder.Type(3645)
					if argument.Kind != functions.LookupOnlyArgument || argument.Expression != nil || argument.Usage.Type != companion || companion.Concrete() || !companion.IsArray() || companion.Element() != binder.Type(sqltype.TSQuery) || !reflect.DeepEqual(argument.Raw, raw.Args[1]) {
						t.Fatal("lookup fabricated a datum/node or lost checked shape/occurrence", argument)
					}
					// Selecting an executable consumer still crosses the existing hard
					// capability boundary; preserving lookup usage is not a fake datum.
					_, err = binder.CoerceArgument(argument, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: argument.Usage})
					if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported array element type" {
						t.Fatal("lookup-only usage acquired execution", err)
					}
					standalone := enginetest.Statement(t, "SELECT "+array).(*parser.Select).Targets[0]
					_, err = binder.Bind(standalone, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
					if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported array element type" {
						t.Fatal("standalone array execution changed", err)
					}
				})
			}
		}
	}
	for _, name := range names {
		t.Run(name+"/malformed_child_precedes_resolution", func(t *testing.T) {
			binder := enginetest.ExpressionBinder(t, t.Context(), engine.New(), sqltype.InputEnvironment{}, nil)
			tracked := &ftsLookupTrackedBinder{Binder: binder}
			raw := enginetest.Statement(t, "SELECT "+name+"("+left+",VARIADIC ARRAY['a &'::tsquery])").(*parser.Select).Targets[0].(parser.FunctionExpr)
			definition, _ := functions.Lookup(name)
			_, err := definition.Bind(tracked, raw)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != `no operand in tsquery: "a &"` || len(tracked.calls) != 2 {
				t.Fatal("lookup skipped/replayed malformed child", err, tracked.calls)
			}
		})
	}
}
