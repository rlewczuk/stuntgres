package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

func TestDeleteUsingLateRecheckFailureDiscardsStage(t *testing.T) {
	e := New()
	writer := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1),(2)"} {
		sessionSQL(t, peer, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, writer, context.Background(), "DELETE FROM t USING s WHERE t.id=s.k RETURNING t.id")
	sessionSQL(t, peer, "UPDATE t SET v=21 WHERE id=2")
	sentinel := errors.New("latest-target recheck failure")
	predicate := b.Predicate
	failed := false
	b.Predicate = queryProbe(predicate.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		if testExport(c.Row[1]) == int64(21) {
			root := scope.stage.deletes[b.Ref.Address]
			if root == nil || len(root.Rows) != 1 {
				t.Fatal("recheck did not follow a staged earlier target", root)
			}
			failed = true
			return sqltype.Value{}, sentinel
		}
		return predicate.Eval(c)
	})
	r, err := scope.deleteSource(b, target, inputs, nil)
	if r.Rows != nil {
		t.Cleanup(func() { r.Rows.Close() })
	}
	target.Close()
	if !failed || !errors.Is(err, sentinel) || r.Count != 0 || r.Rows != nil {
		t.Fatal(r, err, failed)
	}
	if err := scope.Finish(err); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 21)
	lockEmpty(t, e)
}

func TestDeleteUsingSourceGrantsStayReadOnly(t *testing.T) {
	e := New()
	writer := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1)", "INSERT INTO s VALUES(1)"} {
		sessionSQL(t, writer, q)
	}
	tx := transactionBegin(t, writer)
	deleteCount(t, writer, "DELETE FROM t USING s WHERE t.id=s.k", 1)
	target := catalogRef(t, e, e.CatalogRead(), "t")
	source := catalogRef(t, e, e.CatalogRead(), "s")
	lockState := e.locks.Inspect()
	var targetMode, sourceMode lock.Mode
	for resource, entry := range lockState.Resources {
		if resource.ObjectRef == target && resource.Row == 0 {
			targetMode = entry.Holders[writer.transactions.ticket].Mode
		}
		if resource.ObjectRef == source {
			if resource.Row != 0 {
				t.Fatal("ordinary source tuple write grant", resource)
			}
			sourceMode = entry.Holders[writer.transactions.ticket].Mode
		}
	}
	if targetMode&lock.RowExclusive == 0 || sourceMode&lock.AccessShare == 0 || sourceMode&lock.RowExclusive != 0 {
		t.Fatal(targetMode, sourceMode)
	}
	transactionEnd(t, writer, tx, false)
	transactionRows(t, writer, "SELECT id FROM t", 1)
	lockEmpty(t, e)
}
