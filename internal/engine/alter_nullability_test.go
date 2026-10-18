package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterNullabilityMetadataAndStorage(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	run(t, e, "INSERT INTO t VALUES(7,'keep')")
	run(t, e, "COMMENT ON COLUMN t.a IS 'stable'")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	store := e.storage[ref.Address]
	root := store.versions
	ends, generation := store.ends, store.generation
	held := run(t, e, "SELECT * FROM t")
	defer held.Rows.Close()
	run(t, e, "ALTER TABLE t ALTER a SET NOT NULL, ADD CONSTRAINT requested NOT NULL a")
	next := e.catalog.state.MustRelation(ref.Address)
	if next.address != old.address || next.revision != old.revision+1 || !reflect.DeepEqual(next.attributes, old.attributes) || !reflect.DeepEqual(next.columnComments, old.columnComments) || e.storage[ref.Address] != store || store.generation != generation || store.ends != ends || !sameVersionRoot(root, store.versions) || &root[0].values[0] != &store.versions[0].values[0] {
		t.Fatal("pure NN rewrote identity, metadata or values")
	}
	if len(next.notNullConstraints) != 1 || next.notNullConstraints[0].Name != "t_a_not_null" {
		t.Fatal(next.notNullConstraints)
	}
	run(t, e, "ALTER TABLE t ALTER a SET NOT NULL, ADD NOT NULL a")
	if e.catalog.state.Get(ref.Address) != next || e.storage[ref.Address] != store {
		t.Fatal("no-op changed descriptor/storage")
	}
	run(t, e, "ALTER TABLE t ALTER a DROP NOT NULL")
	if e.storage[ref.Address] != store || store.generation != generation || !sameVersionRoot(root, store.versions) {
		t.Fatal("drop copied values")
	}
	if !held.Rows.Schema()[0].Nullable {
		t.Fatal("retained metadata changed")
	}
}

func TestAlterNullabilityLifecycle(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL, DROP COLUMN b", "ALTER TABLE t ALTER a DROP NOT NULL, DROP CONSTRAINT IF EXISTS missing", "ALTER TABLE t ADD CONSTRAINT n NOT NULL a, ALTER a SET NOT NULL"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(a INT,b INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(7,9)")
			ref := roleTableRef(t, e, "t")
			descriptor, store := e.catalog.state.Get(ref.Address), e.storage[ref.Address]
			frontier := e.commitSeq
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := waitsCommandAsync(e, b, ctx, q, false)
			lockPending(t, e, 1)
			cancel()
			lockAwait(t, done, context.Canceled)
			lockPending(t, e, 0)
			if e.catalog.state.Get(ref.Address) != descriptor || e.storage[ref.Address] != store || e.commitSeq != frontier {
				t.Fatal("canceled list published")
			}
			transactionEnd(t, a, tx, false)
			lockEmpty(t, e)
			sessionSQL(t, b, q)
			lockEmpty(t, e)
		})
	}
}

// Exercise every real Err checkpoint rather than pinning an obsolete handler count.
func TestAlterNullabilityCancellationBoundaries(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL"} {
		t.Run(q, func(t *testing.T) {
			setup := func() (*Engine, ids.ObjectRef) {
				e := New()
				run(t, e, "CREATE TABLE t(a INT,b INT)")
				run(t, e, "INSERT INTO t VALUES"+strings.TrimSuffix(strings.Repeat("(7,9),", 12), ","))
				return e, roleTableRef(t, e, "t")
			}
			calibration, _ := setup()
			ctx, cancel := context.WithCancel(context.Background())
			op, done, err := testOperation(calibration, ctx)
			if err != nil {
				t.Fatal(err)
			}
			counter := &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 1 << 30}
			op.ctx = counter
			if _, err = calibration.Execute(op, statement(t, q), 0, nil); err != nil {
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
				store := e.storage[ref.Address]
				descriptor := e.catalog.state.Get(ref.Address)
				frontier, generation, ends := e.commitSeq, store.generation, store.ends
				all := append([]rowVersion(nil), store.versions[:cap(store.versions)]...)
				for i := range all {
					all[i].values = append([]sqltype.Value(nil), all[i].values...)
				}
				ctx, cancel := context.WithCancel(context.Background())
				op, done, err := testOperation(e, ctx)
				if err != nil {
					t.Fatal(err)
				}
				op.ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: at}
				_, err = e.Execute(op, statement(t, q), 0, nil)
				done()
				cancel()
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(at, err)
					}
					if e.catalog.state.Get(ref.Address) != descriptor || e.storage[ref.Address] != store || store.generation != generation || store.ends != ends || e.commitSeq != frontier || !reflect.DeepEqual(all, store.versions[:cap(store.versions)]) {
						t.Fatal("cancellation mutated a prefix or spare suffix", at)
					}
				} else if e.catalog.state.Get(ref.Address) == descriptor {
					t.Fatal("success without complete metadata install", at)
				}
				lockEmpty(t, e)
			}
		})
	}
}

func TestAlterNullabilityProviderReplacement(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(a INT,b INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(7,9)")
			if explicit {
				transactionBegin(t, s)
			}
			fired := false
			e.catalog.providers[ids.RelationClassID] = alterDropProvider{CatalogProvider: e.catalog.providers[ids.RelationClassID], callback: func() {
				if fired {
					return
				}
				fired = true
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			}}
			op := transactionOperation(t, s)
			_, err := e.Execute(op, statement(t, "ALTER TABLE t DROP b, ALTER a SET NOT NULL"), 0, nil)
			op.Done()
			if !fired || !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal(fired, err)
			}
			if len(e.storage) != 0 {
				t.Fatal("stale callback installed storage")
			}
		})
	}
}
