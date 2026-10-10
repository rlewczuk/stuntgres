package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestTransactionTimeGrammar(t *testing.T) {
	for _, name := range []string{"now", "transaction_timestamp", "statement_timestamp", "clock_timestamp", "timeofday"} {
		for _, form := range []string{name, strings.ToUpper(name), `"` + name + `"`, `pg_catalog.` + name, `"pg_catalog"."` + name + `"`} {
			statements, err := Parse("/*é*/ SELECT " + form + "()")
			if err != nil || len(statements) != 1 {
				t.Fatal(form, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || len(call.Args) != 0 || call.Name.Parts[len(call.Name.Parts)-1].Text != name || call.Pos.Character != 14 {
				t.Fatal(form, call)
			}
		}
		for _, item := range []struct {
			suffix                            string
			star, distinct, all, filter, over bool
			args, order                       int
		}{
			{suffix: "(*)", star: true},
			{suffix: "(DISTINCT 1)", distinct: true, args: 1},
			{suffix: "(ALL 1)", all: true, args: 1},
			{suffix: "(1 ORDER BY 1)", args: 1, order: 1},
			{suffix: "() FILTER (WHERE true)", filter: true},
			{suffix: "() OVER ()", over: true},
			{suffix: "() OVER w", over: true},
			{suffix: "(1) FILTER (WHERE true)", args: 1, filter: true},
		} {
			statements, err := Parse("SELECT " + name + item.suffix)
			if err != nil || len(statements) != 1 {
				t.Fatal(name, item.suffix, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || call.Star != item.star || call.Distinct != item.distinct || call.All != item.all || (call.Filter != nil) != item.filter || (call.Window != nil) != item.over || len(call.Args) != item.args || len(call.Order) != item.order {
				t.Fatal("lost modifier", name, item.suffix, call)
			}
		}
		for _, suffix := range []string{"(ORDER BY 1)", "(DISTINCT)", "(ALL)", "(1,)", "("} {
			_, err := Parse("SELECT " + name + suffix)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
				t.Fatal(name, suffix, err)
			}
		}
		for _, query := range []string{"CREATE TABLE " + name + "(" + name + " INTEGER)", "SELECT " + name + " FROM t", "SELECT lower(timeofday())", "SELECT clock_timestamp()::TEXT", "CREATE VIEW v AS SELECT " + name + "()"} {
			if _, err := Parse(query); err != nil {
				t.Fatal(query, err)
			}
		}
	}
}

func TestTransactionTimeWindowStructure(t *testing.T) {
	for _, name := range []string{"now", "transaction_timestamp", "statement_timestamp", "clock_timestamp", "timeofday"} {
		for _, item := range []struct {
			over             string
			window           string
			bare             bool
			partition, order int
		}{
			{over: "OVER ()"},
			{over: "OVER w", window: "w", bare: true},
			{over: "OVER (w)", window: "w"},
			{over: "OVER (PARTITION BY 1 ORDER BY 1)", partition: 1, order: 1},
		} {
			query := "SELECT " + name + "() FILTER (WHERE true) " + item.over
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, err)
			}
			call := statements[0].(*Select).Targets[0].(FunctionExpr)
			if call.Filter == nil || call.Window == nil {
				t.Fatal("lost ordered decorations", query, call)
			}
			spec := call.Window
			if (spec.Name != nil) != (item.window != "") || spec.Bare != item.bare || len(spec.Partition) != item.partition || len(spec.Order) != item.order {
				t.Fatal("lost shared window specification", query, spec)
			}
			if spec.Name != nil && spec.Name.Text != item.window {
				t.Fatal("lost window name", query, spec.Name)
			}
		}
		base := "SELECT " + name + "()"
		for _, item := range []struct{ prefix, token string }{
			{base + " FILTER (WHERE true) ", "FILTER"},
			{base + " FILTER (WHERE missing_tx_time_filter) ", "FILTER"},
			{base + " OVER () ", "FILTER"},
			{base + " OVER w ", "FILTER"},
			{base + " OVER () ", "OVER"},
			{base + " OVER w ", "OVER"},
			{base + " FILTER (WHERE true) OVER () ", "FILTER"},
			{base + " FILTER (WHERE true) OVER () ", "OVER"},
		} {
			suffix := "()"
			if item.token == "FILTER" {
				suffix = "(WHERE true)"
			}
			query := item.prefix + item.token + " " + suffix
			statements, err := Parse(query)
			var diagnostic *sqlstate.Error
			if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at or near \""+item.token+"\"" || diagnostic.Position != strconv.Itoa(len(item.prefix)+1) {
				t.Fatal(query, statements, err)
			}
		}
	}
}
