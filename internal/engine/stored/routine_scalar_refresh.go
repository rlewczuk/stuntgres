package stored

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// RefreshStoredScalarRoutine is a publication operation, not historical remapping.
// The DDL owner supplies its immutable pre-mutation read and checked private
// successor. Returned descriptors must join that same stage or all be discarded.
// No executable value, row, physical index entry or retained source is changed.
func RefreshStoredScalarRoutine(source, target catalog.Read, before, after *catalog.Procedure, revision func() (ids.DefinitionRevision, error)) (*ScalarRoutinePublication, error) {
	if source.State() == nil || target.State() == nil || source.Owner() == nil || source.Owner() != target.Owner() ||
		source.Owner().Domain == nil || source.Owner().Principals == nil || source.Epoch() != target.Epoch() ||
		before == nil || after == nil || before == after || revision == nil {
		return nil, ids.ErrCatalogReplaced
	}
	if source.Get(before.Address()) != before || target.Get(after.Address()) != after ||
		before.Address() != after.Address() || before.Namespace() != after.Namespace() || before.Name() != after.Name() ||
		before.Signature != after.Signature || before.RoutineKind != catalog.FunctionRoutine || after.RoutineKind != before.RoutineKind ||
		before.Result != after.Result || before.Owner != after.Owner || before.Extension != after.Extension || !authorization.EqualACL(before.ACL, after.ACL) ||
		after.Revision() <= before.Revision() {
		return nil, fmt.Errorf("stuntgres: invalid routine replacement publication")
	}
	xml2Configuration := extension.StockXML2.SupportsRoutineConfiguration(before) && extension.StockXML2.SupportsRoutineConfiguration(after)
	if xml2Configuration {
		// Only this checked configuration transition may update installed
		// carriers. Ordinary remap and declaration comparison stay strict.
		expected := *before
		expected.Configuration = after.Configuration
		if before.Configuration == after.Configuration || !exec.SameRoutineDeclaration(after, &expected) {
			return nil, fmt.Errorf("stuntgres: invalid XML2 configuration publication")
		}
	} else if before.RoutineDefinition() == nil || after.RoutineDefinition() == nil {
		return nil, fmt.Errorf("stuntgres: invalid routine replacement publication")
	}
	if err := (ProcedureProvider{}).Validate(source, before); err != nil {
		return nil, err
	}
	if err := (ProcedureProvider{}).Validate(target, after); err != nil {
		return nil, err
	}
	op := scalarRoutineRefresh{source: source, target: target, before: before, after: after, xml2Configuration: xml2Configuration}
	var changed []catalog.Descriptor
	for _, descriptor := range source.State().Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		next, err := op.relation(relation)
		if err != nil {
			return nil, err
		}
		if next == relation {
			continue
		}
		if target.Get(relation.Address()) != relation {
			return nil, ids.ErrCatalogReplaced
		}
		next.ObjectRevision, err = revision()
		if err != nil {
			return nil, err
		}
		changed = append(changed, next)
	}
	// Validate the complete private result before returning any installable set.
	// The source and caller's target remain untouched on every failure.
	completed := target
	if len(changed) != 0 {
		state := catalog.CloneState(target.State())
		for _, descriptor := range changed {
			state.SetDescriptor(descriptor.Address(), descriptor)
		}
		view := target.WithState(state)
		completed = view
		for _, descriptor := range changed {
			provider := view.Owner().Provider(descriptor.Address().ClassID)
			if provider == nil {
				return nil, ids.ErrCatalogReplaced
			}
			if err := provider.Validate(view, descriptor); err != nil {
				return nil, err
			}
		}
	}
	publication := &ScalarRoutinePublication{source: source, completed: completed, descriptors: changed, before: before, after: after}
	for _, descriptor := range changed {
		relation := descriptor.(*catalog.Relation)
		relation.ScalarPublication = publication
	}
	return publication, nil
}

type scalarRoutineRefresh struct {
	source, target catalog.Read
	before, after  *catalog.Procedure
	xml2Configuration bool
}

