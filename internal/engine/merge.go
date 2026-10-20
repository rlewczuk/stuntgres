package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type boundMerge struct {
	source        plan.Node
	on            expr.Expr
	arms          []boundMergeArm
	hasInsert     bool
	hasDelete     bool
	hasNothing    bool
	hasBySource   bool
	hasByTarget   bool
	targetWidth   int
	sourceWidth   int
	insertTargets []int
	updateTargets []int
}

type boundMergeArm struct {
	match     parser.MergeMatch
	action    parser.MergeAction
	condition expr.Expr
	input     binding
	syntax    []parser.Expr
}

func mergeRelationKind(relation *catalog.Relation) error {
	if relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.PartitionedRelation {
		return nil
	}
	if relation.RelationKind == catalog.ViewRelation {
		return sqlError("0A000", "MERGE into views is unsupported")
	}
	err := sqlError("0A000", fmt.Sprintf(`cannot execute MERGE on relation "%s"`, relation.Name())).(*sqlstate.Error)
	switch relation.RelationKind {
	case catalog.SequenceRelation:
		err.Detail = "This operation is not supported for sequences."
	case catalog.IndexRelation:
		err.Detail = "This operation is not supported for indexes."
	default:
		err.Detail = "This operation is not supported for this relation kind."
	}
	return err
}

// Reachability is independent of catalog binding and precedes relation admission.
func validateMergeReachability(s *parser.Merge) error {
	var unconditional [3]bool
	for _, arm := range s.Arms {
		if unconditional[arm.Match] {
			return sqlError("42601", "unreachable WHEN clause specified after unconditional WHEN clause")
		}
		if arm.Condition == nil {
			unconditional[arm.Match] = true
		}
	}
	return nil
}

