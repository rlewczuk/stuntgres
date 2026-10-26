package stored

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func (m *ViewQueryRemapper) retag(node expr.Expr, typ func(*sqltype.Descriptor) *sqltype.Descriptor) (expr.Expr, bool, error) {
	procedure := m.Procedure
	if procedure == nil {
		// Structural type-only mapping preserves existing authority; it does
		// not acquire destination principal or catalog provenance.
		procedure = func(original *catalog.Procedure) (*catalog.Procedure, error) { return original, nil }
	}
	return mapExpressionWithProcedure(m.Catalog, nil, node, typ, nil, nil, false, nil, procedure, m.Collation)
}

func (m *ViewQueryRemapper) procedure(original *catalog.Procedure) (*catalog.Procedure, error) {
	if original == nil || m.Procedure == nil {
		return original, nil
	}
	return m.Procedure(original)
}

// usage maps a usage's type and collation to their remapped generations.
func (m *ViewQueryRemapper) usage(old sqltype.Usage) (sqltype.Usage, error) {
	old.Type = m.Type(old.Type)
	if m.Collation == nil {
		return old, nil
	}
	var err error
	old.Collation, err = m.Collation(old.Collation)
	return old, err
}

// provider replaces *slot with the remapped provider. On failure it keeps the
// slot unchanged and returns the error.
func (m *ViewQueryRemapper) provider(slot *functions.AggregateProvider, provider functions.RemappableProvider, argument expr.Expr) error {
	remap := functions.ProviderRemap{Type: m.Type, Usage: m.usage}
	remap.Routine = func(reference expr.RoutineReference) (expr.RoutineReference, error) {
		old, ok := reference.(*catalog.Procedure)
		if !ok || old == nil {
			return nil, fmt.Errorf("stuntgres: invalid transition routine reference")
		}
		return m.procedure(old)
	}
	remap.Expression = m.transitionExpression
	if argument != nil {
		remap.Argument = argument.ResultUsage()
	}
	next, err := provider.Remap(remap)
	if err != nil {
		return err
	}
	if transition, ok := next.(*exec.TransitionAggregate); ok {
		if !m.referencesOnly {
			if err := validateTransitionFrame(m.Catalog, transition); err != nil {
				return err
			}
		} else if m.Catalog.State() != nil {
			if err := routine.ValidateAggregateDefinition(m.Catalog, transition.Aggregate); err != nil {
				return err
			}
			for _, selected := range []*catalog.Procedure{transition.Support, transition.InverseSupport, transition.Final} {
				if selected == nil {
					continue
				}
				current, found := m.Catalog.Procedure(selected.Address())
				if !found || !exec.SameRoutineDeclaration(current, selected) {
					return fmt.Errorf("stuntgres: mismatched aggregate support reference")
				}
			}
		}
	}
	*slot = next
	return nil
}

// Transition support expressions own a separate, closed value frame. Its typed
// parameters are not the enclosing query/routine's external parameter namespace.
func (m *ViewQueryRemapper) transitionExpression(old expr.Expr, source, destination []sqltype.Usage) (expr.Expr, error) {
	if len(source) != len(destination) {
		return nil, fmt.Errorf("stuntgres: mismatched transition formal namespace")
	}
	for i := range source {
		if source[i].Type == nil || !source[i].Type.Concrete() || !source[i].Valid() || destination[i].Type == nil || !destination[i].Type.Concrete() || !destination[i].Valid() {
			return nil, fmt.Errorf("stuntgres: invalid transition formal usage")
		}
	}
	if err := validateRoutineScalar(old, source, catalog.Read{}); err != nil {
		return nil, err
	}
	next, err := remapScalar(old, m.Ref, m.Ref, nil, m.Nested, len(source), m.addressMapper)
	if err != nil {
		return nil, err
	}
	if !m.referencesOnly {
		next, _, err = m.retag(next, m.Type)
		if err != nil {
			return nil, err
		}
	}
	if err := validateRoutineScalar(next, destination, catalog.Read{}); err != nil {
		return nil, err
	}
	return next, nil
}

