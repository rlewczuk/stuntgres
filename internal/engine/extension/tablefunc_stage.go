package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const tablefuncCrosstabStart = len(tablefuncMembers)
const tablefuncMemberCount = tablefuncCrosstabStart + 11

var tablefuncColumnNames = [...]string{"row_name", "category_1", "category_2", "category_3", "category_4"}
var tablefuncTypeRoles = [...]string{"", "tablefunc_crosstab_2", "tablefunc_crosstab_2[]", "tablefunc_crosstab_3", "tablefunc_crosstab_3[]", "tablefunc_crosstab_4", "tablefunc_crosstab_4[]"}

func tablefuncDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000}
}

func tablefuncProcedure(slot int, ext *catalog.Extension) (string, catalog.ProcedureSignature, ids.OID) {
	switch slot - tablefuncCrosstabStart {
	case 0:
		return "crosstab", "25", 2249
	case 7, 8, 9:
		return [...]string{"crosstab2", "crosstab3", "crosstab4"}[slot-tablefuncCrosstabStart-7], "25", ext.Members[tablefuncCrosstabStart+1+2*(slot-tablefuncCrosstabStart-7)].ObjectID
	case 10:
		return "crosstab", "25,23", 2249
	}
	return "", "", 0
}

func (p *TablefuncProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	destination := ext
	candidate := *ext
	ext = &candidate
	ext.Members = make([]ids.ObjectAddress, tablefuncMemberCount)
	createProc := func(slot int) error {
		name, signature, result := tablefuncProcedure(slot, ext)
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		volatility, declaration, source := expr.Stable, tablefuncDeclaration(), "crosstab"
		if slot < tablefuncCrosstabStart {
			member := tablefuncMembers[slot]
			name, signature, result = member.name, member.signature, member.result
			volatility, declaration, source = member.volatility, tablefuncNormalDeclaration(), member.source
		}
		proc := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: volatility, Strict: true, Installed: declaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: source}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		ext.Members[slot] = proc.Address()
		return nil
	}
	for slot := 0; slot <= tablefuncCrosstabStart; slot++ {
		if err := createProc(slot); err != nil {
			return err
		}
	}
	text, _ := stage.Read().LookupType(sqltype.Text)
	for n := 2; n <= 4; n++ {
		name := tablefuncTypeRoles[1+2*(n-2)]
		columns := make([]catalog.Column, n+1)
		for i := range columns {
			columns[i] = catalog.Column{Name: tablefuncColumnNames[i], Usage: text.Usage(), Nullable: true, NullabilityKnown: true}
		}
		typ, array, err := stage.Composite(ext.TargetNamespace, name, owner, columns, ext.Address())
		if err != nil {
			if e, ok := err.(*sqlstate.Error); ok {
				copy := *e
				copy.Context = fmt.Sprintf("SQL statement \"CREATE TYPE %s AS\n(\n\trow_name TEXT", name)
				for i := 1; i <= n; i++ {
					copy.Context += ",\n\t" + tablefuncColumnNames[i] + " TEXT"
				}
				copy.Context += fmt.Sprintf("\n)\"\nextension script file \"tablefunc--1.0.sql\", near line %d", [...]int{18, 25, 33}[n-2])
				return &copy
			}
			return err
		}
		slot := tablefuncCrosstabStart + 1 + 2*(n-2)
		ext.Members[slot], ext.Members[slot+1] = typ.Address(), array.Address()
	}
	for _, slot := range []int{12, 13, 14, 15} {
		if err := createProc(slot); err != nil {
			return err
		}
	}
	destination.Members = ext.Members
	return nil
}
