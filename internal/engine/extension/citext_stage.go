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

// Stage allocates the scalar, codecs and six scalar comparison pairs atomically.
func (p *CitextProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, "citext")
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, "citext") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "citext" already exists`, Context: CitextTypeContext}
	}
	ext.Members = make([]ids.ObjectAddress, CitextMemberCount)
	for i := range ext.Members {
		class := ids.ProcedureClassID
		if i == 0 {
			class = ids.TypeClassID
		} else if i >= CitextComparisonOperatorStart {
			class = ids.OperatorClassID
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := stage.Read().Get(ext.TargetNamespace).Name()
	typ, err := sqltype.Citext(sqltype.ID(ext.Members[0].ObjectID), "citext", catalog.DependencyIdentifier(ns)+".citext")
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
		name, signature, result := "citextin", catalog.ProcedureSignature("2275"), ids.OID(typ.ID())
		if i == 1 {
			name, signature, result = "citextout", CitextSignature(typ.ID()), 2275
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[i+1], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: [2]string{"textin", "textout"}[i]}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
			if i == 0 {
				err.Context = CitextInputContext
			} else {
				err.Context = "SQL statement \"CREATE FUNCTION citextout(citext)\nRETURNS cstring\nAS 'textout'\nLANGUAGE internal IMMUTABLE STRICT PARALLEL SAFE\"\nextension script file \"citext--1.4.sql\", near line 27"
			}
			return err
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	// CREATE TYPE resolves its I/O names through the registry's staged script
	// lookup, which excludes the installer's own temporary namespace.
	helpers := [2]ScriptFunction{{Name: "citextin", Signature: "2275"}, {Name: "citextout", Signature: CitextSignature(typ.ID())}}
	found, err := stage.ScriptFunctions(owner, ext.TargetNamespace, helpers[:])
	if err != nil {
		return err
	}
	for i, proc := range found {
		if proc == nil {
			args := "cstring"
			if i == 1 {
				args = "citext"
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", helpers[i].Name, args), Context: "SQL statement \"CREATE TYPE citext (\n    INPUT          = citextin,\n    OUTPUT         = citextout,\n    RECEIVE        = citextrecv,\n    SEND           = citextsend,\n    INTERNALLENGTH = VARIABLE,\n    STORAGE        = extended,\n    -- make it a non-preferred member of string type category\n    CATEGORY       = 'S',\n    PREFERRED      = false,\n    COLLATABLE     = true\n)\"\nextension script file \"citext--1.4.sql\", near line 46"}
		}
	}
	return p.stageComparisons(stage, ext, owner, typ)
}
