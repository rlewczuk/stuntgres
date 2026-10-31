package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func namespaceUnitSource(t *testing.T, e *core.Engine, c catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2615}
	d, ok := c.Relation(a)
	if !ok {
		t.Fatal("missing namespace source")
	}
	return d, e.Catalog.Domain.Ref(c.Epoch(), a)
}

func namespaceUnitCapture(t *testing.T, e *core.Engine, c catalog.Read) *exec.Source {
	t.Helper()
	d, ref := namespaceUnitSource(t, e, c)
	e.Mu.Lock()
	rows, err := catalogrt.CaptureRows(pgcatalog.Input{Context: context.Background(), Catalog: c, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, c), Ref: ref, Source: d, Owner: &e.Catalog.Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}, capturePGNamespace)
	e.Mu.Unlock()
	if err != nil || rows == nil || rows.Ref != ref {
		t.Fatal(rows, err)
	}
	return rows
}

func namespaceUnitRow(t *testing.T, rows *exec.Source, id uint32) []sqltype.Value {
	t.Helper()
	var found []sqltype.Value
	for _, row := range rows.Anonymous {
		oid, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if oid == id {
			if found != nil {
				t.Fatal("duplicate namespace", id)
			}
			found = row
		}
	}
	if found == nil {
		t.Fatal("missing namespace", id)
	}
	return found
}

type namespaceCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	calls, at int
}

func (c *namespaceCancelContext) Err() error {
	c.calls++
	if c.calls == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
