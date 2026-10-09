package parser

import (
	"errors"
	"reflect"
	"testing"
)

func TestQueryAggregateOrderSourceWalk(t *testing.T) {
	const aggregate = "SELECT string_agg((SELECT v FROM argument_source),(SELECT v FROM separator_source) ORDER BY (SELECT v FROM order_source FOR UPDATE),(SELECT v FROM order_source)) FROM aggregate_source"
	for _, sql := range []string{
		aggregate,
		"SELECT EXISTS(SELECT (" + aggregate + ") FROM wrapper_source)",
		"CREATE VIEW admission_v AS " + aggregate,
	} {
		t.Run(sql, func(t *testing.T) {
			statements, err := Parse(sql)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			want := []string{"aggregate_source", "argument_source", "separator_source", "order_source", "order_source"}
			wantModes := []string{"", "", "", "FOR UPDATE", ""}
			if _, ok := statements[0].(*Select); ok && sql != aggregate {
				want = append([]string{"wrapper_source"}, want...)
				wantModes = append([]string{""}, wantModes...)
			}
			var names, modes []string
			err = VisitQuerySources(statements[0], nil, func(table *TableReference, query *Select) error {
				names = append(names, table.Name.Parts[0].Text)
				mode := ""
				if len(query.Locking) != 0 {
					mode = query.Locking[0].Mode
				}
				modes = append(modes, mode)
				return nil
			})
			if err != nil || !reflect.DeepEqual(names, want) || !reflect.DeepEqual(modes, wantModes) {
				t.Fatal(names, modes, err)
			}
			// A second traversal retains every original occurrence, including
			// target-only sources inside the eligible EXISTS wrapper and view.
			var repeated []string
			err = VisitQuerySources(statements[0], nil, func(table *TableReference, query *Select) error {
				repeated = append(repeated, table.Name.Parts[0].Text)
				return nil
			})
			if err != nil || !reflect.DeepEqual(repeated, want) {
				t.Fatal(repeated, err)
			}
			stop := errors.New("stop source admission")
			for failAt := range want {
				var visited []string
				err = VisitQuerySources(statements[0], nil, func(table *TableReference, query *Select) error {
					visited = append(visited, table.Name.Parts[0].Text)
					if len(visited) == failAt+1 {
						return stop
					}
					return nil
				})
				if err != stop || !reflect.DeepEqual(visited, want[:failAt+1]) {
					t.Fatal(failAt, visited, err)
				}
			}
		})
	}
}
