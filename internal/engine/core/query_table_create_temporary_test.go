package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func queryTemporaryScope(t *testing.T, e *core.Engine, s *session.Session, ctx context.Context) (*session.Operation, *core.StatementScope) {
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
	t.Cleanup(func() { scope.Finish(context.Canceled); op.Done() })
	return op, scope
}

func queryTemporaryCommand(t *testing.T, e *core.Engine, s *session.Session, statement parser.Statement) core.Result {
	t.Helper()
	op, scope := queryTemporaryScope(t, e, s, context.Background())
	result, err := scope.Execute(statement, 0, nil)
	err = scope.Finish(err)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("query-table utility exposed query rows")
	}
	return result
}

func queryTemporaryRelation(t *testing.T, read catalog.Read, token ids.SessionToken, name string) *catalog.Relation {
	t.Helper()
	for _, descriptor := range read.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && d.Name() == name && read.MustNamespace(d.Namespace()).Temporary == token {
			return d
		}
	}
	t.Fatal("missing session-owned relation", name)
	return nil
}

func queryTemporaryType(t *testing.T, read catalog.Read, d *catalog.Relation) *catalog.Type {
	t.Helper()
	typ := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
	if d.RowType == 0 || typ.OwningRelation != d.Address() || typ.Namespace() != d.Namespace() || typ.Owner != d.Owner {
		t.Fatal("row type lost destination identity", typ, d)
	}
	return typ
}

func TestQueryTableCreateTemporaryDescriptor(t *testing.T) {
	for _, action := range []struct {
		clause string
		value  parser.OnCommitAction
	}{{"", parser.OnCommitUnspecified}, {" ON COMMIT PRESERVE ROWS", parser.OnCommitPreserve}, {" ON COMMIT DELETE ROWS", parser.OnCommitDelete}, {" ON COMMIT DROP", parser.OnCommitDrop}} {
		for _, demand := range []string{"no_data", "empty_data", "data"} {
			t.Run(action.clause+"/"+demand, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				op, scope := queryTemporaryScope(t, e, s, context.Background())
				syntax := enginetest.Statement(t, "CREATE TEMP TABLE private_copy"+action.clause+" AS SELECT 7::integer AS v").(*parser.CreateQueryTable)
				syntax.NoData = demand == "no_data"
				if err := scope.Gate(syntax); err != nil {
					t.Fatal(err)
				}
				before := e.Catalog.State
				types := queryCreateTypes(e)
				e.Mu.Lock()
				auth, err := e.StatementAuthorizationLocked(op, syntax)
				var stage *core.CatalogStage
				var d *catalog.Relation
				if err == nil {
					binding, bindErr := core.Describe(op.Context(), e.AnalysisHost(op), syntax, auth, ids.ObjectRef{}, scope.Input, nil, nil)
					err = bindErr
					if err == nil {
						var skipped bool
						stage, d, skipped, err = core.EnginePrepareQueryTableLocked(e, scope, auth, syntax, binding)
						if skipped {
							err = errors.New("new private destination skipped")
						}
					}
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				defer e.DiscardCatalogStage(stage)
				read := e.StageRead(stage)
				namespace := read.MustNamespace(d.Namespace())
				if scope.Catalog == nil || scope.Catalog.Descriptors[d.Namespace()] != namespace || namespace.Temporary != s.SessionToken || namespace.TemporaryToast || d.Persistence != "temporary" || d.OnCommit != action.value || stage.AuthSession != s || stage.Base.Get(namespace.Address()) != namespace {
					t.Fatal("destination did not retain its coherent private namespace/authority", d, namespace)
				}
				if len(d.Columns) != 1 || d.Columns[0].Usage != enginetest.Usage(e, sqltype.Int4) || d.Columns[0].OriginTable != uint32(d.Address().ObjectID) || d.Columns[0].OriginAttribute != 1 {
					t.Fatal("query-derived Usage or destination column identity lost", d.Columns)
				}
				queryTemporaryType(t, read, d)
				if e.Catalog.State != before || before.Get(namespace.Address()) != nil || s.TemporaryNamespace != (ids.ObjectAddress{}) || s.Transactions.Catalog != nil || e.Storage.Relations[d.Address()] != nil {
					t.Fatal("destination preparation published first TEMP state")
				}
				queryCreateAssertTypes(t, e, types)
				var rows [][]sqltype.Value
				if demand == "data" {
					value, er := sqltype.Integer(d.Columns[0].Usage.Type, 7)
					if er != nil {
						t.Fatal(er)
					}
					rows = [][]sqltype.Value{{value}}
				}
				if demand != "no_data" {
					if err := e.StageQueryTableRows(op.Context(), stage, d, rows, scope.TxRef); err != nil {
						t.Fatal(err)
					}
					if stage.InitialTx != scope.TxRef || stage.InitialTx == (ids.TxRef{}) {
						t.Fatal("demanded producer lost its real owner", stage.InitialTx)
					}
				} else if stage.InitialTx != (ids.TxRef{}) || core.StageInitialTables(stage) != nil {
					t.Fatal("NO DATA demanded a producer")
				}
				if demand == "data" && &core.StageInitialTables(stage)[d.Address()].Versions[0].Values[0] != &rows[0][0] {
					t.Fatal("detached initial row backing was copied")
				}
				if demand == "empty_data" && core.StageInitialTables(stage) != nil {
					t.Fatal("empty producer invented initial rows")
				}
				prepared, err := e.ValidateCatalogStage(op.Context(), stage)
				if err == nil {
					_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
				}
				if err != nil {
					t.Fatal(err)
				}
				change := scope.Catalog.Storage[d.Address()]
				if change.Original != nil || change.Replacement == nil || change.Relation != d || len(change.Replacement.Versions) != 0 || e.Storage.Relations[d.Address()] != nil || len(scope.Stage.Rows[d.Address()]) != len(rows) {
					t.Fatal("overlay lost honest committed absence or detached rows", change)
				}
				scope.CompleteUtility()
				if scope.Completed || s.TemporaryNamespace != (ids.ObjectAddress{}) || e.Catalog.State != before {
					t.Fatal("private destination bypassed Finish")
				}
				if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				op.Done()
				if e.Catalog.State != before || s.TemporaryNamespace != (ids.ObjectAddress{}) || s.Transactions.Catalog != nil || e.Storage.Relations[d.Address()] != nil {
					t.Fatal("discarded stage escaped its statement")
				}
				queryCreateAssertTypes(t, e, types)
			})
		}
	}
}

