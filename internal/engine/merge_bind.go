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

func bindMerge(out *binding, binder *expressionBinder, auth analyze.Authority, requiredRef ids.ObjectRef, s *parser.Merge) error {
	ref, err := binder.host.AdmittedReference(auth, s.Name, requiredRef)
	if err != nil {
		return err
	}
	if ref != requiredRef {
		return ids.ErrCatalogReplaced
	}
	out.Ref = ref
	out.Relation = auth.ReadCatalog().MustRelation(ref.Address)
	if !guardedCatalogRelation(out.Relation) {
		if err := mergeRelationKind(out.Relation); err != nil {
			return err
		}
	}
	out.Storage = binder.host.AuthorizationStorage(auth, ref.Address)
	if out.Storage == nil && out.Relation.RelationKind != catalog.PartitionedRelation && !guardedCatalogRelation(out.Relation) {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	binder.target.relation, binder.target.ref = out.Relation, ref
	binder.target.name, binder.target.alias = out.Relation.Name(), s.Alias.Text
	binder.target.insertTarget = out.Relation
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
	merge := &boundMerge{Source: root, On: on, Arms: make([]boundMergeArm, len(s.Arms)), TargetWidth: len(target.frame), SourceWidth: len(source.frame)}
	var outputParameters map[int]sqltype.Usage
	if s.Returning != nil {
		wasUpdateParameters := binder.params.updateParameters
		binder.params.updateParameters = true
		out.Returning, err = binder.bindReturning(s.Returning, returningMerge)
		binder.params.updateParameters = wasUpdateParameters
		if err != nil {
			return err
		}
		out.Returning.ReadRequired = binder.target.returningReadRequired
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
			merge.HasBySource = true
		case parser.MergeNotMatched:
			merge.HasByTarget = true
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
		arm := &merge.Arms[i]
		arm.Match, arm.Action = raw.Match, raw.Action
		if raw.Condition != nil {
			binder.target.mergeWhen = true
			binder.clause.aggregateClause = "WHEN"
			arm.Condition, err = binder.bindBoolean(raw.Condition, filterContext, "WHEN")
			binder.target.mergeWhen = false
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
			binder.clause.aggregateClause = "UPDATE"
			u := &boundUpdate{Assignments: make([]expr.Expr, len(raw.Assignments))}
			if err := bindUpdateAssignments(&arm.Input, binder, raw.Assignments, u); err != nil {
				return err
			}
			merge.UpdateTargets = mergeTargetUnion(merge.UpdateTargets, arm.Input.Targets)
		case parser.MergeInsertAction:
			merge.HasInsert = true
			arm.Syntax = raw.Values
			insert := parser.Insert{Name: s.Name, Targets: raw.Targets, Rows: [][]parser.Expr{raw.Values}, DefaultValues: raw.DefaultValues, Override: raw.Override, Pos: raw.ActionPos}
			binder.target.mergeInput = true
			err = bindInsertInput(&arm.Input, binder, auth, &insert)
			binder.target.mergeInput = false
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
	binder.query.hidden = hidden
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