func validateTransitionFrame(read catalog.Read, p *exec.TransitionAggregate) error {
	if err := routine.ValidateAggregateDefinition(read, p.Aggregate); err != nil {
		return err
	}
	if err := p.ValidateSelection(); err != nil {
		return err
	}
	stateID, supportSlot, finalSlot := p.Aggregate.Aggregate.Definition.TransitionType, 0, 1
	if p.Moving {
		stateID, supportSlot, finalSlot = p.Aggregate.Aggregate.Definition.MovingType, 5, 7
	}
	invalid := func() error { return fmt.Errorf("stuntgres: mismatched transition frame specialization") }
	inputs, err := routine.AggregateSignature(read, p.Aggregate.Signature)
	if err != nil || len(p.Inputs) == 0 || p.Inputs[0] != p.StateUsage || p.StateUsage.Type == nil || (!p.StateUsage.Valid() && !(p.ContextSupport != nil && p.StateUsage.Type.ID() == 2281 && p.StateUsage == p.StateUsage.Type.Usage())) || p.StateUsage.Type.ID() != sqltype.ID(stateID) || p.Result.Type == nil || !p.Result.Valid() || p.Result.Type.ID() != sqltype.ID(p.Aggregate.Result) {
		return invalid()
	}
	stateType, stateFound := read.LookupType(sqltype.ID(stateID))
	resultType, resultFound := read.LookupType(sqltype.ID(p.Aggregate.Result))
	if !stateFound || !resultFound || p.StateUsage.Type != stateType || p.Result.Type != resultType || p.Support == nil || p.Support.Address().ObjectID != p.Aggregate.Aggregate.Definition.Functions[supportSlot] {
		return invalid()
	}
	currentSupport, found := read.Procedure(p.Support.Address())
	if !found || !exec.SameRoutineDeclaration(currentSupport, p.Support) {
		return invalid()
	}
	directCount := int(p.Aggregate.Aggregate.Definition.DirectArgs)
	if directCount > len(inputs) {
		return invalid()
	}
	declaredRows := inputs[directCount:]
	if p.Aggregate.Aggregate.Variadic && directCount == len(inputs) {
		declaredRows = inputs[len(inputs)-1:]
	}
	matches := func(declared, actual []sqltype.Usage) bool {
		variadic := len(declared) > 0 && declared[len(declared)-1].Type.ID() == 2276
		fixed := len(declared)
		if variadic {
			fixed--
		}
		if len(actual) < fixed || !variadic && len(actual) != fixed {
			return false
		}
		for i := range fixed {
			if actual[i].Type != declared[i].Type {
				return false
			}
		}
		for _, usage := range actual {
			if usage.Type == nil || !usage.Type.Concrete() || !usage.Valid() {
				return false
			}
		}
		return true
	}
	if !matches(inputs[:directCount], p.DirectInputs) || !matches(declaredRows, p.Inputs[1:]) {
		return invalid()
	}
	formals, result, err := routine.AggregateSupportTypes(read, nil, p.Support, p.Aggregate.Aggregate, p.Inputs)
	if err != nil || result.Type != p.StateUsage.Type || len(formals) != len(p.Formals) {
		return invalid()
	}
	for i, formal := range formals {
		if formal != p.Formals[i] {
			return invalid()
		}
	}
	if p.InverseSupport != nil {
		current, found := read.Procedure(p.InverseSupport.Address())
		if !found || !exec.SameRoutineDeclaration(current, p.InverseSupport) {
			return invalid()
		}
		formals, result, err := routine.AggregateSupportTypes(read, nil, p.InverseSupport, p.Aggregate.Aggregate, p.Inputs)
		if err != nil || result.Type != p.StateUsage.Type || len(formals) != len(p.InverseFormals) {
			return invalid()
		}
		for i, formal := range formals {
			if formal != p.InverseFormals[i] {
				return invalid()
			}
		}
	}
	if p.Final != nil {
		if p.Final.Address().ObjectID != p.Aggregate.Aggregate.Definition.Functions[finalSlot] || p.ContextSupport == nil && len(p.FinalConversions) != len(p.FinalFormals) {
			return invalid()
		}
		currentFinal, found := read.Procedure(p.Final.Address())
		if !found || !exec.SameRoutineDeclaration(currentFinal, p.Final) {
			return invalid()
		}
		finalFormals, finalResult, err := routine.AggregateSupportTypes(read, nil, p.Final, p.Aggregate.Aggregate, p.FinalInputs)
		if err != nil || finalResult.Type != p.Result.Type || len(finalFormals) != len(p.FinalFormals) {
			return invalid()
		}
		for i, formal := range finalFormals {
			if formal != p.FinalFormals[i] {
				return invalid()
			}
			if p.ContextSupport != nil {
				continue
			}
			expected, err := catalog.AggregateConversion(p.Aggregate.Aggregate.BinaryRecipe, p.FinalInputs[i], formal)
			if err != nil || !p.FinalConversions[i].Equal(expected) {
				return invalid()
			}
		}
	}
	return nil
}

