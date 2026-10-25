package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// Observe the actual tracker candidate, not goroutine arrival or elapsed time.
// Cancel only after the utility registers its real unresolved-writer dependency,
// then join it and ensure cancellation leaves no snapshot or publication behind.
func TestQueryTableCreateSnapshotAdmission(t *testing.T) {
	for _, form := range []parser.QueryTableForm{parser.CreateTableAs, parser.SelectInto} {
		for _, describe := range []bool{false, true} {
			name := map[parser.QueryTableForm]string{parser.CreateTableAs: "ctas", parser.SelectInto: "into"}[form] + map[bool]string{false: "/execute", true: "/describe"}[describe]
			t.Run(name, func(t *testing.T) {
				e := New()
				relation := serializableUnitRelation(t, e)
				writer := sessionStart(t, e, nil)
				source := parser.ObjectName{Parts: []parser.Identifier{{Text: "ssi_unit"}}}
				literal := func(n int64) parser.Expr {
					return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
				}
				preparedUnitStatement(t, e, writer, &parser.Insert{Name: source, Rows: [][]parser.Expr{{literal(0)}}})
				writerOp := transactionOperation(t, writer)
				writerRef, err := e.Begin(writerOp, session.TxOptions{Isolation: session.Serializable})
				writerOp.Done()
				if err != nil {
					t.Fatal(err)
				}
				preparedUnitStatement(t, e, writer, &parser.Update{Name: source, Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: "v"}, Value: literal(1)}}})
				writerEnded := false
				t.Cleanup(func() {
					if !writerEnded {
						transactionEnd(t, writer, writerRef, false)
					}
				})
				writerNode := e.Visibility.Serializable.Nodes[writerRef]
				if writerNode == nil || !writerNode.Captured || writerNode.Ended || writerNode.Committed || len(writer.Transactions.Writes[relation.Address]) != 1 {
					t.Fatal("writer was not actually admitted with unresolved staged data")
				}
				beforeWrites := append([]storage.RowVersion(nil), writer.Transactions.Writes[relation.Address]...)
				beforeStorage := e.Storage.Relations[relation.Address]
				beforeFrontier, beforeRows, beforeVersions := e.Visibility.CommitSeq(), e.Storage.RowFrontier(), e.Storage.VersionFrontier()
				beforeCatalog := e.Catalog.State
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				reader, node := serializableUnitAcquisition(t, e, ctx, true, true)
				scope, err := e.StartStatement(reader.Op)
				if err != nil {
					t.Fatal(err)
				}
				statement := &parser.CreateQueryTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "query_admission_target"}}}, Form: form, Query: &parser.Select{Targets: []parser.Expr{literal(1)}, Labels: []parser.Identifier{{Text: "i"}}}}
				done := make(chan error, 1)
				joined := false
				t.Cleanup(func() {
					cancel()
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("utility admission worker did not join")
						}
					}
				})
				go func() {
					var err error
					if describe {
						_, err = scope.Describe(statement)
					} else {
						result, executeErr := scope.Execute(statement, 0, nil)
						err = executeErr
						if result.Rows != nil {
							_ = result.Rows.Close()
						}
					}
					done <- scope.Finish(err)
				}()
				deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				var outcome error
				waiting := false
				for !joined {
					select {
					case outcome = <-done:
						joined = true
					default:
					}
					if joined {
						break
					}
					e.Mu.Lock()
					_, containsWriter := node.Possible[writerNode]
					waiting = node.Ready != nil && !node.Resolved && containsWriter && !writerNode.Ended && !writerNode.Committed
					e.Mu.Unlock()
					if waiting {
						cancel()
						select {
						case outcome = <-done:
							joined = true
						case <-deadline.Done():
							t.Fatal("admitted utility did not leave canceled safe-snapshot wait")
						}
						break
					}
					if deadline.Err() != nil {
						t.Fatal("utility neither completed nor registered a real safe-snapshot candidate")
					}
					runtime.Gosched()
				}
				if !waiting || !errors.Is(outcome, context.Canceled) {
					t.Errorf("utility safe-snapshot wait=%t: got %v, want cancellation after admission", waiting, outcome)
				}
				snapshot := e.Visibility.Entries[node.Ref]
				if node.Captured || node.Ready != nil || len(node.Possible) != 0 || reader.Owner.SnapshotSet || len(reader.Owner.Roots) != 0 || reader.Owner.CurrentSnapshot != nil || reader.Owner.SequenceCatalog != nil || reader.Owner.Frontier != 0 || snapshot.Snapshot || snapshot.Xmin != 0 || snapshot.SafeBuilder {
					t.Error("canceled utility retained candidate/root/snapshot evidence")
				}
				if reader.Owner.State != session.TxAborted || writerNode.Ended || writerNode.Committed || !reflect.DeepEqual(beforeWrites, writer.Transactions.Writes[relation.Address]) || e.Catalog.State != beforeCatalog || e.Storage.Relations[relation.Address] != beforeStorage || e.Visibility.CommitSeq() != beforeFrontier || e.Storage.RowFrontier() != beforeRows || e.Storage.VersionFrontier() != beforeVersions {
					t.Error("utility changed writer/source/publication state or did not abort its owner")
				}
				reader.Op.Done()
				transactionEnd(t, reader.Op.Session, node.Ref, false)
				if reader.Owner.State != session.TxIdle || len(reader.Owner.Roots) != 0 || !node.Ended {
					t.Fatal("rollback did not retire the canceled owner")
				}
				preparedUnitStatement(t, e, reader.Op.Session, &parser.Select{Targets: []parser.Expr{literal(1)}})
				// Ordinary SELECT still waits on this exact writer and resumes only
				// after its rollback; canceled utility admission cannot disable it.
				controlContext, controlCancel := context.WithCancel(context.Background())
				t.Cleanup(controlCancel)
				control, controlNode := serializableUnitAcquisition(t, e, controlContext, true, true)
				controlScope, err := e.StartStatement(control.Op)
				if err != nil {
					t.Fatal(err)
				}
				controlDone := make(chan error, 1)
				controlJoined := false
				t.Cleanup(func() {
					controlCancel()
					if !controlJoined {
						select {
						case <-controlDone:
						case <-time.After(5 * time.Second):
							t.Error("ordinary snapshot control did not join")
						}
					}
				})
				go func() {
					result, err := controlScope.Execute(&parser.Select{Targets: []parser.Expr{literal(1)}}, 0, nil)
					if err == nil {
						if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
							err = errors.New("ordinary snapshot control changed its selected value")
						}
					}
					if result.Rows != nil {
						_ = result.Rows.Close()
					}
					controlDone <- controlScope.Finish(err)
				}()
				serializableUnitAwaitCandidate(t, e, controlNode)
				e.Mu.Lock()
				_, containsWriter := controlNode.Possible[writerNode]
				registered := containsWriter && len(controlNode.Possible) == 1 && !controlNode.Resolved && !control.Owner.SnapshotSet
				e.Mu.Unlock()
				if !registered {
					t.Fatal("ordinary SELECT control did not retain the actual unresolved writer")
				}
				transactionEnd(t, writer, writerRef, false)
				writerEnded = true
				select {
				case err := <-controlDone:
					controlJoined = true
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("ordinary SELECT did not resume after writer rollback")
				}
				if !control.Owner.SnapshotSet || !controlNode.Resolved || !writerNode.Ended || e.Storage.Relations[relation.Address] != beforeStorage || e.Visibility.CommitSeq() != beforeFrontier {
					t.Fatal("snapshot control or writer rollback changed committed source state")
				}
				control.Op.Done()
				transactionEnd(t, control.Op.Session, controlNode.Ref, false)
			})
		}
	}
}
