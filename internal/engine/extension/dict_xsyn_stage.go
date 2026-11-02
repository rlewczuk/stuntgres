package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

var dictXsynScriptFunctions = [2]ScriptFunction{
	{Name: "dxsyn_init", Signature: catalog.UnaccentInitSignature},
	{Name: "dxsyn_lexize", Signature: catalog.UnaccentLexizeSignature},
}

// REL_18_6 contrib/dict_xsyn/dict_xsyn--1.0.sql, template statement at line 16.
const dictXsynTemplateContext = "SQL statement \"CREATE TEXT SEARCH TEMPLATE xsyn_template (\n        LEXIZE = dxsyn_lexize,\n\tINIT   = dxsyn_init\n)\"\nextension script file \"dict_xsyn--1.0.sql\", near line 16"

func (p *DictXsynProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, 4)
	for i, class := range [4]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	for i, helper := range dictXsynScriptFunctions {
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		procedure := &catalog.Procedure{ObjectAddress: ext.Members[i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: helper.Name, Signature: helper.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2281, Volatility: expr.Volatile, Strict: true, Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'}, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: helper.Name, Binary: "$libdir/dict_xsyn", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
			return dictXsynMemberConflict(procedure, ext.TargetNamespace)
		}
		if err := stage.Create(procedure); err != nil {
			return err
		}
	}
	found, err := stage.ScriptFunctions(owner, ext, dictXsynScriptFunctions[:])
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
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", dictXsynScriptFunctions[i].Name, arguments), Context: dictXsynTemplateContext}
		}
		helpers[i] = procedure.Address()
	}
	rev, err := stage.Revision()
	if err != nil {
		return err
	}
	template := &catalog.TSTemplate{ObjectAddress: ext.Members[2], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "xsyn_template", Init: helpers[0], Lexize: helpers[1], Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(template)) != (ids.ObjectAddress{}) {
		return dictXsynMemberConflict(template, ext.TargetNamespace)
	}
	if err := stage.Create(template); err != nil {
		return err
	}
	rev, err = stage.Revision()
	if err != nil {
		return err
	}
	runtime, err := functions.CompileXsynDictionary(context.Background(), false, "", true, false, true, true)
	if err != nil {
		return err
	}
	dictionary := &catalog.TSDictionary{ObjectAddress: ext.Members[3], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "xsyn", Comment: "eXtended synonym dictionary", Template: template.Address(), Owner: owner, Implementation: p, Runtime: runtime}
	if stage.LookupName(catalog.DescriptorNameKey(dictionary)) != (ids.ObjectAddress{}) {
		return dictXsynMemberConflict(dictionary, ext.TargetNamespace)
	}
	return stage.Create(dictionary)
}

// Conflicts retain the failed declaration's actual released SQL, not another
// provider's script context. Detection and publication remain ordinary staging.
func dictXsynMemberConflict(d catalog.Descriptor, ns ids.ObjectAddress) error {
	err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR"}
	switch d := d.(type) {
	case *catalog.Procedure:
		args, line := "internal", 6
		if d.Signature == catalog.UnaccentLexizeSignature {
			args, line = "internal, internal, internal, internal", 11
		}
		err.Code = "42723"
		err.Message = fmt.Sprintf(`function "%s" already exists with same argument types`, d.Name())
		err.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n        RETURNS internal\n        AS '$libdir/dict_xsyn'\n        LANGUAGE C STRICT\"\nextension script file \"dict_xsyn--1.0.sql\", near line %d", d.Name(), args, line)
	case *catalog.TSTemplate:
		err.Code, err.Schema, err.Table, err.Constraint = "23505", "pg_catalog", "pg_ts_template", "pg_ts_template_tmplname_index"
		err.Detail = fmt.Sprintf("Key (tmplname, tmplnamespace)=(%s, %d) already exists.", d.Name(), ns.ObjectID)
		err.Context = dictXsynTemplateContext
	case *catalog.TSDictionary:
		err.Code, err.Schema, err.Table, err.Constraint = "23505", "pg_catalog", "pg_ts_dict", "pg_ts_dict_dictname_index"
		err.Detail = fmt.Sprintf("Key (dictname, dictnamespace)=(%s, %d) already exists.", d.Name(), ns.ObjectID)
		err.Context = "SQL statement \"CREATE TEXT SEARCH DICTIONARY xsyn (\n\tTEMPLATE = xsyn_template\n)\"\nextension script file \"dict_xsyn--1.0.sql\", near line 21"
	}
	if err.Code == "23505" {
		err.Message = fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, err.Constraint)
	}
	return err
}
