package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Stage publishes the bounded hstore manifest through the shared atomic stage.
func (p *HstoreProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, "hstore")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, "hstore") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "hstore" already exists`}
	}
	ext.Members = make([]ids.ObjectAddress, HstoreMemberCount)
	for i := range ext.Members {
		class := ids.ProcedureClassID
		if i == HstoreTypeSlot {
			class = ids.TypeClassID
		} else if i == HstoreArrayOperatorSlot || i == HstoreMatrixOperatorSlot {
			class = ids.OperatorClassID
		} else if i == HstoreJSONBCastSlot {
			class = ids.CastClassID
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := stage.Read().Get(ext.TargetNamespace).Name()
	typ, err := sqltype.Hstore(sqltype.ID(ext.Members[0].ObjectID), "hstore", catalog.DependencyIdentifier(ns)+".hstore")
	if err != nil {
		return err
	}
	arrayOID, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	array := sqltype.LookupArray(sqltype.ID(arrayOID), stage.AssociatedArrayName(ext.TargetNamespace, typ.Name()), typ)
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	acl, synthetic := stage.CreationTypeACL(owner, ext.TargetNamespace)
	if err := stage.Create(&catalog.Type{SQLType: typ, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID()}); err != nil {
		return err
	}
	if err := stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ext.TargetNamespace, ObjectRevision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}); err != nil {
		return err
	}
	for _, slot := range [...]int{HstoreInputSlot, HstoreOutputSlot, HstoreArrayProcedureSlot, HstoreMatrixProcedureSlot, HstoreJSONProcedureSlot, HstoreJSONLooseProcedureSlot, HstoreJSONBProcedureSlot, HstoreJSONBLooseProcedureSlot} {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		name, signature, result := "hstore_in", catalog.ProcedureSignature("2275"), ids.OID(typ.ID())
		switch slot {
		case HstoreOutputSlot:
			name, signature, result = "hstore_out", HstoreSignature(typ.ID()), 2275
		case HstoreArrayProcedureSlot, HstoreMatrixProcedureSlot:
			name, signature, result = []string{"hstore_to_array", "hstore_to_matrix"}[slot-HstoreArrayProcedureSlot], HstoreSignature(typ.ID()), 1009
		case HstoreJSONProcedureSlot, HstoreJSONLooseProcedureSlot:
			name, signature, result = []string{"hstore_to_json", "hstore_to_json_loose"}[slot-HstoreJSONProcedureSlot], HstoreSignature(typ.ID()), 114
		case HstoreJSONBProcedureSlot, HstoreJSONBLooseProcedureSlot:
			name, signature, result = []string{"hstore_to_jsonb", "hstore_to_jsonb_loose"}[slot-HstoreJSONBProcedureSlot], HstoreSignature(typ.ID()), 3802
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[slot], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/hstore", true
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	for i, name := range []string{"%%", "%#"} {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[HstoreArrayOperatorSlot+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Owner: owner, Right: ids.OID(typ.ID()), Result: 1009, Function: ext.Members[HstoreArrayProcedureSlot+i].ObjectID}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	revision, err = stage.Revision()
	if err != nil {
		return err
	}
	if err := stage.Create(&catalog.Cast{OID: ext.Members[HstoreJSONBCastSlot].ObjectID, ObjectRevision: revision, Source: typ.ID(), Target: sqltype.JSONB, Function: ext.Members[HstoreJSONBProcedureSlot], Context: "e", Method: "f", Extension: ext.Address()}); err != nil {
		return err
	}
	return nil
}