// Ordinary descriptor evolution reowns only consumers of changed type or
// callable carriers; replacement additionally remaps imported references.
func ExpressionDescriptorsChanged(view catalog.Read, d *catalog.Relation, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	typ = analyze.RecordTypeMapper(typ)
	changed := false
	expression := func(node expr.Expr) {
		analyze.WalkQueryArgumentExpression(node, func(child expr.Expr) {
			old := child.ResultUsage().Type
			changed = changed || old != typ(old)
			if earth, ok := child.(plan.InstalledEarthExpr); ok {
				changed = changed || earth.Plan.Earth() != typ(earth.Plan.Earth())
			}
			if view.State() != nil {
				captured, err := exec.ExpressionRoutineDescriptor(child)
				if err != nil {
					changed = true
				} else if captured != nil {
					current, _ := view.Procedure(captured.Address())
					changed = changed || !exec.SameRoutineDeclaration(current, captured)
				}
			}
		})
	}
	for _, definition := range d.ColumnMeta.Defaults {
		expression(definition.Expression)
	}
	for _, definition := range d.ColumnMeta.Generations {
		expression(definition.Expression)
		expression(definition.Planned)
	}
	for _, constraint := range d.Constraints.Check {
		expression(constraint.Expression)
	}
	for _, policy := range d.Security.Policies {
		for _, condition := range []*Policy{PolicyOf(policy.Using), PolicyOf(policy.Check)} {
			if condition != nil {
				expression(condition.expression)
				changed = changed || analyze.QueryDescriptorsChanged(condition.presentation.query.Root, typ) || analyze.QueryRoutineDeclarationsChanged(view, condition.presentation.query.Root)
				for _, usage := range condition.presentation.castUsages {
					changed = changed || usage.Type != typ(usage.Type)
				}
				for _, captured := range condition.presentation.composites {
					changed = changed || captured.Base.Type != typ(captured.Base.Type)
				}
			}
		}
	}
	if d.SQLIndex != nil {
		for _, key := range d.SQLIndex.Keys {
			if key.Derived != nil {
				expression(key.Derived.Definition)
				expression(key.Derived.Execution)
			}
		}
		if predicate := d.SQLIndex.Predicate; predicate != nil {
			expression(predicate.Definition)
			expression(predicate.Execution)
		}
	}
	if d.Partition != nil {
		for _, key := range d.Partition.Keys {
			changed = changed || key.Usage.Type != typ(key.Usage.Type)
			if key.Derived != nil {
				expression(key.Derived.Definition)
				expression(key.Derived.Execution)
			}
		}
	}
	if d.Membership != nil {
		for _, tuple := range [][]catalog.PartitionDatum{d.Membership.Lower, d.Membership.Upper, d.Membership.Values} {
			for _, datum := range tuple {
				if datum.Kind == 0 {
					changed = changed || datum.Value.Type() != typ(datum.Value.Type())
				}
			}
		}
	}
	if d.View != nil {
		for _, usage := range ViewOf(d).castUsages {
			changed = changed || usage.Type != typ(usage.Type)
		}
		for _, captured := range ViewOf(d).composites {
			changed = changed || captured.Base.Type != typ(captured.Base.Type)
		}
		for _, literal := range ViewOf(d).literals {
			expression(literal)
		}
		changed = changed || analyze.QueryDescriptorsChanged(ViewOf(d).query.Root, typ) || analyze.QueryRoutineDeclarationsChanged(view, ViewOf(d).query.Root)
	}
	return changed
}
func (m *ViewQueryRemapper) CTE(old *plan.CTEDefinition) (*plan.CTEDefinition, error) {
	if mapped := m.ctes[old]; mapped != nil {
		return mapped, nil
	}
	if m.ctes == nil {
		m.ctes = make(map[*plan.CTEDefinition]*plan.CTEDefinition)
	}
	copy := *old
	m.ctes[old] = &copy
	copy.Schema = append(plan.QuerySchema(nil), old.Schema...)
	for i := range copy.Schema {
		copy.Schema[i].Usage.Type = m.Type(copy.Schema[i].Usage.Type)
		if m.Collation != nil {
			collation, err := m.Collation(copy.Schema[i].Usage.Collation)
			if err != nil {
				return nil, err
			}
			copy.Schema[i].Usage.Collation = collation
		}
	}
	if old.Search != nil {
		var err error
		copy.Search, err = m.search(old.Search)
		if err != nil {
			return nil, err
		}
	}
	if old.Cycle != nil {
		var err error
		copy.Cycle, err = m.cycle(old.Cycle)
		if err != nil {
			return nil, err
		}
	}
	if old.Query != nil {
		query := *old.Query
		root, err := m.Nested(query.Root)
		if err != nil {
			return nil, err
		}
		query.Root = root
		copy.Query = &query
	}
	if old.Mutation != nil && m.Mutation != nil {
		body, err := m.Mutation(old)
		if err != nil {
			return nil, err
		}
		copy.Mutation = &body
	}
	return &copy, nil
}

