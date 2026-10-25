package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func truncateUnitAST(names ...string) *parser.Truncate {
	stmt := &parser.Truncate{}
	for _, name := range names {
		stmt.Targets = append(stmt.Targets, parser.LockTarget{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}})
	}
	return stmt
}
func truncateUnitFixture(t *testing.T) (*engine.Engine, *session.Session, ids.ObjectRef, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE q(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1),(2)", "INSERT INTO q VALUES(7)"} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s, enginetest.RoleTableRef(t, e, "p"), enginetest.RoleTableRef(t, e, "q")
}
func truncateUnitScope(t *testing.T, e *engine.Engine, s *session.Session, ctx context.Context) (*engine.StatementScope, *session.Operation) {
	t.Helper()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(errors.New("truncate unit cleanup")); op.Done() })
	return scope, op
}
func truncateUnitStage(t *testing.T, e *engine.Engine, s *session.Session, names ...string) *engine.StatementScope {
	t.Helper()
	scope, _ := truncateUnitScope(t, e, s, context.Background())
	if _, err := scope.Execute(truncateUnitAST(names...), 0, nil); err != nil {
		t.Fatal(err)
	}
	return scope
}
func truncateUnitExecute(t *testing.T, e *engine.Engine, s *session.Session, stmt *parser.Truncate) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	if _, err = e.Execute(op, stmt, 0, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTruncateGeneration(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e, s, p, q := truncateUnitFixture(t)
			originalP, originalQ := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address]
			versions, ends, generation := originalP.Versions, originalP.Ends, originalP.Generation
			descriptor, frontier := e.Catalog.State.Get(p.Address), e.Visibility.CommitSeq()
			scope := truncateUnitStage(t, e, s, "p", "q")
			for _, ref := range []ids.ObjectRef{p, q} {
				shape := scope.Catalog.Storage[ref.Address]
				if shape.Original != e.Storage.Relations[ref.Address] || len(shape.Replacement.Versions) != 0 || shape.Replacement.Ends != nil || !shape.Truncated || !shape.Rewritten || shape.Replacement.Generation != shape.Original.Generation+1 {
					t.Fatal("not an empty generation replacement", shape)
				}
				if shape.Relation != e.Catalog.State.Get(ref.Address) {
					t.Fatal("descriptor copied/reminted")
				}
			}
			if e.Storage.Relations[p.Address] != originalP || e.Storage.Relations[q.Address] != originalQ || originalP.TruncateLineage != nil || originalQ.TruncateLineage != nil || e.Visibility.CommitSeq() != frontier {
				t.Fatal("staging published or stamped predecessor")
			}
			var err error
			if !commit {
				err = context.Canceled
			}
			if got := scope.Finish(err); !errors.Is(got, err) {
				t.Fatal(got, err)
			}
			if !storage.SameVersionRoot(originalP.Versions, versions) || originalP.Ends != ends || originalP.Generation != generation || e.Catalog.State.Get(p.Address) != descriptor {
				t.Fatal("retained predecessor/definition changed")
			}
			if commit {
				if e.Storage.Relations[p.Address] == originalP || e.Storage.Relations[q.Address] == originalQ || !originalP.SameTruncateLineage(e.Storage.Relations[p.Address]) || e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal("not one atomic frontier")
				}
			} else if e.Storage.Relations[p.Address] != originalP || e.Storage.Relations[q.Address] != originalQ || originalP.TruncateLineage != nil || e.Visibility.CommitSeq() != frontier {
				t.Fatal("rollback changed committed lineage/roots")
			}
		})
	}
}
func TestTruncateIndexes(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	idx := enginetest.RoleTableRef(t, e, "p_pkey")
	retained := e.Storage.IndexRoots[idx.Address]
	tx := enginetest.TransactionBegin(t, s)
	scope := truncateUnitStage(t, e, s, "p")
	candidate := index.JournalIndexesOrZero(scope.Catalog).UniqueRoots[idx.Address]
	if candidate == nil || len(candidate.Entries) != 0 || len(retained.Entries) != 2 || e.Storage.IndexRoots[idx.Address] != retained {
		t.Fatal("private index root not rebuilt independently")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.Op.Done()
	shape := s.Transactions.Catalog.Storage[p.Address]
	if e.Storage.Relations[p.Address] == shape.Replacement {
		t.Fatal("explicit generation published before commit")
	}
	enginetest.TransactionEnd(t, s, tx, true)
	if len(e.Storage.IndexRoots[idx.Address].Entries) != 0 || len(retained.Entries) != 2 {
		t.Fatal("live/retained index roots confused")
	}
}
func TestTruncatePrivateRoots(t *testing.T) {
	e, s, p, q := truncateUnitFixture(t)
	tx := enginetest.TransactionBegin(t, s)
	for _, q := range []string{"UPDATE p SET id=11 WHERE id=1", "DELETE FROM p WHERE id=2", "INSERT INTO p VALUES(3)", "INSERT INTO q VALUES(8)"} {
		enginetest.SessionSQL(t, s, q)
	}
	pWrites, pDeletes := s.Transactions.Writes[p.Address], s.Transactions.Deletes[p.Address]
	qWrites := s.Transactions.Writes[q.Address]
	original := e.Storage.Relations[p.Address]
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	first := s.Transactions.Catalog.Storage[p.Address]
	if len(s.Transactions.Writes[p.Address]) != 0 || s.Transactions.Deletes[p.Address] != nil || !storage.SameVersionRoot(s.Transactions.Writes[q.Address], qWrites) {
		t.Fatal("selected roots not retired or unrelated roots lost")
	}
	if len(pWrites) == 0 || pDeletes == nil {
		t.Fatal("fixture did not own real write/delete roots")
	}
	enginetest.SessionSQL(t, s, "INSERT INTO p VALUES(4)")
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	second := s.Transactions.Catalog.Storage[p.Address]
	if second.Original != original || second.Replacement == first.Replacement || second.Replacement.TruncateLineage != first.Replacement.TruncateLineage || !storage.SameVersionRoot(second.Root, first.Root) || second.Ends != first.Ends || second.Generation != first.Generation || len(s.Transactions.Writes[p.Address]) != 0 {
		t.Fatal("repeat did not retain first committed fences/retire latest writes")
	}
	enginetest.TransactionEnd(t, s, tx, false)
	if original.TruncateLineage != nil || len(original.Versions) != 2 {
		t.Fatal("private lineage escaped rollback")
	}
}
func TestTruncateSavepoints(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "INSERT INTO p VALUES(3)")
	enginetest.SessionSQL(t, s, "SAVEPOINT before")
	beforeWrites := s.Transactions.Writes[p.Address]
	beforeCatalog := s.Transactions.Catalog
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	first := s.Transactions.Catalog.Storage[p.Address].Replacement
	enginetest.SessionSQL(t, s, "SAVEPOINT empty")
	enginetest.SessionSQL(t, s, "INSERT INTO p VALUES(4)")
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	enginetest.SessionSQL(t, s, "ROLLBACK TO empty")
	if s.Transactions.Catalog.Storage[p.Address].Replacement != first || len(s.Transactions.Writes[p.Address]) != 0 {
		t.Fatal("private empty incarnation not restored")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO before")
	if s.Transactions.Catalog != beforeCatalog || !storage.SameVersionRoot(s.Transactions.Writes[p.Address], beforeWrites) {
		t.Fatal("savepoint did not restore exact prior catalog/write roots")
	}
	enginetest.TransactionEnd(t, s, tx, false)
}
func TestTruncateProvenance(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	first := e.Storage.Relations[p.Address]
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	second := e.Storage.Relations[p.Address]
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	third := e.Storage.Relations[p.Address]
	if !first.SameTruncateLineage(second) || !first.SameTruncateLineage(third) {
		t.Fatal("successive truncations lost bounded lineage")
	}
	retained := slices.Clone(first.Versions)
	enginetest.SessionSQL(t, s, "ALTER TABLE p ADD note TEXT")
	ordinary := e.Storage.Relations[p.Address]
	if ordinary.TruncateLineage != nil || first.SameTruncateLineage(ordinary) {
		t.Fatal("ordinary rewrite bridged truncate provenance")
	}
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	if first.SameTruncateLineage(e.Storage.Relations[p.Address]) || !ordinary.SameTruncateLineage(e.Storage.Relations[p.Address]) || len(first.Versions) != len(retained) {
		t.Fatal("new incarnation reused retired lineage or old rows changed")
	}
	// A non-TRUNCATE replacement inside the same private overlay also breaks
	// continuity; a later private TRUNCATE must not restamp the committed head.
	tx := enginetest.TransactionBegin(t, s)
	committed := e.Storage.Relations[p.Address]
	token := committed.TruncateLineage
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	enginetest.SessionSQL(t, s, "ALTER TABLE p ADD extra INT")
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	enginetest.TransactionEnd(t, s, tx, true)
	if committed.TruncateLineage != token || committed.SameTruncateLineage(e.Storage.Relations[p.Address]) {
		t.Fatal("private ordinary rewrite silently bridged lineage")
	}
}