func (r *scalarRoutineRefresh) expression(node expr.Expr, refs []ids.ObjectRef, dependencies []catalog.ExpressionDependency, indexDefinition expr.Expr) (expr.Expr, bool, error) {
	indexExecution := indexDefinition != nil
	// Only an actual index execution recipe may contain a cooked inline call.
	// Its definition remains strict stored authority. Authenticate every other
	// source field and original kernel through a local noninline view, then
	// publish conservative execution: the index owner's definition-first
	// PrepareIndexRecipe performs live admission and recomputes the route.
	xml2Source := func(call plan.InstalledXML2Expr) (plan.InstalledXML2Expr, error) {
		if call.Inline {
			if !indexExecution || call.Procedure == nil || call.Procedure.Configuration != nil ||
				!extension.StockXML2.SupportsRoutineConfiguration(call.Procedure) {
				return call, ids.ErrCatalogReplaced
			}
			call.Inline = false
		}
		return call, ValidateInstalledXML2(call, r.source)
	}
	needed := false
	executionXML2 := false
	var invalid error
	plan.WalkExpression(node, func(node expr.Expr) {
		if invalid != nil {
			return
		}
		var ref ids.ObjectRef
		var captured *catalog.Procedure
		switch call := node.(type) {
		case plan.SQLRoutineExpr:
			if call.Ref.Address != r.before.Address() {
				return
			}
			ref, captured = call.Ref, call.Procedure
			invalid = call.ValidateStoredProvider()
			if invalid == nil {
				_, invalid = currentRoutineCall(r.source, call)
			}
		case plan.InstalledXML2Expr:
			if call.Ref.Address != r.before.Address() && !(r.xml2Configuration && indexExecution) {
				return
			}
			executionXML2 = true
			ref, captured = call.Ref, call.Procedure
			if !r.xml2Configuration {
				invalid = ids.ErrCatalogReplaced
				return
			}
			_, invalid = xml2Source(call)
			if invalid == nil && call.Kernel != nil {
				invalid = (ScalarRenderer{CapturedManifest: refs, CapturedChecks: dependencies}).recordStoredRoutineDescriptor(r.source, call.Kernel)
			}
			if invalid == nil && indexExecution && call.Kernel != nil {
				inner := call.Ref
				inner.Address = call.Kernel.Address()
				if !slices.Contains(refs, inner) {
					invalid = fmt.Errorf("stuntgres: missing original index XML2 kernel capture")
				}
			}
		default:
			return
		}
		selected := ref.Address == r.before.Address()
		needed = needed || selected
		if invalid != nil {
			return
		}
		if selected && !exec.SameRoutineDeclaration(r.before, captured) {
			invalid = ids.ErrCatalogReplaced
			return
		}
		found := false
		for _, original := range refs {
			found = found || original == ref
		}
		for _, dep := range dependencies {
			if dep.Ref == ref {
				if dep.Revision != captured.Revision() || dep.Purpose != catalog.DefinitionDependency {
					invalid = fmt.Errorf("stuntgres: mismatched routine replacement dependency")
				}
				found = true
			}
		}
		if !found {
			invalid = fmt.Errorf("stuntgres: missing routine replacement dependency")
		}
	})
	if invalid != nil {
		return nil, false, invalid
	}
	validatePair := indexExecution && r.xml2Configuration && executionXML2
	if validatePair && !needed {
		// A selected constant may have folded out of execution without
		// removing the need to authenticate its remaining sibling calls.
		validatePair = false
		plan.WalkExpression(indexDefinition, func(node expr.Expr) {
			if call, ok := node.(plan.InstalledXML2Expr); ok && call.Ref.Address == r.before.Address() {
				validatePair = true
			}
		})
	}
	if validatePair {
		// Revision-insensitive current declaration validation is not original
		// occurrence authority. Pair each execution with its own frozen
		// Definition, never with a current or canonical kernel substitute.
		type occurrence struct {
			ref ids.ObjectRef
			source expr.FoldSourceOccurrence
			ordinal uint32
		}
		type capture struct {
			call plan.InstalledXML2Expr
			remaining int
		}
		key := func(call plan.InstalledXML2Expr) occurrence {
			return occurrence{call.Ref, call.Source, call.Occurrence}
		}
		sameOriginal := func(a, b *catalog.Procedure) bool {
			if a == nil || b == nil {
				return a == b
			}
			return a.Revision() != 0 && a.Revision() == b.Revision() && exec.SameRoutineDeclaration(a, b)
		}
		var originals map[occurrence]capture
		plan.WalkExpression(indexDefinition, func(node expr.Expr) {
			call, ok := node.(plan.InstalledXML2Expr)
			if !ok || invalid != nil {
				return
			}
			if invalid = ValidateInstalledXML2(call, r.source); invalid != nil {
				return
			}
			if originals == nil {
				originals = make(map[occurrence]capture)
			}
			identity := key(call)
			original, exists := originals[identity]
			if exists && (!sameOriginal(original.call.Procedure, call.Procedure) || !sameOriginal(original.call.Kernel, call.Kernel)) {
				invalid = fmt.Errorf("stuntgres: conflicting original index XML2 occurrence")
				return
			}
			original.call = call
			original.remaining++
			originals[identity] = original
		})
		plan.WalkExpression(node, func(node expr.Expr) {
			call, ok := node.(plan.InstalledXML2Expr)
			if !ok || invalid != nil {
				return
			}
			identity := key(call)
			original, exists := originals[identity]
			if !exists || original.remaining == 0 ||
				!sameOriginal(original.call.Procedure, call.Procedure) || !sameOriginal(original.call.Kernel, call.Kernel) {
				invalid = fmt.Errorf("stuntgres: mismatched original index XML2 occurrence")
				return
			}
			original.remaining--
			originals[identity] = original
		})
		if invalid != nil {
			return nil, false, invalid
		}
	}
	if !needed {
		return node, false, nil
	}
	identity := func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }
	original := func(p *catalog.Procedure) (*catalog.Procedure, error) {
		if _, err := exec.ValidateStoredRoutineDescriptor(r.source, p); err != nil {
			return nil, err
		}
		if _, err := exec.ValidateStoredRoutineDescriptor(r.target, p); err != nil {
			return nil, err
		}
		return p, nil
	}
	var mapNode func(expr.Expr) (expr.Expr, bool, error)
	leaf := func(node expr.Expr) (expr.Expr, bool, error) {
		if call, ok := node.(plan.InstalledXML2Expr); ok && (call.Ref.Address == r.before.Address() || r.xml2Configuration && indexExecution) {
			if !r.xml2Configuration {
				return nil, false, ids.ErrCatalogReplaced
			}
			var err error
			call, err = xml2Source(call)
			if err != nil {
				return nil, false, err
			}
			var args []expr.Expr
			for i, arg := range call.Args {
				next, changed, err := mapNode(arg)
				if err != nil {
					return nil, false, err
				}
				if changed {
					if args == nil {
						args = slices.Clone(call.Args)
					}
					args[i] = next
				}
			}
			if args == nil {
				args = call.Args
			}
			properties := RewrittenIntrinsicProperties(call)
			for _, arg := range args {
				properties = expr.MergeProperties(properties, arg.Properties())
			}
			if call.Ref.Address == r.before.Address() {
				call.Procedure, call.RoutineReference = r.after, r.after
			}
			call.Args = args
			call.Property = properties
			next, err := call.Rebuild(call.Info, args)
			if err != nil {
				return nil, false, err
			}
			if err := ValidateInstalledXML2(next.(plan.InstalledXML2Expr), r.target); err != nil {
				return nil, false, err
			}
			return next, true, nil
		}
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok || call.Ref.Address != r.before.Address() {
			return node, false, nil
		}
		if err := call.ValidateStoredProvider(); err != nil {
			return nil, false, err
		}
		if _, err := currentRoutineCall(r.source, call); err != nil {
			return nil, false, err
		}
		if !exec.SameRoutineDeclaration(r.before, call.Procedure) || !call.Signature.ValidFor(r.after.RoutineDefinition()) {
			return nil, false, ids.ErrCatalogReplaced
		}
		var args []expr.Expr
		for i, arg := range call.Args {
			next, changed, err := mapNode(arg)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = slices.Clone(call.Args)
				}
				args[i] = next
			}
		}
		if args == nil {
			args = call.Args
		}
		call.Procedure, call.RoutineReference = r.after, r.after
		call.Property = call.GroupProperties(args)
		next, err := call.Rebuild(call.Info, args)
		if err != nil {
			return nil, false, err
		}
		call = next.(plan.SQLRoutineExpr)
		if err := call.ValidateStoredProvider(); err != nil {
			return nil, false, err
		}
		if _, err := currentRoutineCall(r.target, call); err != nil {
			return nil, false, err
		}
		return call, true, nil
	}
	mapNode = func(node expr.Expr) (expr.Expr, bool, error) {
		return mapExpressionWithProcedure(r.target, nil, node, identity, nil, nil, false, leaf, original, nil)
	}
	return mapNode(node)
}

