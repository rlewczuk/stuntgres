package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func deleteCount(t *testing.T, s *Session, query string, want int64) {
	t.Helper()
	result := sessionSQL(t, s, query)
	if result.Count != want || result.Rows != nil {
		t.Fatalf("%s: count=%d rows=%v, want %d/no relation", query, result.Count, result.Rows, want)
	}
}

func TestDeleteCoreSelection(t *testing.T) {
	for _, query := range []string{"DELETE FROM t WHERE v=10", "DELETE FROM t AS d WHERE d.v=10", "DELETE FROM t d WHERE d.v=10", `/*é*/ DELETE FROM public.t AS "d" WHERE "d"."v"=10`} {
		t.Run(query, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)")
			retained := sessionSQL(t, s, "SELECT * FROM t ORDER BY id")
			defer retained.Rows.Close()
			before := tuples(retained)
			deleteCount(t, s, query, 3)
			got := sessionSQL(t, s, "SELECT * FROM t ORDER BY id")
			defer got.Rows.Close()
			want := [][]driver.Value{{int64(2), nil, nil}, {int64(3), int64(20), false}}
			if !reflect.DeepEqual(tuples(got), want) {
				t.Fatal(tuples(got), want)
			}
			if !reflect.DeepEqual(tuples(retained), before) {
				t.Fatal("retained membership changed")
			}
			deleteCount(t, s, query, 0)
			deleteCount(t, s, "DELETE FROM t WHERE FALSE", 0)
			deleteCount(t, s, "DELETE FROM t WHERE NULL", 0)
			deleteCount(t, s, "DELETE FROM t WHERE TRUE", 2)
			deleteCount(t, s, "DELETE FROM t", 0)
			transactionRows(t, s, "SELECT id FROM t ORDER BY id")
			lockEmpty(t, e)
		})
	}
}

func TestDeleteCoreVisibility(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(1),(1),(2)")
			ref := transactionBegin(t, a)
			old := transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 1, 2)
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 2)
			first := transactionRows(t, a, "SELECT v FROM t ORDER BY v", 2)
			target := catalogRef(t, e, e.CatalogRead(), "t")
			firstRoot := first.source.view.Data().Deleted[target.Address]
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 0)
			sessionSQL(t, a, "INSERT INTO t VALUES(7),(7)")
			private := transactionRows(t, a, "SELECT v FROM t ORDER BY v", 2, 7, 7)
			deleteCount(t, a, "DELETE FROM t WHERE v=7", 2)
			if firstRoot == nil || len(firstRoot.Rows) != 2 || a.transactions.deletes[target.Address] == firstRoot || len(a.transactions.deletes[target.Address].Rows) != 4 {
				t.Fatal("prior-own deletion root was mutated/reused")
			}
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2)
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 1, 2)
			if first.count != 1 || private.count != 3 || old.count != 3 {
				t.Fatal("own immutable views changed")
			}
			transactionEnd(t, a, ref, commit)
			if commit {
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2)
			} else {
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 1, 2)
			}
			if got := tuples(Result{Rows: private}); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}, {int64(7)}, {int64(7)}}) {
				t.Fatal("private retained cursor", got)
			}
			lockEmpty(t, e)
		})
	}
}

func TestDeleteCorePrepared(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(10),(10),(20)")
	op := transactionOperation(t, s)
	defer op.Done()
	ast := statement(t, "DELETE FROM t AS d WHERE d.v=$1 OR d.v=$1")
	description, err := e.Describe(op, ast)
	if err != nil || description.Parameters.Count != 1 || description.Parameters.Types[0].Type.ID() != sqltype.Int4 || len(description.Results) != 0 {
		t.Fatal(description, err)
	}
	for _, tc := range []struct {
		value any
		count int64
	}{{int64(10), 2}, {int64(10), 0}, {nil, 0}, {int64(20), 1}} {
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &description
		result, err := scope.Execute(ast, 1, func(ctx context.Context, p ParameterInfo, dst []sqltype.Value, input sqltype.InputEnvironment) error {
			if tc.value == nil {
				dst[0] = sqltype.Null(p.Types[0].Type)
				return nil
			}
			value, err := sqltype.Integer(p.Types[0].Type, tc.value.(int64))
			if err == nil {
				dst[0] = value
			}
			return err
		})
		err = scope.Finish(err)
		if err != nil || result.Count != tc.count || result.Rows != nil {
			t.Fatal(result, err, tc.count)
		}
	}
}

func TestDeleteCoreCombinedCommit(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "CREATE TABLE u(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
	sessionSQL(t, b, "INSERT INTO u VALUES(9)")
	ref := transactionBegin(t, a)
	deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
	sessionSQL(t, a, "INSERT INTO t VALUES(3),(7)")
	deleteCount(t, a, "DELETE FROM t WHERE v=7", 1)
	sessionSQL(t, a, "INSERT INTO u VALUES(8)")
	deleteCount(t, a, "DELETE FROM u WHERE v=9", 1)
	sessionSQL(t, b, "INSERT INTO t VALUES(4)")
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 2, 3, 4)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 4)
	frontier := e.visibility.CommitSeq()
	transactionEnd(t, a, ref, true)
	if e.visibility.CommitSeq() != frontier+1 {
		t.Fatal("combined commit advanced multiple frontiers")
	}
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 3, 4)
	transactionRows(t, b, "SELECT v FROM u ORDER BY v", 8)
	for _, store := range e.storage.Relations {
		if store.Ends == nil {
			continue
		}
		for _, end := range store.Ends.Rows {
			if end.End != e.visibility.CommitSeq() {
				t.Fatal("mixed deletion frontier", end)
			}
		}
	}
	lockEmpty(t, e)
}
