package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Observe the actual tracker candidate, not goroutine arrival or elapsed time.
// Cancel only after the utility registers its real unresolved-writer dependency,
// then join it and ensure cancellation leaves no snapshot or publication behind.
func TestQueryTableCreateSnapshotAdmission(t *testing.T) {
	for _, form := range []parser.QueryTableForm{parser.CreateTableAs, parser.SelectInto} {
		for _, describe := range []bool{false, true} {
			name := map[parser.QueryTableForm]string{parser.CreateTableAs: "ctas", parser.SelectInto: "into"}[form] + map[bool]string{false: "/execute", true: "/describe"}[describe]
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				relation := serializableUnitRelation(t, e)
				writer := enginetest.StartSession(t, e, nil)
				source := parser.ObjectName{Parts: []parser.Identifier{{Text: "ssi_unit"}}}
				literal := func(n int64) parser.Expr {
					return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
				}
				preparedUnitStatement(t, e, writer, &parser.Insert{Name: source, Rows: [][]parser.Expr{{literal(0)}}})
				writerOp := enginetest.TransactionOperation(t, writer)
				writerRef, err := e.Begin(writerOp, session.TxOptions{Isolation: session.Serializable})
				writerOp.Done()
				if err != nil {
					t.Fatal(err)
				}
				preparedUnitStatement(t, e, writer, &parser.Update{Name: source, Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: "v"}, Value: literal(1)}}})
				writerEnded := false
				t.Cleanup(func() {
					if !writerEnded {
						enginetest.TransactionEnd(t, writer, writerRef, false)
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
				enginetest.TransactionEnd(t, reader.Op.Session, node.Ref, false)
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
						if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
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
				enginetest.TransactionEnd(t, writer, writerRef, false)
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
				enginetest.TransactionEnd(t, control.Op.Session, controlNode.Ref, false)
			})
		}
	}
}

func serializableUnitRelation(t *testing.T, e *core.Engine) ids.ObjectRef {
	t.Helper()
	base := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}
	d := &catalog.Relation{ObjectAddress: address, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "ssi_unit", ObjectRevision: 1, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "v", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}

func preparedUnitStatement(t *testing.T, e *core.Engine, s *session.Session, stmt parser.Statement) {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
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

func serializableUnitAcquisition(t *testing.T, e *core.Engine, parent context.Context, readOnly, deferrable bool) (*core.StatementScope, *visibility.SerializableTx) {
	t.Helper()
	sess := enginetest.StartSession(t, e, nil)
	op, err := sess.Acquire(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	ref, err := e.Begin(op, session.TxOptions{Isolation: session.Serializable, ReadOnly: readOnly, Deferrable: deferrable, DeferrableSet: true})
	if err != nil {
		t.Fatal(err)
	}
	return &core.StatementScope{Owner: sess.Transactions, Op: op, TxRef: ref}, e.Visibility.Serializable.Nodes[ref]
}

func serializableUnitAwaitCandidate(t *testing.T, e *core.Engine, node *visibility.SerializableTx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		e.Mu.Lock()
		ready := node.Ready != nil && !node.Resolved && len(node.Possible) > 0
		e.Mu.Unlock()
		if ready {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("candidate registration", err)
		}
		runtime.Gosched()
	}
}

// Fixture identities use the real allocator: bootstrap definitions can occupy
// user-range OIDs without becoming relation descriptors.
func catalogUnitOID(t *testing.T, e *core.Engine) ids.OID {
	t.Helper()
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return oid
}
