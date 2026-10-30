package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/util"
)

// These fixtures enter the complete replacement owner, including real TEMP
// collision allocation, captured index roots and the admitted repair statement.
// No handcrafted remap, evaluator or checkpoint stands in for that composition.
type pausedReplacementFixture struct {
	target  *core.Engine
	owner   *session.Session
	kept    *catalog.Relation
	route   string
	retired bool
}

func newPausedReplacementFixture(t *testing.T, route string) *pausedReplacementFixture {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEMP TABLE repair_kept(v integer)")
	enginetest.SessionSQL(t, s, "INSERT INTO repair_kept VALUES(7)")
	kept := analyzeTemporaryRelation(t, e, s, "repair_kept")
	enginetest.SessionSQL(t, s, "CREATE TABLE repair_incumbent(v integer)")
	enginetest.SessionSQL(t, s, "INSERT INTO repair_incumbent VALUES(11)")
	return &pausedReplacementFixture{target: e, owner: s, kept: kept, route: route}
}

func (f *pausedReplacementFixture) source(t *testing.T) (*core.Engine, *session.Session) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	// The two actual destination TEMP namespace allocations precede its heap.
	enginetest.SessionSQL(t, s, "CREATE SCHEMA repair_padding")
	enginetest.SessionSQL(t, s, "CREATE SCHEMA repair_toast_padding")
	definition := "CREATE TABLE repair_import(payload json,g bigint GENERATED ALWAYS AS(CASE WHEN tableoid<>0::oid THEN tableoid::bigint+json_array_length(payload) ELSE 0 END) STORED)"
	if f.route == "index" {
		definition = "CREATE TABLE repair_import(payload json)"
	}
	enginetest.SessionSQL(t, s, definition)
	enginetest.SessionSQL(t, s, "INSERT INTO repair_import(payload) VALUES('[1,2]')")
	if f.route == "index" {
		// BRIN snapshots genuinely require expression-root rebuilding in Repair.
		enginetest.SessionSQL(t, s, "CREATE INDEX repair_import_index ON repair_import USING brin((json_array_length(payload)))")
	} else {
		enginetest.SessionSQL(t, s, "CREATE INDEX repair_import_index ON repair_import(g)")
	}
	imported := enginetest.CatalogRef(t, e, e.CatalogRead(), "repair_import")
	if imported.Address != f.kept.Address() {
		t.Fatal("fixture lacks actual retained TEMP/imported heap collision", imported.Address, f.kept.Address())
	}
	if f.route == "generation" {
		held := e.Storage.Relations[imported.Address].Versions[0].Values
		t.Cleanup(func() {
			generated, err := held[1].Integer()
			if err != nil || generated != int64(imported.Address.ObjectID)+2 {
				t.Error("remapped repair mutated retained source row", generated, err)
			}
		})
	}
	return e, s
}

func (f *pausedReplacementFixture) start(t *testing.T) (context.Context, *session.Operation, *util.ExecutionPause, <-chan error) {
	t.Helper()
	source, sess := f.source(t)
	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	pause := util.NewExecutionPause("json.array_length")
	ctx := util.WithExecutionPause(parent, pause)
	op, err := sess.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		op.Cancel(context.Canceled)
		pause.Resume()
		select {
		case <-finished:
		case <-parent.Done():
			t.Error("repair cleanup did not join")
		}
		op.Done()
	})
	go func() {
		_, err := f.target.ReplaceCatalogInOperation(ctx, source, source.CatalogRead(), op)
		done <- err
		close(finished)
	}()
	if err := pause.Wait(parent); err != nil {
		t.Fatal("real remapped repair never reached JSON provider", err)
	}
	return parent, op, pause, done
}

func awaitPausedReplacement(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5*time.Second):
		t.Fatal("remapped repair did not join without Resume")
		return nil
	}
}

