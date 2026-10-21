package table

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ValidateCookedDefaultTemporary(node expr.Expr, consumer ids.SessionToken) error {
	var err error
	plan.WalkExpression(node, func(node expr.Expr) {
		if err != nil {
			return
		}
		if call, ok := node.(functions.SequenceCall); ok {
			err = sequence.ValidateDefaultTemporaryProvider(call.Temporary, consumer, parser.Position{})
		}
	})
	return err
}

// AlterDefaultColumn is the slot of the column whose DEFAULT s changes, after
// the relation-kind, system, generated and identity column guards.
func AlterDefaultColumn(old *catalog.Relation, s *parser.AlterTableDefault) (int, error) {
	if err := analyze.TableKindError(old, "ALTER COLUMN ... SET DEFAULT"); err != nil {
		return 0, err
	}
	if catalog.IsSystemColumn(s.Column.Text) {
		return 0, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, s.Column.Text))
	}
	slot, exists := old.Index[s.Column.Text]
	if !exists {
		return 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, s.Column.Text, old.Name()))
	}
	attr := old.Attributes[slot]
	if _, generated := old.ColumnMeta.Generations[attr]; generated {
		diagnostic := sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is a generated column`, s.Column.Text, old.Name())).(*sqlstate.Error)
		diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... SET EXPRESSION instead."
		if s.Action == parser.DropDefault {
			diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... DROP EXPRESSION instead."
		}
		return 0, diagnostic
	}
	if _, ok := old.ColumnMeta.Identities[attr]; ok {
		diagnostic := sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, s.Column.Text, old.Name())).(*sqlstate.Error)
		if s.Action == parser.DropDefault {
			diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... DROP IDENTITY instead."
		}
		return 0, diagnostic
	}
	return slot, nil
}

// AlterDefaultCandidate is old with the DEFAULT of attr replaced by definition,
// or dropped when definition has no expression.
func AlterDefaultCandidate(old *catalog.Relation, attr int32, definition catalog.ColumnDefault) (*catalog.Relation, error) {
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	capacity := len(old.ColumnMeta.Defaults)
	if definition.Expression == nil {
		if _, present := old.ColumnMeta.Defaults[attr]; present {
			capacity--
		}
	} else if _, present := old.ColumnMeta.Defaults[attr]; !present {
		capacity++
	}
	next.ColumnMeta.Defaults = nil
	if capacity != 0 {
		next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, capacity)
	}
	for key, value := range old.ColumnMeta.Defaults {
		if key != attr {
			next.ColumnMeta.Defaults[key] = value
		}
	}
	if definition.Expression != nil {
		next.ColumnMeta.Defaults[attr] = definition
	}
	return &next, nil
}

// CookAlterDefault binds and renders the SET DEFAULT expression of old's slot;
// a default the column type makes redundant is omitted.
func CookAlterDefault(h AlterHost, auth ddl.Authority, old *catalog.Relation, slot int, expression parser.Expr) (catalog.ColumnDefault, error) {
	view := h.AccessView(auth)
	var definition catalog.ColumnDefault
	var err error
	definition.Expression, err = h.BindDefault(auth, old, old.Columns[slot], expression)
	if err == nil {
		_, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot, CatalogAliasReferences: &definition.CatalogAliasReferences}).SQL(h.Context(), view.Catalog, definition.Expression)
	}
	if err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return catalog.ColumnDefault{}, err
	}
	if stored.OmitColumnDefault(view.Catalog, definition.Expression, old.Columns[slot].Usage) {
		return catalog.ColumnDefault{}, nil
	}
	definition.OID, err = h.AllocateOID()
	return definition, err
}
