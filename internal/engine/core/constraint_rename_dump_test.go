package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

func TestConstraintRenameCheckOrder(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT,CONSTRAINT a CHECK(k>0) NO INHERIT,CONSTRAINT b CHECK(k>1))")
	ref := roleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	a, b := old.Constraints.Check[0], old.Constraints.Check[1]
	next, err := table.RenameLocalConstraint(old, "a", "z", 'c')
	if err != nil {
		t.Fatal(err)
	}
	if old.Constraints.Check[0].Name != "a" || next.Constraints.Check[0].Name != "b" || next.Constraints.Check[1].Name != "z" {
		t.Fatal("lexical order or immutable vector")
	}
	renamed := next.Constraints.Check[1]
	renamed.Name = a.Name
	if !reflect.DeepEqual(renamed, a) || !reflect.DeepEqual(next.Constraints.Check[0], b) {
		t.Fatal("CHECK expression/OID/dependency/ordinal changed")
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal("real CHECK provider rejected sorted vector", err)
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT a TO z")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Index(dump.String(), `CONSTRAINT "z"`) > strings.Index(dump.String(), `CONSTRAINT "b"`) {
		t.Fatal("Dump replaced declaration ordinal with enforcement order", dump.String())
	}
}

func TestConstraintRenamePublicationFences(t *testing.T) {
	for _, phase := range []string{"validation", "finish"} {
		for _, change := range []string{"cancel", "reset", "restore", "collision", "descriptor", "authorization"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
				ref := roleTableRef(t, e, "t")
				old := e.Catalog.State.MustRelation(ref.Address)
				index := old.Constraints.Unique[0].Index
				backing := e.Catalog.State.Get(index)
				store, root := e.Storage.Relations[ref.Address], e.Storage.IndexRoots[index]
				var dump bytes.Buffer
				if err := engine.Dump(context.Background(), e, &dump); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				mutate := func() {
					switch change {
					case "cancel":
						cancel()
					case "reset":
						if err := engine.Reset(context.Background(), e); err != nil {
							t.Fatal(err)
						}
						enginetest.Run(t, e, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
					case "restore":
						if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					case "collision":
						enginetest.Run(t, e, "CREATE TABLE new(k INT)")
					case "descriptor":
						e.Mu.Lock()
						replacement := *old
						replacement.ObjectRevision++
						replacement.Comment = "concurrent"
						state := catalog.CloneState(e.Catalog.State)
						state.SetDescriptor(ref.Address, &replacement)
						e.Catalog.PublishLocked(state)
						e.Mu.Unlock()
					case "authorization":
						enginetest.Run(t, e, "CREATE ROLE changed_authority")
					}
				}
				provider := e.Catalog.Providers[ids.RelationClassID]
				if phase == "validation" {
					var once sync.Once
					e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() { once.Do(func() { e.Catalog.Providers[ids.RelationClassID] = provider; mutate() }) }}
				}
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				_, err = scope.Execute(enginetest.Statement(t, "ALTER TABLE t RENAME CONSTRAINT old TO new"), 0, nil)
				if phase == "finish" {
					if err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					mutate()
				}
				err = scope.Finish(err)
				if err == nil {
					t.Fatal("publication fence absent")
				}
				if change == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if change == "reset" || change == "restore" {
					if !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal(err)
					}
					return
				}
				if e.Catalog.State.Get(index) != backing || e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[index] != root {
					t.Fatal("failed rename partially published backing/storage")
				}
				if change != "descriptor" && e.Catalog.State.Get(ref.Address) != old {
					t.Fatal("failed rename published parent")
				}
			})
		}
	}
}
