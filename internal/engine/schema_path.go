package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// explicitPath runs under the engine mutex and captures one immutable setting value.
func (e *Engine) explicitPath(auth AuthorizationView) ([]ids.ObjectAddress, bool, error) {
	return e.explicitPathFor(auth, analyze.RelationLookup)
}
func (e *Engine) explicitPathFor(auth AuthorizationView, kind analyze.LookupKind) ([]ids.ObjectAddress, bool, error) {
	entries := e.settings.Definitions()["search_path"].BootValue().Path
	if auth.settings != nil && !auth.materializedPopulation {
		auth.settings.mu.Lock()
		entries = auth.settings.typedValue("search_path").Path
		auth.settings.mu.Unlock()
	}
	restricted := [2]string{"pg_catalog", "pg_temp"}
	if auth.materializedPopulation {
		entries = restricted[:]
	}
	var result []ids.ObjectAddress
	if auth.pathPrefix != (ids.ObjectAddress{}) && !auth.materializedPopulation {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), auth.pathPrefix)
		if err := auth.CanUse(ref); err != nil {
			return nil, false, err
		}
		result = append(result, auth.pathPrefix)
	}
	pendingTemp := false
	for _, name := range entries {
		if name == "" {
			continue
		}
		if name == "$user" {
			principal := auth.State.CurrentUser
			if auth.namespaceUser != (authorization.PrincipalRef{}) {
				principal = auth.namespaceUser
			}
			d, exists := auth.Roles.Find(principal)
			if !exists {
				continue
			}
			name = d.Name
		}
		var a ids.ObjectAddress
		if name == "pg_temp" {
			a = temporaryNamespace(auth)
			if a == (ids.ObjectAddress{}) {
				if len(result) == 0 {
					pendingTemp = true
				}
				continue
			}
		} else {
			a = auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
		}
		_, ok := auth.definitionDescriptor(a)
		if !ok {
			continue
		}
		if ns, ok := auth.definitionNamespace(a); ok && ns.Temporary != (ids.SessionToken{}) && !ownTemporaryNamespace(auth, ns) && kind != analyze.InstalledFunctionLookup {
			continue
		}
		duplicate := false
		for _, existing := range result {
			if existing == a {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		rights, err := auth.Effective(e.catalog.Domain.Ref(auth.Catalog.Epoch(), a), false)
		if err != nil {
			return nil, false, err
		}
		if rights&authorization.UsagePrivilege == 0 {
			continue
		}
		result = append(result, a)
	}
	return result, pendingTemp, nil
}

func (e *Engine) referencePath(auth AuthorizationView) ([]ids.ObjectAddress, error) {
	return e.lookupPath(auth, analyze.RelationLookup)
}
func (e *Engine) lookupPath(auth AuthorizationView, kind analyze.LookupKind) ([]ids.ObjectAddress, error) {
	path, _, err := e.explicitPathFor(auth, kind)
	if err != nil {
		return nil, err
	}
	return e.effectivePath(auth, path, kind), nil
}
func (e *Engine) effectivePath(auth AuthorizationView, path []ids.ObjectAddress, kind analyze.LookupKind) []ids.ObjectAddress {
	if kind == analyze.InstalledFunctionLookup || kind == analyze.FunctionLookup || kind == analyze.OperatorLookup || kind == analyze.CollationLookup || kind == analyze.ConversionLookup {
		filtered := path[:0]
		for _, a := range path {
			ns, _ := auth.definitionNamespace(a)
			if ns.Temporary == (ids.SessionToken{}) || kind == analyze.InstalledFunctionLookup && !ownTemporaryNamespace(auth, ns) {
				filtered = append(filtered, a)
			}
		}
		path = filtered
	}
	catalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	foundCatalog := false
	for _, a := range path {
		if a == catalog {
			foundCatalog = true
			break
		}
	}
	if !foundCatalog {
		path = append([]ids.ObjectAddress{catalog}, path...)
	}
	if temp := temporaryNamespace(auth); temp != (ids.ObjectAddress{}) && (kind == analyze.RelationLookup || kind == analyze.TypeLookup) {
		found := false
		for _, a := range path {
			if a == temp {
				found = true
			}
		}
		if !found {
			path = append([]ids.ObjectAddress{temp}, path...)
		}
	}
	return path
}

// lookupNamespaces is the shared qualified/unqualified namespace authority.
// Relations retain missing-object diagnostics; other named providers diagnose
// a missing explicit namespace before considering signatures or type names.
func (e *Engine) lookupNamespaces(auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	if err := e.catalog.validateCatalogViewLocked(auth.Catalog); err != nil {
		return analyze.NamespaceSelection{}, err
	}
	return e.lookupNamespacesSnapshot(auth, name, kind)
}

// Immutable statement authority resolves names without consulting live epochs.
func (e *Engine) lookupNamespacesSnapshot(auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	if len(name.Parts) == 1 {
		path, err := e.lookupPath(auth, kind)
		return analyze.NamespaceSelection{Path: path}, err
	}
	if len(name.Parts) != 2 {
		return analyze.NamespaceSelection{}, fmt.Errorf("stuntgres: invalid object name")
	}
	n := name.Parts[0]
	a := auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: n.Text})
	if n.Text == "pg_temp" {
		a = temporaryNamespace(auth)
	}
	if a == (ids.ObjectAddress{}) {
		if kind != analyze.RelationLookup {
			return analyze.NamespaceSelection{}, expr.ErrorAt("3F000", fmt.Sprintf(`schema "%s" does not exist`, n.Text), n.Pos)
		}
		return analyze.NamespaceSelection{}, nil
	}
	ns, _ := auth.definitionNamespace(a)
	foreignTemporary := ns.Temporary != (ids.SessionToken{}) && !ns.TemporaryToast && !ownTemporaryNamespace(auth, ns)
	if foreignTemporary {
		actor, exists := auth.Roles.Find(auth.State.CurrentUser)
		foreignTemporary = !exists || !actor.Superuser
	}
	if foreignTemporary && kind != analyze.InstalledFunctionLookup {
		if kind == analyze.RelationLookup {
			return analyze.NamespaceSelection{}, sqlError("42501", fmt.Sprintf("permission denied for schema %s", ns.Name()))
		}
		return analyze.NamespaceSelection{}, expr.ErrorAt("42501", fmt.Sprintf("permission denied for schema %s", ns.Name()), n.Pos)
	}
	if err := auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), a)); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && n.Pos.Character > 0 {
			copy := *diagnostic
			copy.Position = strconv.Itoa(n.Pos.Character)
			return analyze.NamespaceSelection{}, &copy
		}
		return analyze.NamespaceSelection{}, err
	}
	return analyze.NamespaceSelection{Qualified: a}, nil
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

