package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func (e *Engine) bindCreateChecks(op *Operation, auth AuthorizationView, syntax *parser.CreateTable, d *catalog.Relation, input sqltype.InputEnvironment, inherited map[string]catalog.CheckConstraint) error {
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

func (e *Engine) bindColumnChecks(op *Operation, auth AuthorizationView, raw []*parser.CheckConstraint, d *catalog.Relation, input sqltype.InputEnvironment, altering bool, inherited map[string]catalog.CheckConstraint) error {
	if len(raw) == 0 {
		return nil
	}
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	columns := make([]queryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = queryColumn{descriptor: c, slot: i, leaf: 0, physical: i}
	}
	qualifier := queryQualifier{name: d.Name(), original: d.Name(), schema: auth.Catalog.Get(d.Namespace()).Name(), ref: ref, columns: columns}
	ns := queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: d.Columns}
	used := constraintNames(auth.Catalog, d.Namespace(), d)
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
		binder.checkConsumer, binder.noParameters = true, true
		binder.queryAuth = &auth
		binder.queryNamespace = &ns
		binder.querySources = []queryLeaf{{ref: ref, relation: d}}
		if altering {
			e.mu.Lock()
			if auth.Catalog.Epoch() != e.catalog.state.Epoch() {
				e.mu.Unlock()
				return ids.ErrCatalogReplaced
			}
		}
		node, err := binder.bindBoolean(c.Expression, projectionContext, "CHECK")
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
			uses := binder.querySources[0].uses
			if len(uses) == 1 {
				column = d.Columns[uses[0]].Name
			}
			name = generatedConstraintName(d.Name(), column, "check", used)
		}
		if current[name] {
			return sqlError("42710", `check constraint "`+name+`" already exists`)
		}
		if ancestor, ok := inherited[name]; ok {
			equality := expressionBinder{checkDefinitionEquality: true}
			if !equality.sameGroupExpression(node, ancestor.Expression) {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, d.Name()))
			}
			if c.NoInherit {
				return sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with inherited constraint on relation "%s"`, name, d.Name()))
			}
			if ancestor.NotValid && !c.NotValid {
				return sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on relation "%s"`, name, d.Name()))
			}
			current[name] = true
			op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`merging constraint "%s" with inherited definition`, name)})
			continue
		}
		if installed[name] {
			if !altering && d.Inheritance != nil {
				if provenance, inherited := d.Inheritance.Checks[name]; inherited {
					old := slices.IndexFunc(d.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == name })
					if old < 0 || !sameGroupExpression(d.Constraints.Check[old].Expression, node) {
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
		if _, err := (scalarRenderer{definition: op.definition, aclOutput: capturedACLItemOutput{owner: auth.Owner, roles: auth.Roles}, owner: d, dependencies: &dependencies, commentSnapshot: &definition.CommentSnapshot, visibilitySnapshot: &definition.VisibilitySnapshot}).sql(op.Context(), auth.Catalog, node); err != nil {
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
			if !ok || !storedReferenceEpochValid(view, d, dep.Ref) || (provider.Kind() != ids.RelationObject && provider.Kind() != ids.TypeObject && provider.Kind() != ids.CollationObject && provider.Kind() != ids.ProcedureObject && provider.Kind() != ids.OperatorObject && provider.Kind() != ids.NamespaceObject && provider.Kind() != ids.TSDictionaryObject) || provider.Kind() == ids.NamespaceObject && dep.Ref.Address.SubID != 0 {
				return fmt.Errorf("stuntgres: invalid check provider dependency")
			}
			if provider.Kind() == ids.CollationObject {
				if err := validateStoredCollationDependency(view, dep.Ref, c.Expression); err != nil {
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

// Persistent definition holds use the incumbent ticket and replacement fence.
// Admission occurs outside Engine.mu; after a wait identities are opened anew,
// never retargeted by name or evaluated to establish a dependency.
func (s *StatementScope) acquireDefinitionProviders(stage *CatalogStage) error {
	if err := s.acquireForeignKeyDefinitionProviders(stage); err != nil {
		return err
	}
	var refs []ids.ObjectRef
	var retained map[ids.ObjectAddress]catalog.Descriptor
	add := func(ref ids.ObjectRef, d *catalog.Relation) error {
		address := ref.Address
		address.SubID = 0
		if address.ClassID != ids.RelationClassID || d != nil && address == d.Address() {
			return nil
		}
		if _, incumbent := stage.Base.Find(address); !incumbent {
			// Newly created providers belong to the private atomic candidate.
			provider, staged := stage.Get(address).(*catalog.Relation)
			if !staged || provider.Address() != address || ref.Domain() != stage.owner.catalog.Domain || ref.Epoch != stage.Base.Epoch() {
				return fmt.Errorf("stuntgres: invalid staged definition provider")
			}
			return nil
		}
		valid := storedReferenceEpochValid(stage.Base, d, ref)
		if d != nil && d.View != nil {
			// The stage's evolved consumer retains the incumbent definition's
			// immutable provenance; old epochs never become generic stage refs.
			incumbent, _ := stage.Base.Relation(d.Address())
			valid = incumbent != nil && incumbent.View == d.View && storedViewReferenceValid(stage.Base, incumbent, ref)
			if ref.Epoch == stage.Base.Epoch() {
				valid = storedViewReferenceValid(stage.Base, d, ref)
			}
		}
		if !valid {
			return fmt.Errorf("stuntgres: invalid retained definition provider")
		}
		// Prove the original attribute reference before using a whole-object lock.
		ref.Address = address
		if ref.Epoch != stage.Base.Epoch() {
			if retained == nil {
				retained = make(map[ids.ObjectAddress]catalog.Descriptor)
			}
			retained[ref.Address] = stage.Base.Get(ref.Address)
		}
		ref.Epoch = stage.Base.Epoch()
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
		return nil
	}
	for _, descriptor := range stage.likeSources {
		ref := stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), descriptor.Address())
		if err := add(ref, nil); err != nil {
			return err
		}
	}
	for _, descriptor := range stage.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if d.View != nil {
			for _, leaf := range viewOf(d).sources {
				if err := add(leaf.ref, d); err != nil {
					return err
				}
			}
			for _, dependency := range viewOf(d).dependencies {
				if err := add(dependency.Ref, d); err != nil {
					return err
				}
			}
		}
		for _, c := range d.Constraints.Check {
			for _, dep := range c.Dependencies {
				if err := add(dep.Ref, d); err != nil {
					return err
				}
			}
		}
		for _, definition := range d.ColumnMeta.Defaults {
			for _, ref := range definition.Dependencies {
				if err := add(ref, d); err != nil {
					return err
				}
			}
		}
		for _, definition := range d.ColumnMeta.Generations {
			for _, ref := range definition.Dependencies {
				if err := add(ref, d); err != nil {
					return err
				}
			}
		}
		if d.SQLIndex != nil {
			for _, key := range d.SQLIndex.Keys {
				if key.Derived == nil {
					continue
				}
				for _, ref := range key.Derived.Dependencies {
					if err := add(ref, d); err != nil {
						return err
					}
				}
			}
			if predicate := d.SQLIndex.Predicate; predicate != nil {
				for _, ref := range predicate.Dependencies {
					if err := add(ref, d); err != nil {
						return err
					}
				}
			}
		}
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
	e := s.op.session.Engine()
	for _, ref := range refs {
		mode := lock.AccessShare
		for _, descriptor := range stage.Descriptors() {
			if d, ok := descriptor.(*catalog.Relation); ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, ref.Address) {
				mode = lock.ShareUpdateExclusive
				break
			}
		}
		if err := s.acquireSequence(ref, mode, true); err != nil {
			return err
		}
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err == nil {
			provider, ok := auth.Catalog.Relation(ref.Address)
			original, existed := stage.Base.Relation(ref.Address)
			if !ok && existed {
				// The provider existed when the definition was analyzed and was
				// dropped while this statement waited for it (PostgreSQL 18.6).
				err = sqlError("42704", "referenced relation was concurrently dropped")
			} else if !ok || !existed || ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || provider.RelationKind != original.RelationKind || retained[ref.Address] != nil && provider != retained[ref.Address] {
				err = fmt.Errorf("stuntgres: definition provider disappeared before publication")
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	return stage.validateLikeSources(auth.Catalog)
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
		if _, err := (scalarRenderer{validation: true, owner: d, capturedChecks: c.Dependencies, dependencies: &actual, commentSnapshot: &commentSnapshot, visibilitySnapshot: &visibilitySnapshot}).sql(context.Background(), view, c.Expression); err != nil {
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
				if !storedReferenceEpochValid(view, d, dep.Ref) || !d.HasAttribute(dep.Ref.Address.SubID) {
					return fmt.Errorf("stuntgres: invalid check column dependency")
				}
			} else {
				target, ok := view.Descriptor(address)
				if !ok || !storedReferenceEpochValid(view, d, dep.Ref) || (target.Kind() != ids.RelationObject && target.Kind() != ids.TypeObject && target.Kind() != ids.CollationObject && target.Kind() != ids.ProcedureObject && target.Kind() != ids.OperatorObject && target.Kind() != ids.NamespaceObject && target.Kind() != ids.TSDictionaryObject) || target.Kind() == ids.NamespaceObject && dep.Ref.Address.SubID != 0 {
					return fmt.Errorf("stuntgres: invalid check provider dependency")
				}
				if target.Kind() == ids.CollationObject {
					if err := validateStoredCollationDependency(view, dep.Ref, c.Expression); err != nil {
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
func validateCheckRow(d *catalog.Relation, row []sqltype.Value, eval *EvalContext) (*catalog.CheckConstraint, error) {
	if len(d.Constraints.Check) == 0 {
		return nil, nil
	}
	oldRow, oldScope, oldFrame := eval.Row, eval.Scope, eval.Frame
	eval.Row, eval.Scope, eval.Frame = row, 1, 1
	defer func() { eval.Row, eval.Scope, eval.Frame = oldRow, oldScope, oldFrame }()
	expressions, prepared := eval.preparedChecks[d]
	if !prepared {
		// Prepare every CHECK before any volatile row evaluation, matching
		// ExecRelCheck. Keep descriptors/dependencies untouched and copy the
		// executable expression slice only when a branch actually changes.
		for i := range d.Constraints.Check {
			checkEvaluation := *eval
			checkEvaluation.routineRecipe, checkEvaluation.routineGraft = true, false
			checkEvaluation.routineChecks = d.Constraints.Check[i].Dependencies
			checkEvaluation.routineReferences = nil
			checkEvaluation.routineConsumer = d
			node, changed, err := preflightCoercionsWithPolicy(&checkEvaluation, d.Constraints.Check[i].Expression, true)
			eval.preparedDomainChecks = checkEvaluation.preparedDomainChecks
			if err != nil {
				return nil, err
			}
			if err := validateExpressionJoinExecution(&checkEvaluation, node, checkEvaluation.catalog); err != nil {
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
		if eval.preparedChecks == nil {
			eval.preparedChecks = make(map[*catalog.Relation][]expr.Expr)
		}
		eval.preparedChecks[d] = expressions
	}
	// Lazy SQL-body demand happens during truth, after preflight's stack
	// context has ended. Keep this actual CHECK's original recipe for Eval;
	// the shared statement manifest cannot stand in for a stored constraint.
	oldRecipe, oldGraft := eval.routineRecipe, eval.routineGraft
	oldChecks, oldReferences, oldConsumer := eval.routineChecks, eval.routineReferences, eval.routineConsumer
	defer func() {
		eval.routineRecipe, eval.routineGraft = oldRecipe, oldGraft
		eval.routineChecks, eval.routineReferences, eval.routineConsumer = oldChecks, oldReferences, oldConsumer
	}()
	for i := range d.Constraints.Check {
		c := &d.Constraints.Check[i]
		eval.routineRecipe, eval.routineGraft = true, false
		eval.routineChecks, eval.routineReferences, eval.routineConsumer = c.Dependencies, nil, d
		expression := c.Expression
		if expressions != nil {
			expression = expressions[i]
		}
		value, err := expression.Eval(eval)
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

type checkDependencyEdge struct {
	relation *catalog.Relation
	check    catalog.CheckConstraint
	target   ids.ObjectAddress
}

// Provider references join the incumbent dependency stage. Owner-local column
// dependencies are AUTO and are handled by DROP COLUMN's stable-slot migration.
func stageCheckDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) ([]checkDependencyEdge, error) {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).checkDependents(targets, mode)
}

func (provider *dropProvider) checkDependents(targets []ids.ObjectAddress, mode parser.DropMode) ([]checkDependencyEdge, error) {
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
