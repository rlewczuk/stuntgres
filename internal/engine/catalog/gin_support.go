package catalog

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Internal GIN support declarations are metadata capabilities, not scalar C ABI
// emulation. Extraction and posting operations belong to the index owner.
var ginSupportDeclarations = [...]struct {
	oid       ids.OID
	name      string
	signature ProcedureSignature
	result    ids.OID
	family    ids.OID
	slot      int16
}{
	{2743, "ginarrayextract", "2277,2281,2281", 2281, 2745, 2},
	{2774, "ginqueryarrayextract", "2277,2281,21,2281,2281,2281,2281", 2281, 2745, 3},
	{2744, "ginarrayconsistent", "2281,21,2277,23,2281,2281,2281,2281", 16, 2745, 4},
	{3920, "ginarraytriconsistent", "2281,21,2277,23,2281,2281,2281", 18, 2745, 6},
	{3724, "gin_cmp_tslexeme", "25,25", 23, 3659, 1},
	{3656, "gin_extract_tsvector", "3614,2281,2281", 2281, 3659, 2},
	{3657, "gin_extract_tsquery", "3614,2281,21,2281,2281,2281,2281", 2281, 3659, 3},
	{3658, "gin_tsquery_consistent", "2281,21,3614,23,2281,2281,2281,2281", 16, 3659, 4},
	{2700, "gin_cmp_prefix", "25,25,21,2281", 23, 3659, 5},
	{3921, "gin_tsquery_triconsistent", "2281,21,3614,23,2281,2281,2281", 18, 3659, 6},
}

func GinSupportProcedures(owner authorization.PrincipalRef) []Procedure {
	out := make([]Procedure, len(ginSupportDeclarations))
	for i, f := range ginSupportDeclarations {
		out[i] = Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: f.oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: f.name, Signature: f.signature, RoutineKind: FunctionRoutine, Owner: owner, Result: f.result, Volatility: expr.Immutable, Strict: true, Display: RoutineDisplay{Language: InternalRoutineLanguage, Source: f.name}, GinSupport: uint8(i + 1)}
	}
	return out
}

func ValidateGinSupport(d *Procedure) error {
	if !OriginalGinSupport(d) {
		return fmt.Errorf("stuntgres: invalid GIN support identity")
	}
	if !supportedInternalProcedureMetadata(d, ginSupportDeclarations[d.GinSupport-1].name, expr.Immutable) {
		return fmt.Errorf("stuntgres: invalid GIN support metadata")
	}
	return nil
}

// OriginalGinSupport authenticates the released intrinsic operation, not its
// current mutable presentation or a newly installed alias with the same ABI.
func OriginalGinSupport(d *Procedure) bool {
	if d == nil || d.GinSupport == 0 || int(d.GinSupport) > len(ginSupportDeclarations) {
		return false
	}
	f := ginSupportDeclarations[d.GinSupport-1]
	return d.AccessMethod == NoAccessMethod && d.BrinSupport == 0 &&
		originalInternalProcedure(d, f.oid, f.signature, f.result)
}

func GinSupportRoutine(family ids.OID, slot int16) (ids.OID, bool) {
	for _, f := range ginSupportDeclarations {
		if f.family == family && f.slot == slot {
			return f.oid, true
		}
	}
	return 0, false
}
