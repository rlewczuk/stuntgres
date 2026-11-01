package exec

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// DefaultPreparationPolicy selects an existing preparation lane, not an
// evaluator or a caller-provided transformation.
type DefaultPreparationPolicy struct {
	Update        bool
	PlanConstants bool
}

func PreflightDefault(c *Context, node expr.Expr, policy DefaultPreparationPolicy) (expr.Expr, bool, error) {
	if policy.Update {
		return PreflightUpdateExpression(c, node, policy.PlanConstants)
	}
	return PreflightCoercionsWithPolicy(c, node, true)
}

// defaultPreparation exists only during the source-bound operation. Neither it
// nor any planning context is retained by the accepted closure.
type defaultPreparation struct {
	self     *defaultPreparation
	artifact *AcceptedDefault
	active   bool
}

type defaultPreparationCall struct {
	expr.Info
	self       *defaultPreparationCall
	attempt    *defaultPreparation
	source     plan.SQLRoutineExpr
	prepared   plan.SQLRoutineExpr
	path       []int
	mode       defaultInvocationMode
	definition *proc.Definition
}

// Prepare reads the canonical stored root itself. No API accepts an independent
// output expression, result setter, or transformation callback as derivation
// authority. The actual incumbent preflight is the only result producer.
func (s *AcceptedDefaultSet) Prepare(c *Context, occurrence DefaultOccurrence, relation *catalog.Relation, ref ids.ObjectRef, attribute int32, policy DefaultPreparationPolicy) (expr.Expr, bool, error) {
	if c == nil || s == nil || s.self != s || s.sealed || s.retired || c.Catalog.Owner() == nil || occurrence.Statement == nil || relation == nil || c.Catalog.Get(relation.Address()) != relation || ref.Address != relation.Address() || ref.Domain() != c.Catalog.Owner().Domain || ref.Epoch != c.Catalog.Epoch() {
		return nil, false, ids.ErrCatalogReplaced
	}
	if occurrence.Kind > 2 || policy.Update != (occurrence.Kind == 1) {
		return nil, false, ids.ErrCatalogReplaced
	}
	definition := relation.ColumnMeta.Defaults[attribute]
	if definition.Expression == nil || c.RoutineConsumer != relation || c.FoldOrigin.Owner != ref || c.FoldOrigin.Attribute != attribute || c.FoldOrigin.Definition != definition.OID || c.DefaultOccurrence != occurrence || c.AcceptedDefault != nil {
		return nil, false, ids.ErrCatalogReplaced
	}
	if err := validateDefaultScalar(definition.Expression); err != nil {
		return nil, false, err
	}
	for _, old := range s.entries {
		if old.occurrence == occurrence && old.ref == ref && old.attribute == attribute {
			return nil, false, fmt.Errorf("stuntgres: duplicate accepted DEFAULT occurrence")
		}
	}
	a := &AcceptedDefault{owner: s, occurrence: occurrence, relation: relation, ref: ref, attribute: attribute, definition: definition}
	a.self = a
	for _, dependency := range definition.Dependencies {
		d := c.Catalog.Get(dependency.Address)
		if dependency.Domain() != ref.Domain() || dependency.Epoch != ref.Epoch || d == nil {
			return nil, false, ids.ErrCatalogReplaced
		}
		a.dependencies = append(a.dependencies, catalog.ExpressionDependency{Ref: dependency, Revision: d.Revision(), Purpose: catalog.DefinitionDependency})
		a.descriptors = append(a.descriptors, d)
	}
	attempt := &defaultPreparation{artifact: a, active: true}
	attempt.self = attempt
	defer func() { attempt.active = false }()
	tagged, _, err := attempt.tag(c.Catalog, definition.Expression, nil)
	if err != nil {
		return nil, false, err
	}
	output, changed, err := PreflightDefault(c, tagged, policy)
	if err != nil {
		return nil, false, err
	}
	if output == nil {
		return nil, false, ids.ErrCatalogReplaced
	}
	if err := EvaluationContext(c); err != nil {
		return nil, false, err
	}
	// Completing and consuming the private result is atomic with respect to the
	// candidate. No failed or partially prepared result is installed.
	owned, _, err := attempt.finish(c.Catalog, output)
	if err != nil {
		return nil, false, err
	}
	root := &acceptedDefaultRoot{Info: expr.InfoOf(owned), artifact: a, children: [1]expr.Expr{owned}}
	root.self = root
	a.root = root
	// Bind's normal local execution still uses the actual prepared expression;
	// historical capability is not available before final accepted-owner sealing.
	ordinary, _, err := attempt.strip(c.Catalog, output)
	if err != nil {
		return nil, false, err
	}
	if err := EvaluationContext(c); err != nil {
		return nil, false, err
	}
	s.entries = append(s.entries, a)
	return ordinary, changed, nil
}

// Validate the entire closure, including independently owned hidden defaults.
// They are not tagged, but cannot smuggle a retained query/frame through Args.
func validateDefaultScalar(root expr.Expr) error {
	var invalid error
	plan.WalkExpression(root, func(node expr.Expr) {
		if invalid != nil {
			return
		}
		properties := node.Properties()
		if properties.RowDependent || properties.ParameterDependent || properties.StageDependent {
			invalid = fmt.Errorf("stuntgres: non-scalar accepted DEFAULT")
			return
		}
		if _, query := node.(plan.SubqueryExpr); query {
			invalid = fmt.Errorf("stuntgres: query in accepted DEFAULT")
			return
		}
		if call, ok := node.(plan.SQLRoutineExpr); ok {
			invalid = call.ValidateStoredProvider()
		}
	})
	return invalid
}

