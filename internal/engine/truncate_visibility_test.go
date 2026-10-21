package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func truncateUnitError(t *testing.T, e *Engine, s *session.Session, q, code string) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, statement(t, q), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("%s: %v, want %s", q, err, code)
	}
}
func TestTruncateSnapshots(t *testing.T) {
	for _, kind := range []string{"truncate", "delete", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			e, writer, p, q := truncateUnitFixture(t)
			reader := sessionStart(t, e, nil)
			sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			tx := reader.Transactions.Ref
			transactionRows(t, reader, "SELECT id FROM q", 7)
			frontier := reader.Transactions.Frontier
			captured := reader.Transactions.Roots[p.Address]
			qRoot := reader.Transactions.Roots[q.Address]
			if captured.Storage != e.storage.Relations[p.Address] || len(captured.Versions) != 2 {
				t.Fatal("stable snapshot did not capture untouched relation")
			}
			switch kind {
			case "truncate":
				truncateUnitExecute(t, e, writer, truncateUnitAST("p"))
				sessionSQL(t, writer, "INSERT INTO p VALUES(9)")
			case "delete":
				sessionSQL(t, writer, "DELETE FROM p")
			case "rollback":
				other := transactionBegin(t, writer)
				truncateUnitExecute(t, e, writer, truncateUnitAST("p"))
				transactionEnd(t, writer, other, false)
			}
			if kind == "truncate" {
				transactionRows(t, reader, "SELECT id FROM p")
				live := e.storage.Relations[p.Address]
				if !captured.Storage.SameTruncateLineage(live) || len(live.Versions) != 1 || live.Versions[0].Born <= frontier {
					t.Fatal("fresh incarnation did not retain ordinary birth frontier filtering")
				}
			} else {
				transactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2)
			}
			retained := reader.Transactions.Roots[p.Address]
			if retained.Storage != captured.Storage || !sameVersionRoot(retained.Versions, captured.Versions) || retained.Ends != captured.Ends || reader.Transactions.Frontier != frontier || !sameVersionRoot(reader.Transactions.Roots[q.Address].Versions, qRoot.Versions) {
				t.Fatal("TRUNCATE became a second snapshot/root authority")
			}
			transactionEnd(t, reader, tx, false)
		})
	}
}
func TestTruncateSnapshotsInherited(t *testing.T) {
	e, writer, p, _ := truncateUnitFixture(t)
	sessionSQL(t, writer, "CREATE TABLE c() INHERITS(p)")
	sessionSQL(t, writer, "INSERT INTO c VALUES(3)")
	child := roleTableRef(t, e, "c")
	reader := sessionStart(t, e, nil)
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	tx := reader.Transactions.Ref
	transactionRows(t, reader, "SELECT id FROM q", 7)
	frontier := reader.Transactions.Frontier
	oldParent, oldChild := reader.Transactions.Roots[p.Address], reader.Transactions.Roots[child.Address]
	sessionSQL(t, writer, "TRUNCATE ONLY c")
	sessionSQL(t, writer, "INSERT INTO c VALUES(9)")
	transactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2)
	if !oldChild.Storage.SameTruncateLineage(e.storage.Relations[child.Address]) || reader.Transactions.Frontier != frontier || reader.Transactions.Roots[p.Address].Storage != oldParent.Storage || !sameVersionRoot(reader.Transactions.Roots[p.Address].Versions, oldParent.Versions) || reader.Transactions.Roots[child.Address].Storage != oldChild.Storage {
		t.Fatal("inherited SELECT replaced the original snapshot authority")
	}
	sessionSQL(t, reader, "INSERT INTO c VALUES(4)")
	transactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2, 4)
	transactionEnd(t, reader, tx, false)
	transactionRows(t, writer, "SELECT id FROM p ORDER BY id", 1, 2, 9)
}
func TestTruncateBuffers(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	old := e.storage.Relations[p.Address]
	root, ends := old.Versions, old.Ends
	result := sessionSQL(t, s, "SELECT id FROM p ORDER BY id")
	t.Cleanup(func() { result.Rows.Close() })
	rows := result.Rows.(*exec.FinalRows)
	schema := rows.Schema()
	count := rows.Count
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	if e.storage.Relations[p.Address] == old || !sameVersionRoot(old.Versions, root) || old.Ends != ends || rows.Count != count || !reflect.DeepEqual(rows.Schema(), schema) {
		t.Fatal("generation swap damaged retained eager buffer/codec")
	}
	if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
		t.Fatal("retained rows changed", got)
	}
}
func TestTruncateActivity(t *testing.T) {
	t.Run("exhausted-producer", func(t *testing.T) {
		e, s, p, _ := truncateUnitFixture(t)
		tx := transactionBegin(t, s)
		sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT id FROM p ORDER BY id")
		transactionRows(t, s, "FETCH ALL FROM c", 1, 2)
		cursor := payloadOf(s).cursors.entries["c"]
		if cursor.root == nil || cursor.position != cursorAfter || len(cursor.leaves) == 0 {
			t.Fatal("fixture did not retain exhausted real producer")
		}
		sessionSQL(t, s, "SAVEPOINT recover")
		truncateUnitError(t, e, s, "TRUNCATE p", "55006")
		if len(e.storage.Relations[p.Address].Versions) != 2 || cursor.root == nil {
			t.Fatal("activity rejection retired producer/storage")
		}
		sessionSQL(t, s, "ROLLBACK TO recover")
		sessionSQL(t, s, "CLOSE c")
		truncateUnitExecute(t, e, s, truncateUnitAST("p"))
		transactionEnd(t, s, tx, true)
	})
	t.Run("failed-producer", func(t *testing.T) {
		e, s, _, _ := truncateUnitFixture(t)
		tx := transactionBegin(t, s)
		sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT id/(id-id) FROM p")
		sessionSQL(t, s, "SAVEPOINT before_fetch")
		truncateUnitError(t, e, s, "FETCH ALL FROM c", "22012")
		cursor := payloadOf(s).cursors.entries["c"]
		if cursor.position != cursorFailed || cursor.root != nil || cursor.leaves != nil {
			t.Fatal("failed portal retained producer references")
		}
		sessionSQL(t, s, "ROLLBACK TO before_fetch")
		truncateUnitExecute(t, e, s, truncateUnitAST("p"))
		transactionEnd(t, s, tx, true)
	})
	t.Run("promoted-hold-buffer", func(t *testing.T) {
		e, s, _, _ := truncateUnitFixture(t)
		tx := transactionBegin(t, s)
		sessionSQL(t, s, "DECLARE c CURSOR WITH HOLD FOR SELECT id FROM p ORDER BY id")
		transactionEnd(t, s, tx, true)
		cursor := payloadOf(s).cursors.entries["c"]
		if !cursor.promoted || cursor.root != nil || len(cursor.leaves) != 0 || cursor.count != 2 {
			t.Fatal("HOLD fixture is not a detached real typed buffer")
		}
		truncateUnitExecute(t, e, s, truncateUnitAST("p"))
		transactionRows(t, s, "FETCH ALL FROM c", 1, 2)
		sessionSQL(t, s, "CLOSE c")
	})
}
func TestTruncateGenerationExhaustion(t *testing.T) {
	e, s, p, q := truncateUnitFixture(t)
	oldP, oldQ := e.storage.Relations[p.Address], e.storage.Relations[q.Address]
	generation := oldQ.Generation
	oldQ.Generation = ^uint64(0)
	defer func() { oldQ.Generation = generation }()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, truncateUnitAST("p", "q"), 0, nil)
	if err == nil || err.Error() != "stuntgres: storage generation exhausted" || e.storage.Relations[p.Address] != oldP || e.storage.Relations[q.Address] != oldQ || oldP.TruncateLineage != nil || oldQ.TruncateLineage != nil {
		t.Fatal("generation exhaustion partially replaced targets", err)
	}
}
func TestTruncateSnapshotsBirthFilter(t *testing.T) {
	e, writer, p, q := truncateUnitFixture(t)
	reader := sessionStart(t, e, nil)
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	tx := reader.Transactions.Ref
	transactionRows(t, reader, "SELECT id FROM q", 7)
	frontier := reader.Transactions.Frontier
	truncateUnitExecute(t, e, writer, truncateUnitAST("p"))
	sessionSQL(t, writer, "INSERT INTO p VALUES(9)")
	sessionSQL(t, writer, "INSERT INTO q VALUES(8)")

	scope, op := truncateUnitScope(t, e, reader, context.Background())
	result, err := scope.Execute(statement(t, "SELECT q.id FROM p RIGHT JOIN q ON p.id=q.id"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { result.Rows.Close() })
	data := scope.view.Data()
	if data.Generations == nil || !data.NeedsBirthFilterAt(p.Address) || data.NeedsBirthFilterAt(q.Address) || data.Frontier != frontier || len(data.Committed[p.Address]) != 1 || data.Ends[p.Address] != nil || data.Deleted[p.Address] != nil || len(data.Own[p.Address]) != 0 {
		t.Fatal("mixed capture lost per-relation birth provenance or fixture acquired deletion/replacement roots")
	}
	for _, test := range []struct {
		ref    ids.ObjectRef
		filter bool
		count  int
	}{{p, true, 0}, {q, false, 1}} {
		source, err := exec.CapturePrefix(scope.view, test.ref)
		if err != nil {
			t.Fatal(err)
		}
		if source.NeedsBirthFilter != test.filter {
			source.Close()
			t.Fatal("source dropped capture birth provenance")
		}
		source, err = exec.SelectQuerySource(context.Background(), source, data.FrontierAt(test.ref.Address), data.Ends[test.ref.Address], data.Deleted[test.ref.Address])
		if err != nil {
			t.Fatal(err)
		}
		if source.Count() != test.count || !test.filter && source.Visible != nil {
			source.Close()
			t.Fatal("selector skipped birth filtering or lost frozen allocation-free path")
		}
		source.Close()
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()

	// A private insert is always visible, but it must not make the undeleted
	// committed peer9 prefix eligible for the all-occurrences shortcut.
	sessionSQL(t, reader, "INSERT INTO p VALUES(3)")
	ownScope, ownOp := truncateUnitScope(t, e, reader, context.Background())
	ownResult, err := ownScope.Execute(statement(t, "SELECT id FROM p ORDER BY id"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ownResult.Rows.Close() })
	ownData := ownScope.view.Data()
	if ownData.Generations != nil || !ownData.SingleNeedsBirthFilter || ownData.Frontier != frontier || ownData.Ends[p.Address] != nil || ownData.Deleted[p.Address] != nil || len(ownData.Own[p.Address]) != 1 || ownData.Own[p.Address][0].Base != 0 {
		t.Fatal("single capture/own-insert birth-filter fixture")
	}
	if got := tuples(ownResult); !reflect.DeepEqual(got, [][]driver.Value{{int64(3)}}) {
		t.Fatal("peer future row leaked beside own row", got)
	}
	if err := ownScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	ownOp.Done()
	transactionEnd(t, reader, tx, false)
}
