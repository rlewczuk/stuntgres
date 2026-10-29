package stored

import (
	"context"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// ruleRendering is request-local presentation state, never part of a stored
// action. Replay and catalog display share the same frozen binding traversal.
type ruleRendering struct {
	RuleAction
	policy             Presentation
	temporaryOwner     ids.SessionToken
	targetAlias        string
	sourceRoot         plan.Node
	sourceSlots        map[int]string
	sourceQualifiers   map[int]string
	sourceText         string
	inference          bool
	returning          bool
	returningNames     map[parser.Position]string
	names, parentNames *ruleRangeTable
}

func (a ruleRendering) clause(replay string, offset int, display string) string {
	if a.policy.Deparse {
		return a.policy.line(offset) + display
	}
	return replay
}

func (a *ruleRendering) renderer(v *View) *QueryRenderer {
	var r *QueryRenderer
	if v == nil {
		r = &QueryRenderer{}
	} else {
		r = NewQueryRenderer(v)
	}
	r.Presentation = a.policy
	r.CurrentNames = a.policy.Deparse
	if a.binding.Relation != nil {
		r.ruleTarget, r.ruleTargetAlias = a.binding.Ref, a.targetAlias
	}
	r.ruleAction, r.ruleParentNames = a, a.parentNames
	if syntax, ok := a.syntax.(*parser.Insert); ok {
		r.ruleInsertSource = syntax.Source != nil && a.binding.Query != nil
		r.ruleValues = !r.ruleInsertSource && len(a.binding.Expressions) > 1
		r.ruleExcluded = a.binding.Insert.Conflict != nil && a.binding.Insert.Conflict.Update != nil
	}
	if a.names != nil && v != nil {
		frame := v.query.Root.(*plan.ProjectNode).Frame
		r.ruleRootNames = a.names
		r.ruleTables = map[uint32]ruleRangeTable{frame: *a.names}
	}
	if v != nil && v.frames[1] != nil {
		for occurrence, leaf := range v.sources {
			if !leaf.TableOIDRead {
				continue
			}
			source := viewSourceAt(analyze.ViewFromRoot(v.frames[1]), occurrence)
			if source == nil {
				continue
			}
			qualifier := a.sourceQualifiers[occurrence]
			if !a.policy.Rule {
				qualifier = a.policy.identifier("_rule_source_" + strconv.Itoa(occurrence))
			}
			if qualifier != "" {
				r.setSourceQualifier(source, qualifier)
			}
		}
	}
	return r
}

func (a ruleRendering) attribute(relation *catalog.Relation, attribute int32) (string, error) {
	if a.policy.Deparse {
		return (Deparser{Output: a.policy.Output}).Attribute(relation, attribute)
	}
	return DeparseAttribute(relation, attribute)
}

func (a ruleRendering) relation(view catalog.Read, ref ids.ObjectRef, qualified bool) (string, error) {
	if !a.policy.Deparse {
		return ruleRelationSQL(view, ref)
	}
	relation, ok := view.Relation(ref.Address)
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	namespace, ok := view.Namespace(relation.Namespace())
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	name := a.policy.identifier(relation.Name())
	if !qualified && relationVisibleAt(view, relation, a.policy.Path) {
		return name, nil
	}
	schema := namespace.Name()
	if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary == a.temporaryOwner {
		schema = "pg_temp"
	}
	return a.policy.identifier(schema) + "." + name, nil
}

// RuleDefinitionSQL presents current identities with the requested display policy.
// It does not serialize replay SQL or execute the rule.
func RuleDefinitionSQL(ctx context.Context, view catalog.Read, relation *catalog.Relation, rule Rule, presentation Presentation, temporaryOwner ids.SessionToken) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	presentation.Deparse = true
	presentation.Wrap = 0
	presentation.Rule = true
	presentation.TemporaryOwner = temporaryOwner
	render := ruleRendering{policy: presentation, temporaryOwner: temporaryOwner}
	target, err := render.relation(view, ids.ObjectRef{Address: relation.Address()}, !presentation.Pretty)
	if err != nil {
		return "", err
	}
	var sql strings.Builder
	sql.WriteString("CREATE RULE " + presentation.identifier(rule.Name) + " AS\n    ON " + rule.event + " TO " + target)
	if rule.condition != nil {
		r := render.renderer(rule.conditionPresentation)
		r.Frame = rule.conditionPresentation.Query().Root.(*plan.ProjectNode).Frame
		condition, err := r.ExpressionSQL(ctx, view, rule.conditionPresentation.Syntax().Targets[0], analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
		if err != nil {
			return "", err
		}
		sql.WriteString("\n   WHERE " + condition)
	}
	sql.WriteString(" DO ")
	if rule.instead {
		sql.WriteString("INSTEAD ")
	}
	if len(rule.actions) == 0 {
		sql.WriteString("NOTHING;")
	} else {
		if len(rule.actions) > 1 {
			sql.WriteByte('(')
		}
		for i, action := range rule.actions {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			render.RuleAction = action
			text, err := ruleActionSQL(ctx, view, render)
			if err != nil {
				return "", err
			}
			if i != 0 {
				sql.WriteByte('\n')
			}
			sql.WriteString(text)
			sql.WriteByte(';')
		}
		if len(rule.actions) > 1 {
			sql.WriteString("\n);")
		}
	}
	return sql.String(), ctx.Err()
}
