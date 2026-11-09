package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func truncateUnitError(t *testing.T, e *core.Engine, s *session.Session, q, code string) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, enginetest.Statement(t, q), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("%s: %v, want %s", q, err, code)
	}
}
func TestTruncateSnapshots(t *testing.T) {
	for _, kind := range []string{"truncate", "delete", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			e, writer, p, q := truncateUnitFixture(t)
			reader := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			tx := reader.Transactions.Ref
			enginetest.TransactionRows(t, reader, "SELECT id FROM q", 7)
			frontier := reader.Transactions.Frontier
			captured := reader.Transactions.Roots[p.Address]
			qRoot := reader.Transactions.Roots[q.Address]
			if captured.Storage != e.Storage.Relations[p.Address] || len(captured.Versions) != 2 {
				t.Fatal("stable snapshot did not capture untouched relation")
			}
			switch kind {
			case "truncate":
				truncateUnitExecute(t, e, writer, truncateUnitAST("p"))
				enginetest.SessionSQL(t, writer, "INSERT INTO p VALUES(9)")
			case "delete":
				enginetest.SessionSQL(t, writer, "DELETE FROM p")
			case "rollback":
				other := enginetest.TransactionBegin(t, writer)
				truncateUnitExecute(t, e, writer, truncateUnitAST("p"))
				enginetest.TransactionEnd(t, writer, other, false)
			}
			if kind == "truncate" {
				enginetest.TransactionRows(t, reader, "SELECT id FROM p")
				live := e.Storage.Relations[p.Address]
				if !captured.Storage.SameTruncateLineage(live) || len(live.Versions) != 1 || live.Versions[0].Born <= frontier {
					t.Fatal("fresh incarnation did not retain ordinary birth frontier filtering")
				}
			} else {
				enginetest.TransactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2)
			}
			retained := reader.Transactions.Roots[p.Address]
			if retained.Storage != captured.Storage || !storage.SameVersionRoot(retained.Versions, captured.Versions) || retained.Ends != captured.Ends || reader.Transactions.Frontier != frontier || !storage.SameVersionRoot(reader.Transactions.Roots[q.Address].Versions, qRoot.Versions) {
				t.Fatal("TRUNCATE became a second snapshot/root authority")
			}
			enginetest.TransactionEnd(t, reader, tx, false)
		})
	}
}
func TestTruncateSnapshotsInherited(t *testing.T) {
	e, writer, p, _ := truncateUnitFixture(t)
	enginetest.SessionSQL(t, writer, "CREATE TABLE c() INHERITS(p)")
	enginetest.SessionSQL(t, writer, "INSERT INTO c VALUES(3)")
	child := roleTableRef(t, e, "c")
	reader := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	tx := reader.Transactions.Ref
	enginetest.TransactionRows(t, reader, "SELECT id FROM q", 7)
	frontier := reader.Transactions.Frontier
	oldParent, oldChild := reader.Transactions.Roots[p.Address], reader.Transactions.Roots[child.Address]
	enginetest.SessionSQL(t, writer, "TRUNCATE ONLY c")
	enginetest.SessionSQL(t, writer, "INSERT INTO c VALUES(9)")
	enginetest.TransactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2)
	if !oldChild.Storage.SameTruncateLineage(e.Storage.Relations[child.Address]) || reader.Transactions.Frontier != frontier || reader.Transactions.Roots[p.Address].Storage != oldParent.Storage || !storage.SameVersionRoot(reader.Transactions.Roots[p.Address].Versions, oldParent.Versions) || reader.Transactions.Roots[child.Address].Storage != oldChild.Storage {
		t.Fatal("inherited SELECT replaced the original snapshot authority")
	}
	enginetest.SessionSQL(t, reader, "INSERT INTO c VALUES(4)")
	enginetest.TransactionRows(t, reader, "SELECT id FROM p ORDER BY id", 1, 2, 4)
	enginetest.TransactionEnd(t, reader, tx, false)
	enginetest.TransactionRows(t, writer, "SELECT id FROM p ORDER BY id", 1, 2, 9)
}
func TestTruncateBuffers(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	old := e.Storage.Relations[p.Address]
	root, ends := old.Versions, old.Ends
	result := enginetest.SessionSQL(t, s, "SELECT id FROM p ORDER BY id")
	t.Cleanup(func() { result.Rows.Close() })
	rows := result.Rows.(*exec.FinalRows)
	schema := rows.Schema()
	count := rows.Count
	computed := enginetest.SessionSQL(t, s, "SELECT id+0 AS id FROM p ORDER BY id")
	computedRows := computed.Rows.(*exec.FinalRows)
	payload := slices.Clone(computedRows.Values)
	if len(payload) != 2 {
		t.Fatal("computed eager Rows did not retain a genuine typed payload")
	}
	versions := slices.Clone(root)
	generation := old.Generation
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "SAVEPOINT original")
	for marker := 1; marker <= 256; marker++ {
		truncateUnitExecute(t, e, s, truncateUnitAST("p"))
		if marker == 64 {
			enginetest.SessionSQL(t, s, "SAVEPOINT repeated")
		}
		if marker == 128 {
			enginetest.SessionSQL(t, s, "ROLLBACK TO repeated")
		}
		if marker == 192 {
			enginetest.SessionSQL(t, s, "RELEASE repeated")
		}
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO original")
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	enginetest.SessionSQL(t, s, "RELEASE original")
	enginetest.TransactionEnd(t, s, tx, true)
	if !reflect.DeepEqual(computedRows.Values, payload) || computedRows.Closed || computedRows.Count != 2 || old.Generation != generation || !reflect.DeepEqual(old.Versions, versions) {
		t.Fatal("replay/savepoint/reappend mutated live typed buffer or predecessor stamps")
	}
	if got := enginetest.Tuples(computed); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
		t.Fatal("retained computed typed rows changed", got)
	}
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	if e.Storage.Relations[p.Address] == old || !storage.SameVersionRoot(old.Versions, root) || old.Ends != ends || rows.Count != count || !reflect.DeepEqual(rows.Schema(), schema) {
		t.Fatal("generation swap damaged retained eager buffer/codec")
	}
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
		t.Fatal("retained rows changed", got)
	}
}
func TestTruncateActivity(t *testing.T) {
	t.Run("exhausted-producer", func(t *testing.T) {
		e, s, p, _ := truncateUnitFixture(t)
		tx := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "DECLARE c CURSOR FOR SELECT id FROM p ORDER BY id")
		enginetest.TransactionRows(t, s, "FETCH ALL FROM c", 1, 2)
		cursor := core.PayloadOf(s).Cursors.Entries["c"]
		if core.CursorRoot(cursor) == nil || cursor.Place != exec.CursorAfter || len(core.CursorLeaves(cursor)) == 0 {
			t.Fatal("fixture did not retain exhausted real producer")
		}
		enginetest.SessionSQL(t, s, "SAVEPOINT recover")
		truncateUnitError(t, e, s, "TRUNCATE p", "55006")
		if len(e.Storage.Relations[p.Address].Versions) != 2 || core.CursorRoot(cursor) == nil {
			t.Fatal("activity rejection retired producer/storage")
		}
		enginetest.SessionSQL(t, s, "ROLLBACK TO recover")
		enginetest.SessionSQL(t, s, "CLOSE c")
		truncateUnitExecute(t, e, s, truncateUnitAST("p"))
		enginetest.TransactionEnd(t, s, tx, true)
	})
	t.Run("failed-producer", func(t *testing.T) {
		e, s, _, _ := truncateUnitFixture(t)
		tx := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "DECLARE c CURSOR FOR SELECT id/(id-id) FROM p")
		enginetest.SessionSQL(t, s, "SAVEPOINT before_fetch")
		truncateUnitError(t, e, s, "FETCH ALL FROM c", "22012")
		cursor := core.PayloadOf(s).Cursors.Entries["c"]
		if cursor.Place != exec.CursorFailed || core.CursorRoot(cursor) != nil || core.CursorLeaves(cursor) != nil {
			t.Fatal("failed portal retained producer references")
		}
		enginetest.SessionSQL(t, s, "ROLLBACK TO before_fetch")
		truncateUnitExecute(t, e, s, truncateUnitAST("p"))
		enginetest.TransactionEnd(t, s, tx, true)
	})
	t.Run("promoted-hold-buffer", func(t *testing.T) {
		e, s, _, _ := truncateUnitFixture(t)
		tx := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "DECLARE c CURSOR WITH HOLD FOR SELECT id FROM p ORDER BY id")
		enginetest.TransactionEnd(t, s, tx, true)
		cursor := core.PayloadOf(s).Cursors.Entries["c"]
		if !core.CursorPromoted(cursor) || core.CursorRoot(cursor) != nil || len(core.CursorLeaves(cursor)) != 0 || core.CursorCount(cursor) != 2 {
			t.Fatal("HOLD fixture is not a detached real typed buffer")
		}
		truncateUnitExecute(t, e, s, truncateUnitAST("p"))
		enginetest.TransactionRows(t, s, "FETCH ALL FROM c", 1, 2)
		enginetest.SessionSQL(t, s, "CLOSE c")
	})
}
func TestTruncateGenerationExhaustion(t *testing.T) {
	e, s, p, q := truncateUnitFixture(t)
	oldP, oldQ := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address]
	generation := oldQ.Generation
	oldQ.Generation = ^uint64(0)
	defer func() { oldQ.Generation = generation }()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, truncateUnitAST("p", "q"), 0, nil)
	if err == nil || err.Error() != "stuntgres: storage generation exhausted" || e.Storage.Relations[p.Address] != oldP || e.Storage.Relations[q.Address] != oldQ || oldP.TruncateLineage != nil || oldQ.TruncateLineage != nil {
		t.Fatal("generation exhaustion partially replaced targets", err)
	}
}
func TestTruncateSnapshotsBirthFilter(t *testing.T) {
	e, writer, p, q := truncateUnitFixture(t)
	reader := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	tx := reader.Transactions.Ref
	enginetest.TransactionRows(t, reader, "SELECT id FROM q", 7)
	frontier := reader.Transactions.Frontier
	truncateUnitExecute(t, e, writer, truncateUnitAST("p"))
	enginetest.SessionSQL(t, writer, "INSERT INTO p VALUES(9)")
	enginetest.SessionSQL(t, writer, "INSERT INTO q VALUES(8)")

	scope, op := truncateUnitScope(t, e, reader, context.Background())
	result, err := scope.Execute(enginetest.Statement(t, "SELECT q.id FROM p RIGHT JOIN q ON p.id=q.id"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { result.Rows.Close() })
	data := scope.View.Data()
	if data.Generations == nil || !data.NeedsBirthFilterAt(p.Address) || data.NeedsBirthFilterAt(q.Address) || data.Frontier != frontier || len(data.Committed[p.Address]) != 1 || data.Ends[p.Address] != nil || data.Deleted[p.Address] != nil || len(data.Own[p.Address]) != 0 {
		t.Fatal("mixed capture lost per-relation birth provenance or fixture acquired deletion/replacement roots")
	}
	for _, test := range []struct {
		ref    ids.ObjectRef
		filter bool
		count  int
	}{{p, true, 0}, {q, false, 1}} {
		source, err := exec.CapturePrefix(scope.View, test.ref)
		if err != nil {
			t.Fatal(err)
		}
		if source.NeedsBirthFilter != test.filter {
			source.Close()
			t.Fatal("source dropped capture birth provenance")
		}
		source, err = exec.SelectQuerySource(context.Background(), source, data.FrontierAt(test.ref.Address), data.Ends[test.ref.Address], data.Deleted[test.ref.Address], accessMethodHeapOperationsUnit(t, data.Catalog))
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
	enginetest.SessionSQL(t, reader, "INSERT INTO p VALUES(3)")
	ownScope, ownOp := truncateUnitScope(t, e, reader, context.Background())
	ownResult, err := ownScope.Execute(enginetest.Statement(t, "SELECT id FROM p ORDER BY id"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ownResult.Rows.Close() })
	ownData := ownScope.View.Data()
	if ownData.Generations != nil || !ownData.SingleNeedsBirthFilter || ownData.Frontier != frontier || ownData.Ends[p.Address] != nil || ownData.Deleted[p.Address] != nil || len(ownData.Own[p.Address]) != 1 || ownData.Own[p.Address][0].Base != 0 {
		t.Fatal("single capture/own-insert birth-filter fixture")
	}
	if got := enginetest.Tuples(ownResult); !reflect.DeepEqual(got, [][]driver.Value{{int64(3)}}) {
		t.Fatal("peer future row leaked beside own row", got)
	}
	if err := ownScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	ownOp.Done()
	enginetest.TransactionEnd(t, reader, tx, false)
}
