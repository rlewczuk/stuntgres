package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestAlterNullabilityMetadataAndStorage(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(7,'keep')")
	enginetest.Run(t, e, "COMMENT ON COLUMN t.a IS 'stable'")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	store := e.Storage.Relations[ref.Address]
	root := store.Versions
	ends, generation := store.Ends, store.Generation
	held := enginetest.Run(t, e, "SELECT * FROM t")
	defer held.Rows.Close()
	enginetest.Run(t, e, "ALTER TABLE t ALTER a SET NOT NULL, ADD NOT NULL a")
	next := e.Catalog.State.MustRelation(ref.Address)
	if next.Address() != old.Address() || next.Revision() != old.Revision()+1 || !reflect.DeepEqual(next.Attributes, old.Attributes) || !reflect.DeepEqual(next.ColumnMeta.Comments, old.ColumnMeta.Comments) || e.Storage.Relations[ref.Address] != store || store.Generation != generation || store.Ends != ends || !storage.SameVersionRoot(root, store.Versions) || &root[0].Values[0] != &store.Versions[0].Values[0] {
		t.Fatal("pure NN rewrote identity, metadata or values")
	}
	if len(next.Constraints.NotNull) != 1 || next.Constraints.NotNull[0].Name != "t_a_not_null" {
		t.Fatal(next.Constraints.NotNull)
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER a SET NOT NULL, ADD NOT NULL a")
	if e.Catalog.State.Get(ref.Address) != next || e.Storage.Relations[ref.Address] != store {
		t.Fatal("no-op changed descriptor/storage")
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER a DROP NOT NULL")
	if e.Storage.Relations[ref.Address] != store || store.Generation != generation || !storage.SameVersionRoot(root, store.Versions) {
		t.Fatal("drop copied values")
	}
	if !held.Rows.Schema()[0].Nullable {
		t.Fatal("retained metadata changed")
	}
}

func TestAlterNullabilityLifecycle(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL, DROP COLUMN b", "ALTER TABLE t ALTER a DROP NOT NULL, DROP CONSTRAINT IF EXISTS missing", "ALTER TABLE t ADD NOT NULL a, ALTER a SET NOT NULL"} {
		t.Run(q, func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT,b INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7,9)")
			ref := enginetest.RoleTableRef(t, e, "t")
			descriptor, store := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
			frontier := e.Visibility.CommitSeq()
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := waitsCommandAsync(e, b, ctx, q, false)
			enginetest.LockPending(t, e, 1)
			cancel()
			enginetest.LockAwait(t, done, context.Canceled)
			enginetest.LockPending(t, e, 0)
			if e.Catalog.State.Get(ref.Address) != descriptor || e.Storage.Relations[ref.Address] != store || e.Visibility.CommitSeq() != frontier {
				t.Fatal("canceled list published")
			}
			enginetest.TransactionEnd(t, a, tx, false)
			enginetest.LockEmpty(t, e)
			enginetest.SessionSQL(t, b, q)
			enginetest.LockEmpty(t, e)
		})
	}
}

// Exercise every real Err checkpoint rather than pinning an obsolete handler count.
func TestAlterNullabilityCancellationBoundaries(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL"} {
		t.Run(q, func(t *testing.T) {
			setup := func() (*core.Engine, ids.ObjectRef) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE t(a INT,b INT)")
				enginetest.Run(t, e, "INSERT INTO t VALUES"+strings.TrimSuffix(strings.Repeat("(7,9),", 12), ","))
				return e, enginetest.RoleTableRef(t, e, "t")
			}
			calibration, _ := setup()
			ctx, cancel := context.WithCancel(context.Background())
			op, done, err := enginetest.Operation(calibration, ctx)
			if err != nil {
				t.Fatal(err)
			}
			counter := &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 1 << 30}
			op.Ctx = counter
			if _, err = calibration.Execute(op, enginetest.Statement(t, q), 0, nil); err != nil {
				t.Fatal(err)
			}
			done()
			cancel()
			checks := counter.checks.Load()
			if checks == 0 {
				t.Fatal("no real checkpoints")
			}
			for at := int32(1); at <= checks; at++ {
				e, ref := setup()
				store := e.Storage.Relations[ref.Address]
				descriptor := e.Catalog.State.Get(ref.Address)
				frontier, generation, ends := e.Visibility.CommitSeq(), store.Generation, store.Ends
				all := append([]storage.RowVersion(nil), store.Versions[:cap(store.Versions)]...)
				for i := range all {
					all[i].Values = append([]sqltype.Value(nil), all[i].Values...)
				}
				ctx, cancel := context.WithCancel(context.Background())
				op, done, err := enginetest.Operation(e, ctx)
				if err != nil {
					t.Fatal(err)
				}
				op.Ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: at}
				_, err = e.Execute(op, enginetest.Statement(t, q), 0, nil)
				done()
				cancel()
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(at, err)
					}
					if e.Catalog.State.Get(ref.Address) != descriptor || e.Storage.Relations[ref.Address] != store || store.Generation != generation || store.Ends != ends || e.Visibility.CommitSeq() != frontier || !reflect.DeepEqual(all, store.Versions[:cap(store.Versions)]) {
						t.Fatal("cancellation mutated a prefix or spare suffix", at)
					}
				} else if e.Catalog.State.Get(ref.Address) == descriptor {
					t.Fatal("success without complete metadata install", at)
				}
				enginetest.LockEmpty(t, e)
			}
		})
	}
}

func TestAlterNullabilityProviderReplacement(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(a INT,b INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(7,9)")
			if explicit {
				enginetest.TransactionBegin(t, s)
			}
			fired := false
			e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: e.Catalog.Providers[ids.RelationClassID], callback: func() {
				if fired {
					return
				}
				fired = true
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}}
			op := enginetest.TransactionOperation(t, s)
			_, err := e.Execute(op, enginetest.Statement(t, "ALTER TABLE t DROP b, ALTER a SET NOT NULL"), 0, nil)
			op.Done()
			if !fired || !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(fired, err)
			}
			if len(e.Storage.Relations) != 0 {
				t.Fatal("stale callback installed storage")
			}
		})
	}
}

func waitsCommandAsync(e *core.Engine, s *session.Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, _, err := s.AcquireCommand(ctx)
		if err == nil {
			if prepare {
				_, err = e.Describe(op, mustLockStatement(q))
			} else {
				var r core.Result
				r, err = e.Execute(op, mustLockStatement(q), 0, nil)
				if r.Rows != nil {
					r.Rows.Close()
				}
			}
			err = op.CommandError(err)
			op.Done()
		}
		done <- err
	}()
	return done
}

func mustLockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}
