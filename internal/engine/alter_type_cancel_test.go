package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAlterTypeCancellationAndReplacement(t *testing.T) {
	for _, fixture := range []struct{ name, declaration, seed, action string }{
		{"conversion", "c TEXT", "('41'),('7')", "ALTER c TYPE INT USING c::int"},
		{"checked", "c INT CONSTRAINT ck CHECK(c>0)", "(41),(7)", "ALTER c TYPE BIGINT"},
	} {
		for _, kind := range []string{"cancel", "reset", "restore"} {
			t.Run(fixture.name+"/"+kind, func(t *testing.T) {
				e := New()
				run(t, e, "CREATE TABLE t("+fixture.declaration+")")
				run(t, e, "INSERT INTO t VALUES"+fixture.seed)
				ref := roleTableRef(t, e, "t")
				old, storage := e.catalog.state.Get(ref.Address), e.storage.Relations[ref.Address]
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, cleanup, err := testOperation(e, ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				provider := e.catalog.Providers[ids.RelationClassID]
				e.catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
					e.catalog.Providers[ids.RelationClassID] = provider
					switch kind {
					case "cancel":
						cancel()
					case "reset":
						if err := e.Reset(context.Background()); err != nil {
							t.Fatal(err)
						}
					case "restore":
						if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE sentinel(v INT);INSERT INTO sentinel VALUES(91)")); err != nil {
							t.Fatal(err)
						}
					}
				}}
				_, err = e.Execute(op, statement(t, "ALTER TABLE t "+fixture.action), 0, nil)
				if kind == "cancel" {
					if !errors.Is(err, context.Canceled) || e.catalog.state.Get(ref.Address) != old || e.storage.Relations[ref.Address] != storage {
						t.Fatal(err, "published canceled rewrite")
					}
					run(t, e, "ALTER TABLE t "+fixture.action)
				} else if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("resurrected replaced catalog", err)
				}
			})
		}
	}
}
