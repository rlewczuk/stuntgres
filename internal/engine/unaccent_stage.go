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

// unaccentInstaller stages the unaccent provider for the engine. Installed
// members hold the bare provider, never the installer.
type unaccentInstaller struct{ *extension.UnaccentProvider }

func (p unaccentInstaller) stage(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.Members = make([]ids.ObjectAddress, 6)
	for i, class := range [6]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	signatures := [4]catalog.ProcedureSignature{catalog.UnaccentDictionarySignature, catalog.UnaccentTextSignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}
	for i, signature := range signatures {
		rev, err := e.catalog.revisionLocked()
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
	// The script resolves supplied helper names against its coherent staged
	// search path, under the actual script execution principal. In particular,
	// unqualified function lookup excludes the installer's own TEMP namespace.
	auth.Catalog = e.stageRead(stage)
	auth.Recapture(auth.Catalog)
	auth.State.CurrentUser = owner
	if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), ext.TargetNamespace)); err != nil {
		return err
	}
	// Extension scripts replace, rather than prefix, the caller's path. The
	// stock script has no prerequisite schemas; implicit pg_catalog remains,
	// and the script's explicit pg_temp entry is excluded from function lookup.
	path := e.effectivePath(auth, []ids.ObjectAddress{ext.TargetNamespace}, installedFunctionLookup)
	var helpers [2]ids.ObjectAddress
	for i, helper := range [...]struct {
		name      string
		signature catalog.ProcedureSignature
		arguments string
	}{{"unaccent_init", catalog.UnaccentInitSignature, "internal"}, {"unaccent_lexize", catalog.UnaccentLexizeSignature, "internal, internal, internal, internal"}} {
		procedure := procedureInNamespaces(auth.Catalog, namespaceSelection{path: path}, helper.name, helper.signature)
		if procedure == nil {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", helper.name, helper.arguments), Context: extension.UnaccentTemplateContext}
		}
		helpers[i] = procedure.Address()
	}
	rev, err := e.catalog.revisionLocked()
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
	rev, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	dict := &catalog.TSDictionary{ObjectAddress: ext.Members[5], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "unaccent", Template: tmpl.Address(), Owner: owner, Implementation: p.UnaccentProvider}
	if stage.LookupName(catalog.DescriptorNameKey(dict)) != (ids.ObjectAddress{}) {
		return extension.MemberConflict(dict, ext.TargetNamespace)
	}
	return stage.Create(dict)
}
