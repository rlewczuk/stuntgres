package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestMathGcdLcmBinding(t *testing.T) {
	for _, name := range []string{"gcd", "lcm"} {
		for _, tc := range []struct {
			args       string
			result     sqltype.ID
			parameters []sqltype.ID
		}{
			{"12,18", sqltype.Int4, nil}, {"12::bigint,18::bigint", sqltype.Int8, nil}, {"1.20,0.1800", sqltype.Numeric, nil},
			{"12::integer,18::bigint", sqltype.Int8, nil}, {"12::bigint,18::integer", sqltype.Int8, nil},
			{"12::integer,18.00", sqltype.Numeric, nil}, {"12.00,18::bigint", sqltype.Numeric, nil},
			{"12::smallint,18::integer", sqltype.Int4, nil}, {"12::smallint,18::bigint", sqltype.Int8, nil}, {"12::smallint,18::numeric", sqltype.Numeric, nil},
			{"NULL,18", sqltype.Int4, nil}, {"NULL,18::bigint", sqltype.Int8, nil}, {"NULL,18.00", sqltype.Numeric, nil},
			{"$1,18", sqltype.Int4, []sqltype.ID{sqltype.Int4}}, {"$1,18::bigint", sqltype.Int8, []sqltype.ID{sqltype.Int8}}, {"$1,18.00", sqltype.Numeric, []sqltype.ID{sqltype.Numeric}},
			{"$1::integer,$2::integer", sqltype.Int4, []sqltype.ID{sqltype.Int4, sqltype.Int4}},
			{"$1::bigint,$2::bigint", sqltype.Int8, []sqltype.ID{sqltype.Int8, sqltype.Int8}},
			{"$1::numeric,$2::numeric", sqltype.Numeric, []sqltype.ID{sqltype.Numeric, sqltype.Numeric}},
		} {
			t.Run(name+"("+tc.args+")", func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
				raw := statement(t, "SELECT "+name+"("+tc.args+")").(*parser.Select).Targets[0]
				node, err := b.bind(raw, expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				mathNode, ok := node.(functions.Math)
				if !ok || mathNode.Plan.Name() != name || len(mathNode.Args) != 2 {
					t.Fatal("shared math plan", node)
				}
				if node.ResultUsage().Type.ID() != tc.result || node.ResultUsage().Typmod != -1 {
					t.Fatal("result usage", node.ResultUsage())
				}
				for _, arg := range mathNode.Args {
					if arg.ResultUsage() != node.ResultUsage() {
						t.Fatal("coherent signature owner", arg.ResultUsage(), node.ResultUsage())
					}
				}
				properties := node.Properties()
				if properties.Volatility != expr.Immutable || properties.ParameterDependent != (len(tc.parameters) != 0) || properties.RowDependent || properties.StageDependent {
					t.Fatal("merged immutable properties", properties)
				}
				for i, id := range tc.parameters {
					if b.parameters[i+1].Type == nil || b.parameters[i+1].Type.ID() != id {
						t.Fatal("contextual parameter", b.parameters)
					}
				}
			})
		}
		for _, tc := range []struct{ args, code, signature string }{
			{"NULL,NULL", "42725", "unknown, unknown"}, {"'12','18'", "42725", "unknown, unknown"}, {"$1,$2", "42725", "unknown, unknown"},
			{"1::smallint,2::smallint", "42725", "smallint, smallint"}, {"1::smallint,NULL", "42725", "smallint, unknown"},
			{"1.0::float8,2.0::float8", "42883", "double precision, double precision"}, {"1::real,2::real", "42883", "real, real"}, {"1::text,2::text", "42883", "text, text"}, {"true,false", "42883", "boolean, boolean"},
			{"", "42883", ""}, {"1", "42883", "integer"}, {"1,2,3", "42883", "integer, integer, integer"},
		} {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			raw := statement(t, "SELECT "+name+"("+tc.args+")").(*parser.Select).Targets[0]
			_, err := b.bind(raw, expressionPolicy{context: projectionContext})
			message := "function " + name + "(" + tc.signature + ") does not exist"
			hint := "No function matches the given name and argument types. You might need to add explicit type casts."
			if tc.code == "42725" {
				message = "function " + name + "(" + tc.signature + ") is not unique"
				hint = "Could not choose a best candidate function. You might need to add explicit type casts."
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: message, Hint: hint, Position: "8"}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatalf("%s(%s): %#v want %#v", name, tc.args, got, want)
			}
		}
	}
}

func TestMathGcdLcmBindingExecutionPhase(t *testing.T) {
	for _, q := range []string{
		"SELECT gcd('-2147483648'::integer,0)",
		"SELECT lcm(2147483647,2)",
		"SELECT gcd('-9223372036854775808'::bigint,0::bigint)",
		"SELECT lcm(9223372036854775807,2::bigint)",
		"SELECT gcd(NULL::integer,lcm(2147483647,2))",
		"SELECT lcm(NULL::integer,lcm(2147483647,2))",
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		node, err := b.bind(statement(t, q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal("binding invoked provider", q, err)
		}
		c := &EvalContext{Context: context.Background()}
		_, _, err = preflightCoercions(c, node)
		message := "integer out of range"
		if strings.Contains(q, "bigint") {
			message = "bigint out of range"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: message}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatal("execution-only child preflight", q, got, err)
		}
	}
}

func TestMathGcdLcmBindingQuotedNames(t *testing.T) {
	for _, name := range []string{"GCD", "LCM"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		raw := statement(t, `SELECT "`+name+`"(12,18)`).(*parser.Select).Targets[0]
		_, err := b.bind(raw, expressionPolicy{context: projectionContext})
		want := sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883",
			Message:  "function " + name + "(integer, integer) does not exist",
			Hint:     "No function matches the given name and argument types. You might need to add explicit type casts.",
			Position: "8",
		}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatal("quoted spelling changed resolution", name, got, err)
		}
	}
}
