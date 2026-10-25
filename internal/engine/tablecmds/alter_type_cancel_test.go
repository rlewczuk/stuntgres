package tablecmds_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestAlterTypeCancellationAndReplacement(t *testing.T) {
	for _, fixture := range []struct{ name, declaration, seed, action string }{
		{"conversion", "c TEXT", "('41'),('7')", "ALTER c TYPE INT USING c::int"},
		{"checked", "c INT CONSTRAINT ck CHECK(c>0)", "(41),(7)", "ALTER c TYPE BIGINT"},
	} {
		for _, kind := range []string{"cancel", "reset", "restore"} {
			t.Run(fixture.name+"/"+kind, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE t("+fixture.declaration+")")
				enginetest.Run(t, e, "INSERT INTO t VALUES"+fixture.seed)
				ref := enginetest.RoleTableRef(t, e, "t")
				old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, cleanup, err := enginetest.Operation(e, ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				provider := e.Catalog.Providers[ids.RelationClassID]
				e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
					e.Catalog.Providers[ids.RelationClassID] = provider
					switch kind {
					case "cancel":
						cancel()
					case "reset":
						if err := engine.Reset(context.Background(), e); err != nil {
							t.Fatal(err)
						}
					case "restore":
						if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE sentinel(v INT);INSERT INTO sentinel VALUES(91)")); err != nil {
							t.Fatal(err)
						}
					}
				}}
				_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE t "+fixture.action), 0, nil)
				if kind == "cancel" {
					if !errors.Is(err, context.Canceled) || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
						t.Fatal(err, "published canceled rewrite")
					}
					enginetest.Run(t, e, "ALTER TABLE t "+fixture.action)
				} else if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("resurrected replaced catalog", err)
				}
			})
		}
	}
}
