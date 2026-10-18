package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type intarrayProvider struct{ control extensionControl }

// Control implements extensionImplementation.
func (p *intarrayProvider) Control() extensionControl { return p.control.detached() }

var stockIntarray = &intarrayProvider{control: extensionControl{Version: "1.5", Trusted: true, Superuser: true, Relocatable: true, Comment: new("functions, operators, and index support for 1-D arrays of integers")}}

const intarrayTypeContext = "SQL statement \"CREATE TYPE query_int (\n\tINTERNALLENGTH = -1,\n\tINPUT = bqarr_in,\n\tOUTPUT = bqarr_out\n)\"\nextension script file \"intarray--1.2.sql\", near line 21"
const intarrayInputContext = "SQL statement \"CREATE FUNCTION bqarr_in(cstring)\nRETURNS query_int\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 11"

func queryIntSignature(id sqltype.ID) procedureSignature {
	return procedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *intarrayProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	available, err := e.displaceAssociatedArrayLocked(stage, ext.TargetNamespace, "query_int")
	if err != nil {
		return err
	}
	if !available || relationTypeNameOccupied(auth.catalog, ext.TargetNamespace, "query_int") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "query_int" already exists`, Context: intarrayTypeContext}
	}
	ext.Members = make([]ids.ObjectAddress, 3)
	for i, class := range [3]ids.OID{ids.TypeClassID, ids.ProcedureClassID, ids.ProcedureClassID} {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := auth.catalog.Get(ext.TargetNamespace).Name()
	typ, err := sqltype.QueryInt(sqltype.ID(ext.Members[0].ObjectID), "query_int", dependencyIdentifier(ns)+".query_int")
	if err != nil {
		return err
	}
	arrayOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	array := sqltype.LookupArray(sqltype.ID(arrayOID), associatedArrayName(stage, ext.TargetNamespace, typ.Name()), typ)
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	acl, synthetic := stage.creationTypeACL(owner, ext.TargetNamespace)
	if err := stage.Create(&typeDescriptor{SQLType: typ, namespace: ext.TargetNamespace, Extension: ext.address, revision: revision, Owner: owner, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID()}); err != nil {
		return err
	}
	if err := stage.Create(&typeDescriptor{SQLType: array, namespace: ext.TargetNamespace, revision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}); err != nil {
		return err
	}
	for i := range 2 {
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		name, signature, result := "bqarr_in", procedureSignature("2275"), ids.OID(typ.ID())
		if i == 1 {
			name, signature, result = "bqarr_out", queryIntSignature(typ.ID()), 2275
		}
		proc := &procedureDescriptor{address: ext.Members[i+1], namespace: ext.TargetNamespace, Extension: ext.address, revision: revision, name: name, Signature: signature, RoutineKind: functionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Implementation: p}
		if stage.lookupName(descriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
			if i == 0 {
				err.Context = intarrayInputContext
			} else {
				err.Context = "SQL statement \"CREATE FUNCTION bqarr_out(query_int)\nRETURNS cstring\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 16"
			}
			return err
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *intarrayProvider) ValidateExtension(v CatalogRead, ext *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intarray manifest") }
	if p != stockIntarray || ext.name != "intarray" || ext.Version != p.control.Version || len(ext.Members) != 3 {
		return invalid()
	}
	if ext.Members[0].ClassID != ids.TypeClassID || ext.Members[1].ClassID != ids.ProcedureClassID || ext.Members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.Members[0])
	if !ok || base.SQLType == nil || base.Address() != ext.Members[0] || !base.SQLType.IsQueryInt() || base.Name() != "query_int" || base.Extension != ext.address || base.Namespace() != ext.TargetNamespace || base.revision == 0 || base.Array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := (typeProvider{}).Validate(v, array); err != nil {
		return err
	}
	for i, address := range ext.Members[1:] {
		proc, ok := v.Procedure(address)
		if !ok || proc.address != address || proc.intarrayProvider() != p || proc.Extension != ext.address || proc.namespace != ext.TargetNamespace || proc.revision == 0 {
			return invalid()
		}
		name, signature, result := "bqarr_in", procedureSignature("2275"), ids.OID(base.SQLType.ID())
		if i == 1 {
			name, signature, result = "bqarr_out", queryIntSignature(base.SQLType.ID()), 2275
		}
		if proc.name != name || proc.Signature != signature || proc.Result != result || !proc.Strict || proc.Volatility != expr.Immutable || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
	}
	return nil
}

func (p *intarrayProvider) validateProcedure(v CatalogRead, proc *procedureDescriptor) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing intarray codec installation")
	}
	return p.ValidateExtension(v, ext)
}