func TestQueryTableCreateTemporaryFinish(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, action := range []struct {
			clause string
			value  parser.OnCommitAction
		}{{"", parser.OnCommitUnspecified}, {" ON COMMIT PRESERVE ROWS", parser.OnCommitPreserve}, {" ON COMMIT DELETE ROWS", parser.OnCommitDelete}, {" ON COMMIT DROP", parser.OnCommitDrop}} {
			for _, noData := range []bool{false, true} {
				name := action.clause
				if explicit {
					name += "/explicit"
				} else {
					name += "/implicit"
				}
				if noData {
					name += "/no_data"
				} else {
					name += "/data"
				}
				t.Run(name, func(t *testing.T) {
					e := engine.New()
					s, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
					queryTemporaryCommand(t, e, peer, enginetest.Statement(t, "CREATE TEMP TABLE private_copy AS SELECT 19::integer AS v"))
					peerD := queryTemporaryRelation(t, e.CatalogRead(), peer.SessionToken, "private_copy")
					peerStore := e.Storage.Relations[peerD.Address()]
					var ref ids.TxRef
					if explicit {
						ref = enginetest.TransactionBegin(t, s)
					}
					op, scope := queryTemporaryScope(t, e, s, context.Background())
					syntax := enginetest.Statement(t, "CREATE TEMP TABLE private_copy"+action.clause+" AS SELECT 7::integer AS v UNION ALL SELECT 8::integer").(*parser.CreateQueryTable)
					syntax.NoData = noData
					before := e.Catalog.State
					result, err := scope.Execute(syntax, 0, nil)
					if err != nil {
						t.Fatal(err)
					}
					d := queryTemporaryRelation(t, e.PrivateCatalogLocked(scope.Catalog), s.SessionToken, "private_copy")
					queryTemporaryType(t, e.PrivateCatalogLocked(scope.Catalog), d)
					count := 2
					if noData {
						count = 0
					}
					retained := scope.Stage.Rows[d.Address()]
					journal := scope.Catalog
					if result.Rows != nil || result.Count != int64(count) || scope.Completed || scope.Catalog == nil || len(retained) != count || s.Transactions.Catalog != nil || e.Catalog.State != before || s.TemporaryNamespace != (ids.ObjectAddress{}) {
						t.Fatal("Execute did not retain private destination until Finish", result, scope.Completed)
					}
					if err := scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
					op.Done()
					if explicit {
						if e.Catalog.State != before || e.Storage.Relations[d.Address()] != nil || s.TemporaryNamespace != (ids.ObjectAddress{}) || len(s.Transactions.Writes[d.Address()]) != count || s.Transactions.Catalog.Descriptors[d.Address()] != d {
							t.Fatal("statement Finish ran an outer commit action")
						}
						if !enginetest.TransactionEnd(t, s, ref, true).Committed {
							t.Fatal("outer commit did not commit")
						}
					}
					if journal.Descriptors[d.Address()] != d || d.OnCommit != action.value || len(retained) != count {
						t.Fatal("commit preparation mutated retained private roots")
					}
					if action.value == parser.OnCommitDrop {
						queryCreateAssertAbsent(t, e, d)
						if e.CatalogRead().Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}) != nil {
							t.Fatal("DROP kept row type")
						}
					} else {
						store := e.Storage.Relations[d.Address()]
						want := count
						if action.value == parser.OnCommitDelete {
							want = 0
						}
						if store == nil || len(store.Versions) != want || e.CatalogRead().Get(d.Address()) != d || s.TemporaryNamespace != d.Namespace() {
							t.Fatal("outer action lost descriptor/rows", store)
						}
						queryTemporaryType(t, e.CatalogRead(), d)
					}
					if e.CatalogRead().Get(peerD.Address()) != peerD || e.Storage.Relations[peerD.Address()] != peerStore || len(peerStore.Versions) != 1 {
						t.Fatal("owner action changed peer token roots")
					}
				})
			}
		}
	}
}

