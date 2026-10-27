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

// unaccentScriptFunctions are the template helpers the unaccent script names,
// with their argument lists as the 42883 message spells them.
var (
	unaccentScriptFunctions = [2]ScriptFunction{{Name: "unaccent_init", Signature: catalog.UnaccentInitSignature}, {Name: "unaccent_lexize", Signature: catalog.UnaccentLexizeSignature}}
	unaccentScriptArguments = [2]string{"internal", "internal, internal, internal, internal"}
)

// Stage installs the unaccent functions, text search template and dictionary
// into stage, owned by owner, allocating identities in member order.
func (p *UnaccentProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, 6)
	for i, class := range [6]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	signatures := [4]catalog.ProcedureSignature{catalog.UnaccentDictionarySignature, catalog.UnaccentTextSignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}
	for i, signature := range signatures {
		rev, err := stage.Revision()
		if err != nil {
			return err
		}
		name := "unaccent"
		result := ids.OID(25)
		vol := expr.Stable
		strict := true
		if i == 2 {
			name = "unaccent_init"
			result = 2281
			vol = expr.Volatile
			strict = false
		}
		if i == 3 {
			name = "unaccent_lexize"
			result = 2281
			vol = expr.Volatile
			strict = false
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: vol, Strict: strict, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: [4]string{"unaccent_dict", "unaccent_dict", "unaccent_init", "unaccent_lexize"}[i]}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/unaccent", true
		if i < 2 {
			proc.Dictionary = ext.Members[5]
		}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return MemberConflict(proc, ext.TargetNamespace)
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	// The script resolves its helpers as the member owner, against the staged
	// catalog, with the target schema as its whole search path.
	found, err := stage.ScriptFunctions(owner, ext.TargetNamespace, unaccentScriptFunctions[:])
	if err != nil {
		return err
	}
	var helpers [2]ids.ObjectAddress
	for i, procedure := range found {
		if procedure == nil {
			helper := unaccentScriptFunctions[i]
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", helper.Name, unaccentScriptArguments[i]), Context: UnaccentTemplateContext}
		}
		helpers[i] = procedure.Address()
	}
	rev, err := stage.Revision()
	if err != nil {
		return err
	}
	tmpl := &catalog.TSTemplate{ObjectAddress: ext.Members[4], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "unaccent", Init: helpers[0], Lexize: helpers[1], Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(tmpl)) != (ids.ObjectAddress{}) {
		return MemberConflict(tmpl, ext.TargetNamespace)
	}
	if err := stage.Create(tmpl); err != nil {
		return err
	}
	rev, err = stage.Revision()
	if err != nil {
		return err
	}
	dict := &catalog.TSDictionary{ObjectAddress: ext.Members[5], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "unaccent", Template: tmpl.Address(), Owner: owner, Implementation: p}
	dict.Options = []catalog.DictionaryOption{{Name: "rules", Value: "unaccent", Supplied: true}}
	dict.Runtime = functions.StockUnaccent
	if stage.LookupName(catalog.DescriptorNameKey(dict)) != (ids.ObjectAddress{}) {
		return MemberConflict(dict, ext.TargetNamespace)
	}
	return stage.Create(dict)
}
