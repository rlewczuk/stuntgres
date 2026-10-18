package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestTimezoneReviewViewAtCaptured(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"SET TIME ZONE 'UTC'", "CREATE TABLE tz_at_source(id INT,value TIMESTAMP,zone TEXT)", "INSERT INTO tz_at_source VALUES(1,'2004-07-01 12:00','UTC')", "CREATE VIEW tz_at AS SELECT (value AT TIME ZONE zone AT TIME ZONE 'UTC')::TEXT AS explicit,(value AT LOCAL AT TIME ZONE 'UTC')::TEXT AS local_value FROM tz_at_source", "CREATE VIEW tz_at_nested AS SELECT * FROM tz_at"} {
		roleExecute(t, e, op, q)
	}
	ref := roleTableRef(t, e, "tz_at")
	definition := e.state.descriptors[ref.Address].(*relationDescriptor).view
	for _, name := range []string{"tz_at", "tz_at_nested"} {
		ast := statement(t, "SELECT explicit,local_value FROM "+name)
		description, err := e.Describe(op, ast)
		if err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			roleExecute(t, e, op, "SET TIME ZONE 'America/New_York'")
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if prepared {
				scope.Expected = &description
			}
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "SET TIME ZONE 'Asia/Tokyo'")
			rows := tuples(result)
			result.Rows.Close()
			if !reflect.DeepEqual(rows, [][]driver.Value{{"2004-07-01 12:00:00", "2004-07-01 16:00:00"}}) {
				t.Fatalf("%s prepared=%t: %v", name, prepared, rows)
			}
		}
	}
	if e.state.descriptors[ref.Address].(*relationDescriptor).view != definition {
		t.Fatal("execution mutated stored definition")
	}
}

func TestTimezoneReviewViewAtRemap(t *testing.T) {
	source := New()
	for _, q := range []string{"CREATE TABLE tz_at_source(value TIMESTAMP,zone TEXT)", "INSERT INTO tz_at_source VALUES('2004-07-01 12:00','UTC')", "CREATE VIEW tz_at AS SELECT value AT TIME ZONE zone AS instant,value AT LOCAL AS local_value FROM tz_at_source"} {
		run(t, source, q)
	}
	ref := roleTableRef(t, source, "tz_at")
	definition := source.state.descriptors[ref.Address].(*relationDescriptor).view
	project := definition.query.Root.(*projectNode)
	for _, expression := range project.expressions {
		original := expression.(functions.Timezone[BoundExpr, ExprContext, ExprProperties])
		before := original
		mapped, err := remapBoundExpression(original, func(ref ids.ObjectRef) ids.ObjectRef {
			if ref != (ids.ObjectRef{}) {
				ref.Address.ObjectID += 1000
			}
			return ref
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(functions.Timezone[BoundExpr, ExprContext, ExprProperties])
		if next.Info != original.Info || next.Local != original.Local {
			t.Fatal("remap changed usage, position, properties or local flag")
		}
		if !reflect.DeepEqual(original, before) {
			t.Fatal("remap mutated original expression")
		}
		if next.Value.(columnExpression).origin == original.Value.(columnExpression).origin {
			t.Fatal("value identity was not remapped")
		}
		if !original.Local && next.Zone.(columnExpression).origin == original.Zone.(columnExpression).origin {
			t.Fatal("zone identity was not remapped")
		}
	}
	target := New()
	run(t, target, "CREATE TABLE sentinel(id INT)")
	if _, err := target.ReplaceCatalog(context.Background(), source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	result := run(t, target, "SELECT (instant AT TIME ZONE 'UTC')::TEXT,(local_value AT TIME ZONE 'UTC')::TEXT FROM tz_at")
	defer result.Rows.Close()
	if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{"2004-07-01 12:00:00", "2004-07-01 12:00:00"}}) {
		t.Fatal(got)
	}
	if source.state.descriptors[ref.Address].(*relationDescriptor).view != definition {
		t.Fatal("catalog import mutated source definition")
	}
}

func TestTimezoneReviewViewAtValidation(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE tz_at_source(value TIMESTAMP,zone TEXT)", "CREATE VIEW tz_at AS SELECT value AT TIME ZONE zone AS instant,TIMESTAMP '2004-07-01 12:00' AT LOCAL AS local_value FROM tz_at_source"} {
		run(t, e, q)
	}
	ref := roleTableRef(t, e, "tz_at")
	definition := e.state.descriptors[ref.Address].(*relationDescriptor).view
	if err := definition.validateLiterals(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	for _, column := range []bool{false, true} {
		bad := *definition
		if column {
			bad.columns = make(map[viewLiteralPath]viewColumn, len(definition.columns))
			for path, slot := range definition.columns {
				bad.columns[path] = slot
			}
			for path := range bad.columns {
				delete(bad.columns, path)
				break
			}
		} else {
			bad.literals = make(map[viewLiteralPath]literalExpression, len(definition.literals))
			for path, literal := range definition.literals {
				bad.literals[path] = literal
			}
			for path := range bad.literals {
				delete(bad.literals, path)
				break
			}
		}
		if err := bad.validateLiterals(e.CatalogRead()); err == nil {
			t.Fatalf("missing timezone child occurrence accepted: column=%t", column)
		}
	}
	if err := definition.validateLiterals(e.CatalogRead()); err != nil {
		t.Fatal("validation mutated stored definition", err)
	}
}
