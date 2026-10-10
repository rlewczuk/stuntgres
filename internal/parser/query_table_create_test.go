package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestQueryTableCreateAST(t *testing.T) {
	for _, tc := range []struct {
		sql     string
		form    QueryTableForm
		noData  bool
		columns []string
	}{
		{`CREATE TABLE "schema"."copy" (a,b) AS SELECT 1,2`, CreateTableAs, false, []string{"a", "b"}},
		{`CREATE TABLE IF NOT EXISTS copy AS VALUES(1),(2) WITH DATA`, CreateTableAs, false, nil},
		{`CREATE TABLE copy AS (SELECT 1 AS x) WITH NO DATA`, CreateTableAs, true, nil},
		{`SELECT 1 AS x INTO TABLE copy`, SelectInto, false, nil},
		{`SELECT INTO copy FROM source`, SelectInto, false, nil},
		{`SELECT 1 INTO copy UNION ALL SELECT 2`, SelectInto, false, nil},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			statements, err := Parse(tc.sql)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			creation, ok := statements[0].(*CreateQueryTable)
			if !ok || creation.Form != tc.form || creation.NoData != tc.noData || creation.Name.Parts[len(creation.Name.Parts)-1].Text != "copy" || creation.Query == nil {
				t.Fatal(statements[0])
			}
			if LeftmostSelect(creation.Query).Into != nil {
				t.Fatal("destination remained executable query metadata")
			}
			var names []string
			for _, name := range creation.Columns {
				names = append(names, name.Text)
			}
			if !reflect.DeepEqual(names, tc.columns) {
				t.Fatal(names, tc.columns)
			}
			if creation.Name.Parts[len(creation.Name.Parts)-1].Pos.Character <= 0 || creation.Pos.Character != 1 {
				t.Fatal(creation.Name, creation.Pos)
			}
			if strings.Contains(tc.sql, "IF NOT EXISTS") != creation.IfNotExists {
				t.Fatal(creation.IfNotExists)
			}
		})
	}
}

func TestQueryTableCreateSourceWalk(t *testing.T) {
	statements, err := Parse("CREATE TABLE copy AS SELECT (SELECT x FROM nested_src),(SELECT x FROM mark_src FOR UPDATE),(SELECT x FROM mark_src) FROM outer_src,(SELECT x FROM derived_src) d")
	if err != nil {
		t.Fatal(err)
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
	if err != nil || !reflect.DeepEqual(names, []string{"outer_src", "derived_src", "nested_src", "mark_src", "mark_src"}) || !reflect.DeepEqual(modes, []string{"", "", "", "FOR UPDATE", ""}) {
		t.Fatal(names, modes, err)
	}
	stop := errors.New("stop acquired occurrence")
	calls := 0
	err = VisitQuerySources(statements[0], nil, func(*TableReference, *Select) error {
		calls++
		if calls == 3 {
			return stop
		}
		return nil
	})
	if err != stop || calls != 3 {
		t.Fatal(calls, err)
	}
}

func TestQueryTableCreateNestedInto(t *testing.T) {
	for _, tc := range []struct{ sql, message string }{
		{"CREATE TABLE copy AS SELECT 1 INTO inner_copy", "SELECT ... INTO is not allowed here"},
		{"CREATE TABLE copy AS SELECT 1 INTO inner_copy UNION ALL SELECT 2", "SELECT ... INTO is not allowed here"},
		{"SELECT * FROM (SELECT 1 INTO inner_copy) d", "SELECT ... INTO is not allowed here"},
		{"SELECT (SELECT 1 INTO inner_copy)", "SELECT ... INTO is not allowed here"},
		{"INSERT INTO copy SELECT 1 INTO inner_copy", "SELECT ... INTO is not allowed here"},
		{"SELECT 1 UNION ALL SELECT 2 INTO inner_copy", "INTO is only allowed on first SELECT of UNION/INTERSECT/EXCEPT"},
	} {
		statements, err := Parse(tc.sql)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != tc.message || diagnostic.Position != strconv.Itoa(strings.Index(tc.sql, "inner_copy")+1) {
			t.Fatal(tc.sql, statements, err)
		}
	}
	statements, err := Parse("CREATE VIEW copy AS SELECT 1 INTO inner_copy UNION ALL SELECT 2")
	if err != nil || LeftmostSelect(statements[0].(*CreateView).Query).Into == nil {
		t.Fatal(statements, err)
	}
}

func TestQueryTableCreateDeferredOptions(t *testing.T) {
	for _, option := range []string{"ON COMMIT PRESERVE ROWS", "ON COMMIT DELETE ROWS", "ON COMMIT DROP", "ON COMMIT DROP TABLESPACE pg_default"} {
		query := "CREATE TABLE copy " + option + " AS SELECT 1"
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "ON COMMIT is unsupported" || diagnostic.Position != "19" {
			t.Fatal(query, statements, err)
		}
	}
	for _, query := range []string{"CREATE TABLE copy AS SELECT 1 WITH NO", "CREATE TABLE copy AS SELECT 1 WITH DATA trailing", "CREATE TABLE copy ON COMMIT PRESERVE frog AS SELECT 1"} {
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(query, statements, err)
		}
	}
}
