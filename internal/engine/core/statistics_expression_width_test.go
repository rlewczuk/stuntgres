package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestStatisticsExpressionSnapshotAllNullWidth(t *testing.T) {
	source := engine.New()
	s := enginetest.StartSession(t, source, nil)
	for _, statement := range []string{
		"CREATE TABLE expr_width(a INT,t TEXT)",
		"INSERT INTO expr_width VALUES(NULL,NULL),(NULL,NULL),(NULL,NULL)",
		"CREATE STATISTICS expr_width_fixed ON (a+1) FROM expr_width",
		"CREATE STATISTICS expr_width_variable ON (coalesce(t,t)) FROM expr_width",
		"ANALYZE expr_width",
	} {
		enginetest.SessionSQL(t, s, statement)
	}
	ref := enginetest.CatalogRef(t, source, source.CatalogRead(), "expr_width")
	fixed := analyzeFacts(source, ref.Address).Extended[ndistinctKey(t, source, "expr_width_fixed")]
	variable := analyzeFacts(source, ref.Address).Extended[ndistinctKey(t, source, "expr_width_variable")]
	if fixed.Expression(0).AverageWidth != 4 || variable.Expression(0).AverageWidth != 0 || fixed.Expression(0).NullCount != 3 || variable.Expression(0).NullCount != 3 {
		t.Fatal("all-NULL datum widths ignored actual fixed/unknown type facts")
	}
	enginetest.SessionSQL(t, s, "UPDATE expr_width SET a=8,t='changed'")
	var dump strings.Builder
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	if err := engine.Restore(context.Background(), target, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	targetRef := enginetest.CatalogRef(t, target, target.CatalogRead(), "expr_width")
	root := analyzeFacts(target, targetRef.Address)
	fixedKey, variableKey := ndistinctKey(t, target, "expr_width_fixed"), ndistinctKey(t, target, "expr_width_variable")
	restoredFixed, restoredVariable := root.Extended[fixedKey], root.Extended[variableKey]
	if restoredFixed == nil || restoredVariable == nil || restoredFixed == fixed || restoredVariable == variable {
		t.Fatal("width facts not reminted into destination backing")
	}
	if restoredFixed.Expression(0).AverageWidth != 4 || restoredVariable.Expression(0).AverageWidth != 0 || restoredFixed.Expression(0).NullCount != 3 || restoredVariable.Expression(0).NullCount != 3 {
		t.Fatal("snapshot recomputed stale all-NULL width or lost fixed width")
	}
	targetSession := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, targetSession, "ANALYZE expr_width")
	fresh := analyzeFacts(target, targetRef.Address)
	if fresh.Extended[fixedKey] == restoredFixed || fresh.Extended[fixedKey].Expression(0).AverageWidth != 4 || fresh.Extended[fixedKey].Expression(0).NullCount != 0 || fresh.Extended[variableKey].Expression(0).AverageWidth != 11 {
		t.Fatal("fresh scan failed to distinguish current non-NULL datum widths")
	}
	if fixed.Expression(0).AverageWidth != 4 || restoredFixed.Expression(0).NullCount != 3 || restoredVariable.Expression(0).AverageWidth != 0 {
		t.Fatal("refresh mutated old captured width facts")
	}
}
