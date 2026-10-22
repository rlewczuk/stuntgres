package table

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// BindChecks binds the raw CHECK constraints of CREATE TABLE (altering false)
// or ALTER TABLE ADD CONSTRAINT (altering true) into relation: it names them,
// merges them with the parents' checks in inherited, rejects duplicates,
// assigns ordinals after the installed ones and renders their dependencies.
// The host binds each expression. With altering, an authority that no longer
// reads the live catalog fails with ids.ErrCatalogReplaced and binding errors
// lose their position. Called with Engine.mu held.
func BindChecks(h Host, auth ddl.Authority, d *catalog.Relation, raw []*parser.CheckConstraint, input sqltype.InputEnvironment, altering bool, inherited map[string]catalog.CheckConstraint) error {
	if len(raw) == 0 {
		return nil
	}
	view := h.AccessView(auth)
	used := catalog.ConstraintNames(view.Catalog, d.Namespace(), d)
	installed := make(map[string]bool, len(d.Constraints.NotNull)+len(d.Constraints.Check))
	start := 0
	for _, c := range d.Constraints.Check {
		installed[c.Name] = true
		if c.Ordinal == int(^uint(0)>>1) {
			return fmt.Errorf("stuntgres: CHECK ordinal exhausted")
		}
		if c.Ordinal >= start {
			start = c.Ordinal + 1
		}
	}
	for _, c := range d.Constraints.NotNull {
		installed[c.Name] = true
	}
	for _, c := range d.Constraints.Unique {
		installed[c.Name] = true
	}
	for _, c := range d.Constraints.Exclusion {
		installed[c.Name] = true
	}
	for _, c := range d.Constraints.ForeignKeys {
		installed[c.Name] = true
	}
	current := make(map[string]bool, len(raw))
	for offset, c := range raw {
		if offset > int(^uint(0)>>1)-start {
			return fmt.Errorf("stuntgres: CHECK ordinal exhausted")
		}
		ordinal := start + offset
		if altering && view.Catalog.Epoch() != h.LiveEpoch() {
			return ids.ErrCatalogReplaced
		}
		node, uses, err := h.BindCheckExpression(auth, d, c.Expression, input)
		if err != nil {
			if altering {
				err = AlterCookingError(err)
			}
			return err
		}
		name := c.Name.Text
		if name == "" {
			column := ""
			if len(uses) == 1 {
				column = d.Columns[uses[0]].Name
			}
			name = catalog.GeneratedConstraintName(d.Name(), column, "check", used)
		}
		if current[name] {
			return sqlError("42710", `check constraint "`+name+`" already exists`)
		}
		if ancestor, ok := inherited[name]; ok {
			equality := analyze.Binder{Consumer: analyze.ConsumerContext{CheckDefinitionEquality: true}}
			if !equality.SameGroupExpression(node, ancestor.Expression) {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, d.Name()))
			}
			if c.NoInherit {
				return sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with inherited constraint on relation "%s"`, name, d.Name()))
			}
			if ancestor.NotValid && !c.NotValid {
				return sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on relation "%s"`, name, d.Name()))
			}
			current[name] = true
			h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`merging constraint "%s" with inherited definition`, name)})
			continue
		}
		if installed[name] {
			if !altering && d.Inheritance != nil {
				if provenance, inherited := d.Inheritance.Checks[name]; inherited {
					old := slices.IndexFunc(d.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == name })
					if old < 0 || !analyze.SameGroupExpression(d.Constraints.Check[old].Expression, node) {
						return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, d.Name()))
					}
					if c.NoInherit {
						return sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with inherited constraint on relation "%s"`, name, d.Name()))
					}
					provenance.Local = true
					d.Inheritance.Checks[name] = provenance
					current[name], used[name] = true, true
					continue
				}
			}
			if altering {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, d.Name()))
			}
			const index = "pg_constraint_conrelid_contypid_conname_index"
			diagnostic := sqlError("23505", `duplicate key value violates unique constraint "`+index+`"`).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", d.Address().ObjectID, name)
			diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = "pg_catalog", "pg_constraint", index
			return diagnostic
		}
		current[name], used[name] = true, true
		definition := catalog.CheckConstraint{Name: name, NoInherit: c.NoInherit, NotValid: c.NotValid, Ordinal: ordinal, Expression: node}
		var dependencies []ids.ObjectRef
		if _, err := (stored.ScalarRenderer{Definition: h.OperationDefinition(), ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: d, Dependencies: &dependencies, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(h.Context(), view.Catalog, node); err != nil {
			return err
		}
		for _, ref := range dependencies {
			address := ref.Address
			address.SubID = 0
			revision := d.Revision()
			if descriptor, ok := view.Catalog.Descriptor(address); ok {
				revision = descriptor.Revision()
			}
			definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: revision})
		}
		d.Constraints.Check = append(d.Constraints.Check, definition)
		if d.Inheritance != nil {
			d.Inheritance.Checks[name] = catalog.InheritedConstraint{Local: true}
		}
	}
	slices.SortFunc(d.Constraints.Check, func(a, b catalog.CheckConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}
