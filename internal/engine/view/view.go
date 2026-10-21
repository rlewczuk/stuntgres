package view

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Output names are downstream definition checks, not SELECT/alias analysis.
// Existing replacements check their descriptor before options; fresh CREATE
// checks options first. The caller has already resolved the real destination.
func CheckViewOutputNames(columns []catalog.Column, old *catalog.Relation) error {
	if len(columns) < 2 {
		return nil
	}
	seen := make(map[string]bool, len(columns))
	for _, column := range columns {
		if seen[column.Name] {
			if old != nil {
				return sqlError("42701", fmt.Sprintf(`column "%s" of relation "%s" already exists`, column.Name, old.Name()))
			}
			return sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, column.Name))
		}
		seen[column.Name] = true
	}
	return nil
}

// Relation occurrences, including unused/join/subquery leaves, determine PG18
// inference. Expression-only regclass/sequence dependencies deliberately do not.
func RequestedViewPersistence(catalog catalog.Read, view *parser.CreateView, sources []exec.Leaf) string {
	if view.Persistence == "temporary" {
		return "temporary"
	}
	for _, source := range sources {
		if namespace, ok := catalog.Namespace(source.Relation.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
			return "temporary"
		}
	}
	return "logged"
}

func CheckViewPrefix(catalog catalog.Read, old, next []catalog.Column) error {
	if len(next) < len(old) {
		return sqlError("42P16", "cannot drop columns from view")
	}
	for i, column := range old {
		replacement := next[i]
		if column.Name != replacement.Name {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P16", Message: fmt.Sprintf(`cannot change name of view column "%s" to "%s"`, column.Name, replacement.Name), Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."}
		}
		a, b := column.Usage, replacement.Usage
		if a.Type.ID() != b.Type.ID() || a.Typmod != b.Typmod {
			oldType, err := analyze.PGType(a)
			if err != nil {
				return err
			}
			newType, err := analyze.PGType(b)
			if err != nil {
				return err
			}
			return sqlError("42P16", fmt.Sprintf(`cannot change data type of view column "%s" from %s to %s`, column.Name, oldType, newType))
		}
		if a.Collation.ID != b.Collation.ID {
			return sqlError("42P16", fmt.Sprintf(`cannot change collation of view column "%s" from "%s" to "%s"`, column.Name, typedef.CollationName(catalog, a.Collation.ID), typedef.CollationName(catalog, b.Collation.ID)))
		}
	}
	return nil
}

// CreationScope is the statement's view slot: the CREATE VIEW target
// namespace, persistence, binding and staged changes, plus the stack of views
// being expanded (for recursion checks).
type CreationScope struct {
	Namespace   ids.ObjectRef
	Persistence string
	Binding     analyze.Binding
	Changes     *session.Journal
	Active      []ids.ObjectAddress
}

// EndStatement clears the view-creation state when the statement finishes.
// The active view stack is lexical expansion state and is left as it was.
func (v *CreationScope) EndStatement() {
	if v == nil {
		return
	}
	v.Namespace, v.Persistence, v.Binding, v.Changes = ids.ObjectRef{}, "", analyze.Binding{}, nil
}
