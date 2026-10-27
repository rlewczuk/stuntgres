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
		class := hstoreMemberClass(i)
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
	for _, recipe := range hstoreRoutines {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		signature, result := recipe.declaration(typ.ID())
		name := recipe.name
		proc := &catalog.Procedure{ObjectAddress: ext.Members[recipe.slot], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: recipe.strict, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: recipe.symbol, Binary: "$libdir/hstore", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	for _, recipe := range hstoreOperators {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		left, right := recipe.operands(typ.ID())
		op := &catalog.Operator{ObjectAddress: ext.Members[recipe.slot], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: recipe.name, Owner: owner, Left: left, Right: right, Result: ids.OID(recipe.result), Function: ext.Members[recipe.procedure].ObjectID, Commutator: recipe.commutatorOID(ext), Restriction: recipe.restriction, Join: recipe.join}
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
	return p.stageComparisons(stage, ext, owner, typ)
}