func (f *pausedReplacementFixture) healthy(t *testing.T) {
	t.Helper()
	source, _ := f.source(t)
	if _, err := f.target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal("healthy subsequent replacement", err)
	}
	imported := enginetest.CatalogRef(t, f.target, f.target.CatalogRead(), "repair_import")
	owner := f.owner
	if f.retired {
		owner = enginetest.StartSession(t, f.target, nil)
		if f.target.CatalogRead().Get(f.kept.Address()) == f.kept {
			t.Fatal("healthy repair resurrected retired TEMP descriptor")
		}
	} else {
		if imported.Address == f.kept.Address() || f.target.CatalogRead().Get(f.kept.Address()) != f.kept {
			t.Fatal("successful repair lost collision remap or retained TEMP identity")
		}
		if rows := enginetest.Tuples(enginetest.SessionSQL(t, owner, "SELECT v FROM repair_kept")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(7)}}) {
			t.Fatal("retained TEMP row", rows)
		}
	}
	query := "SELECT json_array_length(payload) FROM repair_import"
	if f.route == "generation" {
		query = "SELECT g-tableoid::bigint FROM repair_import"
	}
	if rows := enginetest.Tuples(enginetest.SessionSQL(t, owner, query)); !reflect.DeepEqual(rows, [][]driver.Value{{int64(2)}}) {
		t.Fatal("healthy remapped generation/index result", rows)
	}
	index := enginetest.CatalogRef(t, f.target, f.target.CatalogRead(), "repair_import_index")
	root := f.target.Storage.IndexRoots[index.Address]
	store := f.target.Storage.Relations[imported.Address]
	if root == nil || store == nil || len(store.Versions) != 1 {
		t.Fatal("healthy repair did not install real heap/index root")
	}
	if f.route == "generation" {
		if len(root.Entries) != 1 || len(root.Entries[0].Keys) != 1 || root.Entries[0].Row != store.Versions[0].Row || root.Entries[0].Version != store.Versions[0].Version {
			t.Fatal("repaired B-tree lost physical row membership", root.Entries)
		}
		key, err := root.Entries[0].Keys[0].Integer()
		if err != nil || key != int64(imported.Address.ObjectID)+2 {
			t.Fatal("repaired B-tree retained old generated key", key, err)
		}
	} else {
		if root.Brin == nil || len(root.Brin.Ranges) != 1 || !root.Brin.Ranges[0].Summarized || len(root.Brin.Ranges[0].Keys) != 1 {
			t.Fatal("repaired BRIN omitted expression summary", root.Brin)
		}
		summary := root.Brin.Ranges[0].Keys[0]
		minimum, minErr := summary.Min.Integer()
		maximum, maxErr := summary.Max.Integer()
		if !summary.HasNonNull || summary.HasNull || minErr != nil || maxErr != nil || minimum != 2 || maximum != 2 {
			t.Fatal("repaired BRIN expression extrema", summary, minErr, maxErr)
		}
	}
}

