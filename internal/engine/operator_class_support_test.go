package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

// This fixture constructs real descriptors and real registered routine bindings;
// no SQL, name-based class cloning or callback stands in for native support.
func operatorClassUnitDefinitions(t *testing.T, e *core.Engine, name string, function ids.OID) (*catalog.OperatorFamily, *catalog.OperatorClass, *catalog.OperatorFamilySupport) {
	t.Helper()
	builtin, ok := e.CatalogRead().OperatorClass(1978)
	if !ok {
		t.Fatal("missing bootstrap class authority")
	}
	family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: builtin.Namespace(), ObjectRevision: 1, ObjectName: name + "_family", Method: 403, Owner: builtin.Owner}
	class := &catalog.OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: catalogUnitOID(t, e)}, ObjectNamespace: builtin.Namespace(), ObjectRevision: 1, ObjectName: name, Method: 403, Family: family.Address().ObjectID, InputType: 23, Owner: builtin.Owner}
	member := &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, Family: family.Address().ObjectID, Left: 23, Right: 23, Number: 1, Function: function, InternalOwner: class.Address()}
	return family, class, member
}
