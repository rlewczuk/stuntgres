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

// Stage installs all scalar triples privately before the complete graph is published.
func (p *LtreeProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, LtreeMemberCount)
	for i := range LtreeMemberCount {
		oid, err := stage.AllocateOID()
		if err != nil { return err }
		class := ids.ProcedureClassID
		if i%3 == 0 { class = ids.TypeClassID }
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	for _, recipe := range ltreeRecipes {
		available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, recipe.name)
		if err != nil {
			return err
		}
		if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, recipe.name) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`type "%s" already exists`, recipe.name), Context: ltreeTypeContext(recipe.name)}
		}
		ns := stage.Read().Get(ext.TargetNamespace).Name()
		typ, err := recipe.constructor(sqltype.ID(ext.Members[recipe.slot].ObjectID), recipe.name, catalog.DependencyIdentifier(ns)+"."+recipe.name)
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
			name, signature, result := recipe.input, catalog.ProcedureSignature("2275"), ids.OID(typ.ID())
			if i == 1 {
				name, signature, result = recipe.output, LquerySignature(typ.ID()), 2275
			}
			proc := &catalog.Procedure{ObjectAddress: ext.Members[recipe.slot+i+1], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: [2]string{recipe.input, recipe.output}[i]}, Implementation: p}
			if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
				err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
				err.Context = ltreeCodecContext(recipe.name, i == 1)
				return err
			}
			if err := stage.Create(proc); err != nil {
				return err
			}
		}
	}
	return nil
}

func ltreeTypeContext(name string) string {
	if name == "lquery" { return LtreeTypeContext }
	if name == "ltxtquery" { return "SQL statement \"CREATE TYPE ltxtquery (\n\tINTERNALLENGTH = -1,\n\tINPUT = ltxtq_in,\n\tOUTPUT = ltxtq_out,\n\tSTORAGE = extended\n)\"\nextension script file \"ltree--1.1.sql\", near line 424" }
	return "SQL statement \"CREATE TYPE ltree (\n\tINTERNALLENGTH = -1,\n\tINPUT = ltree_in,\n\tOUTPUT = ltree_out,\n\tSTORAGE = extended\n)\"\nextension script file \"ltree--1.1.sql\", near line 22"
}

func ltreeCodecContext(name string, output bool) string {
	if name == "lquery" && !output { return LtreeInputContext }
	line, argument, result, suffix := 6, "cstring", name, "_in"
	if output { line, argument, result, suffix = 11, name, "cstring", "_out" }
	if name == "lquery" { line += 299 }
	if name == "ltxtquery" {
		name = "ltxtq"
		line = 414
		if output { line = 419 }
	}
	return fmt.Sprintf("SQL statement \"CREATE FUNCTION %s%s(%s)\nRETURNS %s\nAS '$libdir/ltree'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"ltree--1.1.sql\", near line %d", name, suffix, argument, result, line)
}
