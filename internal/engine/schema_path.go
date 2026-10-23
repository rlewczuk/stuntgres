package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// pathInput is auth as the input of the analyze search path decisions. With
// searchPath it reads the search_path entries once, where the decisions read
// them before: the session's value under its settings mutex, or the engine's
// boot value for a view without session settings. Materialized population
// uses its fixed path and reads none. Callers that resolve no unqualified
// name pass false, so qualified lookups take no settings lock.
func pathInput(auth AuthorizationView, searchPath bool) analyze.PathInput[AuthorizationView] {
	in := analyze.PathInput[AuthorizationView]{
		View:            auth.View,
		Env:             auth,
		Definition:      auth.definition,
		Domain:          engineDomain(auth),
		Prefix:          auth.pathPrefix,
		NamespaceUser:   auth.namespaceUser,
		StagedTemporary: auth.stagedTemporary,
		Restricted:      auth.materializedPopulation,
	}
	if !searchPath || auth.materializedPopulation {
		return in
	}
	if auth.settings != nil {
		auth.settings.Mu.Lock()
		in.Entries = auth.settings.TypedValue("search_path").Path
		auth.settings.Mu.Unlock()
	} else {
		in.Entries = auth.inputs.settings.Definitions()["search_path"].BootValue().Path
	}
	return in
}

// explicitPath runs under the engine mutex and captures one immutable setting
// value: the session's search_path, or the engine's boot value for a view
// without session settings.
func explicitPath(auth AuthorizationView) ([]ids.ObjectAddress, bool, error) {
	return analyze.ExplicitPath(pathInput(auth, true), analyze.RelationLookup, false)
}

func referencePath(auth AuthorizationView) ([]ids.ObjectAddress, error) {
	return lookupPath(auth, analyze.RelationLookup)
}

func lookupPath(auth AuthorizationView, kind analyze.LookupKind) ([]ids.ObjectAddress, error) {
	return analyze.LookupPath(pathInput(auth, true), kind)
}

func effectivePath(auth AuthorizationView, path []ids.ObjectAddress, kind analyze.LookupKind) []ids.ObjectAddress {
	return analyze.EffectivePath(pathInput(auth, false), path, kind)
}

// lookupNamespaces is the shared qualified/unqualified namespace authority
// over a validated catalog view (analyze.LookupNamespaces).
func (e *Engine) lookupNamespaces(auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	if err := e.catalog.validateCatalogViewLocked(auth.Catalog); err != nil {
		return analyze.NamespaceSelection{}, err
	}
	return lookupNamespacesSnapshot(auth, name, kind)
}

// Immutable statement authority resolves names without consulting live epochs.
func lookupNamespacesSnapshot(auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return analyze.LookupNamespaces(pathInput(auth, len(name.Parts) == 1), name, kind)
}

func (e *Engine) creationNamespace(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	return e.creationNamespaceWithSystem(auth, name, false)
}

func (e *Engine) creationNamespaceWithSystem(auth AuthorizationView, name parser.ObjectName, system bool) (ids.ObjectRef, error) {
	ref, deferredTemp, err := e.creationNamespaceDestination(auth, name, system)
	if err == nil && deferredTemp {
		return ids.ObjectRef{}, sqlError("0A000", "temporary schema creation is unsupported")
	}
	return ref, err
}

// Resolve creation without establishing a deferred temporary namespace
// (analyze.CreationNamespace). Callers requiring an existing namespace retain
// the rejecting policy above.
func (e *Engine) creationNamespaceDestination(auth AuthorizationView, name parser.ObjectName, system bool) (ids.ObjectRef, bool, error) {
	if err := e.catalog.validateCatalogViewLocked(auth.Catalog); err != nil {
		return ids.ObjectRef{}, false, err
	}
	a, deferredTemp, err := analyze.CreationNamespace(pathInput(auth, len(name.Parts) == 1), name, system)
	if err != nil || deferredTemp {
		return ids.ObjectRef{}, deferredTemp, err
	}
	return e.catalog.Domain.Ref(auth.Catalog.Epoch(), a), false, nil
}