// Only the checked XML2 configuration publication uses this frozen-query
// transition. Source captures authenticate both actual declaration lanes before
// the selected wrapper stamp changes; its original kernel is never replaced.
func (r *scalarRoutineRefresh) xml2View(original *View) (*View, error) {
	if err := validateRoutineCapture(RuleAction{presentation: original}, original.dependencies, r.source); err != nil {
		return nil, err
	}
	procedure := func(captured *catalog.Procedure) (*catalog.Procedure, error) {
		if _, err := exec.ValidateStoredRoutineDescriptor(r.source, captured); err != nil {
			return nil, err
		}
		if captured.Address() == r.before.Address() {
			if !exec.SameRoutineDeclaration(r.before, captured) {
				return nil, ids.ErrCatalogReplaced
			}
			return r.after, nil
		}
		if _, err := exec.ValidateStoredRoutineDescriptor(r.target, captured); err != nil {
			return nil, err
		}
		return captured, nil
	}
	refresh := func(dependency catalog.ExpressionDependency) (catalog.ExpressionDependency, error) {
		if dependency.Ref.Address != r.before.Address() {
			return dependency, nil
		}
		switch dependency.Purpose {
		case catalog.DefinitionDependency, catalog.AggregateSupportDependency:
			if dependency.Ref.Domain() != r.source.Domain() || dependency.Ref.Epoch != r.source.Epoch() || dependency.Revision == 0 {
				return dependency, ids.ErrCatalogReplaced
			}
			dependency.Revision = r.after.Revision()
		}
		return dependency, nil
	}
	mapped, err := remapView(r.target, original, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref },
		func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, procedure, refresh)
	if err != nil {
		return nil, err
	}
	referencesCopied := false
	for ref, descriptor := range original.references {
		if descriptor.Address() == r.before.Address() {
			if !referencesCopied {
				mapped.references = maps.Clone(original.references)
				referencesCopied = true
			}
			mapped.references[ref] = r.after
		}
	}
	return mapped, nil
}

