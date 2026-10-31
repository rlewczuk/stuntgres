package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestUpdateReturningPreflight(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(id INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	op := enginetest.TransactionOperation(t, s)
	ast := enginetest.Statement(t, "UPDATE t SET id=id RETURNING CAST($1::TEXT AS INTEGER)")
	_, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, ast, 1, func(ctx context.Context, p analyze.ParameterInfo, dst []sqltype.Value, input sqltype.InputEnvironment) error {
		dst[0] = sqltype.String(p.Types[0].Type, "bad")
		return nil
	})
	enginetest.State(t, err, "22P02")
	if result.Rows != nil || result.Count != 0 {
		t.Fatal(result)
	}
	enginetest.TransactionRows(t, s, "SELECT id FROM t ORDER BY id", 1, 2)
	enginetest.LockEmpty(t, e)
}

func TestUpdateReturningCurrentSchemasDescription(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(id INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1)")
	op := enginetest.TransactionOperation(t, s)
	description, err := e.Describe(op, enginetest.Statement(t, "UPDATE t SET id=id RETURNING current_schemas(NULL) AS schemas"))
	if err != nil {
		t.Fatal(err)
	}
	if len(description.Results) != 1 {
		t.Fatal(description)
	}
	column := description.Results[0]
	if column.Name != "schemas" || column.Usage.Type.ID() != sqltype.NameArray || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 {
		t.Fatal(column)
	}
	enginetest.TransactionRows(t, s, "SELECT id FROM t", 1)
}