func TestExecutionPauseRemappedRepairChild(t *testing.T) {
	for _, route := range []string{"generation", "index"} {
		for _, release := range []string{"cancel", "done", "request_end"} {
			t.Run(route+"/"+release, func(t *testing.T) {
				f := newPausedReplacementFixture(t, route)
				before := f.target.CatalogRead().State()
				rows := cloneGeneratedRepairStorage(f.target.Storage.Relations)
				backing := maps.Clone(f.target.Storage.Relations)
				oid, row, version := f.target.Catalog.NextOID(), f.target.Storage.RowFrontier(), f.target.Storage.VersionFrontier()
				roles, frontier, revision := f.target.RoleStore.Published, f.target.Visibility.CommitSeq(), f.target.Catalog.NextRevision()
				indexes, sequences := maps.Clone(f.target.Storage.IndexRoots), maps.Clone(f.target.Storage.Sequences)
				parent, op, _, done := f.start(t)
				child := op.Context()
				cause := error(context.Canceled)
				switch release {
				case "cancel":
					cause = errors.New("remapped repair child cancellation")
					op.Cancel(cause)
				case "done":
					op.Done()
				case "request_end":
					op.Session.RequestEnd(session.CleanClose)
				}
				if err := awaitPausedReplacement(t, done); err != context.Canceled {
					t.Fatal("repair child error", err)
				}
				if child.Err() != context.Canceled || context.Cause(child) != cause || parent.Err() != nil {
					t.Fatal("repair lost exact child cause or canceled live source", child.Err(), context.Cause(child), parent.Err())
				}
				if f.target.CatalogRead().State() != before || !reflect.DeepEqual(f.target.Storage.Relations, rows) || f.target.Catalog.NextOID() != oid || f.target.Storage.RowFrontier() != row || f.target.Storage.VersionFrontier() != version {
					t.Fatal("failed repair published destination state or allocator")
				}
				if f.target.RoleStore.Published != roles || f.target.Visibility.CommitSeq() != frontier || f.target.Catalog.NextRevision() != revision || !reflect.DeepEqual(f.target.Storage.IndexRoots, indexes) || !reflect.DeepEqual(f.target.Storage.Sequences, sequences) {
					t.Fatal("failed repair changed roles, index roots, sequences or publication frontier")
				}
				if !maps.Equal(backing, f.target.Storage.Relations) || !maps.Equal(indexes, f.target.Storage.IndexRoots) || !maps.Equal(sequences, f.target.Storage.Sequences) {
					t.Fatal("failed repair replaced accepted storage backing")
				}
				op.Done()
				op.Session.Mu.Lock()
				leases, members := op.Session.Leases, len(op.Session.Operations)
				op.Session.Mu.Unlock()
				if leases != 0 || members != 0 {
					t.Fatal("repair retained admitted operation", leases, members)
				}
				f.healthy(t)
			})
		}
	}
}

func TestExecutionPauseRemappedRepairDestination(t *testing.T) {
	for _, route := range []string{"generation", "index"} {
		t.Run(route, func(t *testing.T) {
			f := newPausedReplacementFixture(t, route)
			parent, _, pause, done := f.start(t)
			// Genuine independent relation mutation must finish while the imported
			// provider is paused. It also supplies the stale-preparation witness.
			peer := enginetest.StartSession(t, f.target, nil)
			op, err := peer.Acquire(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			stmt := enginetest.Statement(t, "INSERT INTO repair_incumbent VALUES(12)")
			mutated := make(chan error, 1)
			go func() {
				_, err := f.target.Execute(op, stmt, 0, nil)
				mutated <- err
			}()
			mutationJoined := false
			defer func() {
				op.Cancel(context.Canceled)
				pause.Resume()
				if !mutationJoined {
					select {
					case <-mutated:
					case <-parent.Done():
						t.Error("independent relation operation did not join")
					}
				}
			}()
			if err := awaitPausedReplacement(t, mutated); err != nil {
				t.Fatal("independent relation operation while repair paused", err)
			}
			mutationJoined = true
			before := f.target.CatalogRead().State()
			rows := cloneGeneratedRepairStorage(f.target.Storage.Relations)
			backing := maps.Clone(f.target.Storage.Relations)
			roles, frontier, revision := f.target.RoleStore.Published, f.target.Visibility.CommitSeq(), f.target.Catalog.NextRevision()
			oid, row, version := f.target.Catalog.NextOID(), f.target.Storage.RowFrontier(), f.target.Storage.VersionFrontier()
			indexes, sequences := maps.Clone(f.target.Storage.IndexRoots), maps.Clone(f.target.Storage.Sequences)
			pause.Resume()
			if err := awaitPausedReplacement(t, done); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("stale detached repair accepted", err)
			}
			if f.target.CatalogRead().State() != before || !reflect.DeepEqual(f.target.Storage.Relations, rows) {
				t.Fatal("stale repair erased concurrent destination mutation")
			}
			if f.target.RoleStore.Published != roles || f.target.Visibility.CommitSeq() != frontier || f.target.Catalog.NextRevision() != revision || f.target.Catalog.NextOID() != oid || f.target.Storage.RowFrontier() != row || f.target.Storage.VersionFrontier() != version || !reflect.DeepEqual(f.target.Storage.IndexRoots, indexes) || !reflect.DeepEqual(f.target.Storage.Sequences, sequences) {
				t.Fatal("stale repair changed post-mutation roles, roots or allocator frontiers")
			}
			if !maps.Equal(backing, f.target.Storage.Relations) || !maps.Equal(indexes, f.target.Storage.IndexRoots) || !maps.Equal(sequences, f.target.Storage.Sequences) {
				t.Fatal("stale repair replaced post-mutation storage backing")
			}
			if rows := enginetest.Tuples(enginetest.SessionSQL(t, f.owner, "SELECT v FROM repair_incumbent ORDER BY v")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(11)}, {int64(12)}}) {
				t.Fatal("concurrent destination rows", rows)
			}
			f.healthy(t)
		})
	}
}

