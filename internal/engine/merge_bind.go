package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

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

func bindMerge(out *binding, binder *expressionBinder, auth analyze.Authority, requiredRef ids.ObjectRef, s *parser.Merge) error {
	ref, err := binder.Host.AdmittedReference(auth, s.Name, requiredRef)
	if err != nil {
		return err
	}
	if ref != requiredRef {
		return ids.ErrCatalogReplaced
	}
	out.Ref = ref
	out.Relation = auth.ReadCatalog().MustRelation(ref.Address)
	if !analyze.GuardedCatalogRelation(out.Relation) {
		if err := mergeRelationKind(out.Relation); err != nil {
			return err
		}
	}
	out.Storage = binder.Host.AuthorizationStorage(auth, ref.Address)
	if out.Storage == nil && out.Relation.RelationKind != catalog.PartitionedRelation && !analyze.GuardedCatalogRelation(out.Relation) {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	binder.Target.Relation, binder.Target.Ref = out.Relation, ref
	binder.Target.Name, binder.Target.alias = out.Relation.Name(), s.Alias.Text
	binder.Target.InsertTarget = out.Relation
	target := binder.mutationTargetNamespace()
	binder.Query.Hidden = append(binder.Query.Hidden, target.Qualifiers[0])
	sourceHiddenStart := len(binder.Query.Hidden)
	root, source, err := binder.BindFrom(s.Source)
	if err != nil {
		return err
	}
	for _, q := range source.Qualifiers {
		t := target.Qualifiers[0]
		if t.Name == q.Name && (t.Alias != "" || q.Alias != "" || t.Ref == q.Ref || q.Ref == (ids.ObjectRef{})) {
			d := sqlError("42712", fmt.Sprintf(`name "%s" specified more than once`, t.Name)).(*sqlstate.Error)
			d.Detail = "The name is used both as MERGE target table and data source."
			return d
		}
	}
	combined, err := combineQueryNamespace(target, rebaseQueryNamespace(source, len(target.Frame)))
	if err != nil {
		return err
	}
	binder.Query.Namespace = &combined
	binder.Clause.AggregateClause = "JOIN conditions"
	on, err := binder.Bind(s.On, expressionPolicy{Context: ProjectionContext})
	if err != nil {
		return err
	}
	on, err = binder.FinalizeProjection(on)
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
	merge := &boundMerge{Source: root, On: on, Arms: make([]boundMergeArm, len(s.Arms)), TargetWidth: len(target.Frame), SourceWidth: len(source.Frame)}
	var outputParameters map[int]sqltype.Usage
	if s.Returning != nil {
		wasUpdateParameters := binder.Params.UpdateParameters
		binder.Params.UpdateParameters = true
		out.Returning, err = binder.BindReturning(s.Returning, returningMerge)
		binder.Params.UpdateParameters = wasUpdateParameters
		if err != nil {
			return err
		}
		out.Returning.ReadRequired = binder.Target.returningReadRequired
		for number, usage := range binder.Params.Parameters {
			if usage.Type != nil {
				if binder.Params.updateInputParameters == nil {
					binder.Params.updateInputParameters = make(map[int]sqltype.Usage, len(binder.Params.Parameters))
				}
				binder.Params.updateInputParameters[number] = usage
			}
		}
		outputParameters = binder.Params.updateInputParameters
		defer func() { binder.Params.updateInputParameters = nil }()
	}
	hidden := binder.Query.Hidden
	var sourceHidden []queryQualifier
	for i, raw := range s.Arms {
		binder.Params.updateInputParameters = outputParameters
		binder.Query.Hidden = hidden
		binder.Query.Namespace = &source
		switch raw.Match {
		case parser.MergeMatched:
			binder.Query.Namespace = &combined
		case parser.MergeNotMatchedBySource:
			binder.Query.Namespace = &target
			merge.HasBySource = true
		case parser.MergeNotMatched:
			merge.HasByTarget = true
		}
		if raw.Match == parser.MergeNotMatchedBySource {
			binder.Query.Namespace = &target
			if sourceHidden == nil {
				sourceHidden = append([]queryQualifier(nil), hidden...)
				// bindFrom already records each source entry, including aliases.
				// Change only its action-local accessibility flag; appending the
				// same entries would turn one missing column into plural ambiguity.
				for i := sourceHiddenStart; i < len(sourceHidden); i++ {
					sourceHidden[i].LateralForbidden = true
				}
			}
			binder.Query.Hidden = sourceHidden
		}
		arm := &merge.Arms[i]
		arm.Match, arm.Action = raw.Match, raw.Action
		if raw.Condition != nil {
			binder.Target.mergeWhen = true
			binder.Clause.AggregateClause = "WHEN"
			arm.Condition, err = binder.BindBoolean(raw.Condition, FilterContext, "WHEN")
			binder.Target.mergeWhen = false
			if err != nil {
				return err
			}
		}
		arm.Input = binding{Ref: ref, Relation: out.Relation, Storage: out.Storage}
		switch raw.Action {
		case parser.MergeNothingAction:
			merge.HasNothing = true
		case parser.MergeDeleteAction:
			merge.HasDelete = true
		case parser.MergeUpdateAction:
			binder.Clause.AggregateClause = "UPDATE"
			u := &boundUpdate{Assignments: make([]expr.Expr, len(raw.Assignments))}
			if err := bindUpdateAssignments(&arm.Input, binder, raw.Assignments, u); err != nil {
				return err
			}
			merge.UpdateTargets = mergeTargetUnion(merge.UpdateTargets, arm.Input.Targets)
		case parser.MergeInsertAction:
			merge.HasInsert = true
			arm.Syntax = raw.Values
			insert := parser.Insert{Name: s.Name, Targets: raw.Targets, Rows: [][]parser.Expr{raw.Values}, DefaultValues: raw.DefaultValues, Override: raw.Override, Pos: raw.ActionPos}
			binder.Target.mergeInput = true
			err = bindInsertInput(&arm.Input, binder, auth, &insert)
			binder.Target.mergeInput = false
			if err != nil {
				return err
			}
			if len(arm.Input.Insert.Projection) == 0 {
				arm.Input.Insert.Projection = make([]int, len(out.Relation.Columns))
				for j := range arm.Input.Insert.Projection {
					arm.Input.Insert.Projection[j] = -1
				}
				for j, target := range arm.Input.Targets {
					arm.Input.Insert.Projection[target] = j
				}
			}
			merge.InsertTargets = mergeTargetUnion(merge.InsertTargets, arm.Input.Targets)
		}
		for _, target := range arm.Input.Targets {
			found := false
			for _, prior := range out.Targets {
				if target == prior {
					found = true
					break
				}
			}
			if !found {
				out.Targets = append(out.Targets, target)
			}
		}
	}
	binder.Query.Hidden = hidden
	out.Merge = merge
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
