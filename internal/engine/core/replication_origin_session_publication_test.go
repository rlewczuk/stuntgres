package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Install ordinary fixture metadata before capture, then select/configure via
// the real admitted statement host. No test-only origin authority is exported.
func originSessionConfigurePublication(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	d := &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 42}, ObjectRevision: 1, ObjectName: "origin_publication_unit"}
	e.Mu.Lock()
	e.Catalog.State.SetDescriptor(d.Address(), d)
	e.Catalog.State.BindName(catalog.DescriptorNameKey(d), d.Address())
	e.Mu.Unlock()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	h := core.HostOf(scope)
	if _, err := originStateUnitCall(t, e, h, functions.ReplicationOriginSessionSetup, d.Name(), 0, false); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	typ := e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1184}).SQLType
	stamp, err := typ.Input(context.Background(), "2022-03-04 05:06:07+00", -1)
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the same public typed values and host bridge as expression evaluation.
	position, err := sqltype.PGLSNValue(e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 3220}).SQLType, 0x10)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	values := [2]sqltype.Value{position, stamp}
	if _, err := h.ReplicationOrigin(functions.ReplicationOriginXactSetup, values, e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 2278}).SQLType); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}
