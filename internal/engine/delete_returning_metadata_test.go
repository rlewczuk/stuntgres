package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDeleteReturningPreparedMetadata(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,n NUMERIC(8,2),ts TIMESTAMP(3),b BYTEA)", "INSERT INTO t VALUES(2,12.30,'2024-01-02 03:04:05.123','\\x0102')", "CREATE SEQUENCE seq START 101 CACHE 1"} {
		sessionSQL(t, s, q)
	}
	op := transactionOperation(t, s)
	ast := statement(t, "DELETE FROM t WHERE id=$1 RETURNING old.id,new.id,n,new.n,ts,new.ts,b,new.b,$2 AS label,nextval('seq')")
	description, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	if description.Parameters.Count != 2 || description.Parameters.Types[0].Type.ID() != sqltype.Int4 || description.Parameters.Types[1].Type.ID() != sqltype.Text || len(description.Results) != 10 {
		t.Fatal(description)
	}
	for i, want := range []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Numeric, sqltype.Numeric, sqltype.Timestamp, sqltype.Timestamp, sqltype.Bytea, sqltype.Bytea, sqltype.Text, sqltype.Int8} {
		if description.Results[i].Usage.Type.ID() != want {
			t.Fatal(i, description.Results[i])
		}
	}
	if description.Results[0].Nullable || !description.Results[0].NullabilityKnown || !description.Results[1].Nullable || !description.Results[1].NullabilityKnown {
		t.Fatal(description.Results[:2])
	}
	if description.Results[0].OriginTable == 0 || description.Results[0].OriginTable != description.Results[1].OriginTable || description.Results[0].OriginAttribute != description.Results[1].OriginAttribute || description.Results[2].Usage != description.Results[3].Usage {
		t.Fatal("lost NEW origin/typmod", description.Results)
	}
	transactionRows(t, s, "SELECT id FROM t", 2)
	seq := catalogRef(t, e, e.CatalogRead(), "seq")
	e.mu.Lock()
	called := e.sequences[seq.Address].called
	e.mu.Unlock()
	if called {
		t.Fatal("Prepare drew")
	}
	// Actual execution signatures include RETURNING, not the empty command schema.
	star := statement(t, "DELETE FROM t RETURNING *")
	before, err := e.Describe(op, star)
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, s, "ALTER TABLE t RENAME COLUMN b TO data")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &before
	result, err := scope.Execute(star, 0, nil)
	err = scope.Finish(err)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
	var diagnostic *sqlstate.Error
	if result.Rows != nil || result.Count != 0 || !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(result, err, diagnostic)
	}
	transactionRows(t, s, "SELECT id FROM t", 2)
}

func TestDeleteReturningPreflight(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	op := transactionOperation(t, s)
	ast := statement(t, "DELETE FROM t RETURNING CAST($1::TEXT AS INTEGER)")
	_, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, ast, 1, func(ctx context.Context, p ParameterInfo, dst []sqltype.Value, input sqltype.InputEnvironment) error {
		dst[0] = sqltype.String(p.Types[0].Type, "bad")
		return nil
	})
	state(t, err, "22P02")
	if result.Rows != nil || result.Count != 0 {
		t.Fatal(result)
	}
	transactionRows(t, s, "SELECT id FROM t ORDER BY id", 1, 2)
	lockEmpty(t, e)
}

func TestDeleteReturningCurrentSchemasDescription(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	op := transactionOperation(t, s)
	description, err := e.Describe(op, statement(t, "DELETE FROM t RETURNING current_schemas(NULL) AS schemas"))
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
	transactionRows(t, s, "SELECT id FROM t", 1)
}
