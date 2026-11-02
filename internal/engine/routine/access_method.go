package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// These descriptors associate released handler identities with actual existing
// operation authorities. They are utility capabilities, not scalar functions
// that manufacture IndexAmRoutine/TableAmRoutine pointers.
func NativeAccessMethodProcedures(owner authorization.PrincipalRef) []catalog.Procedure {
	// Native declarations are intrinsically parallel-safe. Parallel is the
	// mutable override, so the canonical seed leaves its zero sentinel intact.
	procedures := make([]catalog.Procedure, 7, 21)
	for i, oid := range [...]ids.OID{3, 330, 331, 332, 333, 334, 335} {
		identity, _ := catalog.BuiltinRoutineIdentityByOID(oid)
		result, _ := catalog.BuiltinRoutineResultByOID(oid)
		procedures[i] = catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: identity.Namespace}, ObjectRevision: 1, ObjectName: identity.Name, Signature: identity.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Volatile, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: identity.Name}, AccessMethod: catalog.NativeHandler(oid)}
	}
	for slot := uint8(1); slot <= 4; slot++ {
		procedures = append(procedures, catalog.NewBrinSupportProcedure(slot, owner))
	}
	procedures = append(procedures, catalog.GinSupportProcedures(owner)...)
	return procedures
}