// A manifest is a discovery hint, never proof that an executable occurrence is
// absent. A missing shared SFUNC/COMBINE capture must still reach validation.
func (r *scalarRoutineRefresh) xml2ViewUses(original *View) bool {
	for _, dependency := range original.dependencies {
		if dependency.Ref.Address == r.before.Address() {
			switch dependency.Purpose {
			case catalog.DefinitionDependency, catalog.AggregateSupportDependency:
				return true
			}
		}
	}
	needed := false
	scalar := func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if call, ok := node.(plan.InstalledXML2Expr); ok && call.Ref.Address == r.before.Address() {
				needed = true
			}
		})
	}
	aggregate := func(provider *exec.TransitionAggregate) {
		if provider == nil {
			return
		}
		for _, frame := range [2]*exec.TransitionAggregate{provider, provider.CombinePlan} {
			if frame == nil {
				continue
			}
			for _, support := range [3]*catalog.Procedure{frame.Support, frame.InverseSupport, frame.Final} {
				if support != nil && support.Address() == r.before.Address() {
					needed = true
				}
			}
			for _, call := range [3]expr.Expr{frame.Call, frame.InverseCall, frame.FinalCall} {
				if call != nil {
					scalar(call)
				}
			}
		}
	}
	if original.query != nil {
		plan.VisitCTEQuery(original.query.Root, func(node plan.Node) {
			switch node := node.(type) {
			case *plan.AggregateNode:
				for _, call := range node.Calls {
					if provider, ok := call.Provider.(*exec.TransitionAggregate); ok {
						aggregate(provider)
					}
				}
			case *plan.WindowNode:
				for _, call := range node.Calls {
					if provider, ok := call.Provider.(*exec.TransitionAggregate); ok {
						aggregate(provider)
					}
				}
			}
		}, scalar, true)
	}
	return needed
}

