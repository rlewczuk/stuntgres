package engine

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func truncateUnitAST(names ...string) *parser.Truncate {
	stmt := &parser.Truncate{}
	for _, name := range names {
		stmt.Targets = append(stmt.Targets, parser.LockTarget{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}})
	}
	return stmt
}
func truncateUnitFixture(t *testing.T) (*Engine, *Session, ids.ObjectRef, ids.ObjectRef) {
	t.Helper()
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE q(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1),(2)", "INSERT INTO q VALUES(7)"} {
		sessionSQL(t, s, q)
	}
	return e, s, roleTableRef(t, e, "p"), roleTableRef(t, e, "q")
}
func truncateUnitScope(t *testing.T, e *Engine, s *Session, ctx context.Context) (*StatementScope, *Operation) {
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
func truncateUnitStage(t *testing.T, e *Engine, s *Session, names ...string) *StatementScope {
	t.Helper()
	scope, _ := truncateUnitScope(t, e, s, context.Background())
	if _, err := scope.Execute(truncateUnitAST(names...), 0, nil); err != nil {
		t.Fatal(err)
	}
	return scope
}
func truncateUnitExecute(t *testing.T, e *Engine, s *Session, stmt *parser.Truncate) {
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
			originalP, originalQ := e.storage.Relations[p.Address], e.storage.Relations[q.Address]
			versions, ends, generation := originalP.Versions, originalP.Ends, originalP.Generation
			descriptor, frontier := e.catalog.state.Get(p.Address), e.visibility.CommitSeq()
			scope := truncateUnitStage(t, e, s, "p", "q")
			for _, ref := range []ids.ObjectRef{p, q} {
				shape := scope.catalog.storage[ref.Address]
				if shape.original != e.storage.Relations[ref.Address] || len(shape.replacement.Versions) != 0 || shape.replacement.Ends != nil || !shape.truncated || !shape.rewritten || shape.replacement.Generation != shape.original.Generation+1 {
					t.Fatal("not an empty generation replacement", shape)
				}
				if shape.relation != e.catalog.state.Get(ref.Address) {
					t.Fatal("descriptor copied/reminted")
				}
			}
			if e.storage.Relations[p.Address] != originalP || e.storage.Relations[q.Address] != originalQ || originalP.TruncateLineage != nil || originalQ.TruncateLineage != nil || e.visibility.CommitSeq() != frontier {
				t.Fatal("staging published or stamped predecessor")
			}
			var err error
			if !commit {
				err = context.Canceled
			}
			if got := scope.Finish(err); !errors.Is(got, err) {
				t.Fatal(got, err)
			}
			if !sameVersionRoot(originalP.Versions, versions) || originalP.Ends != ends || originalP.Generation != generation || e.catalog.state.Get(p.Address) != descriptor {
				t.Fatal("retained predecessor/definition changed")
			}
			if commit {
				if e.storage.Relations[p.Address] == originalP || e.storage.Relations[q.Address] == originalQ || !originalP.SameTruncateLineage(e.storage.Relations[p.Address]) || e.visibility.CommitSeq() != frontier+1 {
					t.Fatal("not one atomic frontier")
				}
			} else if e.storage.Relations[p.Address] != originalP || e.storage.Relations[q.Address] != originalQ || originalP.TruncateLineage != nil || e.visibility.CommitSeq() != frontier {
				t.Fatal("rollback changed committed lineage/roots")
			}
		})
	}
}
func TestTruncateIndexes(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	index := roleTableRef(t, e, "p_pkey")
	retained := e.storage.IndexRoots[index.Address]
	tx := transactionBegin(t, s)
	scope := truncateUnitStage(t, e, s, "p")
	candidate := scope.catalog.uniqueRoots[index.Address]
	if candidate == nil || len(candidate.Entries) != 0 || len(retained.Entries) != 2 || e.storage.IndexRoots[index.Address] != retained {
		t.Fatal("private index root not rebuilt independently")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.op.Done()
	shape := s.transactions.catalog.storage[p.Address]
	if e.storage.Relations[p.Address] == shape.replacement {
		t.Fatal("explicit generation published before commit")
	}
	transactionEnd(t, s, tx, true)
	if len(e.storage.IndexRoots[index.Address].Entries) != 0 || len(retained.Entries) != 2 {
		t.Fatal("live/retained index roots confused")
	}
}
func TestTruncatePrivateRoots(t *testing.T) {
	e, s, p, q := truncateUnitFixture(t)
	tx := transactionBegin(t, s)
	for _, q := range []string{"UPDATE p SET id=11 WHERE id=1", "DELETE FROM p WHERE id=2", "INSERT INTO p VALUES(3)", "INSERT INTO q VALUES(8)"} {
		sessionSQL(t, s, q)
	}
	pWrites, pDeletes := s.transactions.writes[p.Address], s.transactions.deletes[p.Address]
	qWrites := s.transactions.writes[q.Address]
	original := e.storage.Relations[p.Address]
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	first := s.transactions.catalog.storage[p.Address]
	if len(s.transactions.writes[p.Address]) != 0 || s.transactions.deletes[p.Address] != nil || !sameVersionRoot(s.transactions.writes[q.Address], qWrites) {
		t.Fatal("selected roots not retired or unrelated roots lost")
	}
	if len(pWrites) == 0 || pDeletes == nil {
		t.Fatal("fixture did not own real write/delete roots")
	}
	sessionSQL(t, s, "INSERT INTO p VALUES(4)")
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	second := s.transactions.catalog.storage[p.Address]
	if second.original != original || second.replacement == first.replacement || second.replacement.TruncateLineage != first.replacement.TruncateLineage || !sameVersionRoot(second.root, first.root) || second.ends != first.ends || second.generation != first.generation || len(s.transactions.writes[p.Address]) != 0 {
		t.Fatal("repeat did not retain first committed fences/retire latest writes")
	}
	transactionEnd(t, s, tx, false)
	if original.TruncateLineage != nil || len(original.Versions) != 2 {
		t.Fatal("private lineage escaped rollback")
	}
}
func TestTruncateSavepoints(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "INSERT INTO p VALUES(3)")
	sessionSQL(t, s, "SAVEPOINT before")
	beforeWrites := s.transactions.writes[p.Address]
	beforeCatalog := s.transactions.catalog
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	first := s.transactions.catalog.storage[p.Address].replacement
	sessionSQL(t, s, "SAVEPOINT empty")
	sessionSQL(t, s, "INSERT INTO p VALUES(4)")
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	sessionSQL(t, s, "ROLLBACK TO empty")
	if s.transactions.catalog.storage[p.Address].replacement != first || len(s.transactions.writes[p.Address]) != 0 {
		t.Fatal("private empty incarnation not restored")
	}
	sessionSQL(t, s, "ROLLBACK TO before")
	if s.transactions.catalog != beforeCatalog || !sameVersionRoot(s.transactions.writes[p.Address], beforeWrites) {
		t.Fatal("savepoint did not restore exact prior catalog/write roots")
	}
	transactionEnd(t, s, tx, false)
}
func TestTruncateProvenance(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	first := e.storage.Relations[p.Address]
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	second := e.storage.Relations[p.Address]
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	third := e.storage.Relations[p.Address]
	if !first.SameTruncateLineage(second) || !first.SameTruncateLineage(third) {
		t.Fatal("successive truncations lost bounded lineage")
	}
	retained := slices.Clone(first.Versions)
	sessionSQL(t, s, "ALTER TABLE p ADD note TEXT")
	ordinary := e.storage.Relations[p.Address]
	if ordinary.TruncateLineage != nil || first.SameTruncateLineage(ordinary) {
		t.Fatal("ordinary rewrite bridged truncate provenance")
	}
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	if first.SameTruncateLineage(e.storage.Relations[p.Address]) || !ordinary.SameTruncateLineage(e.storage.Relations[p.Address]) || len(first.Versions) != len(retained) {
		t.Fatal("new incarnation reused retired lineage or old rows changed")
	}
	// A non-TRUNCATE replacement inside the same private overlay also breaks
	// continuity; a later private TRUNCATE must not restamp the committed head.
	tx := transactionBegin(t, s)
	committed := e.storage.Relations[p.Address]
	token := committed.TruncateLineage
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	sessionSQL(t, s, "ALTER TABLE p ADD extra INT")
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	transactionEnd(t, s, tx, true)
	if committed.TruncateLineage != token || committed.SameTruncateLineage(e.storage.Relations[p.Address]) {
		t.Fatal("private ordinary rewrite silently bridged lineage")
	}
}
