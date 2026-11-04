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
	if !OriginalBrinSupport(d) {
		return fmt.Errorf("stuntgres: invalid BRIN summary operation identity")
	}
	if !supportedInternalProcedureMetadata(d, brinSupportDeclarations[d.BrinSupport-1].name, expr.Immutable) {
		return fmt.Errorf("stuntgres: invalid BRIN summary operation metadata")
	}
	return nil
}

// OriginalBrinSupport is the intrinsic released fmgr identity, independent of
// supported mutable SQL names, namespaces, owners and declaration attributes.
// It is not invocation authority; callers must validate the current descriptor
// header and the selected source lifetime separately.
func OriginalBrinSupport(d *Procedure) bool {
	if d == nil || d.BrinSupport < 1 || int(d.BrinSupport) > len(brinSupportDeclarations) {
		return false
	}
	f := brinSupportDeclarations[d.BrinSupport-1]
	return d.AccessMethod == NoAccessMethod && d.GinSupport == 0 &&
		originalInternalProcedure(d, f.oid, f.signature, f.result)
}
