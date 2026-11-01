package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

var dictIntScriptFunctions = [2]ScriptFunction{
	{Name: "dintdict_init", Signature: catalog.UnaccentInitSignature},
	{Name: "dintdict_lexize", Signature: catalog.UnaccentLexizeSignature},
}

// REL_18_6 contrib/dict_int/dict_int--1.0.sql, template statement at line 16.
const dictIntTemplateContext = "SQL statement \"CREATE TEXT SEARCH TEMPLATE intdict_template (\n        LEXIZE = dintdict_lexize,\n\tINIT   = dintdict_init\n)\"\nextension script file \"dict_int--1.0.sql\", near line 16"

func (p *DictIntProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, 4)
	for i, class := range [4]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	for i, helper := range dictIntScriptFunctions {
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		procedure := &catalog.Procedure{ObjectAddress: ext.Members[i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: helper.Name, Signature: helper.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2281, Volatility: expr.Volatile, Strict: true, Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'}, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: helper.Name, Binary: "$libdir/dict_int", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
			return MemberConflict(procedure, ext.TargetNamespace)
		}
		if err := stage.Create(procedure); err != nil {
			return err
		}
	}
	found, err := stage.ScriptFunctions(owner, ext.TargetNamespace, dictIntScriptFunctions[:])
	if err != nil {
		return err
	}
	var helpers [2]ids.ObjectAddress
	for i, procedure := range found {
		if procedure == nil {
			arguments := "internal"
			if i == 1 {
				arguments = "internal, internal, internal, internal"
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", dictIntScriptFunctions[i].Name, arguments), Context: dictIntTemplateContext}
		}
		helpers[i] = procedure.Address()
	}
	rev, err := stage.Revision()
	if err != nil {
		return err
	}
	template := &catalog.TSTemplate{ObjectAddress: ext.Members[2], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "intdict_template", Init: helpers[0], Lexize: helpers[1], Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(template)) != (ids.ObjectAddress{}) {
		return MemberConflict(template, ext.TargetNamespace)
	}
	if err := stage.Create(template); err != nil {
		return err
	}
	rev, err = stage.Revision()
	if err != nil {
		return err
	}
	dictionary := &catalog.TSDictionary{ObjectAddress: ext.Members[3], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "intdict", Template: template.Address(), Owner: owner, Implementation: p, Runtime: functions.NewIntDictionary(6, false, false)}
	if stage.LookupName(catalog.DescriptorNameKey(dictionary)) != (ids.ObjectAddress{}) {
		return MemberConflict(dictionary, ext.TargetNamespace)
	}
	return stage.Create(dictionary)
}
