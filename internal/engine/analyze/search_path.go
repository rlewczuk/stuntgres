package analyze

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PathInput is the statement authority a search path decision reads, by
// value. The root builds it from its AuthorizationView, which is also Env.
// Env is a type parameter, so building the input boxes nothing: the view
// becomes an access.Environment only where a check takes one, as it did
// before the decisions moved here.
type PathInput[E access.Environment] struct {
	View access.View
	// Env answers the session's own TEMP namespace and the reference path that
	// qualifies type names in check diagnostics. The root's ReferencePath
	// resolves that path here again (LookupPath).
	Env E
	// Definition is the cooking-only sparse reader; nil reads View.Catalog.
	Definition catalog.DefinitionReader
	// Domain mints namespace references at the view's catalog epoch. It is the
	// engine's domain, not View.Owner's, so a view with a missing or foreign
	// owner still reaches the check that rejects it.
	Domain *ids.Domain
	// Entries is the search_path, read by the root under the session settings
	// mutex. Only unqualified resolution reads it, and not when Restricted.
	Entries []string
	// Prefix is a nested schema element's enclosing namespace, searched first.
	Prefix ids.ObjectAddress
	// NamespaceUser stands in for the current user in $user when trusted
	// replay elevates permissions.
	NamespaceUser authorization.PrincipalRef
	// StagedTemporary is an owned TEMP table builder's namespace before
	// session publication; only CREATE reads it.
	StagedTemporary ids.ObjectAddress
	// Restricted is materialized view population: the path is pg_catalog and
	// pg_temp, and Prefix is ignored.
	Restricted bool
}

// restrictedSearchPath is the search path of materialized view population.
var restrictedSearchPath = [...]string{"pg_catalog", "pg_temp"}

func (in PathInput[E]) ref(a ids.ObjectAddress) ids.ObjectRef {
	return in.Domain.Ref(in.View.Catalog.Epoch(), a)
}

