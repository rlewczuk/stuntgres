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

// explicitPath runs under the engine mutex and captures one immutable setting
// value: the session's search_path, or the engine's boot value for a view
// without session settings.
func explicitPath(auth AuthorizationView) ([]ids.ObjectAddress, bool, error) {
	return explicitPathFor(auth, analyze.RelationLookup)
}
func explicitPathFor(auth AuthorizationView, kind analyze.LookupKind) ([]ids.ObjectAddress, bool, error) {
	var entries []string
	restricted := [2]string{"pg_catalog", "pg_temp"}
	switch {
	case auth.materializedPopulation:
		entries = restricted[:]
	case auth.settings != nil:
		auth.settings.Mu.Lock()
		entries = auth.settings.TypedValue("search_path").Path
		auth.settings.Mu.Unlock()
	default:
		entries = auth.inputs.settings.Definitions()["search_path"].BootValue().Path
	}
	var result []ids.ObjectAddress
	if auth.pathPrefix != (ids.ObjectAddress{}) && !auth.materializedPopulation {
		ref := auth.ref(auth.pathPrefix)
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
		rights, err := auth.Effective(auth.ref(a), false)
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

func referencePath(auth AuthorizationView) ([]ids.ObjectAddress, error) {
	return lookupPath(auth, analyze.RelationLookup)
}
func lookupPath(auth AuthorizationView, kind analyze.LookupKind) ([]ids.ObjectAddress, error) {
	path, _, err := explicitPathFor(auth, kind)
	if err != nil {
		return nil, err
	}
	return effectivePath(auth, path, kind), nil
}
func effectivePath(auth AuthorizationView, path []ids.ObjectAddress, kind analyze.LookupKind) []ids.ObjectAddress {
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
	return lookupNamespacesSnapshot(auth, name, kind)
}

// Immutable statement authority resolves names without consulting live epochs.
func lookupNamespacesSnapshot(auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	if len(name.Parts) == 1 {
		path, err := lookupPath(auth, kind)
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
	if err := auth.CanUse(auth.ref(a)); err != nil {
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
		path, temp, err := explicitPath(auth)
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