func (m *ViewQueryRemapper) cycle(old *plan.CycleRecipe) (*plan.CycleRecipe, error) {
	copy := *old
	copy.Record, copy.Path = m.Type(old.Record), m.Type(old.Path)
	if !sqltype.OccurrenceRecordArray(copy.Path) || copy.Path.Element() != copy.Record {
		return nil, ids.ErrCatalogReplaced
	}
	expression := func(old expr.Expr) (expr.Expr, error) {
		next, err := remapScalar(old, m.Ref, m.Ref, nil, m.Nested, m.routineFormals, m.addressMapper)
		if err != nil {
			return nil, err
		}
		if !m.referencesOnly {
			next, _, err = m.retag(next, m.Type)
		}
		return next, err
	}
	var err error
	copy.Mark, err = expression(old.Mark)
	if err != nil {
		return nil, err
	}
	copy.Fallback, err = expression(old.Fallback)
	if err != nil {
		return nil, err
	}
	if m.referencesOnly {
		return &copy, nil
	}
	for i, usage := range old.TypedConstants {
		if usage.Type == nil {
			continue
		}
		usage.Type = m.Type(usage.Type)
		if m.Collation != nil {
			usage.Collation, err = m.Collation(usage.Collation)
			if err != nil {
				return nil, err
			}
		}
		copy.TypedConstants[i] = usage
	}
	copy.Grouping = make([]sqltype.Grouping, len(old.Grouping))
	for i := range copy.Grouping {
		copy.Grouping[i], err = sqltype.BindGrouping(copy.Record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	boolean, ok := m.Catalog.LookupType(sqltype.Bool)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	usage := copy.Mark.ResultUsage()
	copy.Inequality, err = sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, usage.Type, usage.Type, boolean, usage.Collation, copy.Mark.CollationIndeterminate())
	if err != nil {
		return nil, err
	}
	return &copy, nil
}

func (m *ViewQueryRemapper) search(old *plan.SearchRecipe) (*plan.SearchRecipe, error) {
	copy := *old
	copy.Record, copy.Result, copy.Depth = m.Type(old.Record), m.Type(old.Result), m.Type(old.Depth)
	if copy.Record == nil || copy.Record.ID() != 2249 || !copy.Record.IsComposite() ||
		old.Syntax.Breadth && copy.Result != copy.Record ||
		!old.Syntax.Breadth && (!sqltype.OccurrenceRecordArray(copy.Result) || copy.Result.Element() != copy.Record) {
		return nil, ids.ErrCatalogReplaced
	}
	copy.Grouping = make([]sqltype.Grouping, len(old.Grouping))
	for i := range copy.Grouping {
		var err error
		copy.Grouping[i], err = sqltype.BindGrouping(copy.Record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	return &copy, nil
}

// remapFoldOwner retargets the owner of a node's retained-fold origin, which
// the node carries as an opaque annotation.
func remapFoldOwner(node expr.Expr, ref func(ids.ObjectRef) ids.ObjectRef) expr.Expr {
	annotated, ok := node.(expr.Annotated)
	if !ok {
		return node
	}
	origin, _ := annotated.Annotation().(exec.FoldOrigin)
	if origin.Owner == (ids.ObjectRef{}) {
		return node
	}
	origin.Owner = ref(origin.Owner)
	return annotated.WithAnnotation(origin)
}

func RemapLikeExpression(in *analyze.LikeInput, node expr.Expr, destination *catalog.Relation, authority catalog.Read) (expr.Expr, error) {
	if in.RowRef == nil {
		in.RowRef, in.Slots = in.RowMapper(destination, authority)
	}
	return RemapScalar(node, nil, in.RowRef, in.Slots, nil)
}
