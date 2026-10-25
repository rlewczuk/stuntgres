package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDeleteReturningSequenceDemand(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1),(2),(3)", "CREATE SEQUENCE seq START 101 CACHE 1"} {
		sessionSQL(t, s, q)
	}
	ast, description := sequencePreparedDescription(t, e, s, "DELETE FROM t RETURNING nextval('seq') AS draw,'seq'::regclass AS name")
	ref := catalogRef(t, e, e.CatalogRead(), "seq")
	check := func(last int64, called bool) {
		t.Helper()
		e.Mu.Lock()
		generator := *e.Storage.Sequences[ref.Address]
		e.Mu.Unlock()
		if generator.Last != last || generator.Called != called {
			t.Fatal(generator, last, called)
		}
	}
	check(101, false)
	tx := transactionBegin(t, s)
	result, err := sequencePreparedExecute(e, s, ast, &description)
	if err != nil || result.Count != 3 {
		t.Fatal(result, err)
	}
	defer result.Rows.Close()
	check(103, true) // Eager before any cursor consumption.
	transactionEnd(t, s, tx, false)
	sessionSQL(t, s, "ALTER SEQUENCE seq RENAME TO renamed")
	values := make([]sqltype.Value, 2)
	for _, want := range []int64{101, 102, 103} {
		present, err := result.Rows.Next(context.Background(), values)
		if err != nil || !present {
			t.Fatal(present, err)
		}
		draw, err := values[0].Integer()
		if err != nil || draw != want {
			t.Fatal(draw, err, want)
		}
		name, err := result.Rows.(*exec.FinalRows).RegclassText(values[1])
		if err != nil || name != "seq" {
			t.Fatal(name, err)
		}
	}
	if present, err := result.Rows.Next(context.Background(), values); present || err != nil {
		t.Fatal(present, err)
	}
	check(103, true)
	transactionRows(t, s, "SELECT nextval('renamed')", 104)
	zero := sessionSQL(t, s, "DELETE FROM t WHERE FALSE RETURNING nextval('renamed')")
	if zero.Count != 0 || len(zero.Rows.Schema()) != 1 {
		t.Fatal(zero)
	}
	zero.Rows.Close()
	check(104, true)
	transactionRows(t, s, "SELECT id FROM t ORDER BY id", 1, 2, 3)
	// Consumer cancellation after successful publication cannot resurrect rows.
	published := sessionSQL(t, s, "DELETE FROM t RETURNING id")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if present, err := published.Rows.Next(ctx, nil); present || err != context.Canceled {
		t.Fatal(present, err)
	}
	published.Rows.Close()
	transactionRows(t, s, "SELECT id FROM t")
}
