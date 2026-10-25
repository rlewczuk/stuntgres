package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func conversionUnitDefinition(e *Engine, oid ids.OID, name string, def bool) *catalog.Conversion {
	return &catalog.Conversion{OID: oid, ObjectName: name, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, Owner: e.RoleStore.Domain.Principal(10), Source: 6, Destination: 8, Handler: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4375}, DefaultConversion: def}
}

func conversionUnitName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{Parts: make([]parser.Identifier, len(parts))}
	for i, text := range parts {
		name.Parts[i] = parser.Identifier{Text: text}
	}
	return name
}

func conversionUnitAuth(t *testing.T, e *Engine) (*session.Operation, AuthorizationView) {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return op, auth
}
