package catalog

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// These released operations consume internal BRIN summary structures. They are
// catalog capabilities for the index owner, not callable scalar comparators.
var brinSupportDeclarations = [...]struct {
	oid       ids.OID
	name      string
	signature ProcedureSignature
	result    ids.OID
}{
	{3383, "brin_minmax_opcinfo", "2281", 2281},
	{3384, "brin_minmax_add_value", "2281,2281,2281,2281", 16},
	{3385, "brin_minmax_consistent", "2281,2281,2281", 16},
	{3386, "brin_minmax_union", "2281,2281,2281", 16},
}

func NewBrinSupportProcedure(slot uint8, owner authorization.PrincipalRef) Procedure {
	f := brinSupportDeclarations[slot-1]
	return Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: f.oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: f.name, Signature: f.signature, RoutineKind: FunctionRoutine, Owner: owner, Result: f.result, Volatility: expr.Immutable, Strict: true, Display: RoutineDisplay{Language: InternalRoutineLanguage, Source: f.name}, BrinSupport: slot}
}

// ValidateBrinSupport authenticates the intrinsic released capability. Current
// extension membership is independently checked by ValidateExtensionMembership;
// adding or detaching a genuine support declaration does not replace its ABI.
func ValidateBrinSupport(d *Procedure) error {
	if d == nil || d.BrinSupport < 1 || int(d.BrinSupport) > len(brinSupportDeclarations) {
		return fmt.Errorf("stuntgres: invalid BRIN summary operation")
	}
	f := brinSupportDeclarations[d.BrinSupport-1]
	if d.Address() != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: f.oid}) || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || d.Name() != f.name || d.Revision() == 0 || d.Signature != f.signature || d.Result != f.result || !d.Strict || d.Volatility != expr.Immutable || d.RoutineKind != FunctionRoutine || d.Display.Language != InternalRoutineLanguage || d.Display.Source != f.name || d.Builtin != nil || d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.Aggregate != nil || d.Foreign != nil || d.RangeConstructor != nil || d.PLHandler != NoPLHandler || d.AccessMethod != NoAccessMethod || d.Implementation != nil || d.Conversion.Name() != "" || d.Dictionary != (ids.ObjectAddress{}) || len(d.Body) != 0 || len(d.DeclarationDependencies) != 0 {
		return fmt.Errorf("stuntgres: invalid BRIN summary operation identity")
	}
	return nil
}
