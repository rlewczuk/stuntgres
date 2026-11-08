package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func queryTemporarySource(t *testing.T, e *core.Engine) ids.ObjectAddress {
	t.Helper()
	stage, source := queryCreateStage(t, e, "on_commit_source", 1)
	one, err := sqltype.Integer(source.Columns[0].Usage.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(source.Columns[0].Usage.Type, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.StageQueryTableRows(context.Background(), stage, source, [][]sqltype.Value{{one}, {two}}, enginetest.CatalogTransaction(t, e)); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return source.Address()
}

func TestQueryTableCreateOnCommitPrecedence(t *testing.T) {
	for _, persistence := range []string{"", "UNLOGGED "} {
		for _, action := range []string{"PRESERVE ROWS", "DELETE ROWS", "DROP"} {
			for _, prepared := range []bool{false, true} {
				for _, witness := range []struct {
					name, query, code, message string
					noData, skip               bool
				}{
					{"planning", "SELECT 1/0 AS v", "22012", "division by zero", false, false},
					{"planned", "SELECT 1 AS v", "42P16", "ON COMMIT can only be used on temporary tables", false, false},
					{"runtime", "SELECT 1/(i-i) AS v FROM on_commit_source", "42P16", "ON COMMIT can only be used on temporary tables", false, false},
					{"sequence", "SELECT nextval('on_commit_sequence') AS v FROM on_commit_source", "42P16", "ON COMMIT can only be used on temporary tables", false, false},
					{"no_data", "SELECT 1/0 AS v", "42P16", "ON COMMIT can only be used on temporary tables", true, false},
					{"cte_planning", "WITH changed AS (UPDATE on_commit_source SET i=i+1 RETURNING i) SELECT 1/0 AS v FROM changed", "22012", "division by zero", false, false},
					{"cte", "WITH changed AS (UPDATE on_commit_source SET i=i+1 RETURNING i) SELECT i FROM changed", "42P16", "ON COMMIT can only be used on temporary tables", false, false},
					{"cte_no_data", "WITH changed AS (UPDATE on_commit_source SET i=i+1 RETURNING i) SELECT 1/0 AS v FROM changed", "42P16", "ON COMMIT can only be used on temporary tables", true, false},
					{"skip_planning", "SELECT 1/0 AS v", "", "", false, true},
					{"skip_cte", "WITH changed AS (UPDATE on_commit_source SET i=i+1 RETURNING i) SELECT nextval('on_commit_sequence') AS v FROM changed", "", "", false, true},
				} {
					name := persistence + action + "/" + witness.name
					if prepared {
						name += "/prepared"
					} else {
						name += "/direct"
					}
					t.Run(name, func(t *testing.T) {
						e := engine.New()
						s := enginetest.StartSession(t, e, nil)
						source := queryTemporarySource(t, e)
						queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE SEQUENCE on_commit_sequence START 10"))
						sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "on_commit_sequence")
						if witness.skip {
							existing, _ := queryCreateStage(t, e, "on_commit_copy", 1)
							queryCreatePublish(t, e, existing)
						}
						syntax := enginetest.Statement(t, "CREATE "+persistence+"TABLE on_commit_copy ON COMMIT "+action+" AS "+witness.query).(*parser.CreateQueryTable)
						syntax.NoData, syntax.IfNotExists = witness.noData, witness.skip
						before, types := e.Catalog.State, queryCreateTypes(e)
						store := e.Storage.Relations[source]
						root, ends := store.Versions, store.Ends
						rows := append(root[:0:0], root...)
						for i := range rows {
							rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
						}
						generator := *e.Storage.Sequences[sequence.Address]
						rowCounter, versionCounter, frontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier(), e.Visibility.CommitSeq()
						var description core.Description
						if prepared {
							op, scope := queryTemporaryScope(t, e, s, context.Background())
							var err error
							description, err = scope.Describe(syntax)
							err = scope.Finish(err)
							op.Done()
							if err != nil || e.Catalog.State != before || s.TemporaryNamespace != (ids.ObjectAddress{}) || e.Storage.RowFrontier() != rowCounter || e.Storage.VersionFrontier() != versionCounter || *e.Storage.Sequences[sequence.Address] != generator {
								t.Fatal("effect-free Prepare performed destination semantics or source work", err)
							}
						}
						op, scope := queryTemporaryScope(t, e, s, context.Background())
						if prepared {
							scope.Expected = &description
						}
						result, err := scope.Execute(syntax, 0, nil)
						if witness.skip {
							if err != nil || result.Count != 0 || result.Rows != nil || scope.Catalog != nil || len(scope.Stage.Rows) != 0 {
								t.Fatal("existing target reached planning/check/runtime", result, err)
							}
						} else {
							var diagnostic *sqlstate.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != witness.code || diagnostic.Message != witness.message || diagnostic.Position != "" || len(scope.Stage.Rows) != 0 || len(scope.Stage.Deletes) != 0 {
								t.Fatal("destination check crossed planning/runtime frontier", err, scope.Stage.Rows, scope.Stage.Deletes)
							}
						}
						finishErr := scope.Finish(err)
						op.Done()
						if !errors.Is(finishErr, err) {
							t.Fatal("Finish changed primary failure", err, finishErr)
						}
						if e.Catalog.State != before || e.Storage.Relations[source] != store || &store.Versions[0] != &root[0] || store.Ends != ends || !reflect.DeepEqual(store.Versions, rows) || *e.Storage.Sequences[sequence.Address] != generator || e.Storage.RowFrontier() != rowCounter || e.Storage.VersionFrontier() != versionCounter || e.Visibility.CommitSeq() != frontier || s.TemporaryNamespace != (ids.ObjectAddress{}) || s.Transactions.Catalog != nil || core.PayloadOf(s).Sequences.Len() != 0 {
							t.Fatal("failed or skipped CTAS changed source/catalog/namespace/producer roots")
						}
						queryCreateAssertTypes(t, e, types)
					})
				}
			}
		}
	}
}

func TestQueryTableCreateTemporaryFailedRoots(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, cancellation := range []bool{false, true} {
			name := "runtime"
			if cancellation {
				name = "cancel_before_finish"
			}
			if explicit {
				name += "/explicit"
			} else {
				name += "/implicit"
			}
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				source := queryTemporarySource(t, e)
				var ref ids.TxRef
				if explicit {
					ref = enginetest.TransactionBegin(t, s)
				}
				before, types := e.Catalog.State, queryCreateTypes(e)
				store := e.Storage.Relations[source]
				root, ends := store.Versions, store.Ends
				rows := append(root[:0:0], root...)
				for i := range rows {
					rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
				}
				frontier := e.Visibility.CommitSeq()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, scope := queryTemporaryScope(t, e, s, ctx)
				query := "CREATE TEMP TABLE failed_copy ON COMMIT DROP AS SELECT 1/(i-2) AS v FROM on_commit_source"
				if cancellation {
					query = "CREATE TEMP TABLE failed_copy ON COMMIT DROP AS SELECT i AS v FROM on_commit_source"
				}
				_, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
				if cancellation {
					if err != nil || scope.Catalog == nil || scope.Completed || len(scope.Stage.Rows) == 0 {
						t.Fatal("missing real unpublished destination", err)
					}
					cancel()
				} else {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || len(scope.Stage.Rows) != 0 {
						t.Fatal("runtime fixture did not fail before row transfer", err)
					}
				}
				rowCounter, versionCounter := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
				candidate := scope.Catalog
				err = scope.Finish(err)
				op.Done()
				if cancellation && !errors.Is(err, context.Canceled) {
					t.Fatal("canceled Finish published destination", err)
				}
				if e.Catalog.State != before || e.Storage.Relations[source] != store || &store.Versions[0] != &root[0] || store.Ends != ends || !reflect.DeepEqual(store.Versions, rows) || e.Visibility.CommitSeq() != frontier || e.Storage.RowFrontier() != rowCounter || e.Storage.VersionFrontier() != versionCounter || s.TemporaryNamespace != (ids.ObjectAddress{}) || s.Transactions.Catalog != nil {
					t.Fatal("failed source/publication mutated committed roots or first namespace")
				}
				queryCreateAssertTypes(t, e, types)
				if candidate == nil {
					t.Fatal("failure did not exercise actual TEMP destination preparation")
				}
				for address := range candidate.Descriptors {
					if before.Get(address) == nil && (e.CatalogRead().Get(address) != nil || e.Storage.Relations[address] != nil) {
						t.Fatal("private namespace/table/type leaked", address)
					}
				}
				if explicit {
					if s.Transactions.State != session.TxAborted {
						t.Fatal("failed explicit statement did not abort block")
					}
					enginetest.TransactionEnd(t, s, ref, false)
				}
				queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE failed_copy AS SELECT 3::integer AS v WITH NO DATA"))
				if queryTemporaryRelation(t, e.CatalogRead(), s.SessionToken, "failed_copy").OnCommit != parser.OnCommitUnspecified {
					t.Fatal("abandoned DROP action survived name reuse")
				}
			})
		}
	}
}

func TestQueryTableCreateTemporaryResolvedAction(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, s)
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TABLE pg_temp.resolved_copy ON COMMIT DROP AS SELECT 7::integer AS v"))
	d := queryTemporaryRelation(t, e.PrivateCatalogLocked(s.Transactions.Catalog), s.SessionToken, "resolved_copy")
	if d.Persistence != "temporary" || d.OnCommit != parser.OnCommitDrop || len(s.Transactions.Writes[d.Address()]) != 1 || s.TemporaryNamespace != (ids.ObjectAddress{}) {
		t.Fatal("semantic check used requested rather than resolved persistence", d)
	}
	enginetest.TransactionEnd(t, s, ref, true)
	queryCreateAssertAbsent(t, e, d)
}