func TestExecutionPauseRemappedRepairAuthority(t *testing.T) {
	for _, route := range []string{"generation", "index"} {
		for _, change := range []string{"session_end", "discard_temp", "prepared", "role"} {
			t.Run(route+"/"+change, func(t *testing.T) {
				f := newPausedReplacementFixture(t, route)
				parent, _, pause, done := f.start(t)
				peer := enginetest.StartSession(t, f.target, nil)
				catalogBeforeChange := f.target.CatalogRead().State()
				var mutate func() error
				if change == "session_end" {
					mutate = func() error {
						f.owner.RequestEnd(session.CleanClose)
						return f.owner.WaitEnd(parent)
					}
				} else {
					actor, sql := peer, []string{"BEGIN", "PREPARE TRANSACTION 'repair_detached'"}
					if change == "discard_temp" {
						actor, sql = f.owner, []string{"DISCARD TEMP"}
					} else if change == "role" {
						sql = []string{"CREATE ROLE repair_new_role"}
					}
					op, err := actor.Acquire(parent)
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					// Parse on the owner before starting the worker.
					first := enginetest.Statement(t, sql[0])
					if len(sql) == 1 {
						mutate = func() error {
							_, err := f.target.Execute(op, first, 0, nil)
							return err
						}
					} else {
						second := enginetest.Statement(t, sql[1])
						mutate = func() error {
							if _, err := f.target.Execute(op, first, 0, nil); err != nil {
								return err
							}
							_, err := f.target.Execute(op, second, 0, nil)
							return err
						}
					}
				}
				mutated := make(chan error, 1)
				go func() { mutated <- mutate() }()
				joined := false
				defer func() {
					pause.Resume()
					if !joined {
						select {
						case <-mutated:
						case <-parent.Done():
							t.Error("authority change did not join")
						}
					}
				}()
				if err := awaitPausedReplacement(t, mutated); err != nil {
					t.Fatal("real destination authority change", err)
				}
				joined = true
				if change == "prepared" {
					if f.target.CatalogRead().State() != catalogBeforeChange || core.EnginePreparedRef(f.target).Lookup("repair_detached") == nil {
						t.Fatal("prepared witness did not isolate unchanged catalog plus live prepared admission")
					}
					t.Cleanup(func() {
						if core.EnginePreparedRef(f.target).Lookup("repair_detached") != nil {
							enginetest.SessionSQL(t, peer, "ROLLBACK PREPARED 'repair_detached'")
						}
					})
				}
				before, roles := f.target.CatalogRead().State(), f.target.RoleStore.Published
				rows := cloneGeneratedRepairStorage(f.target.Storage.Relations)
				backing := maps.Clone(f.target.Storage.Relations)
				roots, sequences := maps.Clone(f.target.Storage.IndexRoots), maps.Clone(f.target.Storage.Sequences)
				oid, row, version := f.target.Catalog.NextOID(), f.target.Storage.RowFrontier(), f.target.Storage.VersionFrontier()
				revision, frontier := f.target.Catalog.NextRevision(), f.target.Visibility.CommitSeq()
				pause.Resume()
				if err := awaitPausedReplacement(t, done); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("changed authority accepted stale repair", err)
				}
				if f.target.CatalogRead().State() != before || f.target.RoleStore.Published != roles || !reflect.DeepEqual(f.target.Storage.Relations, rows) || !reflect.DeepEqual(f.target.Storage.IndexRoots, roots) || !reflect.DeepEqual(f.target.Storage.Sequences, sequences) || f.target.Catalog.NextOID() != oid || f.target.Storage.RowFrontier() != row || f.target.Storage.VersionFrontier() != version || f.target.Catalog.NextRevision() != revision || f.target.Visibility.CommitSeq() != frontier {
					t.Fatal("stale repair overwrote actual destination authority change")
				}
				if !maps.Equal(backing, f.target.Storage.Relations) || !maps.Equal(roots, f.target.Storage.IndexRoots) || !maps.Equal(sequences, f.target.Storage.Sequences) {
					t.Fatal("stale authority repair replaced accepted storage backing")
				}
				if change == "prepared" {
					enginetest.SessionSQL(t, peer, "ROLLBACK PREPARED 'repair_detached'")
				} else if change == "session_end" || change == "discard_temp" {
					f.retired = true
					if f.target.CatalogRead().Get(f.kept.Address()) != nil || f.target.Storage.Relations[f.kept.Address()] != nil {
						t.Fatal("retired TEMP authority resurrected")
					}
				}
				f.healthy(t)
			})
		}
	}
}

