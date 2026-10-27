package core_test

import (
	"bytes"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"testing"
)

func TestStatisticsExpressionBindingDumpDependencies(t *testing.T) {
	source := engine.New()
	session := enginetest.StartSession(t, source, nil)
	for _, sql := range []string{
		"CREATE TABLE expression_dump_input(a INT,b INT)",
		"CREATE FUNCTION expression_dump_fn(a INT) RETURNS INT LANGUAGE SQL RETURN a+1",
		"CREATE STATISTICS expression_dump_builtin ON (a+1) FROM expression_dump_input",
		"CREATE STATISTICS expression_dump_callable ON (expression_dump_fn(a)) FROM expression_dump_input",
		"ALTER TABLE expression_dump_input RENAME COLUMN a TO renamed",
	} {
		enginetest.SessionSQL(t, session, sql)
	}
	var captured bytes.Buffer
	if err := engine.Dump(t.Context(), source, &captured); err != nil {
		t.Fatal(err)
	}
	sql := captured.String()
	table, function, statistics := strings.Index(sql, "CREATE TABLE"), strings.Index(sql, "CREATE FUNCTION"), strings.Index(sql, "CREATE STATISTICS")
	if table < 0 || function < 0 || statistics < table || statistics < function {
		t.Fatal("statistics missing declaration predecessors", sql)
	}
	destination := engine.New()
	if err := engine.Restore(t.Context(), destination, bytes.NewReader(captured.Bytes())); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, raw := range destination.CatalogRead().Descriptors() {
		d, ok := raw.(*catalog.Statistics)
		if !ok {
			continue
		}
		count++
		if d.Expressions.Len() != 1 || d.Selection.Kinds() != "e" {
			t.Fatal("restored expression declaration changed", d)
		}
		if err := (catalog.StatisticsProvider{}).Validate(destination.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
	}
	if count != 2 {
		t.Fatal("missing restored expression definitions", count)
	}
}
