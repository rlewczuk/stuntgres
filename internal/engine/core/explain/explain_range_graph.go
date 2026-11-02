package explain

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func (c *rangeCapture) source(s *rangeScope, env *rangeEnvironment, n *plan.SourceNode, e map[string]any) error {
	if n.Occurrence < 0 || n.Occurrence >= len(env.leaves) {
		return fmt.Errorf("stuntgres: missing analyzed range occurrence")
	}
	leaf := &env.leaves[n.Occurrence]
	if leaf.Ref.Address != n.Ref.Address || leaf.Relation == nil {
		return fmt.Errorf("stuntgres: mismatched analyzed range occurrence")
	}
	if err := c.relation(e, env.auth, n.Ref, "source"); err != nil {
		return err
	}
	e["Occurrence"] = n.Occurrence
	s.sources[n.Occurrence] = e
	demand := env.demand
	demand.analysisOnly = demand.analysisOnly || leaf.AnalysisOnly
	demand.executableRead = demand.executableRead && !demand.analysisOnly
	if demand.analysisOnly {
		e["Analysis Only"] = true
	}
	principal := env.auth.State.CurrentUser
	if leaf.Principal != (authorization.PrincipalRef{}) {
		principal = leaf.Principal
	}
	visit := func(req analyze.Requirement) error { return c.permission(e, env.auth, principal, req) }
	if demand.executableRead {
		if err := analyze.SourceReadRequirements(leaf, visit); err != nil {
			return err
		}
	} else if demand.markValidation {
		if err := visit(view.DefinitionReadRequirement(leaf.Ref, leaf.Relation, leaf.Uses)); err != nil {
			return err
		}
	}
	if err := analyze.SourceRowMarkRequirements(leaf, visit); err != nil {
		return err
	}
	if leaf.Relation.RelationKind == catalog.ViewRelation {
		if w := env.write; w != nil {
			layer := view.ViewWriteOf(w.binding.ViewWrite).Layers[w.layer]
			if n.Occurrence == layer.MainOccurrence {
				return c.writeLayer(s, e, env, w.binding, w.statement, w.layer+1)
			}
			// CheckWrite directly validates each non-main view definition,
			// even when it has no inherited or stored mark of its own.
			demand.markValidation = true
		}
		demand.markValidation = demand.markValidation || view.NeedsRowMarkValidation(env.auth.Catalog, leaf.Relation, leaf.Mark)
		return c.view(s, e, env, leaf.Relation, leaf.Mark, demand)
	}
	return nil
}

