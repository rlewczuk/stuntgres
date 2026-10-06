package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestAlterRenamePublication(t *testing.T) {
	for _, kind := range []string{"collision", "cancel", "reset", "authority"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(a INT)")
			run(t, e, "INSERT INTO t VALUES(7)")
			ref := roleTableRef(t, e, "t")
			old := e.state.descriptors[ref.Address]
			storage := e.storage[ref.Address]
			frontier := e.nextRevision
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			provider := e.providers[RelationClassID]
			e.providers[RelationClassID] = alterDropProvider{CatalogProvider: provider, callback: func() {
				once.Do(func() {
					e.providers[RelationClassID] = provider
					switch kind {
					case "collision":
						run(t, e, "CREATE TABLE winner(a INT)")
					case "cancel":
						cancel()
					case "reset":
						if err := e.Reset(context.Background()); err != nil {
							t.Fatal(err)
						}
					case "authority":
						run(t, e, "CREATE USER changed_role")
					}
				})
			}}
			_, err := testExecute(e, ctx, statement(t, "ALTER TABLE t RENAME TO winner"), nil)
			switch kind {
			case "collision":
				renameWantError(t, err, "42P07", `relation "winner" already exists`)
				run(t, e, "SELECT * FROM winner")
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "reset":
				if !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal(err)
				}
			case "authority":
				if err == nil {
					t.Fatal("role revision fence absent")
				}
			}
			if kind != "reset" && (e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != storage) {
				t.Fatal("failed publication changed source")
			}
			if kind == "cancel" && e.nextRevision != frontier {
				t.Fatal("canceled publication consumed revision")
			}
		})
	}
}