func TestCatalogReplacementOperationOwner(t *testing.T) {
	for _, owner := range []string{"destination", "unrelated", "released", "self"} {
		t.Run(owner, func(t *testing.T) {
			f := newPausedReplacementFixture(t, "generation")
			source, sourceSession := f.source(t)
			actor := sourceSession
			if owner == "destination" || owner == "self" {
				actor = f.owner
			} else if owner == "unrelated" {
				actor = enginetest.StartSession(t, engine.New(), nil)
			}
			op, err := actor.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			if owner == "released" {
				op.Done()
			}
			if owner == "self" {
				source = f.target
			}
			before, sourceBefore := f.target.CatalogRead().State(), source.CatalogRead().State()
			rows, sourceRows := cloneGeneratedRepairStorage(f.target.Storage.Relations), cloneGeneratedRepairStorage(source.Storage.Relations)
			backing, sourceBacking := maps.Clone(f.target.Storage.Relations), maps.Clone(source.Storage.Relations)
			roots, sequences := maps.Clone(f.target.Storage.IndexRoots), maps.Clone(f.target.Storage.Sequences)
			roles, frontier := f.target.RoleStore.Published, f.target.Visibility.CommitSeq()
			oid, row, version, revision := f.target.Catalog.NextOID(), f.target.Storage.RowFrontier(), f.target.Storage.VersionFrontier(), f.target.Catalog.NextRevision()
			change, err := f.target.ReplaceCatalogInOperation(context.Background(), source, source.CatalogRead(), op)
			var diagnostic interface{ SQLState() string }
			if err == nil || errors.As(err, &diagnostic) || change.Replacement {
				t.Fatal("invalid replacement operation was admitted or fabricated a SQL diagnostic", change, err)
			}
			if owner == "released" && err.Error() != "stuntgres: inactive operation" {
				t.Fatal("released source operation lost admission diagnostic", err)
			}
			if f.target.CatalogRead().State() != before || source.CatalogRead().State() != sourceBefore || !reflect.DeepEqual(f.target.Storage.Relations, rows) || !reflect.DeepEqual(source.Storage.Relations, sourceRows) || !reflect.DeepEqual(f.target.Storage.IndexRoots, roots) || !reflect.DeepEqual(f.target.Storage.Sequences, sequences) || f.target.RoleStore.Published != roles || f.target.Visibility.CommitSeq() != frontier || f.target.Catalog.NextOID() != oid || f.target.Storage.RowFrontier() != row || f.target.Storage.VersionFrontier() != version || f.target.Catalog.NextRevision() != revision {
				t.Fatal("rejected operation changed source or destination publication")
			}
			if !maps.Equal(backing, f.target.Storage.Relations) || !maps.Equal(sourceBacking, source.Storage.Relations) || !maps.Equal(roots, f.target.Storage.IndexRoots) || !maps.Equal(sequences, f.target.Storage.Sequences) {
				t.Fatal("rejected operation replaced source or destination storage backing")
			}
			// These tests reject only invalid supplied operations, not ordinary
			// foreign replacement or a genuine source-owned Restore operation.
			f.healthy(t)
		})
	}
}
