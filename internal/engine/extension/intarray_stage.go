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

const (
	IntarraySetProcedureStart = 6
	IntarrayEstimatorStart    = 16
	IntarraySetOperatorStart  = 22
	IntarraySliceProcedureStart = 32
	IntarraySliceOperatorStart  = 37
	IntarrayMemberCount         = 39
)

var intarrayTypeNames = [2]string{"query_int", "intbig_gkey"}
var intarrayIONames = [4]string{"bqarr_in", "bqarr_out", "_intbig_in", "_intbig_out"}
var intarrayTypeContexts = [2]string{
	IntarrayTypeContext,
	"SQL statement \"CREATE TYPE intbig_gkey (\n        INTERNALLENGTH = -1,\n        INPUT = _intbig_in,\n        OUTPUT = _intbig_out\n)\"\nextension script file \"intarray--1.2.sql\", near line 432",
}
var intarrayIOContexts = [4]string{
	IntarrayInputContext,
	"SQL statement \"CREATE FUNCTION bqarr_out(query_int)\nRETURNS cstring\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 16",
	"SQL statement \"CREATE FUNCTION _intbig_in(cstring)\nRETURNS intbig_gkey\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 422",
	"SQL statement \"CREATE FUNCTION _intbig_out(intbig_gkey)\nRETURNS cstring\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line 427",
}

// Stage installs both real base types and their I/O in one private candidate.
// The original query_int slots remain stable; generated companions are expanded
// by the ordinary extension installation path, not declared as extra roles.
func (p *IntarrayProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, IntarrayMemberCount)
	for i := range ext.Members {
		class := ids.ProcedureClassID
		if i < IntarraySetProcedureStart && i%3 == 0 {
			class = ids.TypeClassID
		}
		if i >= IntarraySetOperatorStart && i < IntarraySliceProcedureStart || i >= IntarraySliceOperatorStart {
			class = ids.OperatorClassID
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := stage.Read().Get(ext.TargetNamespace).Name()
	for group, name := range intarrayTypeNames {
		available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, name)
		if err != nil {
			return err
		}
		if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, name) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`type "%s" already exists`, name), Context: intarrayTypeContexts[group]}
		}
		slot := group * 3
		constructor := sqltype.QueryInt
		if group == 1 {
			constructor = sqltype.IntbigGkey
		}
		typ, err := constructor(sqltype.ID(ext.Members[slot].ObjectID), name, catalog.DependencyIdentifier(ns)+"."+name)
		if err != nil {
			return err
		}
		arrayOID, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		arrayName := stage.AssociatedArrayName(ext.TargetNamespace, name)
		var array *sqltype.Descriptor
		if group == 1 {
			array, err = sqltype.Array(sqltype.ID(arrayOID), arrayName, typ)
			if err != nil {
				return err
			}
		} else {
			array = sqltype.LookupArray(sqltype.ID(arrayOID), arrayName, typ)
		}
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
		for direction := range 2 {
			revision, err := stage.Revision()
			if err != nil {
				return err
			}
			index := group*2 + direction
			name, signature, result := intarrayIONames[index], catalog.ProcedureSignature("2275"), ids.OID(typ.ID())
			if direction == 1 {
				signature, result = QueryIntSignature(typ.ID()), 2275
			}
			proc := &catalog.Procedure{ObjectAddress: ext.Members[slot+direction+1], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/_int", HasBinary: true}, Implementation: p}
			if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name), Context: intarrayIOContexts[index]}
			}
			if err := stage.Create(proc); err != nil {
				return err
			}
		}
	}
	if err := p.stageSet(stage, ext, owner); err != nil {
		return err
	}
	return p.stageSlice(stage, ext, owner)
}
