package core_test

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestStatisticsExpressionBindingRenameRetention(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{
		"CREATE TABLE expression_rename_input(a INT,b INT)",
		"INSERT INTO expression_rename_input VALUES(1,10),(1,10),(2,20)",
		"CREATE STATISTICS expression_rename_stats ON (a+1) FROM expression_rename_input",
		"ANALYZE expression_rename_input",
	} {
		enginetest.SessionSQL(t, session, sql)
	}
	var original *catalog.Statistics
	for _, raw := range e.CatalogRead().Descriptors() {
		if d, ok := raw.(*catalog.Statistics); ok && d.Name() == "expression_rename_stats" {
			original = d
		}
	}
	if original == nil {
		t.Fatal("missing retained definition")
	}
	facts := analyzeFacts(e, original.Relation.Address)
	if facts == nil || len(facts.Extended) != 1 {
		t.Fatal("missing collected baseline")
	}
	for _, sql := range []string{
		"CREATE TABLE expression_unrelated(a INT)",
		"ALTER TABLE expression_rename_input RENAME COLUMN a TO renamed",
		"ALTER STATISTICS expression_rename_stats SET STATISTICS 0",
	} {
		enginetest.SessionSQL(t, session, sql)
		current, ok := e.CatalogRead().Statistics(original.Address())
		if !ok || current.Expressions != original.Expressions {
			t.Fatal("metadata-only publication replaced binding", sql)
		}
		retained := analyzeFacts(e, current.Relation.Address)
		if retained == nil || len(retained.Extended) != 1 {
			t.Fatal("metadata-only publication lost expression facts", sql)
		}
		for key, fact := range facts.Extended {
			if retained.Extended[key] != fact {
				t.Fatal("metadata-only publication replaced fact backing", sql)
			}
		}
	}
	current := e.CatalogRead()
	if original.Relation.Address.ClassID != ids.RelationClassID || current.MustRelation(original.Relation.Address).Columns[0].Name != "renamed" {
		t.Fatal("rename did not publish current names")
	}
}
