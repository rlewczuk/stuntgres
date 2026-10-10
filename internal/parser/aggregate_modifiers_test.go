package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAggregateModifiersGrammar(t *testing.T) {
	for _, name := range []string{"count", "bool_and", "bool_or", "every", "avg", "any_value", "min", "max", "string_agg"} {
		args := "v"
		if name == "string_agg" {
			args = "v,d"
		}
		for _, suffix := range []string{"(" + args + ") FILTER(WHERE FALSE)", "(DISTINCT " + args + " ORDER BY v)", "(ALL " + args + " ORDER BY v DESC NULLS LAST)", "(" + args + " ORDER BY v)"} {
			for _, prefix := range []string{"", "pg_catalog."} {
				q := "SELECT " + prefix + name + suffix + " FROM t"
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, statements, err)
				}
				call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
				if !ok || call.Name != name || len(call.Args) == 0 {
					t.Fatal(q, call)
				}
			}
		}
	}
	statements, err := Parse("SELECT /*é*/ count(*) FILTER(WHERE t.v>0) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	call := statements[0].(*Select).Targets[0].(AggregateExpr)
	if !call.Star || call.Filter == nil || call.Pos.Character != 14 {
		t.Fatal(call)
	}
	key := ColumnExpr{Name: ObjectName{Parts: []Identifier{{Text: "t"}, {Text: "v"}}}, QualificationPos: Position{Character: 7}}
	raw := AggregateExpr{Name: "count", Star: true, Filter: key}
	if token, _ := nonqueryQualification(raw, false); token == nil || token.pos.Character != 7 {
		t.Fatal("FILTER qualification not visited", token)
	}
	grouped := groupedQualification(raw).(AggregateExpr)
	if !grouped.Filter.(ColumnExpr).QualificationGrouped {
		t.Fatal("FILTER grouping not retained")
	}
}

func TestAggregateModifiersStarSyntax(t *testing.T) {
	for _, q := range []string{"SELECT count(DISTINCT *) FROM t", "SELECT count(ALL *) FROM t", "SELECT count(* ORDER BY v) FROM t"} {
		statements, err := Parse("INSERT INTO t VALUES(1);" + q)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
}

func TestAggregateModifiersFetchGrammar(t *testing.T) {
	for _, prefix := range []string{"", "pg_catalog."} {
		for _, tc := range []struct {
			call                   string
			distinct, star, filter bool
			args, keys             int
		}{
			{"count(DISTINCT v ORDER BY v DESC) FILTER(WHERE f)", true, false, true, 1, 1},
			{"count(ALL v ORDER BY v)", false, false, false, 1, 1},
			{"count(nextval('s') ORDER BY v) FILTER(WHERE f)", false, false, true, 1, 1},
			{"string_agg(v,',' ORDER BY v) FILTER(WHERE f)", false, false, true, 2, 1},
			{"string_agg(nextval('s'),',' ORDER BY v)", false, false, false, 2, 1},
			{"count(*) FILTER(WHERE f)", false, true, true, 0, 0},
		} {
			statements, err := Parse("SELECT v FROM t FETCH FIRST " + prefix + tc.call + " ROWS ONLY")
			if err != nil || len(statements) != 1 {
				t.Fatal(prefix, tc.call, statements, err)
			}
			call, ok := statements[0].(*Select).Bounds.Count.(AggregateExpr)
			if !ok || call.Distinct != tc.distinct || call.Star != tc.star || len(call.Args) != tc.args || len(call.Order) != tc.keys || call.Pos.Character != 29 {
				t.Fatal(prefix, tc.call, call)
			}
			if (call.Filter != nil) != tc.filter {
				t.Fatal("FETCH FILTER not retained", call)
			}
		}
	}
	for _, q := range []string{"SELECT v FROM t FETCH FIRST count(DISTINCT *) ROWS ONLY", "SELECT v FROM t FETCH FIRST count(ALL *) ROWS ONLY", "SELECT v FROM t FETCH FIRST count(* ORDER BY v) ROWS ONLY"} {
		statements, err := Parse(q)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{"SELECT filter FROM filter", "CREATE TABLE filter(filter INT)", "SELECT v FROM t FETCH FIRST 2 ROWS ONLY", "SELECT v FROM t FETCH FIRST (1+1) ROWS ONLY", "SELECT v FROM t FETCH FIRST ROW ONLY"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("FETCH scalar/keyword semantics changed", q, statements, err)
		}
	}
}

func TestAggregateModifiersFilterAttachment(t *testing.T) {
	for _, q := range []string{
		"SELECT count(count(*) FILTER(WHERE inner_flag)) FILTER(WHERE outer_flag) FROM t",
		"SELECT v FROM t FETCH FIRST count(count(*) FILTER(WHERE inner_flag)) FILTER(WHERE outer_flag) ROWS ONLY",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		selectStmt := statements[0].(*Select)
		expr := selectStmt.Targets[0]
		if selectStmt.Bounds != nil {
			expr = selectStmt.Bounds.Count
		}
		outer, ok := expr.(AggregateExpr)
		if !ok || outer.Name != "count" || len(outer.Args) != 1 {
			t.Fatal(q, expr)
		}
		inner, ok := outer.Args[0].(AggregateExpr)
		if !ok || !inner.Star || len(inner.Args) != 0 {
			t.Fatal(q, outer)
		}
		innerFilter, innerOK := inner.Filter.(ColumnExpr)
		outerFilter, outerOK := outer.Filter.(ColumnExpr)
		if !innerOK || !outerOK || innerFilter.Name.Parts[0].Text != "inner_flag" || outerFilter.Name.Parts[0].Text != "outer_flag" {
			t.Fatal("FILTER attached to the wrong call", q, inner, outer)
		}
	}
	for _, q := range []string{
		"SELECT count(*) FILTER(WHERE filter.flag)+1 FROM filter",
		"SELECT v FROM filter FETCH FIRST (count(*) FILTER(WHERE filter.flag)+1) ROWS ONLY",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		selectStmt := statements[0].(*Select)
		expr := selectStmt.Targets[0]
		if selectStmt.Bounds != nil {
			expr = selectStmt.Bounds.Count
		}
		composition, ok := expr.(OperatorExpr)
		if !ok || len(composition.Args) != 2 {
			t.Fatal("scalar composition lost", q, expr)
		}
		call, ok := composition.Args[0].(AggregateExpr)
		if !ok || !call.Star {
			t.Fatal(q, composition)
		}
		filter, ok := call.Filter.(ColumnExpr)
		if !ok || len(filter.Name.Parts) != 2 || filter.Name.Parts[0].Text != "filter" || filter.Name.Parts[1].Text != "flag" {
			t.Fatal("suffix or keyword qualification lost", q, call)
		}
	}
	statements, err := Parse("SELECT pg_catalog.count(filter) FILTER(WHERE filter>0) AS filter FROM filter")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	selectStmt := statements[0].(*Select)
	call, ok := selectStmt.Targets[0].(AggregateExpr)
	if !ok || call.Schema == nil || call.Schema.Text != "pg_catalog" || len(call.Args) != 1 || call.Filter == nil || selectStmt.Labels[0].Text != "filter" {
		t.Fatal("FILTER name contexts changed", selectStmt)
	}
	argument, ok := call.Args[0].(ColumnExpr)
	if !ok || argument.Name.Parts[0].Text != "filter" {
		t.Fatal("FILTER argument name lost", call)
	}
}
