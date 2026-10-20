package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// unaccentScriptFunctions are the template helpers the unaccent script names,
// with their argument lists as the 42883 message spells them.
var (
	unaccentScriptFunctions = [2]extension.ScriptFunction{{Name: "unaccent_init", Signature: catalog.UnaccentInitSignature}, {Name: "unaccent_lexize", Signature: catalog.UnaccentLexizeSignature}}
	unaccentScriptArguments = [2]string{"internal", "internal, internal, internal, internal"}
)

// unaccentInstaller stages the unaccent provider for the engine. Installed
// members hold the bare provider, never the installer.
type unaccentInstaller struct{ *extension.UnaccentProvider }

func (p unaccentInstaller) Stage(stage extension.Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
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
		proc := &catalog.Procedure{ObjectAddress: ext.Members[i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: vol, Strict: strict, Implementation: p.UnaccentProvider}
		if i < 2 {
			proc.Dictionary = ext.Members[5]
		}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return extension.MemberConflict(proc, ext.TargetNamespace)
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
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", helper.Name, unaccentScriptArguments[i]), Context: extension.UnaccentTemplateContext}
		}
		helpers[i] = procedure.Address()
	}
	rev, err := stage.Revision()
	if err != nil {
		return err
	}
	tmpl := &catalog.TSTemplate{ObjectAddress: ext.Members[4], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "unaccent", Init: helpers[0], Lexize: helpers[1], Implementation: p.UnaccentProvider}
	if stage.LookupName(catalog.DescriptorNameKey(tmpl)) != (ids.ObjectAddress{}) {
		return extension.MemberConflict(tmpl, ext.TargetNamespace)
	}
	if err := stage.Create(tmpl); err != nil {
		return err
	}
	rev, err = stage.Revision()
	if err != nil {
		return err
	}
	dict := &catalog.TSDictionary{ObjectAddress: ext.Members[5], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "unaccent", Template: tmpl.Address(), Owner: owner, Implementation: p.UnaccentProvider}
	if stage.LookupName(catalog.DescriptorNameKey(dict)) != (ids.ObjectAddress{}) {
		return extension.MemberConflict(dict, ext.TargetNamespace)
	}
	return stage.Create(dict)
}