func (p *defaultPreparation) tag(read catalog.Read, node expr.Expr, path []int) (expr.Expr, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	if call, ok := node.(plan.SQLRoutineExpr); ok {
		source := call
		if err := source.ValidateStoredProvider(); err != nil {
			return nil, false, err
		}
		current, err := ValidateStoredRoutineDescriptor(read, source.Procedure)
		if err != nil {
			return nil, false, err
		}
		definition, mode := source.Procedure.RoutineDefinition(), defaultPredecessor
		if definition.Kind == proc.PLpgSQLBody {
			if !currentPLDefault(current) {
				return nil, false, ids.ErrCatalogReplaced
			}
			mode = defaultCurrentPL
		}
		if !mode.valid(definition, source, source) {
			return nil, false, ids.ErrCatalogReplaced
		}
		args := call.Args
		for i, child := range call.Args {
			if i >= call.WrittenCount {
				break
			} // Hidden defaults retain their independent manifest.
			next, changed, err := p.tag(read, child, append(path, i))
			if err != nil {
				return nil, false, err
			}
			if changed {
				if len(args) != 0 && &args[0] == &call.Args[0] {
					args = slices.Clone(args)
				}
				args[i] = next
			}
		}
		call.Args = args
		token := &defaultPreparationCall{Info: call.Info, attempt: p, source: source, prepared: call, path: slices.Clone(path), mode: mode, definition: definition}
		token.self = token
		return token, true, nil
	}
	index := 0
	return preparation.MapAcceptedDefaultChildren(read, node, func(child expr.Expr) (expr.Expr, bool, error) {
		step := index
		index++
		return p.tag(read, child, append(path, step))
	})
}

func (n *defaultPreparationCall) valid() bool {
	return n != nil && n.self == n && n.attempt != nil && n.attempt.self == n.attempt && n.attempt.active &&
		n.mode.valid(n.definition, n.source, n.prepared)
}
func (n *defaultPreparationCall) Children() []expr.Expr { return n.prepared.Args }
func (*defaultPreparationCall) PlanningFold() expr.Fold { return expr.FoldNever }
func (n *defaultPreparationCall) IntrinsicVolatility() expr.Volatility {
	return n.prepared.IntrinsicVolatility()
}
func (n *defaultPreparationCall) GroupProperties(children []expr.Expr) expr.Properties {
	return n.prepared.GroupProperties(children)
}
func (n *defaultPreparationCall) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return n.prepared.Capture(kind)
}
func (n *defaultPreparationCall) Rebuild(expr.Info, []expr.Expr) (expr.Expr, error) {
	return nil, ids.ErrCatalogReplaced
}
func (n *defaultPreparationCall) Eval(c expr.Context) (sqltype.Value, error) {
	if !n.valid() {
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	return n.prepared.Eval(c)
}
func (n *defaultPreparationCall) preflight(c *Context, decisive, conditional bool) (expr.Expr, bool, error) {
	if !n.valid() {
		return nil, false, ids.ErrCatalogReplaced
	}
	var output expr.Expr
	var changed bool
	var err error
	if conditional {
		output, changed, err = simplifyConditionalNode(c, n.prepared, decisive)
	} else {
		output, changed, err = preflightCoercionNode(c, n.prepared, decisive)
	}
	if err != nil {
		return nil, false, err
	}
	call, ok := output.(plan.SQLRoutineExpr)
	if !ok {
		return output, true, nil
	}
	if !changed {
		return n, false, nil
	}
	next := *n
	next.Info, next.prepared = call.Info, call
	next.self = &next
	return &next, true, nil
}

func (p *defaultPreparation) finish(read catalog.Read, node expr.Expr) (expr.Expr, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	if token, ok := node.(*defaultPreparationCall); ok {
		if !token.valid() || token.attempt != p {
			return nil, false, ids.ErrCatalogReplaced
		}
		call := token.prepared
		args := call.Args
		for i, child := range call.Args {
			if i >= call.WrittenCount {
				break
			}
			next, changed, err := p.finish(read, child)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if len(args) != 0 && &args[0] == &call.Args[0] {
					args = slices.Clone(args)
				}
				args[i] = next
			}
		}
		call.Args = args
		wrapper := &acceptedDefaultCall{Info: call.Info, artifact: p.artifact, source: token.source, original: call, path: token.path, mode: token.mode, definition: token.definition}
		wrapper.self = wrapper
		p.artifact.calls = append(p.artifact.calls, wrapper)
		return wrapper, true, nil
	}
	return preparation.MapAcceptedDefaultChildren(read, node, func(child expr.Expr) (expr.Expr, bool, error) { return p.finish(read, child) })
}

// Strip preparation-only tokens from the ordinary local result. This does not
// evaluate or prepare anything again; it only COW-removes private annotations.
func (p *defaultPreparation) strip(read catalog.Read, node expr.Expr) (expr.Expr, bool, error) {
	if token, ok := node.(*defaultPreparationCall); ok {
		if !token.valid() || token.attempt != p {
			return nil, false, ids.ErrCatalogReplaced
		}
		call := token.prepared
		args := call.Args
		for i, child := range call.Args {
			if i >= call.WrittenCount {
				break
			}
			next, changed, err := p.strip(read, child)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if len(args) != 0 && &args[0] == &call.Args[0] {
					args = slices.Clone(args)
				}
				args[i] = next
			}
		}
		call.Args = args
		return call, true, nil
	}
	return preparation.MapAcceptedDefaultChildren(read, node, func(child expr.Expr) (expr.Expr, bool, error) { return p.strip(read, child) })
}
