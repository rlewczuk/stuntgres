package tablecmds_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestAlterRenamePublication(t *testing.T) {
	for _, kind := range []string{"collision", "cancel", "reset", "authority"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(a INT)")
			enginetest.Run(t, e, "INSERT INTO t VALUES(7)")
			ref := enginetest.RoleTableRef(t, e, "t")
			old := e.Catalog.State.Get(ref.Address)
			storage := e.Storage.Relations[ref.Address]
			frontier := e.Catalog.NextRevision()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			provider := e.Catalog.Providers[ids.RelationClassID]
			e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() {
				once.Do(func() {
					e.Catalog.Providers[ids.RelationClassID] = provider
					switch kind {
					case "collision":
						enginetest.Run(t, e, "CREATE TABLE winner(a INT)")
					case "cancel":
						cancel()
					case "reset":
						if err := engine.Reset(context.Background(), e); err != nil {
							t.Fatal(err)
						}
					case "authority":
						enginetest.Run(t, e, "CREATE USER changed_role")
					}
				})
			}}
			_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "ALTER TABLE t RENAME TO winner"), nil)
			switch kind {
			case "collision":
				// The destination was absent during capture; the provider
				// publishes it before name admission, matching the pinned
				// concurrent rename pg_class uniqueness diagnostic.
				want := sqlstate.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
					Message: `duplicate key value violates unique constraint "pg_class_relname_nsp_index"`,
					Detail:  fmt.Sprintf("Key (relname, relnamespace)=(winner, %d) already exists.", old.Namespace().ObjectID),
					Schema:  "pg_catalog", Table: "pg_class", Constraint: "pg_class_relname_nsp_index",
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal(err, diagnostic, want)
				}
				enginetest.Run(t, e, "SELECT * FROM winner")
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "reset":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			case "authority":
				if err == nil {
					t.Fatal("role revision fence absent")
				}
			}
			if kind != "reset" && (e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage) {
				t.Fatal("failed publication changed source")
			}
			if kind == "cancel" && e.Catalog.NextRevision() != frontier {
				t.Fatal("canceled publication consumed revision")
			}
		})
	}
}