// ExplicitPath resolves the configured search path: the prefix, then each
// usable entry once. The second result reports a leading pg_temp whose
// namespace does not exist yet (pending creation). Only VACUUM passes
// allowForeignTemporary to keep peer TEMP namespaces; parsing, $user and
// pg_temp, pending creation, deduplication and USAGE omission are shared.
func ExplicitPath[E access.Environment](in PathInput[E], kind LookupKind, allowForeignTemporary bool) ([]ids.ObjectAddress, bool, error) {
	entries := in.Entries
	if in.Restricted {
		entries = restrictedSearchPath[:]
	}
	var result []ids.ObjectAddress
	if in.Prefix != (ids.ObjectAddress{}) && !in.Restricted {
		ref := in.ref(in.Prefix)
		if err := in.View.CanUse(in.Env, ref); err != nil {
			return nil, false, err
		}
		result = append(result, in.Prefix)
	}
	pendingTemp := false
	for _, name := range entries {
		if name == "" {
			continue
		}
		if name == "$user" {
			principal := in.View.State.CurrentUser
			if in.NamespaceUser != (authorization.PrincipalRef{}) {
				principal = in.NamespaceUser
			}
			d, exists := in.View.Roles.Find(principal)
			if !exists {
				continue
			}
			name = d.Name
		}
		var a ids.ObjectAddress
		if name == "pg_temp" {
			a = in.Env.TemporaryNamespace()
			if a == (ids.ObjectAddress{}) {
				if len(result) == 0 {
					pendingTemp = true
				}
				continue
			}
		} else {
			a = catalog.DefinitionLookupAt(in.View.Catalog, in.Definition, ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
		}
		_, ok := catalog.DescriptorAt(in.View.Catalog, in.Definition, a)
		if !ok {
			continue
		}
		if ns, ok := catalog.DefinitionNamespaceAt(in.View.Catalog, in.Definition, a); ok && ns.Temporary != (ids.SessionToken{}) && !access.OwnTemporaryNamespace(in.Env.TemporaryNamespace(), ns) && kind != InstalledFunctionLookup && !allowForeignTemporary {
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
		rights, err := in.View.Effective(in.Env, in.ref(a), false)
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

// LookupPath is the effective path for kind: the explicit path with
// pg_catalog and, for relations and types, the own TEMP namespace implied.
func LookupPath[E access.Environment](in PathInput[E], kind LookupKind) ([]ids.ObjectAddress, error) {
	path, _, err := ExplicitPath(in, kind, false)
	if err != nil {
		return nil, err
	}
	return EffectivePath(in, path, kind), nil
}

// EffectivePath turns an explicit path into the path kind searches. Function,
// operator, collation and conversion lookups drop TEMP namespaces (installed
// functions keep peer ones) and may filter path in place.
func EffectivePath[E access.Environment](in PathInput[E], path []ids.ObjectAddress, kind LookupKind) []ids.ObjectAddress {
	if kind == InstalledFunctionLookup || kind == FunctionLookup || kind == OperatorLookup || kind == CollationLookup || kind == ConversionLookup {
		filtered := path[:0]
		for _, a := range path {
			ns, _ := catalog.DefinitionNamespaceAt(in.View.Catalog, in.Definition, a)
			if ns.Temporary == (ids.SessionToken{}) || kind == InstalledFunctionLookup && !access.OwnTemporaryNamespace(in.Env.TemporaryNamespace(), ns) {
				filtered = append(filtered, a)
			}
		}
		path = filtered
	}
	pgCatalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	foundCatalog := false
	for _, a := range path {
		if a == pgCatalog {
			foundCatalog = true
			break
		}
	}
	if !foundCatalog {
		path = append([]ids.ObjectAddress{pgCatalog}, path...)
	}
	if temp := in.Env.TemporaryNamespace(); temp != (ids.ObjectAddress{}) && (kind == RelationLookup || kind == TypeLookup) {
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

// LookupNamespaces is the shared qualified and unqualified namespace
// authority over an immutable statement view; it consults no live epoch.
// Relations retain missing-object diagnostics; other named providers diagnose
// a missing explicit namespace before considering signatures or type names.
func LookupNamespaces[E access.Environment](in PathInput[E], name parser.ObjectName, kind LookupKind) (NamespaceSelection, error) {
	if len(name.Parts) == 1 {
		path, err := LookupPath(in, kind)
		return NamespaceSelection{Path: path}, err
	}
	if len(name.Parts) != 2 {
		return NamespaceSelection{}, fmt.Errorf("stuntgres: invalid object name")
	}
	n := name.Parts[0]
	a := catalog.DefinitionLookupAt(in.View.Catalog, in.Definition, ids.NameKey{ClassID: ids.NamespaceClassID, Name: n.Text})
	if n.Text == "pg_temp" {
		a = in.Env.TemporaryNamespace()
	}
	if a == (ids.ObjectAddress{}) {
		if kind != RelationLookup {
			return NamespaceSelection{}, expr.ErrorAt("3F000", fmt.Sprintf(`schema "%s" does not exist`, n.Text), n.Pos)
		}
		return NamespaceSelection{}, nil
	}
	ns, _ := catalog.DefinitionNamespaceAt(in.View.Catalog, in.Definition, a)
	foreignTemporary := ns.Temporary != (ids.SessionToken{}) && !ns.TemporaryToast && !access.OwnTemporaryNamespace(in.Env.TemporaryNamespace(), ns)
	if foreignTemporary {
		actor, exists := in.View.Roles.Find(in.View.State.CurrentUser)
		foreignTemporary = !exists || !actor.Superuser
	}
	if foreignTemporary && kind != InstalledFunctionLookup {
		if kind == RelationLookup {
			return NamespaceSelection{}, sqlError("42501", fmt.Sprintf("permission denied for schema %s", ns.Name()))
		}
		return NamespaceSelection{}, expr.ErrorAt("42501", fmt.Sprintf("permission denied for schema %s", ns.Name()), n.Pos)
	}
	if err := in.View.CanUse(in.Env, in.ref(a)); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && n.Pos.Character > 0 {
			copy := *diagnostic
			copy.Position = strconv.Itoa(n.Pos.Character)
			return NamespaceSelection{}, &copy
		}
		return NamespaceSelection{}, err
	}
	return NamespaceSelection{Qualified: a}, nil
}

// CreationNamespace is the namespace a CREATE of name lands in, without
// establishing a deferred TEMP namespace: the second result reports that
// pg_temp (explicit, or leading the search path) has no namespace yet.
// Callers requiring an existing namespace reject that case themselves.
func CreationNamespace[E access.Environment](in PathInput[E], name parser.ObjectName, system bool) (ids.ObjectAddress, bool, error) {
	if len(name.Parts) < 1 || len(name.Parts) > 2 {
		return ids.ObjectAddress{}, false, fmt.Errorf("stuntgres: invalid relation name")
	}
	var a ids.ObjectAddress
	if len(name.Parts) == 2 {
		n := name.Parts[0].Text
		if n == "pg_temp" {
			ns, ok := in.View.Catalog.Namespace(in.StagedTemporary)
			if !ok || !access.OwnTemporaryNamespace(in.Env.TemporaryNamespace(), ns) {
				return ids.ObjectAddress{}, true, nil
			}
			a = ns.Address()
		} else {
			var ok bool
			a, ok = in.View.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: n})
			if !ok {
				return ids.ObjectAddress{}, false, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, n))
			}
		}
	} else {
		path, temp, err := ExplicitPath(in, RelationLookup, false)
		if err != nil {
			return ids.ObjectAddress{}, false, err
		}
		if temp {
			return ids.ObjectAddress{}, true, nil
		}
		if len(path) == 0 {
			return ids.ObjectAddress{}, false, sqlError("3F000", "no schema has been selected to create in")
		}
		a = path[0]
	}
	if !system && a.ObjectID == ids.PGCatalogNamespaceOID {
		return ids.ObjectAddress{}, false, sqlError("0A000", "system schema creation is unsupported")
	}
	return a, false, nil
}
