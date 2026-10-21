package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
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
				err = alterCookingError(err)
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
		if _, err := (scalarRenderer{Definition: op.Definition, ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: d, Dependencies: &dependencies, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(op.Context(), auth.Catalog, node); err != nil {
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

func orderedChecks(d *catalog.Relation) []catalog.CheckConstraint {
	checks := slices.Clone(d.Constraints.Check)
	slices.SortFunc(checks, func(a, b catalog.CheckConstraint) int { return cmp.Compare(a.Ordinal, b.Ordinal) })
	return checks
}

func checkDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	for _, c := range d.Constraints.Check {
		for _, dep := range c.Dependencies {
			a := dep.Ref.Address
			if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
				return true
			}
		}
	}
	return false
}

// The merge fence checks external identities against the candidate current root,
// not the stage's older validation snapshot. The publisher retries if that root
// changes before its single final install. No rendering or row evaluation here.
func validateCheckProviders(view catalog.Read, d *catalog.Relation) error {
	for _, c := range d.Constraints.Check {
		for _, dep := range c.Dependencies {
			address := dep.Ref.Address
			address.SubID = 0
			if address == d.Address() {
				continue
			}
			provider, ok := view.Descriptor(address)
			if !ok || !ReferenceEpochValid(view, d, dep.Ref) || (provider.Kind() != ids.RelationObject && provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.ProcedureObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.NamespaceObject && provider.Kind() != ids.TSDictionaryObject) || provider.Kind() == ids.NamespaceObject && dep.Ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid check provider dependency")
			}
			if provider.Kind() == ids.CollationObject {
				if err := ValidateCollationDependency(view, dep.Ref, c.Expression); err != nil {
					return err
				}
			}
			if relation, ok := provider.(*catalog.Relation); ok && dep.Ref.Address.SubID != 0 && !relation.HasAttribute(dep.Ref.Address.SubID) {
				return fmt.Errorf("stuntgres: missing check provider attribute")
			}
		}
	}
	return nil
}

