package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// PathInput is auth as the input of the analyze search path decisions. With
// searchPath it reads the search_path entries once, where the decisions read
// them before: the session's value under its settings mutex, or the engine's
// boot value for a view without session settings. Materialized population
// uses its fixed path and reads none. Callers that resolve no unqualified
// name pass false, so qualified lookups take no settings lock.
func PathInput(auth AuthorizationView, searchPath bool) analyze.PathInput[AuthorizationView] {
	in := analyze.PathInput[AuthorizationView]{
		View:            auth.View,
		Env:             auth,
		Definition:      auth.Definition,
		Domain:          engineDomain(auth),
		Prefix:          auth.PathPrefix,
		NamespaceUser:   auth.namespaceUser,
		StagedTemporary: auth.StagedTemporary,
		Restricted:      auth.materializedPopulation,
	}
	if auth.restoreDefinitionLookup {
		// The live authority capture verified the authenticated bootstrap and
		// isolated import marker. Elevate only lookup's permission view; keep
		// $user and all destination ownership semantics on the named principal.
		in.View.State.CurrentUser = auth.State.Authenticated
		if in.NamespaceUser == (authorization.PrincipalRef{}) {
			in.NamespaceUser = auth.State.CurrentUser
		}
	}
	if !searchPath || auth.materializedPopulation {
		return in
	}
	if auth.settings != nil {
		auth.settings.Mu.Lock()
		in.Entries = auth.settings.TypedValue("search_path").Path
		auth.settings.Mu.Unlock()
	} else {
		in.Entries = auth.inputs.Settings.Definitions()["search_path"].BootValue().Path
	}
	return in
}

// ExplicitPath runs under the engine mutex and captures one immutable setting
// value: the session's search_path, or the engine's boot value for a view
// without session settings.
func ExplicitPath(auth AuthorizationView) ([]ids.ObjectAddress, bool, error) {
	return analyze.ExplicitPath(PathInput(auth, true), analyze.RelationLookup, false)
}

func ReferencePath(auth AuthorizationView) ([]ids.ObjectAddress, error) {
	return LookupPath(auth, analyze.RelationLookup)
}

func LookupPath(auth AuthorizationView, kind analyze.LookupKind) ([]ids.ObjectAddress, error) {
	return analyze.LookupPath(PathInput(auth, true), kind)
}

func EffectivePath(auth AuthorizationView, path []ids.ObjectAddress, kind analyze.LookupKind) []ids.ObjectAddress {
	return analyze.EffectivePath(PathInput(auth, false), path, kind)
}

// LookupNamespaces is the shared qualified/unqualified namespace authority
// over a validated catalog view (analyze.LookupNamespaces).
func (e *Engine) LookupNamespaces(auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	if err := e.Catalog.ValidateCatalogViewLocked(auth.Catalog); err != nil {
		return analyze.NamespaceSelection{}, err
	}
	return LookupNamespacesSnapshot(auth, name, kind)
}

// Immutable statement authority resolves names without consulting live epochs.
func LookupNamespacesSnapshot(auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return analyze.LookupNamespaces(PathInput(auth, len(name.Parts) == 1), name, kind)
}

func (e *Engine) CreationNamespace(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	return e.creationNamespaceWithSystem(auth, name, false)
}

func (e *Engine) creationNamespaceWithSystem(auth AuthorizationView, name parser.ObjectName, system bool) (ids.ObjectRef, error) {
	ref, deferredTemp, err := e.creationNamespaceDestination(auth, name, system)
	if err == nil && deferredTemp {
		return ids.ObjectRef{}, SQLError("0A000", "temporary schema creation is unsupported")
	}
	return ref, err
}

// Resolve creation without establishing a deferred temporary namespace
// (analyze.CreationNamespace). Callers requiring an existing namespace retain
// the rejecting policy above.
func (e *Engine) creationNamespaceDestination(auth AuthorizationView, name parser.ObjectName, system bool) (ids.ObjectRef, bool, error) {
	if err := e.Catalog.ValidateCatalogViewLocked(auth.Catalog); err != nil {
		return ids.ObjectRef{}, false, err
	}
	a, deferredTemp, err := analyze.CreationNamespace(PathInput(auth, len(name.Parts) == 1), name, system)
	if err != nil || deferredTemp {
		return ids.ObjectRef{}, deferredTemp, err
	}
	return e.Catalog.Domain.Ref(auth.Catalog.Epoch(), a), false, nil
}
