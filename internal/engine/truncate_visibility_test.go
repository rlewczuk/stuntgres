package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func truncateUnitError(t *testing.T, e *Engine, s *Session, q, code string) {
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
			tx := reader.transactions.ref
			transactionRows(t, reader, "SELECT id FROM q", 7)
			frontier := reader.transactions.frontier
			captured := reader.transactions.roots[p.Address]
			qRoot := reader.transactions.roots[q.Address]
			if captured.storage != e.storage.relations[p.Address] || len(captured.versions) != 2 {
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
				live := e.storage.relations[p.Address]
				if !sameTruncateLineage(captured.storage, live) || len(live.versions) != 1 || live.versions[0].born <= frontier {
					t.Fatal("fresh incarnation did not retain ordinary birth frontier filtering")
				}
			} else {
				transactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2)
			}
			retained := reader.transactions.roots[p.Address]
			if retained.storage != captured.storage || !sameVersionRoot(retained.versions, captured.versions) || retained.ends != captured.ends || reader.transactions.frontier != frontier || !sameVersionRoot(reader.transactions.roots[q.Address].versions, qRoot.versions) {
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
	tx := reader.transactions.ref
	transactionRows(t, reader, "SELECT id FROM q", 7)
	frontier := reader.transactions.frontier
	oldParent, oldChild := reader.transactions.roots[p.Address], reader.transactions.roots[child.Address]
	sessionSQL(t, writer, "TRUNCATE ONLY c")
	sessionSQL(t, writer, "INSERT INTO c VALUES(9)")
	transactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2)
	if !sameTruncateLineage(oldChild.storage, e.storage.relations[child.Address]) || reader.transactions.frontier != frontier || reader.transactions.roots[p.Address].storage != oldParent.storage || !sameVersionRoot(reader.transactions.roots[p.Address].versions, oldParent.versions) || reader.transactions.roots[child.Address].storage != oldChild.storage {
		t.Fatal("inherited SELECT replaced the original snapshot authority")
	}
	sessionSQL(t, reader, "INSERT INTO c VALUES(4)")
	transactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2, 4)
	transactionEnd(t, reader, tx, false)
	transactionRows(t, writer, "SELECT id FROM p ORDER BY id", 1, 2, 9)
}
func TestTruncateBuffers(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	old := e.storage.relations[p.Address]
	root, ends := old.versions, old.ends
	result := sessionSQL(t, s, "SELECT id FROM p ORDER BY id")
	t.Cleanup(func() { result.Rows.Close() })
	rows := result.Rows.(*finalRows)
	schema := rows.Schema()
	count := rows.count
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	if e.storage.relations[p.Address] == old || !sameVersionRoot(old.versions, root) || old.ends != ends || rows.count != count || !reflect.DeepEqual(rows.Schema(), schema) {
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
		cursor := s.cursors.entries["c"]
		if cursor.root == nil || cursor.position != cursorAfter || len(cursor.leaves) == 0 {
			t.Fatal("fixture did not retain exhausted real producer")
		}
		sessionSQL(t, s, "SAVEPOINT recover")
		truncateUnitError(t, e, s, "TRUNCATE p", "55006")
		if len(e.storage.relations[p.Address].versions) != 2 || cursor.root == nil {
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
		cursor := s.cursors.entries["c"]
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
		cursor := s.cursors.entries["c"]
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
	oldP, oldQ := e.storage.relations[p.Address], e.storage.relations[q.Address]
	generation := oldQ.generation
	oldQ.generation = ^uint64(0)
	defer func() { oldQ.generation = generation }()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, truncateUnitAST("p", "q"), 0, nil)
	if err == nil || err.Error() != "stuntgres: storage generation exhausted" || e.storage.relations[p.Address] != oldP || e.storage.relations[q.Address] != oldQ || oldP.truncateLineage != nil || oldQ.truncateLineage != nil {
		t.Fatal("generation exhaustion partially replaced targets", err)
	}
}
func TestTruncateSnapshotsBirthFilter(t *testing.T) {
	e, writer, p, q := truncateUnitFixture(t)
	reader := sessionStart(t, e, nil)
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	tx := reader.transactions.ref
	transactionRows(t, reader, "SELECT id FROM q", 7)
	frontier := reader.transactions.frontier
	truncateUnitExecute(t, e, writer, truncateUnitAST("p"))
	sessionSQL(t, writer, "INSERT INTO p VALUES(9)")
	sessionSQL(t, writer, "INSERT INTO q VALUES(8)")

	scope, op := truncateUnitScope(t, e, reader, context.Background())
	result, err := scope.Execute(statement(t, "SELECT q.id FROM p RIGHT JOIN q ON p.id=q.id"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { result.Rows.Close() })
	data := scope.view.read()
	if data.generations == nil || !data.needsBirthFilterAt(p.Address) || data.needsBirthFilterAt(q.Address) || data.frontier != frontier || len(data.committed[p.Address]) != 1 || data.ends[p.Address] != nil || data.deleted[p.Address] != nil || len(data.own[p.Address]) != 0 {
		t.Fatal("mixed capture lost per-relation birth provenance or fixture acquired deletion/replacement roots")
	}
	for _, test := range []struct {
		ref    ids.ObjectRef
		filter bool
		count  int
	}{{p, true, 0}, {q, false, 1}} {
		source, err := captureQueryPrefix(scope.view, test.ref)
		if err != nil {
			t.Fatal(err)
		}
		if source.needsBirthFilter != test.filter {
			source.close()
			t.Fatal("source dropped capture birth provenance")
		}
		source, err = selectQuerySource(context.Background(), source, data.frontierAt(test.ref.Address), data.ends[test.ref.Address], data.deleted[test.ref.Address])
		if err != nil {
			t.Fatal(err)
		}
		if source.count() != test.count || !test.filter && source.visible != nil {
			source.close()
			t.Fatal("selector skipped birth filtering or lost frozen allocation-free path")
		}
		source.close()
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
	ownData := ownScope.view.read()
	if ownData.generations != nil || !ownData.singleNeedsBirthFilter || ownData.frontier != frontier || ownData.ends[p.Address] != nil || ownData.deleted[p.Address] != nil || len(ownData.own[p.Address]) != 1 || ownData.own[p.Address][0].base != 0 {
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