func bindMerge(out *binding, binder *expressionBinder, auth AuthorizationView, requiredRef ids.ObjectRef, s *parser.Merge) error {
	ref, err := binder.host.AdmittedReference(auth, s.Name, requiredRef)
	if err != nil {
		return err
	}
	if ref != requiredRef {
		return ids.ErrCatalogReplaced
	}
	out.ref = ref
	out.relation = auth.Catalog.MustRelation(ref.Address)
	if !guardedCatalogRelation(out.relation) {
		if err := mergeRelationKind(out.relation); err != nil {
			return err
		}
	}
	out.storage = binder.host.AuthorizationStorage(auth, ref.Address)
	if out.storage == nil && out.relation.RelationKind != catalog.PartitionedRelation && !guardedCatalogRelation(out.relation) {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	binder.target.relation, binder.target.ref = out.relation, ref
	binder.target.name, binder.target.alias = out.relation.Name(), s.Alias.Text
	binder.target.insertTarget = out.relation
	target := binder.mutationTargetNamespace()
	binder.query.hidden = append(binder.query.hidden, target.qualifiers[0])
	sourceHiddenStart := len(binder.query.hidden)
	root, source, err := binder.bindFrom(s.Source)
	if err != nil {
		return err
	}
	for _, q := range source.qualifiers {
		t := target.qualifiers[0]
		if t.name == q.name && (t.alias != "" || q.alias != "" || t.ref == q.ref || q.ref == (ids.ObjectRef{})) {
			d := sqlError("42712", fmt.Sprintf(`name "%s" specified more than once`, t.name)).(*sqlstate.Error)
			d.Detail = "The name is used both as MERGE target table and data source."
			return d
		}
	}
	combined, err := combineQueryNamespace(target, rebaseQueryNamespace(source, len(target.frame)))
	if err != nil {
		return err
	}
	binder.query.namespace = &combined
	binder.clause.aggregateClause = "JOIN conditions"
	on, err := binder.bind(s.On, expressionPolicy{context: projectionContext})
	if err != nil {
		return err
	}
	on, err = binder.finalizeProjection(on)
	if err != nil {
		return err
	}
	// PostgreSQL18 does not coerce MERGE ON to BOOL. Its released Datum
	// behavior is retained only for supported integer/text carriers here.
	switch on.ResultUsage().Type.BaseType().ID() {
	case sqltype.Bool, sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Text:
	default:
		return expr.ErrorAt("0A000", "MERGE ON type is unsupported", s.On.SourcePosition())
	}
	merge := &boundMerge{source: root, on: on, arms: make([]boundMergeArm, len(s.Arms)), targetWidth: len(target.frame), sourceWidth: len(source.frame)}
	var outputParameters map[int]sqltype.Usage
	if s.Returning != nil {
		wasUpdateParameters := binder.params.updateParameters
		binder.params.updateParameters = true
		out.returning, err = binder.bindReturning(s.Returning, returningMerge)
		binder.params.updateParameters = wasUpdateParameters
		if err != nil {
			return err
		}
		out.returning.readRequired = binder.target.returningReadRequired
		for number, usage := range binder.params.parameters {
			if usage.Type != nil {
				if binder.params.updateInputParameters == nil {
					binder.params.updateInputParameters = make(map[int]sqltype.Usage, len(binder.params.parameters))
				}
				binder.params.updateInputParameters[number] = usage
			}
		}
		outputParameters = binder.params.updateInputParameters
		defer func() { binder.params.updateInputParameters = nil }()
	}
	hidden := binder.query.hidden
	var sourceHidden []queryQualifier
	for i, raw := range s.Arms {
		binder.params.updateInputParameters = outputParameters
		binder.query.hidden = hidden
		binder.query.namespace = &source
		switch raw.Match {
		case parser.MergeMatched:
			binder.query.namespace = &combined
		case parser.MergeNotMatchedBySource:
			binder.query.namespace = &target
			merge.hasBySource = true
		case parser.MergeNotMatched:
			merge.hasByTarget = true
		}
		if raw.Match == parser.MergeNotMatchedBySource {
			binder.query.namespace = &target
			if sourceHidden == nil {
				sourceHidden = append([]queryQualifier(nil), hidden...)
				// bindFrom already records each source entry, including aliases.
				// Change only its action-local accessibility flag; appending the
				// same entries would turn one missing column into plural ambiguity.
				for i := sourceHiddenStart; i < len(sourceHidden); i++ {
					sourceHidden[i].lateralForbidden = true
				}
			}
			binder.query.hidden = sourceHidden
		}
		arm := &merge.arms[i]
		arm.match, arm.action = raw.Match, raw.Action
		if raw.Condition != nil {
			binder.target.mergeWhen = true
			binder.clause.aggregateClause = "WHEN"
			arm.condition, err = binder.bindBoolean(raw.Condition, filterContext, "WHEN")
			binder.target.mergeWhen = false
			if err != nil {
				return err
			}
		}
		arm.input = binding{ref: ref, relation: out.relation, storage: out.storage}
		switch raw.Action {
		case parser.MergeNothingAction:
			merge.hasNothing = true
		case parser.MergeDeleteAction:
			merge.hasDelete = true
		case parser.MergeUpdateAction:
			binder.clause.aggregateClause = "UPDATE"
			u := &boundUpdate{assignments: make([]expr.Expr, len(raw.Assignments))}
			if err := bindUpdateAssignments(&arm.input, binder, raw.Assignments, u); err != nil {
				return err
			}
			merge.updateTargets = mergeTargetUnion(merge.updateTargets, arm.input.targets)
		case parser.MergeInsertAction:
			merge.hasInsert = true
			arm.syntax = raw.Values
			insert := parser.Insert{Name: s.Name, Targets: raw.Targets, Rows: [][]parser.Expr{raw.Values}, DefaultValues: raw.DefaultValues, Override: raw.Override, Pos: raw.ActionPos}
			binder.target.mergeInput = true
			err = bindInsertInput(&arm.input, binder, auth, &insert)
			binder.target.mergeInput = false
			if err != nil {
				return err
			}
			if len(arm.input.insert.projection) == 0 {
				arm.input.insert.projection = make([]int, len(out.relation.Columns))
				for j := range arm.input.insert.projection {
					arm.input.insert.projection[j] = -1
				}
				for j, target := range arm.input.targets {
					arm.input.insert.projection[target] = j
				}
			}
			merge.insertTargets = mergeTargetUnion(merge.insertTargets, arm.input.targets)
		}
		for _, target := range arm.input.targets {
			found := false
			for _, prior := range out.targets {
				if target == prior {
					found = true
					break
				}
			}
			if !found {
				out.targets = append(out.targets, target)
			}
		}
	}
	binder.query.hidden = hidden
	out.merge = merge
	return nil
}

func mergeTargetUnion(union, targets []int) []int {
	for _, target := range targets {
		found := false
		for _, prior := range union {
			if target == prior {
				found = true
				break
			}
		}
		if !found {
			union = append(union, target)
		}
	}
	return union
}

// Each Execute has a newly analyzed binding. Preflight mutates only that private
// binding, never a stored source/default definition or held prepared statement.
func preflightMerge(c *exec.Context, merge *boundMerge, catalog catalog.Read) error {
	owner := *c
	owner.Scope, owner.Frame = 1, 1
	var err error
	merge.source, err = preflightQuery(&owner, merge.source)
	if err != nil {
		return err
	}
	if err := validateQueryJoinExecution(&owner, merge.source, catalog, 0); err != nil {
		return err
	}
	merge.on, _, err = preflightCoercions(&owner, merge.on)
	if err != nil {
		return err
	}
	if err := validateExpressionJoinExecution(&owner, merge.on, catalog); err != nil {
		return err
	}
	for i := range merge.arms {
		arm := &merge.arms[i]
		arm.condition, _, err = preflightCoercions(&owner, arm.condition)
		if err != nil {
			return err
		}
		if err := validateExpressionJoinExecution(&owner, arm.condition, catalog); err != nil {
			return err
		}
		if arm.action == parser.MergeUpdateAction {
			arm.input.update, err = preflightUpdate(&owner, arm.input.update)
			if err != nil {
				return err
			}
			if err := validateMutationJoinExecution(&owner, &arm.input, nil, catalog); err != nil {
				return err
			}
			continue
		}
		if arm.action == parser.MergeDeleteAction || arm.action == parser.MergeNothingAction {
			continue
		}
		for j, expression := range arm.input.expressions[0] {
			if len(arm.input.insert.identityDiscard) != 0 && arm.input.insert.identityDiscard[arm.input.targets[j]] {
				continue
			}
			folded, _, err := preflightCoercions(&owner, expression)
			if err != nil {
				return insertValueError(catalog, err, arm.input.relation.Columns[arm.input.targets[j]], arm.syntax[j], nil)
			}
			arm.input.expressions[0][j] = folded
		}
		for j, expression := range arm.input.insert.defaults {
			if expression == nil {
				continue
			}
			defaultOwner := owner
			applyDefaultOrigin(bindingDefaultOrigin(&arm.input, j), &defaultOwner)
			arm.input.insert.defaults[j], _, err = preflightCoercionsWithPolicy(&defaultOwner, expression, true)
			if err != nil {
				return err
			}
		}
		if err := validateMutationJoinExecution(&owner, &arm.input, arm.input.expressions, catalog); err != nil {
			return err
		}
	}
	if merge.hasBySource && merge.hasByTarget {
		var facts fullJoinFacts
		var err error
		properties := merge.on.Properties()
		_, literal := merge.on.(plan.LiteralExpr)
		if merge.on.ResultUsage().Type.BaseType().ID() != sqltype.Bool && (literal || properties.Volatility == expr.Immutable && !properties.RowDependent && !properties.StageDependent && !properties.RuntimeDomainConversion) {
			// MERGE admits integer/TEXT carriers independently of Boolean JOIN
			// predicates. Preserve that truth contract in constant capability facts
			// without replacing the expression or its analyzed dependencies.
			var value sqltype.Value
			value, err = merge.on.Eval(&owner)
			if err == nil {
				facts.truth, err = mergeOnTruth(value)
				facts.constant = true
			}
		} else {
			facts, err = fullJoinCondition(&owner, &plan.JoinNode{LeftWidth: merge.targetWidth, RightWidth: merge.sourceWidth}, merge.on, false)
		}
		if err != nil {
			return err
		}
		if !facts.constant && len(facts.keys) == 0 {
			return sqlError("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
		}
	}
	return nil
}

func mergeOnTruth(value sqltype.Value) (plan.Truth, error) {
	if value.Type().IsDomain() {
		var err error
		value, err = value.BaseValue()
		if err != nil {
			return plan.False, err
		}
	}
	if value.IsNull() {
		return plan.Unknown, nil
	}
	switch value.Type().ID() {
	case sqltype.Bool:
		return plan.TruthOf(value)
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		i, err := value.Integer()
		if err != nil {
			return plan.False, err
		}
		if i != 0 {
			return plan.True, nil
		}
		return plan.False, nil
	case sqltype.Text:
		return plan.True, nil
	}
	return plan.False, fmt.Errorf("stuntgres: invalid MERGE ON carrier")
}

func mergeHasVirtualReads(merge *boundMerge) bool {
	if exec.QueryHasVirtualReads(merge.source) || exec.BoundHasVirtualReads(merge.on) {
		return true
	}
	for _, arm := range merge.arms {
		if exec.BoundHasVirtualReads(arm.condition) {
			return true
		}
		if arm.input.update != nil {
			for _, expr := range arm.input.update.assignments {
				if exec.BoundHasVirtualReads(expr) {
					return true
				}
			}
			for _, target := range arm.input.update.arrayTargets {
				if assignmentTargetHasVirtualReads(target) {
					return true
				}
			}
		}
		for _, row := range arm.input.expressions {
			for _, expr := range row {
				if exec.BoundHasVirtualReads(expr) {
					return true
				}
			}
		}
	}
	return false
}

func (e *Engine) executeMerge(ctx context.Context, scope *StatementScope, b binding, target, inputs *exec.Source, parameters []sqltype.Value, schema string) (Result, error) {
	defer target.Close()
	defer inputs.Close()
	unique, err := scope.bindUniqueWrite(b.ref)
	if err != nil {
		return Result{}, err
	}
	if err := exec.SelectQuerySources(ctx, inputs); err != nil {
		return Result{}, err
	}
	state := &exec.State{Source: inputs, Parameters: parameters, Input: scope.input, Output: scope.output, Locking: inputs.Locks != nil, VirtualReads: mergeHasVirtualReads(b.merge)}
	if b.returning != nil {
		for _, expr := range b.returning.expressions {
			state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expr)
		}
	}
	defer state.ReleaseMemberships()
	cursor, err := exec.OpenNode(ctx, state, b.merge.source)
	if err != nil {
		return Result{}, err
	}
	rows, err := e.assembleMergeRows(ctx, scope, b, target, &cursor, state, schema, len(unique.keys) != 0 || unique.checked)
	if err != nil {
		return Result{}, err
	}
	complete := false
	defer func() {
		if !complete && rows.returning != nil {
			rows.returning.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := rows.stage(scope.Writes(), b.ref); err != nil {
		return Result{}, err
	}
	if rows.returning != nil && b.returning.regclassOutput && rows.returning.Count != 0 {
		if err := e.captureRegclassOutput(scope.op, rows.returning); err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	complete = true
	result := Result{Count: int64(len(rows.actions))}
	if rows.returning != nil {
		result.Rows = rows.returning
	}
	return result, nil
}
