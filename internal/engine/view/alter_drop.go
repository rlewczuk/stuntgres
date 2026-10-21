package view

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// AlterChange decides ALTER VIEW s on old for DEPENDS ON EXTENSION and
// SET or RESET: it returns the changed relation with a new revision, or nil
// when nothing changes, and the extension the stage records as a query
// source. RENAME and SET SCHEMA never reach it; other actions are 0A000.
func AlterChange(a ddl.Allocator, catalogView catalog.Read, old *catalog.Relation, s *parser.AlterView) (*catalog.Relation, *catalog.Extension, error) {
	next := *old
	changed := true
	var extension *catalog.Extension
	switch s.Action {
	case "depends", "no_depends":
		address, exists := catalogView.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Target.Text})
		if !exists {
			return nil, nil, sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, s.Target.Text))
		}
		extension, _ = catalogView.Extension(address)
		if extension == nil {
			return nil, nil, ids.ErrCatalogReplaced
		}
		slot := sort.Search(len(old.AutoExtensions), func(i int) bool { return old.AutoExtensions[i].ObjectID >= address.ObjectID })
		present := slot < len(old.AutoExtensions) && old.AutoExtensions[slot] == address
		changed = present == (s.Action == "no_depends")
		if changed {
			if present {
				next.AutoExtensions = make([]ids.ObjectAddress, len(old.AutoExtensions)-1)
				copy(next.AutoExtensions, old.AutoExtensions[:slot])
				copy(next.AutoExtensions[slot:], old.AutoExtensions[slot+1:])
			} else {
				next.AutoExtensions = make([]ids.ObjectAddress, len(old.AutoExtensions)+1)
				copy(next.AutoExtensions, old.AutoExtensions[:slot])
				next.AutoExtensions[slot] = address
				copy(next.AutoExtensions[slot+1:], old.AutoExtensions[slot:])
			}
		}
	case "set", "reset":
		altered, err := alterViewOptions(old, s)
		if err != nil {
			return nil, nil, err
		}
		next.View = altered
	default:
		return nil, nil, sqlError("0A000", "ALTER VIEW action is unsupported")
	}
	if !changed {
		return nil, extension, nil
	}
	var err error
	next.ObjectRevision, err = a.Revision()
	if err != nil {
		return nil, nil, err
	}
	return &next, extension, nil
}

// alterViewOptions applies ALTER VIEW SET or RESET to the definition of old
// and checks a resulting check option.
func alterViewOptions(old *catalog.Relation, s *parser.AlterView) (*stored.View, error) {
	definition := stored.ViewOf(old).Parts()
	if s.Action == "set" {
		barrier, invoker, mode, err := stored.ViewOptions(s.Options)
		if err != nil {
			return nil, err
		}
		for _, option := range s.Options {
			switch option.Name.Text {
			case "security_barrier":
				definition.Barrier = barrier
			case "security_invoker":
				definition.Invoker = invoker
			case "check_option":
				definition.CheckOption = mode
			}
		}
	} else {
		for _, option := range s.Options {
			if option.HasValue {
				return nil, sqlError("42601", "RESET must not include values for parameters")
			}
			switch option.Name.Text {
			case "security_barrier":
				definition.Barrier = false
			case "security_invoker":
				definition.Invoker = false
			case "check_option":
				definition.CheckOption = stored.CheckNone
			}
		}
	}
	definition.Options = TransformViewOptions(stored.ViewOf(old).Options(), s.Options, s.Action == "reset")
	altered := stored.NewView(definition)
	if altered.CheckOption() != stored.CheckNone {
		if err := stored.ValidateViewCheckDeclaration(altered); err != nil {
			return nil, err
		}
	}
	return altered, nil
}

// DropTargets resolves the relations DROP [MATERIALIZED] VIEW s names, in
// order and without duplicates. Each must be an owned relation of the dropped
// kind; with IF EXISTS a missing one becomes a NOTICE instead.
func DropTargets(h Host, auth ddl.Authority, s *parser.DropView) ([]ids.ObjectRef, []sqlstate.Error, error) {
	catalogAccess := h.AccessView(auth)
	kind, label := catalog.ViewRelation, "view"
	if s.Materialized {
		kind, label = catalog.MaterializedViewRelation, "materialized view"
	}
	refs := make([]ids.ObjectRef, 0, len(s.Names))
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	var notices []sqlstate.Error
	for _, name := range s.Names {
		ref, err := h.ResolveRelation(auth, name)
		if err != nil {
			if !s.IfExists || !table.RenameMissing(err) {
				return nil, nil, ViewDropMissing(err, name, label)
			}
			diagnostic := ViewDropMissing(err, name, label).(*sqlstate.Error)
			notices = append(notices, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: diagnostic.Message + ", skipping"})
			continue
		}
		d := catalogAccess.Catalog.MustRelation(ref.Address)
		if d.RelationKind != kind {
			hint := "Use DROP TABLE to remove a table."
			switch d.RelationKind {
			case catalog.SequenceRelation:
				hint = "Use DROP SEQUENCE to remove a sequence."
			case catalog.IndexRelation:
				hint = "Use DROP INDEX to remove an index."
			case catalog.ViewRelation:
				hint = "Use DROP VIEW to remove a view."
			case catalog.MaterializedViewRelation:
				hint = "Use DROP MATERIALIZED VIEW to remove a materialized view."
			}
			return nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`"%s" is not a %s`, d.Name(), label), Hint: hint}
		}
		if err := ViewDropOwner(catalogAccess, auth, ref, d); err != nil {
			return nil, nil, err
		}
		if !seen[ref.Address] {
			seen[ref.Address] = true
			refs = append(refs, ref)
		}
	}
	return refs, notices, nil
}

// RecheckDropTargets checks the DROP VIEW targets again after the lock
// waits: each must still exist in v's catalog domain and still be owned.
func RecheckDropTargets(v access.View, env access.Environment, refs []ids.ObjectRef) error {
	for _, ref := range refs {
		d, ok := v.Catalog.Relation(ref.Address)
		if !ok || ref.Domain() != v.Owner.Domain {
			return ids.ErrCatalogReplaced
		}
		if err := ViewDropOwner(v, env, ref, d); err != nil {
			return err
		}
	}
	return nil
}