func (r *scalarRoutineRefresh) relation(old *catalog.Relation) (*catalog.Relation, error) {
	var next *catalog.Relation
	copyRelation := func() error {
		if next != nil {
			return nil
		}
		provider := r.source.Owner().Provider(old.Address().ClassID)
		if provider == nil {
			return ids.ErrCatalogReplaced
		}
		if err := provider.Validate(r.source, old); err != nil {
			return err
		}
		copy := *old
		next = &copy
		return nil
	}
	defaultsCopied := false
	for attribute, definition := range old.ColumnMeta.Defaults {
		node, changed, err := r.expression(definition.Expression, definition.Dependencies, nil, nil)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if err := copyRelation(); err != nil {
			return nil, err
		}
		if !defaultsCopied {
			defaults := make(map[int32]catalog.ColumnDefault, len(old.ColumnMeta.Defaults))
			for key, value := range next.ColumnMeta.Defaults {
				defaults[key] = value
			}
			next.ColumnMeta.Defaults = defaults
			defaultsCopied = true
		}
		definition.Expression = node
		next.ColumnMeta.Defaults[attribute] = definition
	}
	checksCopied := false
	for i, check := range old.Constraints.Check {
		node, changed, err := r.expression(check.Expression, nil, check.Dependencies, nil)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		if err := copyRelation(); err != nil {
			return nil, err
		}
		if !checksCopied {
			next.Constraints.Check = slices.Clone(old.Constraints.Check)
			checksCopied = true
		}
		check.Expression = node
		check.Dependencies = slices.Clone(check.Dependencies)
		for j := range check.Dependencies {
			if check.Dependencies[j].Ref.Address == r.before.Address() {
				check.Dependencies[j].Revision = r.after.Revision()
			}
		}
		next.Constraints.Check[i] = check
	}
	if old.SQLIndex != nil {
		var index *catalog.IndexDefinition
		copyIndex := func() error {
			if index != nil {
				return nil
			}
			if err := copyRelation(); err != nil {
				return err
			}
			copy := *old.SQLIndex
			index = &copy
			next.SQLIndex = index
			return nil
		}
		keysCopied := false
		for i, key := range old.SQLIndex.Keys {
			if key.Derived == nil {
				continue
			}
			definition, a, err := r.expression(key.Derived.Definition, key.Derived.Dependencies, nil, nil)
			if err != nil {
				return nil, err
			}
			execution, b, err := r.expression(key.Derived.Execution, key.Derived.Dependencies, nil, key.Derived.Definition)
			if err != nil {
				return nil, err
			}
			if r.xml2Configuration && b && !a {
				return nil, ids.ErrCatalogReplaced
			}
			if r.xml2Configuration && a {
				// The whole original execution has been authenticated above.
				// Reuse the updated strict definition rather than retaining any
				// sibling's cooked route; tuple preparation selects it afresh.
				execution = definition
			}
			if !a && !b {
				continue
			}
			if err := copyIndex(); err != nil {
				return nil, err
			}
			if !keysCopied {
				index.Keys = slices.Clone(old.SQLIndex.Keys)
				keysCopied = true
			}
			derived := *key.Derived
			derived.Definition, derived.Execution = definition, execution
			index.Keys[i].Derived = &derived
		}
		if predicate := old.SQLIndex.Predicate; predicate != nil {
			definition, a, err := r.expression(predicate.Definition, predicate.Dependencies, nil, nil)
			if err != nil {
				return nil, err
			}
			execution, b, err := r.expression(predicate.Execution, predicate.Dependencies, nil, predicate.Definition)
			if err != nil {
				return nil, err
			}
			if r.xml2Configuration && b && !a {
				return nil, ids.ErrCatalogReplaced
			}
			if r.xml2Configuration && a {
				execution = definition
			}
			if a || b {
				if err := copyIndex(); err != nil {
					return nil, err
				}
				copy := *predicate
				copy.Definition, copy.Execution = definition, execution
				index.Predicate = &copy
			}
		}
	}
	if original := ViewOf(old); r.xml2Configuration && original != nil {
		if r.xml2ViewUses(original) {
			if err := copyRelation(); err != nil {
				return nil, err
			}
			mapped, err := r.xml2View(original)
			if err != nil {
				return nil, err
			}
			next.View = ViewPayload(mapped)
		}
	}
	if next == nil {
		return old, nil
	}
	return next, nil
}