func TestQueryTableCreateTemporarySavepointRoots(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, s)
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SAVEPOINT before_namespace"))
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE before_mark ON COMMIT DELETE ROWS AS SELECT 7::integer AS v"))
	first := s.Transactions.Catalog
	d := queryTemporaryRelation(t, e.PrivateCatalogLocked(first), s.SessionToken, "before_mark")
	rows := s.Transactions.Writes[d.Address()]
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SAVEPOINT after_first"))
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE after_mark ON COMMIT DROP AS SELECT 8::integer AS v"))
	later := s.Transactions.Catalog
	dropped := queryTemporaryRelation(t, e.PrivateCatalogLocked(later), s.SessionToken, "after_mark")
	if first == later || first.Descriptors[dropped.Address()] != nil || first.Descriptors[d.Address()] != d || len(rows) != 1 {
		t.Fatal("later creation mutated savepoint journal/action roots")
	}
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "ROLLBACK TO after_first"))
	if s.Transactions.Catalog.Descriptors[dropped.Address()] != nil || s.Transactions.Catalog.Descriptors[d.Address()] != d || len(s.Transactions.Writes[d.Address()]) != 1 || later.Descriptors[dropped.Address()] != dropped || dropped.OnCommit != parser.OnCommitDrop {
		t.Fatal("rollback did not restore exact private descriptor/action roots")
	}
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "ROLLBACK TO before_namespace"))
	if s.Transactions.Catalog != nil || len(s.Transactions.Writes) != 0 || s.TemporaryNamespace != (ids.ObjectAddress{}) || e.CatalogRead().Get(d.Namespace()) != nil || first.Descriptors[d.Address()] != d || len(rows) != 1 {
		t.Fatal("first namespace or retained roots escaped savepoint rollback")
	}
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE before_mark ON COMMIT DELETE ROWS AS SELECT 9::integer AS v"))
	recreated := queryTemporaryRelation(t, e.PrivateCatalogLocked(s.Transactions.Catalog), s.SessionToken, "before_mark")
	if recreated.Address() == d.Address() || recreated.Namespace() == d.Namespace() {
		t.Fatal("abandoned incarnation reused")
	}
	if !enginetest.TransactionEnd(t, s, ref, true).Committed {
		t.Fatal("outer commit failed")
	}
	if len(e.Storage.Relations[recreated.Address()].Versions) != 0 || e.CatalogRead().Get(dropped.Address()) != nil || first.Descriptors[d.Address()] != d || len(rows) != 1 {
		t.Fatal("commit lost restored action or mutated abandoned roots")
	}
	ref = enginetest.TransactionBegin(t, s)
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE after_mark ON COMMIT DROP AS SELECT 8::integer AS v"))
	rolled := queryTemporaryRelation(t, e.PrivateCatalogLocked(s.Transactions.Catalog), s.SessionToken, "after_mark")
	enginetest.TransactionEnd(t, s, ref, false)
	queryCreateAssertAbsent(t, e, rolled)
	if e.CatalogRead().Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(rolled.RowType)}) != nil || e.CatalogRead().Get(recreated.Address()) != recreated {
		t.Fatal("outer rollback kept row type or damaged predecessor")
	}
}
