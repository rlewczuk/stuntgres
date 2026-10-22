package table

import (
	"fmt"
	"iter"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// TypedResolver is what resolving the OF type of a typed table needs: the
// authority's view and the type name lookup. Its methods run with Engine.mu
// held and never wait.
type TypedResolver interface {
	AccessView(auth ddl.Authority) access.View
	// NamedType resolves an OF type name. OF names are catalog object
	// names, not SQL declaration aliases.
	NamedType(auth ddl.Authority, name parser.ObjectName) (*catalog.Type, error)
}

// ResolveTypedSource resolves an OF type name and the standalone composite
// relation behind it, on auth's reference path.
func ResolveTypedSource(h TypedResolver, auth ddl.Authority, name parser.ObjectName) (*catalog.Type, *catalog.Relation, error) {
	typ, err := h.NamedType(auth, name)
	if err != nil {
		return nil, nil, err
	}
	path, err := auth.ReferencePath()
	if err != nil {
		return nil, nil, err
	}
	source, err := typedef.StandaloneComposite(h.AccessView(auth).Catalog, typ, path)
	if err != nil {
		return nil, nil, err
	}
	return typ, source, nil
}

// AlterTableOf is old after ALTER TABLE ... OF (attach) or NOT OF (detach),
// with its revision advanced. Attachment needs USAGE on the type and a table
// that inherits nothing and matches the type's shape.
func AlterTableOf(h TypedResolver, auth ddl.Authority, old *catalog.Relation, s *parser.AlterTableOf) (*catalog.Relation, error) {
	if err := analyze.TableKindError(old, "OF"); err != nil {
		return nil, err
	}
	next := *old
	if s.Type == nil {
		if old.OfType == (ids.ObjectAddress{}) {
			return nil, sqlError("42809", fmt.Sprintf(`"%s" is not a typed table`, old.Name()))
		}
		next.OfType = ids.ObjectAddress{}
	} else {
		typ, source, err := ResolveTypedSource(h, auth, *s.Type)
		if err == nil {
			// Like CREATE OF, attachment needs source USAGE (PostgreSQL 18.6,
			// CVE-2026-6470), checked before inheritance and shape.
			view := h.AccessView(auth)
			err = view.CanUse(auth, view.Ref(typ.Address()))
		}
		if err != nil {
			return nil, err
		}
		if old.Inheritance != nil && len(old.Inheritance.Parents) != 0 {
			return nil, sqlError("42809", "typed tables cannot inherit")
		}
		if err := typedef.TypedShape(old, source); err != nil {
			return nil, err
		}
		next.OfType = typ.Address()
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.ObjectRevision++
	return &next, nil
}

// TypedSources are the OF types whose owning relations a typed-table change
// locks, in OID order: the dropped types when drop is set, otherwise the
// types of the staged typed tables. Empty means nothing to lock.
func TypedSources(drop bool, dropped []ids.ObjectAddress, staged iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) []ids.ObjectAddress {
	var sources map[ids.ObjectAddress]bool
	if drop {
		if len(dropped) != 0 {
			sources = make(map[ids.ObjectAddress]bool, len(dropped))
		}
		for _, source := range dropped {
			sources[source] = true
		}
	} else {
		for _, descriptor := range staged {
			if table, ok := descriptor.(*catalog.Relation); ok && table.OfType != (ids.ObjectAddress{}) {
				if sources == nil {
					sources = make(map[ids.ObjectAddress]bool)
				}
				sources[table.OfType] = true
			}
		}
	}
	if len(sources) == 0 {
		return nil
	}
	addresses := make([]ids.ObjectAddress, 0, len(sources))
	for source := range sources {
		addresses = append(addresses, source)
	}
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	return addresses
}
