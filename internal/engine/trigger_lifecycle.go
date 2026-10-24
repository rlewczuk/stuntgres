package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// planTriggerLifecycle decides ALTER TRIGGER RENAME, ALTER TRIGGER [NO]
// DEPENDS ON EXTENSION and DROP TRIGGER. Lifecycle utilities alter only the
// immutable attached payload; publication retains the relation and extension
// source fences.
func planTriggerLifecycle(h view.Host, auth ddl.Authority, stmt parser.Statement, locked ids.ObjectRef) (view.RelationChange, error) {
	var target parser.ObjectName
	var name string
	alter, renaming := stmt.(*parser.AlterTrigger)
	drop, dropping := stmt.(*parser.DropTrigger)
	if renaming {
		target, name = alter.Table, alter.Name.Text
	} else {
		target, name = drop.Table, drop.Name.Text
	}
	ref, err := h.ResolveRelation(auth, target)
	if err != nil {
		if dropping && drop.IfExists && table.RenameMissing(err) {
			return view.RelationChange{Skip: err.(*sqlstate.Error).Message + ", skipping"}, nil
		}
		return view.RelationChange{}, err
	}
	if ref != locked {
		return view.RelationChange{}, ids.ErrCatalogReplaced
	}
	catalogAccess := h.AccessView(auth)
	old := catalogAccess.Catalog.MustRelation(ref.Address)
	list := stored.TriggersOf(old)
	index := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Name() == name })
	missing := func() error {
		return sqlError("42704", fmt.Sprintf(`trigger "%s" for table "%s" does not exist`, name, old.Name()))
	}
	// Generic attached-object lookup checks absence before ownership. Rename
	// checks table ownership first, including when the trigger is missing.
	rename := renaming && alter.Action == "rename"
	if !rename && index < 0 {
		if dropping && drop.IfExists {
			return view.RelationChange{Skip: fmt.Sprintf(`trigger "%s" for relation "%s" does not exist, skipping`, name, catalog.ObjectNameText(target))}, nil
		}
		return view.RelationChange{}, missing()
	}
	noun := "relation"
	if rename {
		noun = "table"
	}
	if err := catalogAccess.Check(auth, access.Requirement{Object: ref, OwnerOnly: true, OwnerNoun: noun}); err != nil {
		return view.RelationChange{}, err
	}
	if index < 0 {
		return view.RelationChange{}, missing()
	}
	if dropping {
		if list[index].Parts().Parent.ObjectID != 0 {
			path, err := auth.ReferencePath()
			if err != nil {
				return view.RelationChange{}, err
			}
			return view.RelationChange{}, view.TriggerPartitionDropError(catalogAccess.Catalog, old, list[index], path)
		}
		return triggerListChange(h, ref, old, append(slices.Clone(list[:index]), list[index+1:]...), nil)
	}
	p, sources, changed, err := alterTriggerParts(catalogAccess, alter, old, list, list[index].Parts())
	if err != nil {
		return view.RelationChange{}, err
	}
	if !changed {
		return view.RelationChange{Ref: ref, Next: old, Sources: sources}, nil
	}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		return view.RelationChange{}, err
	}
	list = slices.Clone(list)
	list[index] = definition
	slices.SortFunc(list, func(a, b stored.Trigger) int { return strings.Compare(a.Name(), b.Name()) })
	return triggerListChange(h, ref, old, list, sources)
}

// alterTriggerParts applies a RENAME or [NO] DEPENDS ON EXTENSION to p.
// changed is false when the trigger already has the requested state; sources
// holds the named extension.
func alterTriggerParts(catalogAccess access.View, alter *parser.AlterTrigger, old *catalog.Relation, list []stored.Trigger, p stored.TriggerParts) (_ stored.TriggerParts, sources []catalog.Descriptor, changed bool, _ error) {
	switch alter.Action {
	case "rename":
		if p.Parent.ObjectID != 0 {
			parent, _ := stored.TriggerParent(catalogAccess.Catalog, p.Parent)
			if parent == nil {
				return p, nil, false, ids.ErrCatalogReplaced
			}
			return p, nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot rename trigger "%s" on table "%s"`, p.Name, old.Name()), Hint: fmt.Sprintf(`Rename the trigger on the partitioned table "%s" instead.`, parent.Name())}
		}
		if p.Name != alter.Target.Text && slices.ContainsFunc(list, func(t stored.Trigger) bool { return t.Name() == alter.Target.Text }) {
			return p, nil, false, sqlError("42710", fmt.Sprintf(`trigger "%s" for relation "%s" already exists`, alter.Target.Text, old.Name()))
		}
		if p.Name == alter.Target.Text {
			return p, nil, false, nil
		}
		p.Name = alter.Target.Text
	case "depends", "no_depends":
		address, exists := catalogAccess.Catalog.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: alter.Target.Text})
		if !exists {
			return p, nil, false, sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, alter.Target.Text))
		}
		extension, ok := catalogAccess.Catalog.Extension(address)
		if !ok {
			return p, nil, false, ids.ErrCatalogReplaced
		}
		sources = append(sources, extension)
		present := slices.Contains(p.AutoExtensions, address)
		if present == (alter.Action == "depends") {
			return p, sources, false, nil
		}
		if alter.Action == "depends" {
			p.AutoExtensions = append(slices.Clone(p.AutoExtensions), address)
		} else {
			p.AutoExtensions = slices.DeleteFunc(slices.Clone(p.AutoExtensions), func(a ids.ObjectAddress) bool { return a == address })
		}
	default:
		return p, nil, false, sqlError("0A000", "unsupported ALTER TRIGGER action")
	}
	return p, sources, true, nil
}