func validateChecks(view catalog.Read, d *catalog.Relation, names map[string]bool) error {
	previous := ""
	for i, c := range d.Constraints.Check {
		if c.Ordinal < 0 {
			return fmt.Errorf("stuntgres: invalid check declaration ordinal")
		}
		for _, previous := range d.Constraints.Check[:i] {
			if previous.Ordinal == c.Ordinal {
				return fmt.Errorf("stuntgres: duplicate check declaration ordinal")
			}
		}
		if c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || previous != "" && previous >= c.Name || c.Expression == nil || c.Expression.ResultUsage().Type == nil || c.Expression.ResultUsage().Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid check constraint identity")
		}
		names[c.Name], previous = true, c.Name
		var actual []ids.ObjectRef
		var commentSnapshot, visibilitySnapshot bool
		if _, err := (scalarRenderer{Validation: true, Owner: d, CapturedChecks: c.Dependencies, Dependencies: &actual, CommentSnapshot: &commentSnapshot, VisibilitySnapshot: &visibilitySnapshot}).SQL(context.Background(), view, c.Expression); err != nil {
			return err
		}
		if commentSnapshot != c.CommentSnapshot {
			return fmt.Errorf("stuntgres: inconsistent check comment snapshot manifest")
		}
		if visibilitySnapshot != c.VisibilitySnapshot {
			return fmt.Errorf("stuntgres: inconsistent check visibility snapshot manifest")
		}
		if len(actual) != len(c.Dependencies) {
			return fmt.Errorf("stuntgres: inconsistent check dependency manifest")
		}
		for _, ref := range actual {
			found := false
			for _, dep := range c.Dependencies {
				if dep.Ref == ref {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("stuntgres: missing check dependency")
			}
		}
		for _, dep := range c.Dependencies {
			address := dep.Ref.Address
			address.SubID = 0
			if address == d.Address() {
				if !ReferenceEpochValid(view, d, dep.Ref) || !d.HasAttribute(dep.Ref.Address.SubID) {
					return fmt.Errorf("stuntgres: invalid check column dependency")
				}
			} else {
				target, ok := view.Descriptor(address)
				if !ok || !ReferenceEpochValid(view, d, dep.Ref) || (target.Kind() != ids.RelationObject && target.Kind() != ids.TypeObject && target.Kind() != ids.CollationObject && target.Kind() != ids.ProcedureObject && target.Kind() != ids.OperatorObject && target.Kind() != ids.NamespaceObject && target.Kind() != ids.TSDictionaryObject) || target.Kind() == ids.NamespaceObject && dep.Ref.Address.SubID != 0 {
					return fmt.Errorf("stuntgres: invalid check provider dependency")
				}
				if target.Kind() == ids.CollationObject {
					if err := ValidateCollationDependency(view, dep.Ref, c.Expression); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// validateCheckRow is the shared DML-independent completed-row validator. A
// future ADD/VALIDATE consumer supplies its own traversal and publication stage.
// UNKNOWN passes, unlike WHERE. The no-CHECK path does not allocate or bind.
func validateCheckRow(d *catalog.Relation, row []sqltype.Value, eval *exec.Context) (*catalog.CheckConstraint, error) {
	if len(d.Constraints.Check) == 0 {
		return nil, nil
	}
	oldRow, oldScope, oldFrame := eval.Row, eval.Scope, eval.Frame
	eval.Row, eval.Scope, eval.Frame = row, 1, 1
	defer func() { eval.Row, eval.Scope, eval.Frame = oldRow, oldScope, oldFrame }()
	expressions, prepared := eval.PreparedChecks[d]
	if !prepared {
		// Prepare every CHECK before any volatile row evaluation, matching
		// ExecRelCheck. Keep descriptors/dependencies untouched and copy the
		// executable expression slice only when a branch actually changes.
		for i := range d.Constraints.Check {
			checkEvaluation := *eval
			checkEvaluation.RoutineRecipe, checkEvaluation.RoutineGraft = true, false
			checkEvaluation.RoutineChecks = d.Constraints.Check[i].Dependencies
			checkEvaluation.RoutineReferences = nil
			checkEvaluation.RoutineConsumer = d
			node, changed, err := exec.PreflightCoercionsWithPolicy(&checkEvaluation, d.Constraints.Check[i].Expression, true)
			eval.PreparedDomainChecks = checkEvaluation.PreparedDomainChecks
			if err != nil {
				return nil, err
			}
			if err := exec.AdmitExpression(&checkEvaluation, node, checkEvaluation.Catalog); err != nil {
				return nil, err
			}
			if changed {
				if expressions == nil {
					expressions = make([]expr.Expr, len(d.Constraints.Check))
					for j := range d.Constraints.Check {
						expressions[j] = d.Constraints.Check[j].Expression
					}
				}
				expressions[i] = node
			}
		}
		if eval.PreparedChecks == nil {
			eval.PreparedChecks = make(map[*catalog.Relation][]expr.Expr)
		}
		eval.PreparedChecks[d] = expressions
	}
	// Lazy SQL-body demand happens during truth, after preflight's stack
	// context has ended. Keep this actual CHECK's original recipe for Eval;
	// the shared statement manifest cannot stand in for a stored constraint.
	oldRecipe, oldGraft := eval.RoutineRecipe, eval.RoutineGraft
	oldChecks, oldReferences, oldConsumer := eval.RoutineChecks, eval.RoutineReferences, eval.RoutineConsumer
	defer func() {
		eval.RoutineRecipe, eval.RoutineGraft = oldRecipe, oldGraft
		eval.RoutineChecks, eval.RoutineReferences, eval.RoutineConsumer = oldChecks, oldReferences, oldConsumer
	}()
	for i := range d.Constraints.Check {
		c := &d.Constraints.Check[i]
		eval.RoutineRecipe, eval.RoutineGraft = true, false
		eval.RoutineChecks, eval.RoutineReferences, eval.RoutineConsumer = c.Dependencies, nil, d
		expression := c.Expression
		if expressions != nil {
			expression = expressions[i]
		}
		value, err := expression.Eval(eval)
		if err != nil {
			return nil, err
		}
		truth, err := plan.TruthOf(value)
		if err != nil {
			return nil, err
		}
		if truth == plan.False {
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

type checkDependencyEdge struct {
	relation *catalog.Relation
	check    catalog.CheckConstraint
	target   ids.ObjectAddress
}

// Provider references join the incumbent dependency stage. Owner-local column
// dependencies are AUTO and are handled by DROP COLUMN's stable-slot migration.
func stageCheckDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]checkDependencyEdge, error) {
	return checkDependents(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, mode)
}

func checkDependents(provider dropDependencyReader, targets []ids.ObjectAddress, mode parser.DropMode) ([]checkDependencyEdge, error) {
	var relations []*catalog.Relation
	for address, descriptor := range provider.inputDescriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(d.Constraints.Check) == 0 {
			continue
		}
		if staged, ok := provider.replacement(address).(*catalog.Relation); ok {
			d = staged
		}
		if provider.removed(address) {
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
	slices.SortFunc(relations, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	var edges []checkDependencyEdge
	for _, d := range relations {
		var kept []catalog.CheckConstraint
		changed := false
		for _, c := range d.Constraints.Check {
			var dependency ids.ObjectAddress
			for _, dep := range c.Dependencies {
				a := dep.Ref.Address
				if a.ClassID == d.Address().ClassID && a.ObjectID == d.Address().ObjectID {
					continue
				}
				for _, target := range targets {
					if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
						dependency = target
						break
					}
				}
			}
			if dependency == (ids.ObjectAddress{}) {
				kept = append(kept, c)
				continue
			}
			edges = append(edges, checkDependencyEdge{d, c, dependency})
			if mode == parser.DropRestrict {
				continue
			}
			changed = true
		}
		if changed {
			if err := provider.inheritanceAlterGuard(d); err != nil {
				return nil, err
			}
			copy := *d
			revision, err := provider.revision(copy.Revision())
			if err != nil {
				return nil, err
			}
			copy.ObjectRevision = revision
			copy.Constraints.Check = kept
			if err := provider.replace(&copy); err != nil {
				return nil, err
			}
		}
	}
	slices.SortStableFunc(edges, func(a, b checkDependencyEdge) int {
		if a.relation.Address().ObjectID < b.relation.Address().ObjectID {
			return -1
		}
		if a.relation.Address().ObjectID > b.relation.Address().ObjectID {
			return 1
		}
		return cmp.Compare(a.check.Ordinal, b.check.Ordinal)
	})
	return edges, nil
}
