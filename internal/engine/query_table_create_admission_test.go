package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Observe the actual tracker candidate, not goroutine arrival or elapsed time.
// If the utility enters safe-snapshot admission, cancel only after its real
// unresolved-writer dependency is registered, join it, and assert the original
// utility contract rather than accepting the cancellation as success.
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
				writerRef, err := e.Begin(writerOp, TxOptions{Isolation: Serializable})
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
				writerNode := e.visibility.Serializable.Nodes[writerRef]
				if writerNode == nil || !writerNode.Captured || writerNode.Ended || writerNode.Committed || len(writer.transactions.writes[relation.Address]) != 1 {
					t.Fatal("writer was not actually admitted with unresolved staged data")
				}
				beforeWrites := append([]storage.RowVersion(nil), writer.transactions.writes[relation.Address]...)
				beforeStorage := e.storage.Relations[relation.Address]
				beforeFrontier, beforeRows, beforeVersions := e.visibility.commitSeq, e.storage.RowFrontier(), e.storage.VersionFrontier()
				beforeCatalog := e.catalog.state
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				reader, node := serializableUnitAcquisition(t, e, ctx, true, true)
				scope, err := e.StartStatement(reader.op)
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
					e.mu.Lock()
					_, containsWriter := node.Possible[writerNode]
					waiting = node.Ready != nil && !node.Resolved && containsWriter && !writerNode.Ended && !writerNode.Committed
					e.mu.Unlock()
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
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25006", Message: "cannot execute CREATE TABLE AS in a read-only transaction"}
				if form == parser.SelectInto {
					want.Message = "cannot execute SELECT INTO in a read-only transaction"
				}
				if describe {
					want.Code, want.Message = "0A000", "statement is unsupported in an explicit transaction"
				}
				var diagnostic *sqlstate.Error
				if !errors.As(outcome, &diagnostic) || *diagnostic != want {
					t.Errorf("utility admitted to safe-snapshot wait=%t: got %v, want %+v", waiting, outcome, want)
				}
				if waiting || node.Captured || node.Ready != nil || len(node.Possible) != 0 || reader.owner.snapshotSet || len(reader.owner.roots) != 0 {
					t.Error("rejected utility acquired candidate/root/snapshot evidence")
				}
				if reader.owner.state != TxAborted || writerNode.Ended || writerNode.Committed || !reflect.DeepEqual(beforeWrites, writer.transactions.writes[relation.Address]) || e.catalog.state != beforeCatalog || e.storage.Relations[relation.Address] != beforeStorage || e.visibility.commitSeq != beforeFrontier || e.storage.RowFrontier() != beforeRows || e.storage.VersionFrontier() != beforeVersions {
					t.Error("utility changed writer/source/publication state or did not abort its owner")
				}
				reader.op.Done()
				transactionEnd(t, reader.op.session, node.Ref, false)
				if reader.owner.state != TxIdle || len(reader.owner.roots) != 0 || !node.Ended {
					t.Fatal("rollback did not retire the rejected owner")
				}
				preparedUnitStatement(t, e, reader.op.session, &parser.Select{Targets: []parser.Expr{literal(1)}})
				// A real ordinary SELECT still waits on exactly this writer and
				// resumes only after its actual rollback. Utility rejection must
				// not disable the existing safe-snapshot owner generally.
				controlContext, controlCancel := context.WithCancel(context.Background())
				t.Cleanup(controlCancel)
				control, controlNode := serializableUnitAcquisition(t, e, controlContext, true, true)
				controlScope, err := e.StartStatement(control.op)
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
				e.mu.Lock()
				_, containsWriter := controlNode.Possible[writerNode]
				registered := containsWriter && len(controlNode.Possible) == 1 && !controlNode.Resolved && !control.owner.snapshotSet
				e.mu.Unlock()
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
				if !control.owner.snapshotSet || !controlNode.Resolved || !writerNode.Ended || e.storage.Relations[relation.Address] != beforeStorage || e.visibility.commitSeq != beforeFrontier {
					t.Fatal("snapshot control or writer rollback changed committed source state")
				}
				control.op.Done()
				transactionEnd(t, control.op.session, controlNode.Ref, false)
			})
		}
	}
}
