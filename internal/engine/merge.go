package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type boundMerge struct {
	source LogicalNode
	on     BoundExpr
	arms   []boundMergeInsert
}

type boundMergeInsert struct {
	condition BoundExpr
	input     binding
	syntax    []parser.Expr
}

func mergeRelationKind(relation *relationDescriptor) error {
	if relation.relationKind == tableRelation {
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

func (e *Engine) bindMerge(out *binding, binder *expressionBinder, auth AuthorizationView, requiredRef ObjectRef, s *parser.Merge) error {
	ref, err := e.resolveReference(auth, s.Name)
	if err != nil {
		return err
	}
	if ref != requiredRef {
		return ErrCatalogReplaced
	}
	out.ref = ref
	out.relation = auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if err := mergeRelationKind(out.relation); err != nil {
		return err
	}
	out.storage = e.authorizationStorageLocked(auth, ref.Address)
	if out.storage == nil {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	binder.relation, binder.ref = out.relation, ref
	binder.targetName, binder.targetAlias = out.relation.name, s.Alias.Text
	binder.insertTarget = out.relation
	target := binder.mutationTargetNamespace()
	binder.queryHidden = append(binder.queryHidden, target.qualifiers[0])
	root, source, err := binder.bindFrom(s.Source)
	if err != nil {
		return err
	}
	for _, q := range source.qualifiers {
		t := target.qualifiers[0]
		if t.name == q.name && (t.alias != "" || q.alias != "" || t.ref == q.ref || q.ref == (ObjectRef{})) {
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
		return expressionError("0A000", "MERGE ON type is unsupported", s.On.SourcePosition())
	}
	merge := &boundMerge{source: root, on: on, arms: make([]boundMergeInsert, len(s.Arms))}
	binder.queryNamespace = &source
	unconditional := false
	for i, raw := range s.Arms {
		if unconditional {
			return sqlError("42601", "unreachable WHEN clause specified after unconditional WHEN clause")
		}
		arm := &merge.arms[i]
		if raw.Condition != nil {
			binder.mergeWhen = true
			binder.aggregateClause = "WHEN"
			arm.condition, err = binder.bindBoolean(raw.Condition, filterContext, "WHEN")
			binder.mergeWhen = false
			if err != nil {
				return err
			}
		} else {
			unconditional = true
		}
		arm.input = binding{ref: ref, relation: out.relation, storage: out.storage}
		arm.syntax = raw.Values
		insert := parser.Insert{Name: s.Name, Targets: raw.Targets, Rows: [][]parser.Expr{raw.Values}, DefaultValues: raw.DefaultValues, Override: raw.Override, Pos: raw.ActionPos}
		binder.mergeInput = true
		err = e.bindInsertInput(&arm.input, binder, auth, &insert)
		binder.mergeInput = false
		if err != nil {
			return err
		}
		// One declaration-order projection serves both identity and ordinary
		// rows; the shared evaluator preserves per-attribute default demand.
		if len(arm.input.insertProjection) == 0 {
			arm.input.insertProjection = make([]int, len(out.relation.columns))
			for j := range arm.input.insertProjection {
				arm.input.insertProjection[j] = -1
			}
			for j, target := range arm.input.targets {
				arm.input.insertProjection[target] = j
			}
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
	out.merge = merge
	return nil
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
			arm.input.defaults[j], _, err = preflightCoercionsWithPolicy(&owner, expression, true)
			if err != nil {
				return err
			}
		}
		if err := validateMutationJoinExecution(&owner, &arm.input, arm.input.expressions, catalog); err != nil {
			return err
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

func (e *Engine) executeMerge(ctx context.Context, scope *StatementScope, b binding, target, inputs *querySource, parameters []sqltype.Value, schema string) (Result, error) {
	defer target.close()
	defer inputs.close()
	if err := selectQuerySources(ctx, inputs); err != nil {
		return Result{}, err
	}
	unique, err := scope.bindUniqueWrite(b.ref)
	if err != nil {
		return Result{}, err
	}
	state := &queryState{source: inputs, parameters: parameters, input: scope.input, output: scope.output, locking: inputs.locks != nil}
	cursor, err := openQueryNode(ctx, state, b.merge.source)
	if err != nil {
		return Result{}, err
	}
	rows, err := e.assembleMergeRows(ctx, scope, b, target, &cursor, state, schema, len(unique.keys) != 0)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := scope.Writes().applyValidated(b.ref, rows); err != nil {
		return Result{}, err
	}
	return Result{Count: int64(len(rows))}, nil
}

func (e *Engine) assembleMergeRows(ctx context.Context, scope *StatementScope, b binding, target *querySource, cursor RowCursor, state *queryState, schema string, unique bool) (rows [][]sqltype.Value, err error) {
	defer func() {
		closeErr := cursor.Close()
		if err == nil {
			err = closeErr
			if err == nil {
				err = ctx.Err()
			}
		}
		if err != nil {
			rows = nil
		}
	}()
	sourceRow := make([]sqltype.Value, len(cursor.Schema()))
	frame := make([]sqltype.Value, len(b.relation.columns)+len(sourceRow))
	onContext := state.evaluation(ctx)
	onContext.Row = frame
	sourceContext := state.evaluation(ctx)
	sourceContext.Row = sourceRow
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		present, err := cursor.Next(ctx, sourceRow)
		if err != nil {
			return nil, err
		}
		if !present {
			break
		}
		copy(frame[len(b.relation.columns):], sourceRow)
		matched := false
		for i := range target.count() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			copy(frame, target.row(i))
			value, err := b.merge.on.Eval(&onContext)
			if err != nil {
				return nil, err
			}
			truth, err := mergeOnTruth(value)
			if err != nil {
				return nil, err
			}
			if truth == True {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		for i := range b.merge.arms {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			arm := &b.merge.arms[i]
			if arm.condition != nil {
				value, err := arm.condition.Eval(&sourceContext)
				if err != nil {
					return nil, err
				}
				truth, err := TruthOf(value)
				if err != nil {
					return nil, err
				}
				if truth != True {
					continue
				}
			}
			row := make([]sqltype.Value, len(b.relation.columns))
			for j, c := range b.relation.columns {
				row[j] = sqltype.Null(c.Usage.Type)
			}
			if err := evaluateIdentityValuesRow(scope.commentCatalog, &arm.input, arm.input.expressions[0], arm.syntax, row, false, &sourceContext); err != nil {
				return nil, err
			}
			// Generation/CHECK evaluation changes its row/frame. It must not
			// overwrite the source context for the next source occurrence.
			rowContext := sourceContext
			if err := e.validateInsertRow(scope, b, schema, row, &rowContext); err != nil {
				return nil, err
			}
			if unique {
				if err := scope.checkUniqueRow(b.ref, row, nil, rows); err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
			break
		}
	}
	return rows, nil
}
