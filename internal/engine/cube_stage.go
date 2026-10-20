package engine

import (
	"fmt"
	"stuntgres/internal/engine/extension"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// cubeInstaller stages the cube provider for the engine. Installed
// members hold the bare provider, never the installer.
type cubeInstaller struct{ *extension.CubeProvider }

func (p cubeInstaller) stage(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
	available, err := e.displaceAssociatedArrayLocked(stage, ext.TargetNamespace, "cube")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(auth.Catalog, ext.TargetNamespace, "cube") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "cube" already exists`, Context: extension.CubeTypeContext}
	}
	ext.Members = make([]ids.ObjectAddress, 3)
	for i, class := range [3]ids.OID{ids.TypeClassID, ids.ProcedureClassID, ids.ProcedureClassID} {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := auth.Catalog.Get(ext.TargetNamespace).Name()
	typ, err := sqltype.Cube(sqltype.ID(ext.Members[0].ObjectID), "cube", catalog.DependencyIdentifier(ns)+".cube")
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
	if err := stage.Create(&catalog.Type{SQLType: typ, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID()}); err != nil {
		return err
	}
	if err := stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ext.TargetNamespace, ObjectRevision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}); err != nil {
		return err
	}
	for i := range 2 {
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		name, signature, result := "cube_in", catalog.ProcedureSignature("2275"), ids.OID(typ.ID())
		if i == 1 {
			name, signature, result = "cube_out", extension.CubeSignature(typ.ID()), 2275
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[i+1], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Implementation: p.CubeProvider}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
			if i == 0 {
				err.Context = extension.CubeInputContext
			} else {
				err.Context = "SQL statement \"CREATE FUNCTION cube_out(cube)\nRETURNS cstring\nAS '$libdir/cube'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"cube--1.2.sql\", near line 16"
			}
			return err
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}
