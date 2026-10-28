package fdw

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The ALTER FOREIGN TABLE decisions. The engine keeps the action loop: its
// DROP COLUMN candidate waits on locks, so it runs between these calls with
// Engine.mu released, as do the table candidates' own lock windows.

// AlterTarget resolves and admits the relation of ALTER FOREIGN TABLE s.
// A missing relation with IF EXISTS emits the skip notice and returns a nil
// relation. The relation must be a foreign table, still be locked, the
// statement's admission reference, and be owned by the actor.
func AlterTarget(h Host, auth ddl.Authority, s *parser.ForeignDDL, locked ids.ObjectRef) (*catalog.Relation, ids.ObjectRef, error) {
	ref, err := h.ResolveRelation(auth, s.Name)
	if err != nil {
		if s.IfExists && table.RenameMissing(err) {
			ddl.Notice(h, "00000", fmt.Sprintf(`relation "%s" does not exist, skipping`, catalog.ObjectNameText(s.Name)))
			return nil, ids.ObjectRef{}, nil
		}
		return nil, ids.ObjectRef{}, err
	}
	old := h.AccessView(auth).Catalog.MustRelation(ref.Address)
	if old.RelationKind != catalog.ForeignRelation {
		return nil, ids.ObjectRef{}, sqlError("42809", fmt.Sprintf(`"%s" is not a foreign table`, old.Name()))
	}
	if ref != locked {
		return nil, ids.ObjectRef{}, ids.ErrCatalogReplaced
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return nil, ids.ObjectRef{}, err
	}
	return old, ref, nil
}

// AlterBases records the server and wrapper of old among the stage's
// observed dependencies and returns the wrapper; ids.ErrCatalogReplaced when
// either is gone.
func AlterBases(h Host, auth ddl.Authority, old *catalog.Relation) (*catalog.ForeignObject, error) {
	view := h.AccessView(auth)
	server, ok := view.Catalog.Foreign(old.Foreign.Server)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	wrapper, ok := view.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	h.AddDomainBase(server)
	h.AddDomainBase(wrapper)
	return wrapper, nil
}

// AlterCandidate is the working copy of old that the actions change: it owns
// its foreign definition and column options map.
func AlterCandidate(old *catalog.Relation) catalog.Relation {
	next := *old
	foreign := *old.Foreign
	foreign.Columns = maps.Clone(old.Foreign.Columns)
	next.Foreign = &foreign
	return next
}

// AlterColumn is the slot of column in next; 42703 when it has none.
func AlterColumn(next *catalog.Relation, column string) (int, error) {
	slot, ok := next.Index[column]
	if !ok {
		return 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, next.Name()))
	}
	return slot, nil
}

// AlterOptions applies the OPTIONS of action, to the table or to its column,
// to next and returns the validator job of the result.
func AlterOptions(h Host, next *catalog.Relation, wrapper *catalog.ForeignObject, action parser.ForeignTableAction) (Validation, error) {
	kind := catalog.ForeignTable
	options := next.Foreign.Options
	var attribute int32
	if action.Column.Text != "" {
		slot, err := AlterColumn(next, action.Column.Text)
		if err != nil {
			return Validation{}, err
		}
		attribute = next.Attributes[slot]
		options = next.Foreign.Columns[attribute]
		kind = catalog.ForeignAttribute
	}
	options, err := TransformOptions(options, action.Options)
	if err != nil {
		return Validation{}, err
	}
	if attribute == 0 {
		next.Foreign.Options = options
	} else {
		next.Foreign.Columns[attribute] = options
	}
	return stageValidatorJob(h, wrapper, kind, options)
}

// CheckAddedColumn rejects the constraints ADD COLUMN cannot declare on a
// foreign table.
func CheckAddedColumn(definition *parser.Column) error {
	return CheckTableDeclaration(&parser.CreateTable{Elements: []parser.TableElement{{Column: definition}}})
}

// AddedColumnOptions sets the OPTIONS of the column ADD COLUMN added to
// candidate and returns their validator job.
func AddedColumnOptions(h Host, candidate *catalog.Relation, wrapper *catalog.ForeignObject, column string, raw []parser.ForeignOption) (Validation, error) {
	options, err := TransformOptions(nil, raw)
	if err != nil {
		return Validation{}, err
	}
	attribute := candidate.Attributes[candidate.Index[column]]
	candidate.Foreign.Columns[attribute] = options
	if len(options) == 0 {
		return Validation{}, nil
	}
	return stageValidatorJob(h, wrapper, catalog.ForeignAttribute, options)
}

// UnsupportedAlter rejects an ALTER FOREIGN TABLE action that needs local
// storage or write support.
func UnsupportedAlter() error {
	return sqlError("0A000", "foreign table action requires unsupported local storage or write support")
}
