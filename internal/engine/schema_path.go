package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// parseSearchPath preserves raw setting text separately from decoded identifiers.
func parseSearchPath(input string) ([]string, error) {
	invalid := func() ([]string, error) {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "search_path": "%s"`, input), Detail: "List syntax is invalid."}
	}
	s := strings.Trim(input, " \t\r\n\f\v")
	var path []string
	for s != "" {
		var name string
		if s[0] == '"' {
			var b strings.Builder
			i := 1
			closed := false
			for i < len(s) {
				if s[i] == '"' {
					if i+1 < len(s) && s[i+1] == '"' {
						b.WriteByte('"')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				b.WriteByte(s[i])
				i++
			}
			if !closed {
				return invalid()
			}
			name = b.String()
			s = strings.Trim(s[i:], " \t\r\n\f\v")
		} else {
			i := 0
			for i < len(s) && s[i] != ',' && !strings.ContainsRune(" \t\r\n\f\v", rune(s[i])) {
				i++
			}
			if i == 0 {
				return invalid()
			}
			name = settingFold(s[:i])
			s = strings.Trim(s[i:], " \t\r\n\f\v")
		}
		if len(name) > 63 {
			n := 63
			for !utf8.RuneStart(name[n]) {
				n--
			}
			name = name[:n]
		}
		path = append(path, name)
		if s == "" {
			break
		}
		if s[0] != ',' {
			return invalid()
		}
		s = strings.Trim(s[1:], " \t\r\n\f\v")
		if s == "" {
			return invalid()
		}
	}
	return path, nil
}

// explicitPath runs under the engine mutex and captures one immutable setting value.
func (e *Engine) explicitPath(auth AuthorizationView) ([]ids.ObjectAddress, bool, error) {
	return e.explicitPathFor(auth, relationLookup)
}
func (e *Engine) explicitPathFor(auth AuthorizationView, kind lookupKind) ([]ids.ObjectAddress, bool, error) {
	entries := e.settingsDefinitions["search_path"].bootValue.path
	if auth.settings != nil && !auth.materializedPopulation {
		auth.settings.mu.Lock()
		entries = auth.settings.typedValue("search_path").path
		auth.settings.mu.Unlock()
	}
	restricted := [2]string{"pg_catalog", "pg_temp"}
	if auth.materializedPopulation {
		entries = restricted[:]
	}
	var result []ids.ObjectAddress
	if auth.pathPrefix != (ids.ObjectAddress{}) && !auth.materializedPopulation {
		ref := e.catalog.domain.Ref(auth.catalog.Epoch(), auth.pathPrefix)
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
			principal := auth.state.CurrentUser
			if auth.namespaceUser != (authorization.PrincipalRef{}) {
				principal = auth.namespaceUser
			}
			d, exists := auth.roles.Find(principal)
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
		if ns, ok := auth.definitionNamespace(a); ok && ns.temporary != (ids.SessionToken{}) && !ownTemporaryNamespace(auth, ns) && kind != installedFunctionLookup {
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
		rights, err := auth.Effective(e.catalog.domain.Ref(auth.catalog.Epoch(), a), false)
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

type lookupKind uint8

const (
	relationLookup lookupKind = iota
	typeLookup
	functionLookup
	installedFunctionLookup // Registered procedures only; ordinary TEMP isolation stays unchanged.
	operatorLookup
	collationLookup
	conversionLookup
)

func (e *Engine) referencePath(auth AuthorizationView) ([]ids.ObjectAddress, error) {
	return e.lookupPath(auth, relationLookup)
}
func (e *Engine) lookupPath(auth AuthorizationView, kind lookupKind) ([]ids.ObjectAddress, error) {
	path, _, err := e.explicitPathFor(auth, kind)
	if err != nil {
		return nil, err
	}
	return e.effectivePath(auth, path, kind), nil
}
func (e *Engine) effectivePath(auth AuthorizationView, path []ids.ObjectAddress, kind lookupKind) []ids.ObjectAddress {
	if kind == installedFunctionLookup || kind == functionLookup || kind == operatorLookup || kind == collationLookup || kind == conversionLookup {
		filtered := path[:0]
		for _, a := range path {
			ns, _ := auth.definitionNamespace(a)
			if ns.temporary == (ids.SessionToken{}) || kind == installedFunctionLookup && !ownTemporaryNamespace(auth, ns) {
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
	if temp := temporaryNamespace(auth); temp != (ids.ObjectAddress{}) && (kind == relationLookup || kind == typeLookup) {
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

// Qualified lookup carries its one address inline, without a singleton slice.
type namespaceSelection struct {
	path      []ids.ObjectAddress
	qualified ids.ObjectAddress
}

func (p namespaceSelection) count() int {
	if p.qualified != (ids.ObjectAddress{}) {
		return 1
	}
	return len(p.path)
}
func (p namespaceSelection) at(i int) ids.ObjectAddress {
	if p.qualified != (ids.ObjectAddress{}) {
		return p.qualified
	}
	return p.path[i]
}

// lookupNamespaces is the shared qualified/unqualified namespace authority.
// Relations retain missing-object diagnostics; other named providers diagnose
// a missing explicit namespace before considering signatures or type names.
func (e *Engine) lookupNamespaces(auth AuthorizationView, name parser.ObjectName, kind lookupKind) (namespaceSelection, error) {
	if err := e.catalog.validateCatalogViewLocked(auth.catalog); err != nil {
		return namespaceSelection{}, err
	}
	return e.lookupNamespacesSnapshot(auth, name, kind)
}

// Immutable statement authority resolves names without consulting live epochs.
func (e *Engine) lookupNamespacesSnapshot(auth AuthorizationView, name parser.ObjectName, kind lookupKind) (namespaceSelection, error) {
	if len(name.Parts) == 1 {
		path, err := e.lookupPath(auth, kind)
		return namespaceSelection{path: path}, err
	}
	if len(name.Parts) != 2 {
		return namespaceSelection{}, fmt.Errorf("stuntgres: invalid object name")
	}
	n := name.Parts[0]
	a := auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: n.Text})
	if n.Text == "pg_temp" {
		a = temporaryNamespace(auth)
	}
	if a == (ids.ObjectAddress{}) {
		if kind != relationLookup {
			return namespaceSelection{}, expressionError("3F000", fmt.Sprintf(`schema "%s" does not exist`, n.Text), n.Pos)
		}
		return namespaceSelection{}, nil
	}
	ns, _ := auth.definitionNamespace(a)
	foreignTemporary := ns.temporary != (ids.SessionToken{}) && !ns.temporaryToast && !ownTemporaryNamespace(auth, ns)
	if foreignTemporary {
		actor, exists := auth.roles.Find(auth.state.CurrentUser)
		foreignTemporary = !exists || !actor.Superuser
	}
	if foreignTemporary && kind != installedFunctionLookup {
		if kind == relationLookup {
			return namespaceSelection{}, sqlError("42501", fmt.Sprintf("permission denied for schema %s", ns.name))
		}
		return namespaceSelection{}, expressionError("42501", fmt.Sprintf("permission denied for schema %s", ns.name), n.Pos)
	}
	if err := auth.CanUse(e.catalog.domain.Ref(auth.catalog.Epoch(), a)); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && n.Pos.Character > 0 {
			copy := *diagnostic
			copy.Position = strconv.Itoa(n.Pos.Character)
			return namespaceSelection{}, &copy
		}
		return namespaceSelection{}, err
	}
	return namespaceSelection{qualified: a}, nil
}

func (b *expressionBinder) builtinVisible(name parser.ObjectName, kind lookupKind) (bool, error) {
	auth, err := b.bindingAuthorization()
	if err != nil {
		return false, err
	}
	path, err := b.engine.lookupNamespaces(auth, name, kind)
	if err != nil {
		return false, err
	}
	for i := range path.count() {
		if path.at(i).ObjectID == ids.PGCatalogNamespaceOID {
			return true, nil
		}
	}
	return false, nil
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
	if err := e.catalog.validateCatalogViewLocked(auth.catalog); err != nil {
		return ids.ObjectRef{}, false, err
	}
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ids.ObjectRef{}, false, fmt.Errorf("stuntgres: invalid relation name")
	}
	var a ids.ObjectAddress
	if len(name.Parts) == 2 {
		n := name.Parts[0].Text
		if n == "pg_temp" {
			ns, ok := auth.catalog.state.descriptors[auth.stagedTemporary].(*namespaceDescriptor)
			if !ok || !ownTemporaryNamespace(auth, ns) {
				return ids.ObjectRef{}, true, nil
			}
			a = ns.address
		} else {
			var ok bool
			a, ok = auth.catalog.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: n}]
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
	return e.catalog.domain.Ref(auth.catalog.Epoch(), a), false, nil
}
