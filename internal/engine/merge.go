package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type boundMerge struct {
	source        LogicalNode
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

func mergeRelationKind(relation *relationDescriptor) error {
	if relation.relationKind == tableRelation || relation.relationKind == partitionedRelation {
		return nil
	}
	if relation.relationKind == viewRelation {
		return sqlError("0A000", "MERGE into views is unsupported")
	}
	err := sqlError("0A000", fmt.Sprintf(`cannot execute MERGE on relation "%s"`, relation.name)).(*sqlstate.Error)
	switch relation.relationKind {
	case sequenceRelation:
		err.Detail = "This operation is not supported for sequences."
	case indexRelation:
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

func (e *Engine) bindMerge(out *binding, binder *expressionBinder, auth AuthorizationView, requiredRef ids.ObjectRef, s *parser.Merge) error {
	ref, err := e.admittedReference(auth, s.Name, requiredRef)
	if err != nil {
		return err
	}
	if ref != requiredRef {
		return ids.ErrCatalogReplaced
	}
	out.ref = ref
	out.relation = auth.catalog.MustRelation(ref.Address)
	if !guardedCatalogRelation(out.relation) {
		if err := mergeRelationKind(out.relation); err != nil {
			return err
		}
	}
	out.storage = e.authorizationStorageLocked(auth, ref.Address)
	if out.storage == nil && out.relation.relationKind != partitionedRelation && !guardedCatalogRelation(out.relation) {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	binder.relation, binder.ref = out.relation, ref
	binder.targetName, binder.targetAlias = out.relation.name, s.Alias.Text
	binder.insertTarget = out.relation
	target := binder.mutationTargetNamespace()
	binder.queryHidden = append(binder.queryHidden, target.qualifiers[0])
	sourceHiddenStart := len(binder.queryHidden)
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
	binder.queryNamespace = &combined
	binder.aggregateClause = "JOIN conditions"
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
		wasUpdateParameters := binder.updateParameters
		binder.updateParameters = true
		out.returning, err = binder.bindReturning(s.Returning, returningMerge)
		binder.updateParameters = wasUpdateParameters
		if err != nil {
			return err
		}
		out.returning.readRequired = binder.returningReadRequired
		for number, usage := range binder.parameters {
			if usage.Type != nil {
				if binder.updateInputParameters == nil {
					binder.updateInputParameters = make(map[int]sqltype.Usage, len(binder.parameters))
				}
				binder.updateInputParameters[number] = usage
			}
		}
		outputParameters = binder.updateInputParameters
		defer func() { binder.updateInputParameters = nil }()
	}
	hidden := binder.queryHidden
	var sourceHidden []queryQualifier
	for i, raw := range s.Arms {
		binder.updateInputParameters = outputParameters
		binder.queryHidden = hidden
		binder.queryNamespace = &source
		switch raw.Match {
		case parser.MergeMatched:
			binder.queryNamespace = &combined
		case parser.MergeNotMatchedBySource:
			binder.queryNamespace = &target
			merge.hasBySource = true
		case parser.MergeNotMatched:
			merge.hasByTarget = true
		}
		if raw.Match == parser.MergeNotMatchedBySource {
			binder.queryNamespace = &target
			if sourceHidden == nil {
				sourceHidden = append([]queryQualifier(nil), hidden...)
				// bindFrom already records each source entry, including aliases.
				// Change only its action-local accessibility flag; appending the
				// same entries would turn one missing column into plural ambiguity.
				for i := sourceHiddenStart; i < len(sourceHidden); i++ {
					sourceHidden[i].lateralForbidden = true
				}
			}
			binder.queryHidden = sourceHidden
		}
		arm := &merge.arms[i]
		arm.match, arm.action = raw.Match, raw.Action
		if raw.Condition != nil {
			binder.mergeWhen = true
			binder.aggregateClause = "WHEN"
			arm.condition, err = binder.bindBoolean(raw.Condition, filterContext, "WHEN")
			binder.mergeWhen = false
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
			binder.aggregateClause = "UPDATE"
			u := &boundUpdate{assignments: make([]expr.Expr, len(raw.Assignments))}
			if err := e.bindUpdateAssignments(&arm.input, binder, raw.Assignments, u); err != nil {
				return err
			}
			merge.updateTargets = mergeTargetUnion(merge.updateTargets, arm.input.targets)
		case parser.MergeInsertAction:
			merge.hasInsert = true
			arm.syntax = raw.Values
			insert := parser.Insert{Name: s.Name, Targets: raw.Targets, Rows: [][]parser.Expr{raw.Values}, DefaultValues: raw.DefaultValues, Override: raw.Override, Pos: raw.ActionPos}
			binder.mergeInput = true
			err = e.bindInsertInput(&arm.input, binder, auth, &insert)
			binder.mergeInput = false
			if err != nil {
				return err
			}
			if len(arm.input.insertProjection) == 0 {
				arm.input.insertProjection = make([]int, len(out.relation.columns))
				for j := range arm.input.insertProjection {
					arm.input.insertProjection[j] = -1
				}
				for j, target := range arm.input.targets {
					arm.input.insertProjection[target] = j
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
	binder.queryHidden = hidden
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
func preflightMerge(c *EvalContext, merge *boundMerge, catalog CatalogRead) error {
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
			if len(arm.input.identityDiscard) != 0 && arm.input.identityDiscard[arm.input.targets[j]] {
				continue
			}
			folded, _, err := preflightCoercions(&owner, expression)
			if err != nil {
				return insertValueError(catalog, err, arm.input.relation.columns[arm.input.targets[j]], arm.syntax[j], nil)
			}
			arm.input.expressions[0][j] = folded
		}
		for j, expression := range arm.input.defaults {
			if expression == nil {
				continue
			}
			defaultOwner := owner
			attribute := arm.input.relation.attributes[j]
			defaultOwner.quoteOrigin = quoteOrigin{owner: arm.input.ref, attribute: attribute, definition: arm.input.relation.defaults[attribute].OID}
			arm.input.defaults[j], _, err = preflightCoercionsWithPolicy(&defaultOwner, expression, true)
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
		_, literal := merge.on.(literalExpression)
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
			facts, err = fullJoinCondition(&owner, &joinNode{leftWidth: merge.targetWidth, rightWidth: merge.sourceWidth}, merge.on, false)
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

func mergeOnTruth(value sqltype.Value) (Truth, error) {
	if value.Type().IsDomain() {
		var err error
		value, err = value.BaseValue()
		if err != nil {
			return False, err
		}
	}
	if value.IsNull() {
		return Unknown, nil
	}
	switch value.Type().ID() {
	case sqltype.Bool:
		return TruthOf(value)
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		i, err := value.Integer()
		if err != nil {
			return False, err
		}
		if i != 0 {
			return True, nil
		}
		return False, nil
	case sqltype.Text:
		return True, nil
	}
	return False, fmt.Errorf("stuntgres: invalid MERGE ON carrier")
}

func (merge *boundMerge) hasVirtualReads() bool {
	if queryHasVirtualReads(merge.source) || boundHasVirtualReads(merge.on) {
		return true
	}
	for _, arm := range merge.arms {
		if boundHasVirtualReads(arm.condition) {
			return true
		}
		if arm.input.update != nil {
			for _, expr := range arm.input.update.assignments {
				if boundHasVirtualReads(expr) {
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
				if boundHasVirtualReads(expr) {
					return true
				}
			}
		}
	}
	return false
}

func (e *Engine) executeMerge(ctx context.Context, scope *StatementScope, b binding, target, inputs *querySource, parameters []sqltype.Value, schema string) (Result, error) {
	defer target.close()
	defer inputs.close()
	unique, err := scope.bindUniqueWrite(b.ref)
	if err != nil {
		return Result{}, err
	}
	if err := selectQuerySources(ctx, inputs); err != nil {
		return Result{}, err
	}
	state := &queryState{source: inputs, parameters: parameters, input: scope.input, output: scope.output, locking: inputs.locks != nil, virtualReads: b.merge.hasVirtualReads()}
	if b.returning != nil {
		for _, expr := range b.returning.expressions {
			state.virtualReads = state.virtualReads || boundHasVirtualReads(expr)
		}
	}
	defer state.releaseMemberships()
	cursor, err := openQueryNode(ctx, state, b.merge.source)
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
	if rows.returning != nil && b.returning.regclassOutput && rows.returning.count != 0 {
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
