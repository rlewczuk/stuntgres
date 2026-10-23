package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// prepareNamespaces uses retained target aliases and the same frozen source
// namespace/slot authority as SELECT display. Replay keeps its existing target
// naming and positional source bridges.
func (a *ruleRendering) prepareNamespaces(ctx context.Context, view catalog.Read) error {
	a.sourceRoot, a.sourceSlots, a.sourceText = nil, nil, ""
	a.targetAlias, a.names = "", nil
	if !a.policy.Rule {
		return nil
	}
	switch syntax := a.syntax.(type) {
	case *parser.Insert:
		a.targetAlias = syntax.Alias.Text
	case *parser.Update:
		a.targetAlias = syntax.Alias.Text
	case *parser.Delete:
		a.targetAlias = syntax.Alias.Text
	}
	r := a.renderer(a.presentation)
	var project *plan.ProjectNode
	if a.presentation != nil {
		project, _ = r.query.Root.(*plan.ProjectNode)
		if project == nil {
			return fmt.Errorf("stuntgres: missing mutation source frame")
		}
		if err := r.prepareRuleRangeTable(ctx, view, project); err != nil {
			return err
		}
		a.names = r.ruleRootNames
	} else {
		table, err := r.initialRuleRangeTable(ctx, view, 1)
		if err != nil {
			return err
		}
		a.names = &table
	}
	relation, ok := view.Relation(a.binding.Ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if a.targetAlias != "" || a.names.target != relation.Name() {
		a.targetAlias = a.names.target
	}
	if a.binding.Update != nil {
		a.sourceRoot = a.binding.Update.From
	} else {
		a.sourceRoot = a.binding.Delete.Using
	}
	if a.sourceRoot == nil {
		return nil
	}
	if a.presentation == nil || a.presentation.syntax == nil || a.presentation.syntax.From == nil {
		return fmt.Errorf("stuntgres: missing retained mutation source presentation")
	}
	r.Frame = project.Frame
	r.namespace = r.renderFrames[r.Frame].namespace
	r.functionSources = true
	from, err := r.fromSQL(ctx, view, a.presentation.syntax.From)
	if err != nil {
		return err
	}
	a.sourceSlots, a.sourceText = from.slots, from.text
	return nil
}

func (a ruleRendering) targetDeclaration(name string) string {
	if a.policy.Rule {
		only := false
		switch syntax := a.syntax.(type) {
		case *parser.Update:
			only = syntax.Only
		case *parser.Delete:
			only = syntax.Only
		}
		if only {
			name = "ONLY " + name
		}
	}
	if a.targetAlias == "" {
		return name
	}
	separator := " "
	if _, insert := a.syntax.(*parser.Insert); insert {
		separator = " AS "
	}
	return name + separator + a.policy.identifier(a.targetAlias)
}

func (a ruleRendering) targetQualifier(relation *catalog.Relation) string {
	if a.targetAlias != "" {
		return a.policy.identifier(a.targetAlias)
	}
	return a.policy.identifier(relation.Name())
}

func ruleReturningNatural(view catalog.Read, expression expr.Expr, action ruleRendering) (string, error) {
	var column plan.ColumnExpr
	switch value := expression.(type) {
	case plan.ColumnExpr:
		column = value
	case plan.ReturningColumnExpr:
		column = value.ColumnExpr
	default:
		return "", nil
	}
	address := column.Origin.Address
	address.SubID = 0
	relation, ok := view.Relation(address)
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	return action.attribute(relation, column.Origin.Address.SubID)
}
