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

// Stage installs the lquery type, its array and its two I/O functions into
// stage, owned by owner, allocating identities in member order.
func (p *LtreeProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, "lquery")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, "lquery") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "lquery" already exists`, Context: LtreeTypeContext}
	}
	ext.Members = make([]ids.ObjectAddress, 3)
	for i, class := range [3]ids.OID{ids.TypeClassID, ids.ProcedureClassID, ids.ProcedureClassID} {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := stage.Read().Get(ext.TargetNamespace).Name()
	typ, err := sqltype.Lquery(sqltype.ID(ext.Members[0].ObjectID), "lquery", catalog.DependencyIdentifier(ns)+".lquery")
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
	for i := range 2 {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		name, signature, result := "lquery_in", catalog.ProcedureSignature("2275"), ids.OID(typ.ID())
		if i == 1 {
			name, signature, result = "lquery_out", LquerySignature(typ.ID()), 2275
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[i+1], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: [2]string{"lquery_in", "lquery_out"}[i]}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
			if i == 0 {
				err.Context = LtreeInputContext
			} else {
				err.Context = "SQL statement \"CREATE FUNCTION lquery_out(lquery)\nRETURNS cstring\nAS '$libdir/ltree'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"ltree--1.1.sql\", near line 310"
			}
			return err
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}
