package stored

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// PolicyCarrier retains the actual immutable condition and its complete frozen
// presentation. It never consults a replacement policy with the same name/OID.
type PolicyCarrier struct{ Policy *Policy }

func PolicyTree(ctx context.Context, view catalog.Read, relation *catalog.Relation, typ *sqltype.Descriptor, payload catalog.PolicyPayload) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if view.Owner() == nil || view.State() == nil || relation == nil || view.Get(relation.Address()) != relation {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign policy owner")
	}
	captured, ok := view.LookupType(sqltype.PgNodeTree)
	if !ok || captured != typ {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign policy tree type")
	}
	if payload == nil {
		return sqltype.Null(typ), nil
	}
	policy := PolicyOf(payload)
	if policy == nil || policy.presentation == nil || policy.expression == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign policy payload")
	}
	found := false
	for _, definition := range relation.Security.Policies {
		if PolicyOf(definition.Using) == policy || PolicyOf(definition.Check) == policy {
			found = true
			break
		}
	}
	if !found {
		for _, mapping := range view.PublicationRelations() {
			if mapping.Relation == relation.Address() && PolicyOf(mapping.Filter) == policy {
				found = true
				break
			}
		}
	}
	if !found {
		return sqltype.Value{}, fmt.Errorf("stuntgres: detached policy payload")
	}
	if err := policy.Validate(view, relation); err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Tree(typ, PolicyCarrier{Policy: policy})
}

func (t PolicyCarrier) StructuralOutput(ctx context.Context) (string, error) {
	if t.Policy == nil || t.Policy.presentation == nil {
		return "", fmt.Errorf("stuntgres: missing policy carrier")
	}
	text, err := (plan.BoundTree{Expression: t.Policy.expression}).StructuralOutput(ctx)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return text, nil
}

// PolicyDisplay uses the same frozen query renderer as replay, with a private
// display environment. PolicySQL intentionally retains its qualified dump SQL.
func PolicyDisplay(ctx context.Context, view catalog.Read, relation *catalog.Relation, carrier PolicyCarrier, presentation Presentation) (string, error) {
	policy := carrier.Policy
	if policy == nil || policy.presentation == nil {
		return "", fmt.Errorf("stuntgres: foreign policy carrier")
	}
	if relation == nil && policy.expression.Properties().RowDependent {
		return "", sqlError("22023", "expression contains variables")
	}
	r := NewQueryRenderer(policy.presentation)
	presentation.Deparse, presentation.Rule, presentation.Policy = true, true, true
	presentation.Wrap = 0
	r.Presentation = presentation
	r.CurrentNames = true
	r.Frame = 1
	source := analyze.ViewFromRoot(r.frames[1])
	leaf := r.sources[0]
	if relation == nil {
		relation, _ = view.Relation(leaf.Ref.Address)
	}
	if relation == nil {
		return "", fmt.Errorf("stuntgres: policy owner is missing")
	}
	r.PolicyRelation = relation
	r.reserved[relation.Name()] = true
	// A policy's frozen root is an expression owner, not a SELECT: it has
	// no FROM syntax to walk. Its actual owner name is nevertheless an outer
	// range-table reservation inherited by every retained subquery. Install
	// that context before the ordinary renderer visits the subquery's own
	// captured SELECT frames; never synthesize a query or rebind its names.
	root, ok := policy.presentation.query.Root.(*plan.ProjectNode)
	if !ok || root != r.frames[1] || root.Syntax != nil || analyze.ViewFromRoot(root) != source {
		return "", fmt.Errorf("stuntgres: invalid policy presentation frame")
	}
	owner, ok := source.(*plan.SourceNode)
	if !ok || owner.Ref != leaf.Ref || owner.Occurrence != 0 {
		return "", fmt.Errorf("stuntgres: invalid policy presentation owner")
	}
	names := ruleRangeTable{used: map[string]int{relation.Name(): 0}}
	r.ruleTables = map[uint32]ruleRangeTable{root.Frame: names}
	r.ruleRootNames = &names
	slots := make(map[int]string, len(leaf.Attributes))
	for i, attribute := range leaf.Attributes {
		for j, current := range relation.Attributes {
			if current == attribute {
				slots[i] = presentation.identifier(relation.Name()) + "." + presentation.identifier(relation.Columns[j].Name)
				break
			}
		}
	}
	r.Slots[source] = slots
	text, err := r.ExpressionSQL(ctx, view, policy.syntax, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}
