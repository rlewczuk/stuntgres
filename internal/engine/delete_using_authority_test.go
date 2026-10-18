package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestDeleteUsingCandidateAuthorityRemainsStrict(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1),(1)", "INSERT INTO s VALUES(1),(1)"} {
		sessionSQL(t, session, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, session, context.Background(), "DELETE FROM t USING s WHERE id=k")
	v := target.versionAt(target.rawIndex(0))
	candidate := deleteCandidate{ref: b.ref, storage: b.storage, row: v.row, version: v.version, ordinal: target.rawIndex(0)}
	if err := scope.acquireRow(candidate); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []deleteCandidate{
		{ref: b.ref, storage: &relationStorage{}, row: v.row, version: v.version, ordinal: candidate.ordinal},
		{ref: b.ref, storage: b.storage, row: v.row, version: v.version + 1, ordinal: candidate.ordinal},
		{ref: b.ref, storage: b.storage, row: v.row, version: v.version, ordinal: candidate.ordinal + 1},
		{ref: b.ref, storage: b.storage, row: v.row, version: v.version, ordinal: candidate.ordinal, own: true},
	} {
		if deleted, err := scope.Writes().Delete(bad); err == nil || deleted {
			t.Fatal("invalid captured authority accepted", bad, deleted, err)
		}
	}
	copied := *scope.Writes()
	if deleted, err := copied.Delete(candidate); err == nil || deleted {
		t.Fatal("copied write authority accepted")
	}
	if deleted, err := scope.Writes().Delete(candidate); err != nil || !deleted {
		t.Fatal(deleted, err)
	}
	deleted, err := scope.Writes().Delete(candidate)
	if deleted || err == nil || err.Error() != "stuntgres: duplicate staged deletion" {
		t.Fatal("consumer dedup weakened stage invariant", deleted, err)
	}
	if finishErr := scope.Finish(err); finishErr != err {
		t.Fatal(finishErr)
	}
	target.close()
	inputs.close()
	op.Done()
	transactionRows(t, session, "SELECT id FROM t", 1, 1)
	lockEmpty(t, e)
}

func TestDeleteUsingRejectedPairsDoNotAllocatePerPair(t *testing.T) {
	allocations := func(count int, using bool) float64 {
		e := New()
		session := sessionStart(t, e, nil)
		sessionSQL(t, session, "CREATE TABLE t(id INT)")
		sessionSQL(t, session, "CREATE TABLE s(k INT)")
		sessionSQL(t, session, "INSERT INTO s VALUES(1),(2)")
		values := make([]string, count)
		for i := range values {
			values[i] = "(" + strconv.Itoa(i+1) + ")"
		}
		sessionSQL(t, session, "INSERT INTO t VALUES"+strings.Join(values, ","))
		q := "DELETE FROM t WHERE FALSE"
		if using {
			q = "DELETE FROM t USING s WHERE FALSE"
		}
		return testing.AllocsPerRun(20, func() {
			op, scope, b, target, inputs := updateFromBoundSources(t, e, session, context.Background(), q)
			r, err := scope.deleteSource(b, target, inputs, nil)
			if r.Rows != nil {
				defer r.Rows.Close()
			}
			target.close()
			if err != nil || r.Count != 0 || r.Rows != nil {
				t.Fatal(r, err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
		})
	}
	for _, using := range []bool{false, true} {
		small, large := allocations(2, using), allocations(128, using)
		// Fixed source cardinality/frame width and immutable prefix captures:
		// increasing rejected pairs must not allocate a tuple/candidate per pair.
		if large > small+2 {
			t.Fatal("allocations grow with rejected pairs", using, small, large)
		}
	}
}

func TestDeleteUsingReferenceKindDoesNotConferSourceLocks(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE SEQUENCE seq START 2 CACHE 1")
	op := transactionOperation(t, session)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			scope.Finish(errors.New("abandoned DELETE reference-kind fixture"))
		}
	})
	ref := catalogRef(t, e, e.CatalogRead(), "seq")
	e.mu.Lock()
	relation := e.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	err = e.initializeQueryLocksLocked(scope, nil, []queryLeaf{{ref: ref, relation: relation, deleteReference: true}})
	e.mu.Unlock()
	waitsDiagnostic(t, err, "42809", `cannot lock rows in sequence "seq"`)
	if scope.queryLocks != nil {
		t.Fatal("reference-kind rejection created tuple-lock authority")
	}
	e.mu.Lock()
	err = e.initializeQueryLocksLocked(scope, nil, []queryLeaf{{ref: ref, relation: relation}})
	e.mu.Unlock()
	if err != nil || scope.queryLocks != nil {
		t.Fatal("DELETE kind policy leaked into ordinary source reads", err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	lockEmpty(t, e)
}
