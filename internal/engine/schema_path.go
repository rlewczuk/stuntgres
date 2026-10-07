package engine

import (
	"fmt"
	"strconv"
	"strings"
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
func (e *Engine) explicitPath(auth AuthorizationView) ([]ObjectAddress, bool, error) {
	entries := e.settingsDefinitions["search_path"].bootValue.path
	if auth.settings != nil {
		auth.settings.mu.Lock()
		entries = auth.settings.typedValue("search_path").path
		auth.settings.mu.Unlock()
	}
	var result []ObjectAddress
	if auth.pathPrefix != (ObjectAddress{}) {
		ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: auth.pathPrefix}
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
			d, err := auth.roles.Descriptor(auth.state.CurrentUser)
			if err != nil {
				return nil, false, err
			}
			name = d.Name
		}
		var a ObjectAddress
		if name == "pg_temp" {
			a = temporaryNamespace(auth)
			if a == (ObjectAddress{}) {
				if len(result) == 0 {
					pendingTemp = true
				}
				continue
			}
		} else {
			a = auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name}]
		}
		_, ok := auth.catalog.state.descriptors[a]
		if !ok {
			continue
		}
		if ns, ok := auth.catalog.state.descriptors[a].(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) && (auth.session == nil || ns.temporary != auth.session.token) {
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
		rights, err := auth.Effective(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: a}, false)
		if err != nil {
			return nil, false, err
		}
		if rights&UsagePrivilege == 0 {
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
	operatorLookup
)

func (e *Engine) referencePath(auth AuthorizationView) ([]ObjectAddress, error) {
	return e.lookupPath(auth, relationLookup)
}
func (e *Engine) lookupPath(auth AuthorizationView, kind lookupKind) ([]ObjectAddress, error) {
	path, _, err := e.explicitPath(auth)
	if err != nil {
		return nil, err
	}
	return e.effectivePath(auth, path, kind), nil
}
func (e *Engine) effectivePath(auth AuthorizationView, path []ObjectAddress, kind lookupKind) []ObjectAddress {
	if kind == functionLookup || kind == operatorLookup {
		filtered := path[:0]
		for _, a := range path {
			ns := auth.catalog.state.descriptors[a].(*namespaceDescriptor)
			if ns.temporary == (SessionToken{}) {
				filtered = append(filtered, a)
			}
		}
		path = filtered
	}
	catalog := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
	foundCatalog := false
	for _, a := range path {
		if a == catalog {
			foundCatalog = true
			break
		}
	}
	if !foundCatalog {
		path = append([]ObjectAddress{catalog}, path...)
	}
	if temp := temporaryNamespace(auth); temp != (ObjectAddress{}) && (kind == relationLookup || kind == typeLookup) {
		found := false
		for _, a := range path {
			if a == temp {
				found = true
			}
		}
		if !found {
			path = append([]ObjectAddress{temp}, path...)
		}
	}
	return path
}

// Qualified lookup carries its one address inline, without a singleton slice.
type namespaceSelection struct {
	path      []ObjectAddress
	qualified ObjectAddress
}

func (p namespaceSelection) count() int {
	if p.qualified != (ObjectAddress{}) {
		return 1
	}
	return len(p.path)
}
func (p namespaceSelection) at(i int) ObjectAddress {
	if p.qualified != (ObjectAddress{}) {
		return p.qualified
	}
	return p.path[i]
}

// lookupNamespaces is the shared qualified/unqualified namespace authority.
// Relations retain missing-object diagnostics; other named providers diagnose
// a missing explicit namespace before considering signatures or type names.
func (e *Engine) lookupNamespaces(auth AuthorizationView, name parser.ObjectName, kind lookupKind) (namespaceSelection, error) {
	if err := e.validateCatalogViewLocked(auth.catalog); err != nil {
		return namespaceSelection{}, err
	}
	if len(name.Parts) == 1 {
		path, err := e.lookupPath(auth, kind)
		return namespaceSelection{path: path}, err
	}
	if len(name.Parts) != 2 {
		return namespaceSelection{}, fmt.Errorf("stuntgres: invalid object name")
	}
	n := name.Parts[0]
	a := auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: n.Text}]
	if n.Text == "pg_temp" {
		a = temporaryNamespace(auth)
	}
	if a == (ObjectAddress{}) {
		if kind != relationLookup {
			return namespaceSelection{}, expressionError("3F000", fmt.Sprintf(`schema "%s" does not exist`, n.Text), n.Pos)
		}
		return namespaceSelection{}, nil
	}
	ns := auth.catalog.state.descriptors[a].(*namespaceDescriptor)
	if ns.temporary != (SessionToken{}) && (auth.session == nil || ns.temporary != auth.session.token) {
		if kind == relationLookup {
			return namespaceSelection{}, sqlError("42501", fmt.Sprintf("permission denied for schema %s", ns.name))
		}
		return namespaceSelection{}, expressionError("42501", fmt.Sprintf("permission denied for schema %s", ns.name), n.Pos)
	}
	if err := auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: a}); err != nil {
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
	auth, err := b.engine.authorizationLocked(b.op)
	if err != nil {
		return false, err
	}
	path, err := b.engine.lookupNamespaces(auth, name, kind)
	if err != nil {
		return false, err
	}
	for i := range path.count() {
		if path.at(i).ObjectID == PGCatalogNamespaceOID {
			return true, nil
		}
	}
	return false, nil
}
func (e *Engine) creationNamespace(auth AuthorizationView, name parser.ObjectName) (ObjectRef, error) {
	if err := e.validateCatalogViewLocked(auth.catalog); err != nil {
		return ObjectRef{}, err
	}
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ObjectRef{}, fmt.Errorf("stuntgres: invalid relation name")
	}
	var a ObjectAddress
	if len(name.Parts) == 2 {
		n := name.Parts[0].Text
		if n == "pg_temp" {
			return ObjectRef{}, sqlError("0A000", "temporary schema creation is unsupported")
		}
		var ok bool
		a, ok = auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: n}]
		if !ok {
			return ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, n))
		}
	} else {
		path, temp, err := e.explicitPath(auth)
		if err != nil {
			return ObjectRef{}, err
		}
		if temp {
			return ObjectRef{}, sqlError("0A000", "temporary schema creation is unsupported")
		}
		if len(path) == 0 {
			return ObjectRef{}, sqlError("3F000", "no schema has been selected to create in")
		}
		a = path[0]
	}
	if a.ObjectID == PGCatalogNamespaceOID {
		return ObjectRef{}, sqlError("0A000", "system schema creation is unsupported")
	}
	return ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: a}, nil
}
