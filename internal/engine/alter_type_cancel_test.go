package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAlterTypeCancellationAndReplacement(t *testing.T) {
	for _, kind := range []string{"cancel", "reset", "restore"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(c TEXT)")
			run(t, e, "INSERT INTO t VALUES('41'),('7')")
			ref := roleTableRef(t, e, "t")
			old, storage := e.state.descriptors[ref.Address], e.storage[ref.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, cleanup, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			provider := e.providers[RelationClassID]
			e.providers[RelationClassID] = alterDropProvider{provider, func() {
				e.providers[RelationClassID] = provider
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
			_, err = e.Execute(op, statement(t, "ALTER TABLE t ALTER c TYPE INT USING c::int"), 0, nil)
			if kind == "cancel" {
				if !errors.Is(err, context.Canceled) || e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != storage {
					t.Fatal(err, "published canceled rewrite")
				}
				run(t, e, "ALTER TABLE t ALTER c TYPE INT USING c::int")
			} else if !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal("resurrected replaced catalog", err)
			}
		})
	}
}
