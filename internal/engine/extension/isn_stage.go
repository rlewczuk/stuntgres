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

// Stage installs all scalar roles and their codecs into the statement's private stage.
func (p *IsnProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, isnMemberCount)
	for _, recipe := range isnRecipes {
		for offset, class := range [...]ids.OID{ids.TypeClassID, ids.ProcedureClassID, ids.ProcedureClassID} {
			oid, err := stage.AllocateOID()
			if err != nil { return err }
			ext.Members[recipe.slot+offset] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
		}
	}
	ns := stage.Read().Get(ext.TargetNamespace).Name()
	for _, recipe := range isnRecipes {
		// The script declares input, then output, before completing the type.
		// An occupied type contributes only its signature identity here; it
		// never becomes the scalar installed under this manifest's new OID.
		outputType := ext.Members[recipe.slot].ObjectID
		existing := stage.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ext.TargetNamespace, Name: recipe.name})
		if existing != (ids.ObjectAddress{}) {
			outputType = existing.ObjectID
		}
		for codec := range 2 {
			name, signature := recipe.input, catalog.ProcedureSignature("2275")
			if codec == 1 {
				name, signature = recipe.output, IsnSignature(sqltype.ID(outputType))
			}
			key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: name, Signature: string(signature)}
			if stage.LookupName(key) != (ids.ObjectAddress{}) {
				return isnCodecConflict(recipe, codec)
			}
		}
		available, err := stage.DisplaceAssociatedArray(ext.TargetNamespace, recipe.name)
		if err != nil { return err }
		if !available || typedef.RelationTypeNameOccupied(stage.Read(), ext.TargetNamespace, recipe.name) {
			e := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`type "%s" already exists`, recipe.name)}
			if recipe.name == "isbn" { e.Context = "SQL statement \"CREATE TYPE isbn (\n\tINPUT = isbn_in,\n\tOUTPUT = isn_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 112" }
			if recipe.name == "isbn13" { e.Context = "SQL statement \"CREATE TYPE isbn13 (\n\tINPUT = isbn13_in,\n\tOUTPUT = ean13_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 50" }
			if recipe.name == "ismn" { e.Context = "SQL statement \"CREATE TYPE ismn (\n\tINPUT = ismn_in,\n\tOUTPUT = isn_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 132" }
			if recipe.name == "ismn13" { e.Context = "SQL statement \"CREATE TYPE ismn13 (\n\tINPUT = ismn13_in,\n\tOUTPUT = ean13_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 70" }
			if recipe.name == "issn" { e.Context = "SQL statement \"CREATE TYPE issn (\n\tINPUT = issn_in,\n\tOUTPUT = isn_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 152" }
			if recipe.name == "issn13" { e.Context = "SQL statement \"CREATE TYPE issn13 (\n\tINPUT = issn13_in,\n\tOUTPUT = ean13_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 90" }
			if recipe.name == "upc" { e.Context = "SQL statement \"CREATE TYPE upc (\n\tINPUT = upc_in,\n\tOUTPUT = isn_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 172" }
			return e
		}
		typ, err := recipe.construct(sqltype.ID(ext.Members[recipe.slot].ObjectID), recipe.name, catalog.DependencyIdentifier(ns)+"."+recipe.name)
		if err != nil { return err }
		arrayOID, err := stage.AllocateOID(); if err != nil { return err }
		array := sqltype.LookupArray(sqltype.ID(arrayOID), stage.AssociatedArrayName(ext.TargetNamespace, typ.Name()), typ)
		revision, err := stage.Revision(); if err != nil { return err }
		acl, synthetic := stage.CreationTypeACL(owner, ext.TargetNamespace)
		comment := ""
		if recipe.name == "isbn13" { comment = "International Standard Book Number 13 (ISBN13)" }
		if recipe.name == "ismn" { comment = "International Standard Music Number (ISMN)" }
		if recipe.name == "ismn13" { comment = "International Standard Music Number 13 (ISMN13)" }
		if recipe.name == "issn" { comment = "International Standard Serial Number (ISSN)" }
		if recipe.name == "issn13" { comment = "International Standard Serial Number 13 (ISSN13)" }
		if recipe.name == "upc" { comment = "Universal Product Code (UPC)" }
		if err := stage.Create(&catalog.Type{SQLType: typ, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, Owner: owner, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID(), Comment: comment}); err != nil { return err }
		if err := stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ext.TargetNamespace, ObjectRevision: revision, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}); err != nil { return err }
		for i := range 2 {
			revision, err := stage.Revision(); if err != nil { return err }
			name, source, signature, result := recipe.input, recipe.inputSource, catalog.ProcedureSignature("2275"), ids.OID(typ.ID())
			if i == 1 { name, source, signature, result = recipe.output, recipe.output, IsnSignature(typ.ID()), 2275 }
			proc := &catalog.Procedure{ObjectAddress: ext.Members[recipe.slot+1+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: source}, Implementation: p}
			if err := stage.Create(proc); err != nil { return err }
		}
	}
	return nil
}

func isnCodecConflict(recipe isnRecipe, codec int) error {
	name := recipe.input
	if codec == 1 {
		name = recipe.output
	}
	e := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
	if recipe.name == "isbn" || recipe.name == "ismn" || recipe.name == "issn" || recipe.name == "upc" {
		argument, result, line := "cstring", recipe.name, 100
		if recipe.name == "ismn" { line = 120 }
		if recipe.name == "issn" { line = 140 }
		if recipe.name == "upc" { line = 160 }
		if codec == 1 {
			argument, result, line = recipe.name, "cstring", line+6
		}
		e.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line %d", name, argument, result, line)
	}
	if recipe.name == "ismn13" {
		argument, result, source, line := "cstring", recipe.name, "'$libdir/isn', 'ismn_in'", 58
		if codec == 1 {
			argument, result, source, line = recipe.name, "cstring", "'$libdir/isn'", 64
		}
		e.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS %s\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line %d", name, argument, result, source, line)
	}
	if recipe.name == "issn13" {
		argument, result, source, line := "cstring", recipe.name, "'$libdir/isn', 'issn_in'", 78
		if codec == 1 { argument, result, source, line = recipe.name, "cstring", "'$libdir/isn'", 84 }
		e.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS %s\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line %d", name, argument, result, source, line)
	}
	return e
}
