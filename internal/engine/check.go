package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) bindCreateChecks(op *session.Operation, auth AuthorizationView, syntax *parser.CreateTable, d *catalog.Relation, input sqltype.InputEnvironment, inherited map[string]catalog.CheckConstraint) error {
	var raw []*parser.CheckConstraint
	for _, element := range syntax.Elements {
		if element.Check != nil {
			raw = append(raw, element.Check)
		}
		if element.Column != nil {
			for _, c := range element.Column.Constraints {
				if c.Check != nil {
					raw = append(raw, c.Check)
				}
			}
		}
	}
	return e.bindColumnChecks(op, auth, raw, d, input, false, inherited)
}

func (e *Engine) bindColumnChecks(op *session.Operation, auth AuthorizationView, raw []*parser.CheckConstraint, d *catalog.Relation, input sqltype.InputEnvironment, altering bool, inherited map[string]catalog.CheckConstraint) error {
	if len(raw) == 0 {
		return nil
	}
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	columns := make([]analyze.QueryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: c, Slot: i, Leaf: 0, Physical: i}
	}
	qualifier := analyze.QueryQualifier{Name: d.Name(), Original: d.Name(), Schema: auth.Catalog.Get(d.Namespace()).Name(), Ref: ref, Columns: columns}
	ns := analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: d.Columns}
	used := catalog.ConstraintNames(auth.Catalog, d.Namespace(), d)
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
		binder := newExpressionBinder(op.Context(), e, input, op, nil)
		binder.Consumer.CheckConsumer, binder.Params.NoParameters = true, true
		binder.Query.Auth = &auth
		binder.Query.Namespace = &ns
		binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: d}}
		if altering {
			e.mu.Lock()
			if auth.Catalog.Epoch() != e.catalog.state.Epoch() {
				e.mu.Unlock()
				return ids.ErrCatalogReplaced
			}
		}
		node, err := binder.BindBoolean(c.Expression, analyze.ProjectionContext, "CHECK")
		if altering {
			e.mu.Unlock()
		}
		if err != nil {
			if altering {
				err = AlterCookingError(err)
			}
			return err
		}
		name := c.Name.Text
		if name == "" {
			column := ""
			uses := binder.Query.Sources[0].Uses
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
			op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`merging constraint "%s" with inherited definition`, name)})
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
		if _, err := (stored.ScalarRenderer{Definition: op.Definition, ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: d, Dependencies: &dependencies, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(op.Context(), auth.Catalog, node); err != nil {
			return err
		}
		for _, ref := range dependencies {
			address := ref.Address
			address.SubID = 0
			revision := d.Revision()
			if descriptor, ok := auth.Catalog.Descriptor(address); ok {
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

// Provider references join the incumbent dependency stage. Owner-local column
// dependencies are AUTO and are handled by DROP COLUMN's stable-slot migration.
func stageCheckDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]checkDependencyEdge, error) {
	return CheckDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}
