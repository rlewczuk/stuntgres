package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Fixture identities use the real allocator: bootstrap definitions can occupy
// user-range OIDs without becoming relation descriptors.
func catalogUnitOID(t *testing.T, e *core.Engine) ids.OID {
	t.Helper()
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func catalogRef(t *testing.T, e *core.Engine, view catalog.Read, name string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	ref, err := e.ResolveReference(catalogAuthorization(e, view), parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