// Resolve creation without establishing a deferred temporary namespace.
// Callers requiring an existing namespace retain the rejecting policy above.
func (e *Engine) creationNamespaceDestination(auth AuthorizationView, name parser.ObjectName, system bool) (ids.ObjectRef, bool, error) {
	if err := e.catalog.validateCatalogViewLocked(auth.Catalog); err != nil {
		return ids.ObjectRef{}, false, err
	}
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ids.ObjectRef{}, false, fmt.Errorf("stuntgres: invalid relation name")
	}
	var a ids.ObjectAddress
	if len(name.Parts) == 2 {
		n := name.Parts[0].Text
		if n == "pg_temp" {
			ns, ok := auth.Catalog.Namespace(auth.stagedTemporary)
			if !ok || !ownTemporaryNamespace(auth, ns) {
				return ids.ObjectRef{}, true, nil
			}
			a = ns.Address()
		} else {
			var ok bool
			a, ok = auth.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: n})
			if !ok {
				return ids.ObjectRef{}, false, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, n))
			}
		}
	} else {
		path, temp, err := e.explicitPath(auth)
		if err != nil {
			return ids.ObjectRef{}, false, err
		}
		if temp {
			return ids.ObjectRef{}, true, nil
		}
		if len(path) == 0 {
			return ids.ObjectRef{}, false, sqlError("3F000", "no schema has been selected to create in")
		}
		a = path[0]
	}
	if !system && a.ObjectID == ids.PGCatalogNamespaceOID {
		return ids.ObjectRef{}, false, sqlError("0A000", "system schema creation is unsupported")
	}
	return e.catalog.Domain.Ref(auth.Catalog.Epoch(), a), false, nil
}
