package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// A CHECK is a typed immutable current-row expression. Its dependencies retain
// stable attribute and provider identities, never a name-keyed SQL cache.
type checkConstraint struct {
	Name         string
	NoInherit    bool
	expression   BoundExpr
	dependencies []expressionDependency
}

func (e *Engine) bindCreateChecks(op *Operation, auth AuthorizationView, syntax *parser.CreateTable, d *relationDescriptor, input sqltype.InputEnvironment) error {
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
	if len(raw) == 0 {
		return nil
	}
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: d.address}
	columns := make([]queryColumn, len(d.columns))
	for i, c := range d.columns {
		columns[i] = queryColumn{c, i, 0, i}
	}
	qualifier := queryQualifier{name: d.name, original: d.name, schema: auth.catalog.state.descriptors[d.namespace].Name(), ref: ref, columns: columns}
	ns := queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: d.columns}
	used := constraintNames(auth.catalog, d.namespace, nil)
	installed := make(map[string]bool, len(d.notNullConstraints))
	for _, c := range d.notNullConstraints {
		installed[c.Name] = true
	}
	current := make(map[string]bool, len(raw))
	for _, c := range raw {
		binder := newExpressionBinder(op.Context(), e, input, op, nil)
		binder.checkConsumer, binder.noParameters = true, true
		binder.queryAuth = &auth
		binder.queryNamespace = &ns
		binder.querySources = []queryLeaf{{ref: ref, relation: d}}
		node, err := binder.bindBoolean(c.Expression, projectionContext, "CHECK")
		if err != nil {
			return err
		}
		name := c.Name.Text
		if name == "" {
			column := ""
			uses := binder.querySources[0].uses
			if len(uses) == 1 {
				column = d.columns[uses[0]].Name
			}
			name = generatedConstraintName(d.name, column, "check", used)
		}
		if current[name] {
			return sqlError("42710", `check constraint "`+name+`" already exists`)
		}
		if installed[name] {
			const index = "pg_constraint_conrelid_contypid_conname_index"
			diagnostic := sqlError("23505", `duplicate key value violates unique constraint "`+index+`"`).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", d.address.ObjectID, name)
			diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = "pg_catalog", "pg_constraint", index
			return diagnostic
		}
		current[name], used[name] = true, true
		definition := checkConstraint{Name: name, NoInherit: c.NoInherit, expression: node}
		var dependencies []ObjectRef
		if _, err := (scalarRenderer{owner: d, dependencies: &dependencies}).sql(op.Context(), auth.catalog, node); err != nil {
			return err
		}
		for _, ref := range dependencies {
			address := ref.Address
			address.SubID = 0
			revision := d.revision
			if descriptor, ok := auth.catalog.Descriptor(address); ok {
				revision = descriptor.Revision()
			}
			definition.dependencies = append(definition.dependencies, expressionDependency{ref: ref, revision: revision})
		}
		d.checkConstraints = append(d.checkConstraints, definition)
	}
	slices.SortFunc(d.checkConstraints, func(a, b checkConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

func validateChecks(view CatalogRead, d *relationDescriptor, names map[string]bool) error {
	previous := ""
	for _, c := range d.checkConstraints {
		if c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || previous != "" && previous >= c.Name || c.expression == nil || c.expression.ResultUsage().Type == nil || c.expression.ResultUsage().Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid check constraint identity")
		}
		names[c.Name], previous = true, c.Name
		var actual []ObjectRef
		if _, err := (scalarRenderer{owner: d, dependencies: &actual}).sql(context.Background(), view, c.expression); err != nil {
			return err
		}
		if len(actual) != len(c.dependencies) {
			return fmt.Errorf("stuntgres: inconsistent check dependency manifest")
		}
		for _, ref := range actual {
			found := false
			for _, dep := range c.dependencies {
				if dep.ref == ref {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("stuntgres: missing check dependency")
			}
		}
		for _, dep := range c.dependencies {
			address := dep.ref.Address
			address.SubID = 0
			if address == d.address {
				if dep.ref.owner != view.owner.owner || dep.ref.Epoch != view.Epoch() || !d.hasAttribute(dep.ref.Address.SubID) {
					return fmt.Errorf("stuntgres: invalid check column dependency")
				}
			} else {
				target, ok := view.Descriptor(address)
				if !ok || dep.ref.owner != view.owner.owner || dep.ref.Epoch != view.Epoch() || (target.Kind() != RelationObject && target.Kind() != TypeObject) {
					return fmt.Errorf("stuntgres: invalid check provider dependency")
				}
			}
		}
	}
	return nil
}

// validateCheckRow is the shared DML-independent completed-row validator. A
// future ADD/VALIDATE consumer supplies its own traversal and publication stage.
// UNKNOWN passes, unlike WHERE. The no-CHECK path does not allocate or bind.
func validateCheckRow(d *relationDescriptor, row []sqltype.Value, eval *EvalContext) (*checkConstraint, error) {
	if len(d.checkConstraints) == 0 {
		return nil, nil
	}
	oldRow, oldScope, oldFrame := eval.Row, eval.Scope, eval.Frame
	eval.Row, eval.Scope, eval.Frame = row, 1, 1
	defer func() { eval.Row, eval.Scope, eval.Frame = oldRow, oldScope, oldFrame }()
	for i := range d.checkConstraints {
		c := &d.checkConstraints[i]
		value, err := c.expression.Eval(eval)
		if err != nil {
			return nil, err
		}
		truth, err := TruthOf(value)
		if err != nil {
			return nil, err
		}
		if truth == False {
			return c, nil
		}
	}
	return nil, nil
}

func checkDetailError(schema, table, name, detail string) error {
	d := sqlError("23514", `new row for relation "`+table+`" violates check constraint "`+name+`"`).(*sqlstate.Error)
	d.Schema, d.Table, d.Constraint, d.Detail = schema, table, name, detail
	return d
}

// Provider references join the incumbent dependency stage. Owner-local column
// dependencies are AUTO and are handled by DROP COLUMN's stable-slot migration.
func stageCheckDependents(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode) error {
	var relations []*relationDescriptor
	for address, descriptor := range stage.base.state.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok || len(d.checkConstraints) == 0 {
			continue
		}
		if staged, ok := stage.descriptors[address].(*relationDescriptor); ok {
			d = staged
		}
		if _, removed := stage.removals[address]; removed {
			continue
		}
		dropped := false
		for _, target := range targets {
			if target == address {
				dropped = true
				break
			}
		}
		if !dropped {
			relations = append(relations, d)
		}
	}
	slices.SortFunc(relations, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	for _, d := range relations {
		var kept []checkConstraint
		changed := false
		for _, c := range d.checkConstraints {
			var dependency ObjectAddress
			for _, dep := range c.dependencies {
				a := dep.ref.Address
				if a.ClassID == d.address.ClassID && a.ObjectID == d.address.ObjectID {
					continue
				}
				for _, target := range targets {
					if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
						dependency = target
						break
					}
				}
			}
			if dependency == (ObjectAddress{}) {
				kept = append(kept, c)
				continue
			}
			object := dependencyObject(stage.base, dependency)
			if mode == parser.DropRestrict {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because other objects depend on it", Detail: "constraint " + c.Name + " on table " + d.name + " depends on " + object, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
			}
			changed = true
			stage.viewCascadeNames = append(stage.viewCascadeNames, "drop cascades to constraint "+c.Name+" on table "+d.name)
		}
		if changed {
			copy := *d
			copy.revision++
			if copy.revision == 0 {
				return fmt.Errorf("stuntgres: definition revision exhausted")
			}
			copy.checkConstraints = kept
			if err := stage.Replace(&copy); err != nil {
				return err
			}
		}
	}
	return nil
}
