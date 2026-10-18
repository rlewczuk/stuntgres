package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestTypedAdmissionCreateSourceRevisionConflict(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		t.Run(map[bool]string{false: "persistent", true: "temporary"}[temporary], func(t *testing.T) {
			e, source, table := typedUnitCatalog(t, false)
			holder, creator := sessionStart(t, e, nil), sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			ref := roleTableRef(t, e, "pair")
			if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			query := "CREATE TABLE late OF pair"
			if temporary {
				query = "CREATE TEMP TABLE late OF pair"
			}
			finished := lockAsync(e, creator, ctx, query, false)
			released, joined := false, false
			defer func() {
				cancel()
				if !released {
					transactionEnd(t, holder, tx, false)
				}
				if !joined {
					select {
					case <-finished:
					case <-time.After(6 * time.Second):
						t.Error("bounded source admission worker did not drain")
					}
				}
			}()
			lockPending(t, e, 1)
			// Publish a coherent new generation while CREATE is queued. The
			// package provider seam deliberately bypasses relation admission;
			// the actual CREATE executor must not adopt that captured shape.
			stage := typedRenameStage(t, e, source, table)
			defer e.DiscardCatalogStage(stage)
			domainUnitPublish(t, e, stage)
			winning := e.CatalogRead().state
			transactionEnd(t, holder, tx, false)
			released = true
			select {
			case err := <-finished:
				joined = true
				var conflict CatalogConflict
				if !errors.As(err, &conflict) {
					t.Fatal("waited CREATE adopted a different source generation", err)
				}
			case <-ctx.Done():
				t.Fatal("source admission did not complete", ctx.Err())
			}
			if e.CatalogRead().state != winning {
				t.Fatal("stale CREATE published over winning source generation")
			}
			for _, descriptor := range winning.Descriptors() {
				if relation, ok := descriptor.(*relationDescriptor); ok && relation.Name() == "late" {
					t.Fatal("stale attachment published")
				}
			}
			lockEmpty(t, e)
			sessionSQL(t, creator, query)
			// A fresh statement may use the new authoritative shape; this is
			// not a retry or restamping of the failed captured statement.
			result := sessionSQL(t, creator, "SELECT * FROM late")
			if result.Rows == nil || len(result.Rows.Schema()) != 2 || result.Rows.Schema()[1].Name != "label" {
				t.Fatal("fresh CREATE did not use current source", result)
			}
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
