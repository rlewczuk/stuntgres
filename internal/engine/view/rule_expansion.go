package view

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// RuleRewriter expands rules over one catalog owner. Expansion reads live
// storage through the rule authority, so the caller holds Engine.mu.
type RuleRewriter struct {
	Owner *catalog.Owner
}

func (rw RuleRewriter) ExpandLocked(auth RuleAuthority, stmt parser.Statement, b analyze.Binding, ancestry []ruleAncestry, program *RuleProgram) (int, error) {
	event := stored.RuleEvent(stmt)
	firstCommand := len(program.Commands)
	b = PhysicalBinding(b)
	if event == "" || b.Relation == nil {
		return appendRuleCommand(program, auth, stmt, b)
	}
	current, ok := auth.Access().Catalog.Relation(b.Ref.Address)
	if !ok || b.Ref.Epoch != auth.Access().Catalog.Epoch() {
		return -1, ids.ErrCatalogReplaced
	}
	var err error
	b, err = stored.RefreshRuleBinding(auth.Access().Catalog, b, current)
	if err != nil {
		return -1, err
	}
	if insert, ok := stmt.(*parser.Insert); ok && b.Rule.Template {
		b.Insert.Defaults = nil
		analyze.BindInsertDefaults(rw.Owner, &b, auth.Access().Catalog, insert.Override)
	}
	if current.RelationKind != catalog.ViewRelation {
		b.Storage = auth.StorageLocked(b.Ref.Address)
	}
	rules := stored.RulesOf(b.Relation)
	replica := auth.RuleReplica()
	present := slices.ContainsFunc(rules, func(r stored.Rule) bool { return r.Event() == event })
	matched := slices.ContainsFunc(rules, func(r stored.Rule) bool { return r.Event() == event && r.Mode().Fires(replica) })
	if present {
		dependency := catalog.ExpressionDependency{Ref: b.Ref, Revision: current.Revision()}
		if !slices.Contains(program.Events, dependency) {
			program.Events = append(program.Events, dependency)
		}
	}
	if s, ok := stmt.(*parser.Insert); ok && s.Conflict != nil {
		for _, r := range rules {
			// UPDATE presence precedes filtering in PostgreSQL's matchLocks.
			if r.Event() == "UPDATE" || r.Event() == "INSERT" && r.Mode().Fires(replica) && len(r.Actions()) != 0 {
				return -1, sqlError("0A000", "INSERT with ON CONFLICT clause cannot be used with table that has INSERT or UPDATE rules")
			}
		}
	}
	if current.RelationKind == catalog.ViewRelation && !matched {
		if w := ViewWriteOf(b.ViewWrite); w != nil && w.Instead {
			// Matching rules were considered first. An unrelated rule must
			// not rebind/recurse past an admitted logical trigger endpoint.
			return appendRuleCommand(program, auth, stmt, b)
		}
		b.ViewWrite = nil
		if err := BindViewWrite(rw.Owner, auth.Access().Catalog, replica, auth.RestrictedRelations(), &b, stmt); err != nil {
			return -1, err
		}
		return rw.ExpandLocked(auth, stmt, b, ancestry, program)
	}
	if !matched {
		return appendRuleCommand(program, auth, stmt, b)
	}
	key := ruleAncestry{b.Ref, event}
	if slices.Contains(ancestry, key) {
		return -1, sqlError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, b.Relation.Name()))
	}
	ancestry = append(ancestry, key)
	// Bind stored routine authority to exact original rule occurrences before
	// composing them with fresh input. Current reference stamps below remain
	// invalidation metadata, never the historical action's call manifest.
	active := make([]stored.Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.Event() == event && rule.Mode().Fires(replica) {
			active = append(active, rule)
		}
	}
	rules = active
	for i, rule := range rules {
		if rule.Event() != event {
			continue
		}
		rules[i], err = stored.GraftRuleRoutineRecipes(auth.Access().Catalog, current, rule)
		if err != nil {
			return -1, err
		}
	}
	original := true
	conditionalInstead := false
	for _, r := range rules {
		if r.Event() == event && r.Instead() && r.Condition() == nil {
			original = false
		}
		if r.Event() == event && r.Instead() && r.Condition() != nil {
			conditionalInstead = true
		}
	}
	residual := b
	if original {
		var err error
		residual, err = RuleResidual(auth, b, event, rules)
		if err != nil {
			return -1, err
		}
	}
	count := -1
	if event == "INSERT" && original {
		var err error
		start := len(program.Commands)
		count, err = rw.expandResidualLocked(auth, stmt, residual, ancestry, program)
		if err != nil {
			return -1, err
		}
		if conditionalInstead {
			markRuleCommandSource(program, start, RuleConditionalInstead)
		}
	}
	providers := 0
	for _, r := range rules {
		if r.Event() != event {
			continue
		}
		for _, address := range r.References() {
			whole := address
			whole.SubID = 0
			if descriptor, ok := auth.Access().Catalog.Find(whole); ok {
				b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: rw.Owner.Domain.Ref(auth.Access().Catalog.Epoch(), address), Revision: descriptor.Revision()})
			}
		}
		for _, action := range r.Actions() {
			actionAuth := auth.AsUser(current.Owner)
			composed, err := ComposeRuleAction(auth, b, event, r, action)
			if err != nil {
				return -1, err
			}
			if ruleReturning(stmt) == nil {
				composed.Returning = nil
			} else if action.Binding().Returning != nil {
				providers++
				if providers > 1 {
					return -1, sqlError("0A000", "cannot have RETURNING lists in multiple rules")
				}
				composed.Returning, err = ruleReturningProjection(auth.Access().Catalog, b.Returning, action.Binding().Returning)
				if err != nil {
					return -1, err
				}
			}
			start := len(program.Commands)
			index, err := rw.ExpandLocked(actionAuth, action.Syntax(), composed, ancestry, program)
			if err != nil {
				return -1, err
			}
			source := RuleInstead
			if !r.Instead() {
				source = RuleAlso
			} else if r.Condition() != nil {
				source = RuleConditionalInstead
			}
			markRuleCommandSource(program, start, source)
			if composed.Returning != nil && ruleReturning(stmt) != nil {
				program.Returning = index
			}
			if !original && r.Instead() && stored.RuleEvent(action.Syntax()) == event {
				count = index
			}
		}
	}
	if event != "INSERT" && original {
		var err error
		start := len(program.Commands)
		count, err = rw.expandResidualLocked(auth, stmt, residual, ancestry, program)
		if err != nil {
			return -1, err
		}
		if conditionalInstead {
			markRuleCommandSource(program, start, RuleConditionalInstead)
		}
	}
	if (!original || conditionalInstead) && ruleReturning(stmt) != nil && providers == 0 {
		return -1, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot perform %s RETURNING on relation "%s"`, event, b.Relation.Name()), Hint: "You need an unconditional ON " + event + " DO INSTEAD rule with a RETURNING clause."}
	}
	if len(parser.StatementWith(stmt)) != 0 && len(program.Commands)-firstCommand > 1 {
		return -1, sqlError("0A000", "WITH cannot be used in a query that is rewritten by rules into multiple queries")
	}
	return count, nil
}

func appendRuleCommand(program *RuleProgram, auth RuleAuthority, stmt parser.Statement, b analyze.Binding) (int, error) {
	// Admit only the surviving command's sources. A matching explicit rule
	// may eliminate its original target without expanding that view.
	if _, err := stored.ValidateViewReadGraph(auth.Access().Catalog, b.QuerySources, auth.RestrictedRelations()); err != nil {
		return -1, err
	}
	index := len(program.Commands)
	program.Commands = append(program.Commands, RuleCommand{Syntax: stmt, Binding: b, Auth: auth})
	return index, nil
}

func markRuleCommandSource(program *RuleProgram, start int, source ruleCommandSource) {
	for i := start; i < len(program.Commands); i++ {
		if program.Commands[i].Source == RuleOriginal {
			program.Commands[i].Source = source
		}
	}
}

func RuleResidual(auth RuleAuthority, b analyze.Binding, event string, rules []stored.Rule) (analyze.Binding, error) {
	catalog := auth.Access().Catalog
	p, err := MakeRuleProducer(catalog, b, event)
	if err != nil {
		return b, err
	}
	var predicates []expr.Expr
	for _, r := range rules {
		if r.Event() != event || !r.Instead() || r.Condition() == nil {
			continue
		}
		qualification := r.Condition()
		if r.ConditionBinding() != nil {
			cb, refreshErr := RefreshRuleSources(auth, *r.ConditionBinding())
			if refreshErr != nil {
				return b, refreshErr
			}
			cb, err = OffsetRuleSources(catalog, cb, len(b.QuerySources))
			if err != nil {
				return b, err
			}
			qualification = cb.Query.Root.(*plan.ProjectNode).Expressions[0]
			maxFrame := ruleBindingMaxFrame(b)
			_, qualification, err = rewriteRuleFrames(catalog, nil, qualification, func(old uint32) uint32 {
				if old <= 1 {
					return old
				}
				return old + maxFrame + 1
			})
			if err != nil {
				return b, err
			}
			b.QuerySources = append(slices.Clone(b.QuerySources), cb.QuerySources...)
			b.Dependencies = append(slices.Clone(b.Dependencies), cb.Dependencies...)
			b.Prepared.SequenceConstants = append(slices.Clone(b.Prepared.SequenceConstants), cb.Prepared.SequenceConstants...)
			b.Prepared.RegtypeConstants = append(slices.Clone(b.Prepared.RegtypeConstants), cb.Prepared.RegtypeConstants...)
		}
		tree := stored.NewRuleTreeMapper(catalog, nil, 0, b.CTEs, stored.RuleTreeMapping{Finish: ruleQueryFinalizer(catalog), Leaf: func(expr expr.Expr) (expr.Expr, bool, error) {
			if column, ok := expr.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
				recipes := p.old
				if column.Frame == 2 {
					recipes = p.New
				}
				slot := slices.Index(b.Relation.Attributes, column.Origin.Address.SubID)
				if slot < 0 || slot >= len(recipes) {
					return nil, false, ids.ErrCatalogReplaced
				}
				recipe := recipes[slot]
				if column.Frame == 2 {
					if err := ValidateRuleNewUpdateOutput(recipe); err != nil {
						return nil, false, err
					}
				}
				return recipe, true, nil
			}
			return expr, false, nil
		}})
		condition, _, err := tree.Expression(qualification)
		if err != nil {
			return b, err
		}
		predicates = append(predicates, plan.TruthTestExpr{Info: expr.Info{Usage: condition.ResultUsage(), Pos: condition.SourcePosition(), Property: condition.Properties()}, Child: condition, Truth: plan.True, Not: true})
	}
	if len(predicates) == 0 {
		return b, nil
	}
	if event == "INSERT" {
		schema := make(plan.QuerySchema, len(b.Targets))
		frame := uint32(1)
		if b.Query != nil {
			frame = b.Query.Root.(*plan.ProjectNode).Frame
		}
		projection := make([]expr.Expr, len(b.Targets))
		for i, slot := range b.Targets {
			schema[i] = b.Relation.Columns[slot]
			projection[i] = p.New[slot]
		}
		root := &plan.FilterNode{QuerySchema: plan.QuerySchema(p.Root.Schema()), Child: p.Root, Predicate: ViewWritePredicate(predicates, nil)}
		b.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: root, Expressions: projection, Frame: frame}}
		b.CommandQuery = true
		b.Expressions = nil
	} else if event == "UPDATE" {
		u := *b.Update
		u.Predicate = ViewWritePredicate(predicates, u.Predicate)
		b.Update = &u
	} else {
		b.Predicate = ViewWritePredicate(predicates, b.Predicate)
	}
	return b, nil
}

func ruleReturningProjection(catalog catalog.Read, original, provider *analyze.Returning) (*analyze.Returning, error) {
	copy := *original
	// Output names/usages belong to the caller; column privileges belong to
	// the physical provider whose expressions replace those output Vars.
	copy.Reads = provider.Reads
	copy.ReadRequired = provider.ReadRequired
	copy.VisibilitySnapshot = copy.VisibilitySnapshot || provider.VisibilitySnapshot
	copy.Expressions = slices.Clone(original.Expressions)
	for i, bound := range copy.Expressions {
		var err error
		copy.Expressions[i], _, err = stored.RewriteViewExpression(catalog, bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
			column, ok := expr.(plan.ReturningColumnExpr)
			if !ok {
				return expr, false, nil
			}
			if column.Slot >= len(provider.Expressions) {
				return nil, false, ids.ErrCatalogReplaced
			}
			return provider.Expressions[column.Slot], true, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return &copy, nil
}

func (rw RuleRewriter) expandResidualLocked(auth RuleAuthority, stmt parser.Statement, b analyze.Binding, ancestry []ruleAncestry, program *RuleProgram) (int, error) {
	if b.Relation.RelationKind != catalog.ViewRelation {
		return appendRuleCommand(program, auth, stmt, b)
	}
	prior := ViewWriteOf(b.ViewWrite)
	b.ViewWrite = nil
	if err := BindViewWrite(rw.Owner, auth.Access().Catalog, auth.RuleReplica(), auth.RestrictedRelations(), &b, stmt, b.Ref); err != nil {
		return -1, err
	}
	if b.ViewWrite != nil && prior != nil {
		ViewWriteOf(b.ViewWrite).Layers = append(slices.Clone(prior.Layers), ViewWriteOf(b.ViewWrite).Layers...)
		ViewWriteOf(b.ViewWrite).Logical = prior.Logical
	}
	return rw.ExpandLocked(auth, stmt, b, ancestry, program)
}

func RefreshRuleSources(auth RuleAuthority, b analyze.Binding) (analyze.Binding, error) {
	catalog := auth.Access().Catalog
	var sources []exec.Leaf
	for i, source := range b.QuerySources {
		current, ok := catalog.Relation(source.Ref.Address)
		if !ok || source.Relation == nil || source.Ref.Domain() != auth.Access().Owner.Domain || source.Ref.Epoch != catalog.Epoch() || source.Ref.Address.SubID != 0 {
			return b, ids.ErrCatalogReplaced
		}
		storage := auth.StorageLocked(source.Ref.Address)
		if current == source.Relation && storage == source.Storage {
			continue
		}
		if sources == nil {
			sources = slices.Clone(b.QuerySources)
		}
		updated := source
		updated.Relation = current
		updated.Storage = storage
		if !slices.Equal(source.Relation.Attributes, current.Attributes) {
			updated.Uses = slices.Clone(source.Uses)
			for j, slot := range source.Uses {
				if slot < 0 || slot >= len(source.Relation.Attributes) {
					return b, ids.ErrCatalogReplaced
				}
				updated.Uses[j] = slices.Index(current.Attributes, source.Relation.Attributes[slot])
				if updated.Uses[j] < 0 {
					return b, ids.ErrCatalogReplaced
				}
			}
		}
		sources[i] = updated
	}
	if sources != nil {
		b.QuerySources = sources
	}
	return b, nil
}

func ruleBindingHasPseudo(b analyze.Binding) bool {
	found := false
	inspect := func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if column, ok := node.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
				found = true
			}
		})
	}
	query := func(root plan.Node) {
		if root != nil && !found {
			plan.VisitCTEQuery(root, func(plan.Node) {}, inspect, true)
		}
	}
	expression := func(bound expr.Expr) {
		if bound != nil && !found {
			query(&plan.ProjectNode{Expressions: []expr.Expr{bound}})
		}
	}
	targets := func(items []*plan.ArrayAssignmentTarget) {
		for _, target := range items {
			for ; target != nil; target = target.Next {
				for _, sub := range target.Node.Subscripts {
					expression(sub.Lower)
					expression(sub.Upper)
				}
			}
		}
	}
	if b.Query != nil {
		query(b.Query.Root)
	}
	for _, row := range b.Expressions {
		for _, expr := range row {
			expression(expr)
		}
	}
	expression(b.Predicate)
	query(b.Delete.Using)
	targets(b.ArrayTargets)
	if b.Update != nil {
		query(b.Update.From)
		expression(b.Update.Predicate)
		for _, expr := range b.Update.Assignments {
			expression(expr)
		}
		targets(b.Update.ArrayTargets)
	}
	if b.Returning != nil {
		for _, expr := range b.Returning.Expressions {
			expression(expr)
		}
	}
	if b.Insert.Conflict != nil {
		expression(b.Insert.Conflict.Predicate)
		for _, key := range b.Insert.Conflict.Keys {
			expression(key.Expression)
		}
		if b.Insert.Conflict.Update != nil {
			expression(b.Insert.Conflict.Update.Predicate)
			for _, expr := range b.Insert.Conflict.Update.Assignments {
				expression(expr)
			}
			targets(b.Insert.Conflict.Update.ArrayTargets)
		}
	}
	return found
}