func (c *rangeCapture) query(parent *rangeScope, owner map[string]any, env *rangeEnvironment, query *plan.Query, kind string) (*rangeScope, error) {
	if query == nil || query.Root == nil {
		return nil, fmt.Errorf("stuntgres: missing analyzed range query")
	}
	key := rangeQueryKey{query, parent}
	if previous := env.queries[key]; previous != nil {
		return previous, nil
	}
	s := c.scope(kind, parent, owner)
	env.queries[key] = s
	local := *env
	if kind == "cte" {
		local.demand.analysisOnly, local.demand.executableRead = true, false
	}
	if err := c.queryNodes(s, &local, query.Root); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *rangeCapture) producer(env *rangeEnvironment, definition *plan.CTEDefinition) (*rangeScope, error) {
	if previous := env.producers[definition]; previous != nil {
		return previous, nil
	}
	declaration, known := env.declarations[definition]
	if declaration.environment == nil {
		// Stored definitions do not carry an analyze.Binding's declaration
		// list. Their accepted Owner still identifies a lexical ancestor.
		owner := env.frames[rangeCoordinate{1, definition.Owner}]
		if known || owner == nil {
			return nil, fmt.Errorf("stuntgres: missing EXPLAIN CTE declaration owner")
		}
		declaration = rangeDeclaration{scope: owner, environment: env}
		env.declarations[definition] = declaration
	}
	env = declaration.environment
	parent := declaration.scope
	rewrite := env.rewrites[definition]
	key := rangeQueryKey{definition.Query, parent}
	if rewrite == nil {
		if previous := env.queries[key]; previous != nil {
			env.producers[definition] = previous
			return previous, nil
		}
	}
	s := c.scope("cte", parent, nil)
	env.producers[definition] = s // Publish before either authority can recurse.
	if rewrite != nil {
		mutation, ok := analyze.MutationBinding(definition)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing rewritten CTE caller binding")
		}
		caller := c.scope("caller", s, nil)
		if err := c.binding(caller, rewrite.Caller.Access(), definition.Statement, *mutation, env); err != nil {
			return nil, err
		}
		rule := c.scope("rule", s, nil)
		command := rewrite.Command
		if err := c.binding(rule, command.Auth.Access(), command.Syntax, command.Binding, env); err != nil {
			return nil, err
		}
	} else if mutation, ok := analyze.MutationBinding(definition); ok {
		if err := c.binding(s, env.auth, definition.Statement, *mutation, env); err != nil {
			return nil, err
		}
	} else {
		if definition.Query == nil {
			return nil, fmt.Errorf("stuntgres: missing analyzed CTE range query")
		}
		env.queries[key] = s
		if err := c.queryNodes(s, env, definition.Query.Root); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (c *rangeCapture) node(s *rangeScope, env *rangeEnvironment, node plan.Node) error {
	if node == nil {
		return nil
	}
	if err := c.host.Context().Err(); err != nil {
		return err
	}
	if _, visited := s.nodes[node]; visited {
		return nil
	}
	// Record non-range nodes too: shared analysis paths do not duplicate demands.
	s.nodes[node] = nil
	kind, name := "", ""
	var aliases []string
	switch n := node.(type) {
	case *plan.SourceNode:
		kind, name, aliases = "relation", n.RangeName, n.ColumnAliases
	case *plan.JoinNode:
		kind, name, aliases = "join", n.RangeName, n.ColumnAliases
	case *plan.WorktableScanNode:
		kind, name, aliases = "worktable", explainDerivedAlias(&n.DerivedNode), n.ColumnAliases
	case *plan.DerivedNode:
		kind, name, aliases = "subquery", explainDerivedAlias(n), n.ColumnAliases
		if n.CTE != nil {
			kind = "cte"
		}
	case *plan.FunctionFromNode:
		kind, name, aliases = "function", n.RangeName, n.ColumnAliases
	case *plan.NamedRelationScanNode:
		kind, name, aliases = "named relation", n.RangeName, n.ColumnAliases
	case *plan.ProjectNode:
		if n.RangeName != "" {
			kind, name, aliases = "values", n.RangeName, n.ColumnAliases
		}
	}
	var e map[string]any
	if kind != "" {
		e = c.entry(s, kind, name, node.Schema(), aliases)
		s.nodes[node] = e
	}
	switch n := node.(type) {
	case *plan.SourceNode:
		return c.source(s, env, n, e)
	case *plan.WorktableScanNode:
		producer, err := c.producer(env, n.CTE)
		if err != nil {
			return err
		}
		e["Producer Scope ID"] = producer.id
		return nil
	case *plan.DerivedNode:
		var child *rangeScope
		var err error
		if n.CTE != nil {
			child, err = c.producer(env, n.CTE)
			if err == nil {
				e["Producer Scope ID"] = child.id
			}
		} else {
			child, err = c.query(s, e, env, n.Query, "derived")
			if err == nil {
				e["Child Scope ID"] = child.id
			}
		}
		return err
	}
	var captures []plan.FunctionCorrelation
	switch n := node.(type) {
	case *plan.JoinNode:
		captures = n.Captures
	case *plan.FunctionFromNode:
		captures = n.Captures
	}
	for _, capture := range captures {
		if err := c.outerFrame(s, env, capture.Frame); err != nil {
			return err
		}
	}
	left, right := plan.QueryInputs(node)
	if err := c.node(s, env, left); err != nil {
		return err
	}
	if err := c.node(s, env, right); err != nil {
		return err
	}
	if n, ok := node.(*plan.JoinNode); ok {
		switch n.Kind {
		case parser.JoinInner:
			e["Join Type"] = "Inner"
		case parser.JoinLeft:
			e["Join Type"] = "Left"
		case parser.JoinRight:
			e["Join Type"] = "Right"
		case parser.JoinFull:
			e["Join Type"] = "Full"
		}
		if input := s.nodes[n.Left]; input != nil {
			e["Left Entry ID"] = input["Entry ID"]
		}
		if input := s.nodes[n.Right]; input != nil {
			e["Right Entry ID"] = input["Entry ID"]
		}
	}
	var err error
	visit := func(expression expr.Expr) {
		if err == nil {
			err = c.expression(s, env, expression)
		}
	}
	plan.QueryExpressions(node, visit)
	// The ordinary walker intentionally excludes inactive window demand.
	// Those accepted expressions still own analyzed subquery ranges.
	if window, ok := node.(*plan.WindowNode); ok {
		for _, call := range window.Calls {
			if !call.AnalysisOnly {
				continue
			}
			for _, argument := range call.Arguments {
				visit(argument)
			}
			visit(call.Argument)
			visit(call.Ordinal)
			visit(call.DefaultValue)
		}
		for i, spec := range window.Specs {
			if window.SpecActive(i) {
				continue
			}
			for _, expression := range spec.Partition {
				visit(expression)
			}
			for _, key := range spec.Order {
				visit(key.Expression)
			}
			visit(spec.Frame.Start.Offset)
			visit(spec.Frame.End.Offset)
		}
	}
	return err
}

func (c *rangeCapture) expression(s *rangeScope, env *rangeEnvironment, expression expr.Expr) error {
	var err error
	plan.WalkExpression(expression, func(expression expr.Expr) {
		if err != nil {
			return
		}
		if err = c.expressionOwner(s, env, expression); err != nil {
			return
		}
		if subquery, ok := expression.(plan.SubqueryExpr); ok && subquery.QueryPlan != nil {
			kind := "subplan"
			if subquery.CTEEdge {
				kind = "cte"
			}
			_, err = c.query(s, nil, env, subquery.QueryPlan, kind)
		}
	})
	return err
}

func (c *rangeCapture) bindingExpressions(s *rangeScope, env *rangeEnvironment, b analyze.Binding) error {
	visit := func(expression expr.Expr) error { return c.expression(s, env, expression) }
	if err := visit(b.Predicate); err != nil {
		return err
	}
	for _, row := range b.Expressions {
		for _, expression := range row {
			if err := visit(expression); err != nil {
				return err
			}
		}
	}
	if b.Update != nil {
		if err := visit(b.Update.Predicate); err != nil {
			return err
		}
		for _, expression := range b.Update.Assignments {
			if err := visit(expression); err != nil {
				return err
			}
		}
	}
	if b.Returning != nil {
		for _, expression := range b.Returning.Expressions {
			if err := visit(expression); err != nil {
				return err
			}
		}
	}
	if conflict := b.Insert.Conflict; conflict != nil {
		if err := visit(conflict.Predicate); err != nil {
			return err
		}
		if conflict.Update != nil {
			if err := visit(conflict.Update.Predicate); err != nil {
				return err
			}
			for _, expression := range conflict.Update.Assignments {
				if err := visit(expression); err != nil {
					return err
				}
			}
		}
	}
	if b.Merge != nil {
		if err := visit(b.Merge.On); err != nil {
			return err
		}
		for _, arm := range b.Merge.Arms {
			if err := visit(arm.Condition); err != nil {
				return err
			}
			if err := c.bindingExpressions(s, env, arm.Input); err != nil {
				return err
			}
		}
	}
	return nil
}
