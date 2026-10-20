package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
)

func preparedUnitStatement(t *testing.T, e *Engine, s *Session, stmt parser.Statement) {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(stmt, 0, nil)
	err = scope.Finish(err)
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreparedSerializableCandidateRetainsAdmittedSnapshot(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			relation := serializableUnitRelation(t, e)
			writer, finisher := sessionStart(t, e, nil), sessionStart(t, e, nil)
			name := parser.ObjectName{Parts: []parser.Identifier{{Text: "ssi_unit"}}}
			literal := func(n int64) parser.Expr {
				return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
			}
			preparedUnitStatement(t, e, writer, &parser.Insert{Name: name, Rows: [][]parser.Expr{{literal(0)}}})
			op := transactionOperation(t, writer)
			_, err := e.Begin(op, TxOptions{Isolation: Serializable})
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			preparedUnitStatement(t, e, writer, &parser.Update{Name: name, Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: "v"}, Value: literal(1)}}})
			reader, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
			join := serializableUnitCaptureWorker(t, reader)
			serializableUnitAwaitCandidate(t, e, node)
			e.mu.Lock()
			original := reader.owner.roots[relation.Address]
			frontier := reader.owner.frontier
			e.mu.Unlock()
			if err := preparedUnitControl(t, e, writer, parser.KindPrepareTransaction, "candidate"); err != nil {
				t.Fatal(err)
			}
			writer.RequestEnd(CleanClose)
			if err := writer.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			if node.Resolved || len(node.Possible) != 1 || reader.owner.snapshotSet || reader.owner.frontier != frontier || reader.owner.roots[relation.Address].Storage != original.Storage {
				e.mu.Unlock()
				t.Fatal("prepare/origin end prematurely resolved or replaced admitted candidate")
			}
			e.mu.Unlock()
			kind := parser.KindRollbackPrepared
			if commit {
				kind = parser.KindCommitPrepared
			}
			if err := preparedUnitControl(t, e, finisher, kind, "candidate"); err != nil {
				t.Fatal(err)
			}
			if err := join(); err != nil {
				t.Fatal(err)
			}
			reader.owner.mu.Lock()
			root := reader.owner.roots[relation.Address]
			if !reader.owner.snapshotSet || reader.owner.frontier != frontier || root.Storage != original.Storage {
				reader.owner.mu.Unlock()
				t.Fatal("final outcome replaced admitted snapshot")
			}
			reader.owner.mu.Unlock()
			source, err := SelectQuerySource(context.Background(), &querySource{Committed: root.Versions}, frontier, root.Ends, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			if source.Count() != 1 {
				t.Fatal("admitted snapshot row count", source.Count())
			}
			value, err := source.VersionAt(source.RawIndex(0)).Values[0].Integer()
			if err != nil || value != 0 {
				t.Fatal("admitted snapshot did not retain predecessor0", value, err)
			}
			e.mu.Lock()
			current := e.storage.Relations[relation.Address]
			published, err := SelectQuerySource(context.Background(), &querySource{Committed: current.Versions}, e.visibility.CommitSeq(), current.Ends, nil)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer published.Close()
			expected := int64(0)
			if commit {
				expected = 1
			}
			value, err = published.VersionAt(published.RawIndex(0)).Values[0].Integer()
			if err != nil || value != expected {
				t.Fatal("actual prepared outcome", value, expected, err)
			}
		})
	}
}

func TestPreparedTemporarySemanticDescriptionIsSticky(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "semantic_temp"}}}
	preparedUnitStatement(t, e, s, &parser.CreateSequence{Name: name, Persistence: "temporary"})
	op := transactionOperation(t, s)
	_, err := e.Begin(op, TxOptions{})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	op = transactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	query := &parser.Select{From: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_temp"}, {Text: "semantic_temp"}}}}, Targets: []parser.Expr{parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "last_value"}}}}}}
	_, err = scope.Describe(query)
	err = scope.Finish(err)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	if !s.transactions.accessedTemporary {
		t.Fatal("semantic description omitted actual temporary namespace access")
	}
	if err := preparedUnitControl(t, e, s, parser.KindPrepareTransaction, "semantic"); err == nil {
		t.Fatal("semantic temporary access allowed prepare")
	}
	if s.TransactionState() != TxIdle || len(e.prepared) != 0 {
		t.Fatal("semantic prepare failure did not fully dispose transaction")
	}
}
