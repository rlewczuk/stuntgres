package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) bindCreateGenerations(op *session.Operation, auth AuthorizationView, d *catalog.Relation, declarations []analyze.ColumnDeclaration, input sqltype.InputEnvironment) error {
	for slot, declaration := range declarations {
		if declaration.Generation != nil {
			if d.ColumnMeta.Generations == nil {
				d.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration)
			}
			kind := catalog.GenerationVirtual
			if declaration.Generation.Kind == "stored" {
				kind = catalog.GenerationStored
			}
			d.ColumnMeta.Generations[d.Attributes[slot]] = catalog.ColumnGeneration{Kind: kind}
		}
	}
	if len(d.ColumnMeta.Generations) == 0 {
		return nil
	}
	ns := e.generationNamespace(auth, d)
	for slot, declaration := range declarations {
		if declaration.Generation == nil {
			continue
		}
		definition, err := e.bindGeneration(op, auth, d, slot, declaration.Generation.Expression, input, &ns)
		if err != nil {
			return err
		}
		d.ColumnMeta.Generations[d.Attributes[slot]] = definition
	}
	return nil
}

func (e *Engine) generationNamespace(auth AuthorizationView, d *catalog.Relation) analyze.QueryNamespace {
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	columns := make([]analyze.QueryColumn, len(d.Columns))
	for i, c := range d.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: c, Slot: i, Leaf: 0, Physical: i}
	}
	qualifier := analyze.QueryQualifier{Name: d.Name(), Original: d.Name(), Schema: auth.Catalog.Get(d.Namespace()).Name(), Ref: ref, Columns: columns}
	return analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: d.Columns}
}

// CREATE and ALTER share binding, security, coercion and provider capture.
func (e *Engine) bindGeneration(op *session.Operation, auth AuthorizationView, d *catalog.Relation, slot int, expression parser.Expr, input sqltype.InputEnvironment, ns *analyze.QueryNamespace) (catalog.ColumnGeneration, error) {
	b := newExpressionBinder(op.Context(), e, input, op, nil)
	b.Target.GeneratedOwner = d
	b.Params.NoParameters = true
	b.Query.Auth = &auth
	b.Query.Namespace = ns
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	b.Query.Sources = []exec.Leaf{{Ref: ref, Relation: d}}
	column := d.Columns[slot]
	kind := d.ColumnMeta.Generations[d.Attributes[slot]].Kind
	if kind == catalog.GenerationVirtual {
		if err := virtualDestination(column); err != nil {
			return catalog.ColumnGeneration{}, err
		}
	}
	node, err := b.Bind(expression, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: column.Usage.Type.Usage()})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	analyzed := node
	if kind == catalog.GenerationVirtual {
		if err := virtualGenerationSecurity(analyzed); err != nil {
			return catalog.ColumnGeneration{}, err
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: b.Input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
	node, _, err = exec.SimplifyConditional(&evaluation, node, true)
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	if !plan.GenerationImmutable(node) {
		return catalog.ColumnGeneration{}, sqlError("42P17", "generation expression is not immutable")
	}
	if node.ResultUsage() != column.Usage {
		input, annotations := analyze.DomainCoercionInput(node, column.Usage)
		info, failure := analyze.StorageCoercionInfo(b.BindingCatalog(), b.Info(column.Usage, parser.Position{}, input.Properties()), input)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		coercionPlan, failure := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if failure != nil {
			diagnostic := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
			diagnostic.Hint = "You will need to rewrite or cast the expression."
			return catalog.ColumnGeneration{}, diagnostic
		}
		info.Property = analyze.CoercionProperties(input, coercionPlan)
		node = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
		analyzedInput, analyzedAnnotations := analyze.DomainCoercionInput(analyzed, column.Usage)
		analyzedInfo, failure := analyze.StorageCoercionInfo(b.BindingCatalog(), b.Info(column.Usage, parser.Position{}, analyzedInput.Properties()), analyzedInput)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		analyzedPlan, failure := sqltype.PlanCoercion(analyzedInput.ResultUsage(), analyzedInfo.Usage, sqltype.Assignment)
		if failure != nil {
			return catalog.ColumnGeneration{}, failure
		}
		analyzedInfo.Property = analyze.CoercionProperties(analyzedInput, analyzedPlan)
		analyzed = analyze.RestoreDomainCastCollations(analyzedAnnotations, plan.CoercionExpr{Info: analyzedInfo, Child: analyzedInput, Plan: analyzedPlan, Mode: sqltype.Assignment})
	}
	node, err = analyze.StorageDestination(node, column.Usage, parser.Position{})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	analyzed, err = analyze.StorageDestination(analyzed, column.Usage, parser.Position{})
	if err != nil {
		return catalog.ColumnGeneration{}, err
	}
	definition := catalog.ColumnGeneration{Kind: kind, Expression: analyzed, Planned: node}
	_, err = (stored.ScalarRenderer{Definition: op.Definition, ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: d, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot}).SQL(op.Context(), auth.Catalog, analyzed)
	return definition, err
}
