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

var stockIntarray = &intarrayProvider{control: extensionControl{version: "1.5", trusted: true, superuser: true, relocatable: true, comment: new("functions, operators, and index support for 1-D arrays of integers")}}

const intarrayTypeContext = "SQL statement \"CREATE TYPE query_int (\n\tINTERNALLENGTH = -1,\n\tINPUT = bqarr_in,\n\tOUTPUT = bqarr_out\n)\"\nextension script file \"intarray--1.2.sql\", near line 21"
const intarrayInputContext = "SQL statement \"CREATE FUNCTION bqarr_in(cstring)\nRETURNS query_int\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 11"

func queryIntSignature(id sqltype.ID) procedureSignature {
	return procedureSignature(strconv.FormatUint(uint64(id), 10))
}

func (p *intarrayProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	available, err := e.displaceAssociatedArrayLocked(stage, ext.targetNamespace, "query_int")
	if err != nil {
		return err
	}
	if !available || relationTypeNameOccupied(auth.catalog, ext.targetNamespace, "query_int") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "query_int" already exists`, Context: intarrayTypeContext}
	}
	ext.members = make([]ids.ObjectAddress, 3)
	for i, class := range [3]ids.OID{ids.TypeClassID, ids.ProcedureClassID, ids.ProcedureClassID} {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		ext.members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := auth.catalog.Get(ext.targetNamespace).Name()
	typ, err := sqltype.QueryInt(sqltype.ID(ext.members[0].ObjectID), "query_int", dependencyIdentifier(ns)+".query_int")
	if err != nil {
		return err
	}
	arrayOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	array := sqltype.LookupArray(sqltype.ID(arrayOID), associatedArrayName(stage, ext.targetNamespace, typ.Name()), typ)
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	acl, synthetic := stage.creationTypeACL(owner, ext.targetNamespace)
	if err := stage.Create(&typeDescriptor{typ: typ, namespace: ext.targetNamespace, extension: ext.address, revision: revision, owner: owner, acl: acl, aclSyntheticDefault: synthetic, array: array.ID()}); err != nil {
		return err
	}
	if err := stage.Create(&typeDescriptor{typ: array, namespace: ext.targetNamespace, revision: revision, owner: owner, acl: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), aclSyntheticDefault: true}); err != nil {
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
		proc := &procedureDescriptor{address: ext.members[i+1], namespace: ext.targetNamespace, extension: ext.address, revision: revision, name: name, signature: signature, routineKind: functionRoutine, owner: owner, result: result, volatility: expr.Immutable, strict: true, implementation: p}
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
	if p != stockIntarray || ext.name != "intarray" || ext.version != p.control.version || len(ext.members) != 3 {
		return invalid()
	}
	if ext.members[0].ClassID != ids.TypeClassID || ext.members[1].ClassID != ids.ProcedureClassID || ext.members[2].ClassID != ids.ProcedureClassID {
		return invalid()
	}
	base, ok := v.Type(ext.members[0])
	if !ok || base.typ == nil || base.Address() != ext.members[0] || !base.typ.IsQueryInt() || base.Name() != "query_int" || base.extension != ext.address || base.Namespace() != ext.targetNamespace || base.revision == 0 || base.array == 0 {
		return invalid()
	}
	array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.array)})
	if !ok || array.typ == nil || !array.typ.IsArray() || array.typ.Concrete() || array.typ.Element() != base.typ || array.Namespace() != base.Namespace() || array.owner != base.owner || array.extension != (ids.ObjectAddress{}) {
		return invalid()
	}
	if err := (typeProvider{}).Validate(v, array); err != nil {
		return err
	}
	for i, address := range ext.members[1:] {
		proc, ok := v.Procedure(address)
		if !ok || proc.address != address || proc.intarrayProvider() != p || proc.extension != ext.address || proc.namespace != ext.targetNamespace || proc.revision == 0 {
			return invalid()
		}
		name, signature, result := "bqarr_in", procedureSignature("2275"), ids.OID(base.typ.ID())
		if i == 1 {
			name, signature, result = "bqarr_out", queryIntSignature(base.typ.ID()), 2275
		}
		if proc.name != name || proc.signature != signature || proc.result != result || !proc.strict || proc.volatility != expr.Immutable || proc.conversion.Name() != "" || proc.dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
	}
	return nil
}

func (p *intarrayProvider) validateProcedure(v CatalogRead, proc *procedureDescriptor) error {
	ext, ok := v.Extension(proc.extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing intarray codec installation")
	}
	return p.ValidateExtension(v, ext)
}
