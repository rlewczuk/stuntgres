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

// Stage allocates the complete scalar, routine, comparison and spatial
// manifest before creating descriptors. The caller publishes the stage once.
func (p *CubeProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, "cube")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, "cube") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "cube" already exists`, Context: CubeTypeContext}
	}
	ext.Members = make([]ids.ObjectAddress, CubeMemberCount)
	for i := range ext.Members {
		class := ids.ProcedureClassID
		switch {
		case i == 0:
			class = ids.TypeClassID
		case i >= 10 && i < 16 || i >= cubeCoordinateOperatorStart && i < cubeSpatialProcedureStart || i >= cubeSpatialOperatorStart:
			class = ids.OperatorClassID
		case i == 16:
			class = ids.OperatorFamilyClassID
		case i == 17:
			class = ids.OperatorClassCatalogID
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := stage.Read().Get(ext.TargetNamespace).Name()
	typ, err := sqltype.Cube(sqltype.ID(ext.Members[0].ObjectID), "cube", catalog.DependencyIdentifier(ns)+".cube")
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
	for i, spec := range cubeRoutines {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		name, signature, result := spec.name, spec.signature(typ.ID()), spec.resultOID(typ.ID())
		proc := &catalog.Procedure{ObjectAddress: ext.Members[cubeRoutineMember(i)], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: spec.source}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/cube", true
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
			if i == 0 {
				err.Context = CubeInputContext
			} else if i == 1 {
				err.Context = "SQL statement \"CREATE FUNCTION cube_out(cube)\nRETURNS cstring\nAS '$libdir/cube'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"cube--1.2.sql\", near line 16"
			}
			return err
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	if err := p.stageComparisons(stage, ext, owner, typ); err != nil {
		return err
	}
	if err := p.stageCoordinates(stage, ext, owner, typ); err != nil {
		return err
	}
	return p.stageSpatial(stage, ext, owner, typ)
}
