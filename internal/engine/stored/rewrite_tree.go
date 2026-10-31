package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RewriteQualCarrier retains the installed rule, including its frozen binding.
// A nil rule is the absent qualification of an actual view definition.
type RewriteQualCarrier struct {
	rule *Rule
	view *View
}

// RewriteActionCarrier retains actual installed definitions, never an OID lookup.
type RewriteActionCarrier struct {
	rule *Rule
	view *View
}

type RewriteCatalogDefinition struct {
	OID          ids.OID
	Name, Event  string
	Mode         catalog.RuleMode
	Instead      bool
	Qual, Action sqltype.Value
}

// RewriteTrees validates owner identity and exact payload membership before capture.
func RewriteTrees(ctx context.Context, read catalog.Read, owner *catalog.Relation, typ *sqltype.Descriptor, rule *Rule, definition *View) (sqltype.Value, sqltype.Value, error) {
	fail := func(message string) (sqltype.Value, sqltype.Value, error) {
		return sqltype.Value{}, sqltype.Value{}, fmt.Errorf("stuntgres: %s", message)
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	if read.Owner() == nil || read.State() == nil || owner == nil || read.Get(owner.Address()) != owner {
		return fail("foreign rewrite owner")
	}
	captured, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok || captured != typ {
		return fail("foreign rewrite tree type")
	}
	if definition != nil {
		if rule != nil || owner.View != definition {
			return fail("detached rewrite view")
		}
		if err := definition.Validate(read, owner); err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
	} else {
		list, ok := owner.Rules.(RuleList)
		if !ok || rule == nil {
			return fail("invalid rewrite payload")
		}
		found := false
		for i := range list {
			if &list[i] == rule {
				found = true
				break
			}
		}
		if !found {
			return fail("detached rewrite rule")
		}
		if err := list.Validate(read, owner); err != nil {
			return sqltype.Value{}, sqltype.Value{}, err
		}
	}
	return rewriteTrees(ctx, typ, rule, definition)
}

// The visitor validates the captured owner and complete immutable payload once.
func rewriteTrees(ctx context.Context, typ *sqltype.Descriptor, rule *Rule, definition *View) (sqltype.Value, sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	qual, err := sqltype.Tree(typ, RewriteQualCarrier{rule: rule, view: definition})
	if err != nil {
		return sqltype.Value{}, sqltype.Value{}, err
	}
	action, err := sqltype.Tree(typ, RewriteActionCarrier{rule: rule, view: definition})
	return qual, action, err
}

func VisitRewriteCatalog(ctx context.Context, read catalog.Read, owner *catalog.Relation, typ *sqltype.Descriptor, visit func(RewriteCatalogDefinition) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if read.Owner() == nil || read.State() == nil || owner == nil || read.Get(owner.Address()) != owner {
		return fmt.Errorf("stuntgres: foreign rewrite owner")
	}
	captured, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok || captured != typ {
		return fmt.Errorf("stuntgres: foreign rewrite tree type")
	}
	if owner.View != nil {
		definition := ViewOf(owner)
		if definition == nil {
			return fmt.Errorf("stuntgres: foreign rewrite view payload")
		}
		if err := definition.Validate(read, owner); err != nil {
			return err
		}
		qual, action, err := rewriteTrees(ctx, typ, nil, definition)
		if err != nil {
			return err
		}
		if err := visit(RewriteCatalogDefinition{OID: definition.RuleOID(), Name: "_RETURN", Event: "1", Mode: catalog.RuleMode('O'), Instead: true, Qual: qual, Action: action}); err != nil {
			return err
		}
	}
	if owner.Rules != nil {
		list, ok := owner.Rules.(RuleList)
		if !ok {
			return fmt.Errorf("stuntgres: foreign rewrite rule payload")
		}
		if err := list.Validate(read, owner); err != nil {
			return err
		}
		for i := range list {
			rule := &list[i]
			qual, action, err := rewriteTrees(ctx, typ, rule, nil)
			if err != nil {
				return err
			}
			event := "4"
			switch rule.event {
			case "UPDATE":
				event = "2"
			case "INSERT":
				event = "3"
			}
			if err := visit(RewriteCatalogDefinition{OID: rule.OID, Name: rule.Name, Event: event, Mode: rule.mode, Instead: rule.instead, Qual: qual, Action: action}); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (t RewriteQualCarrier) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.rule == nil && t.view == nil {
		return "", fmt.Errorf("stuntgres: missing rewrite qualification owner")
	}
	if t.rule == nil || t.rule.condition == nil {
		return "<>", nil
	}
	return (plan.BoundTree{Expression: t.rule.condition}).StructuralOutput(ctx)
}

func (t RewriteActionCarrier) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("{RewriteActions")
	if t.view != nil {
		if err := parser.AppendStructuralSyntax(ctx, &out, t.view.syntax); err != nil {
			return "", err
		}
		if err := plan.AppendQueryTree(ctx, &out, t.view.query.Root); err != nil {
			return "", err
		}
	} else if t.rule != nil {
		if len(t.rule.actions) == 0 {
			fmt.Fprintf(&out, " {Nothing event %q pseudo OLD 1 NEW 2}", t.rule.event)
		}
		for _, action := range t.rule.actions {
			out.WriteString(" {Action ")
			if err := parser.AppendStructuralSyntax(ctx, &out, action.syntax); err != nil {
				return "", err
			}
			if err := appendRewriteBinding(ctx, &out, action.binding); err != nil {
				return "", err
			}
			if err := VisitRuleModifiers(action.binding, func(definition *plan.CTEDefinition, binding *analyze.Binding) error {
				fmt.Fprintf(&out, " {modifier owner %d name %q", definition.Owner, definition.Name)
				if err := appendRewriteBinding(ctx, &out, *binding); err != nil {
					return err
				}
				out.WriteByte('}')
				return nil
			}); err != nil {
				return "", err
			}
			out.WriteByte('}')
		}
	} else {
		return "", fmt.Errorf("stuntgres: missing rewrite action owner")
	}
	out.WriteByte('}')
	return out.String(), ctx.Err()
}

// Mutation bindings remain opaque below analyze. The existing checked modifier
// visitor exposes their genuine upper owners here; all query roots use the same
// plan serializer, including sublinks inside these ordered scalar roots.
func appendRewriteBinding(ctx context.Context, out *strings.Builder, binding analyze.Binding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fmt.Fprintf(out, " target %d/%d/%d", binding.Ref.Address.ClassID, binding.Ref.Address.ObjectID, binding.Ref.Address.SubID)
	targets := func(relation *catalog.Relation, slots []int) error {
		for _, target := range slots {
			if relation == nil || target < 0 || target >= len(relation.Attributes) {
				return fmt.Errorf("stuntgres: invalid rewrite target attribute")
			}
			fmt.Fprintf(out, " attribute %d", relation.Attributes[target])
		}
		return nil
	}
	if err := targets(binding.Relation, binding.Targets); err != nil {
		return err
	}
	if err := appendRewriteAssignmentTargets(ctx, out, "input", binding.Relation, binding.Targets, binding.ArrayTargets); err != nil { return err }
	var failure error
	visitRuleActionValues(binding, func(node expr.Expr) {
		if node != nil && failure == nil {
			out.WriteByte(' ')
			failure = plan.AppendBoundTree(ctx, out, node)
		}
	})
	if failure != nil {
		return failure
	}
	if binding.Query != nil {
		if err := plan.AppendQueryTree(ctx, out, binding.Query.Root); err != nil {
			return err
		}
	}
	if binding.Update != nil {
		if err := appendRewriteAssignmentTargets(ctx, out, "update", binding.Relation, binding.Targets, binding.Update.ArrayTargets); err != nil { return err }
		for _, group := range binding.Update.Groups {
			fmt.Fprintf(out, " {assignment_group start %d width %d}", group.Start, group.Width)
		}
		if err := plan.AppendQueryTree(ctx, out, binding.Update.From); err != nil {
			return err
		}
	}
	if err := plan.AppendQueryTree(ctx, out, binding.Delete.Using); err != nil {
		return err
	}
	if binding.Merge != nil {
		if err := plan.AppendQueryTree(ctx, out, binding.Merge.Source); err != nil {
			return err
		}
		for i, arm := range binding.Merge.Arms {
			fmt.Fprintf(out, " {arm %d", i)
			if err := targets(arm.Input.Relation, arm.Input.Targets); err != nil {
				return err
			}
			if err := appendRewriteAssignmentTargets(ctx, out, "input", arm.Input.Relation, arm.Input.Targets, arm.Input.ArrayTargets); err != nil { return err }
			if arm.Input.Update != nil {
				if err := appendRewriteAssignmentTargets(ctx, out, "update", arm.Input.Relation, arm.Input.Targets, arm.Input.Update.ArrayTargets); err != nil { return err }
				for _, group := range arm.Input.Update.Groups {
					fmt.Fprintf(out, " {assignment_group start %d width %d}", group.Start, group.Width)
				}
			}
			for _, node := range arm.Input.Insert.Defaults {
				if node != nil {
					out.WriteString(" default ")
					if err := plan.AppendBoundTree(ctx, out, node); err != nil {
						return err
					}
				}
			}
			out.WriteByte('}')
		}
	}
	for _, node := range binding.Insert.Defaults {
		if node != nil {
			out.WriteString(" default ")
			if err := plan.AppendBoundTree(ctx, out, node); err != nil {
				return err
			}
		}
	}
	if conflict := binding.Insert.Conflict; conflict != nil {
		fmt.Fprintf(out, " {conflict constraint %d/%d/%d index %d/%d/%d", conflict.Constraint.ClassID, conflict.Constraint.ObjectID, conflict.Constraint.SubID, conflict.NamedIndex.ClassID, conflict.NamedIndex.ObjectID, conflict.NamedIndex.SubID)
		for _, key := range conflict.Keys {
			fmt.Fprintf(out, " {key attribute %d collation %d class %d", key.Attribute, key.Collation, key.Class.OID())
			if key.Expression != nil {
				if err := plan.AppendBoundTree(ctx, out, key.Expression); err != nil {
					return err
				}
			}
			out.WriteByte('}')
		}
		if conflict.Predicate != nil {
			out.WriteString(" predicate ")
			if err := plan.AppendBoundTree(ctx, out, conflict.Predicate); err != nil {
				return err
			}
		}
		if conflict.Update != nil {
			if err := targets(binding.Relation, conflict.UpdateTargets); err != nil {
				return err
			}
			if err := appendRewriteAssignmentTargets(ctx, out, "conflict", binding.Relation, conflict.UpdateTargets, conflict.Update.ArrayTargets); err != nil { return err }
			for _, group := range conflict.Update.Groups {
				fmt.Fprintf(out, " {assignment_group start %d width %d}", group.Start, group.Width)
			}
			for _, node := range conflict.Update.Assignments {
				out.WriteString(" assignment ")
				if err := plan.AppendBoundTree(ctx, out, node); err != nil {
					return err
				}
			}
			if conflict.Update.Predicate != nil {
				out.WriteString(" predicate ")
				if err := plan.AppendBoundTree(ctx, out, conflict.Update.Predicate); err != nil {
					return err
				}
			}
			if err := plan.AppendQueryTree(ctx, out, conflict.Update.From); err != nil {
				return err
			}
		}
		if conflict.Returning != nil {
			for _, node := range conflict.Returning.Expressions {
				out.WriteString(" returning ")
				if err := plan.AppendBoundTree(ctx, out, node); err != nil {
					return err
				}
			}
		}
		out.WriteByte('}')
	}
	return ctx.Err()
}

// Preflight matches PostgreSQL's varno checks before context relation lookup.
func (t RewriteQualCarrier) Preflight(ctx context.Context, relation ids.OID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.rule == nil && t.view == nil {
		return fmt.Errorf("stuntgres: missing rewrite qualification owner")
	}
	if t.rule == nil || t.rule.condition == nil {
		return nil
	}
	var variables, other bool
	var inspectExpression func(expr.Expr)
	var inspectQuery func(plan.Node)
	inspectQuery = func(node plan.Node) {
		if node == nil || ctx.Err() != nil {
			return
		}
		plan.QueryExpressions(node, inspectExpression)
		left, right := plan.QueryInputs(node)
		inspectQuery(left)
		inspectQuery(right)
	}
	inspectExpression = func(expression expr.Expr) {
		plan.WalkExpression(expression, func(node expr.Expr) {
			switch n := node.(type) {
			case plan.ColumnExpr:
				if n.Scope == analyze.RulePseudoScope {
					variables = true
					other = other || n.Frame != 1
				}
			case plan.RowExpr:
				variables = variables || n.RuleSide != 0
				other = other || n.RuleSide != 0 && n.RuleSide != 1
			case plan.SubqueryExpr:
				if n.QueryPlan != nil {
					inspectQuery(n.QueryPlan.Root)
				}
			}
		})
	}
	inspectExpression(t.rule.condition)
	if err := ctx.Err(); err != nil {
		return err
	}
	if relation == 0 && variables {
		return sqlError("22023", "expression contains variables")
	}
	if relation != 0 && other {
		return sqlError("22023", "expression contains variables of more than one relation")
	}
	return nil
}

// RewriteQualDisplay reports absent nodes separately from SQL-NULL arguments.
func RewriteQualDisplay(ctx context.Context, t RewriteQualCarrier, renderer Deparser) (string, bool, error) {
	if t.rule == nil || t.rule.condition == nil {
		return "", true, nil
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	presentation := t.rule.conditionPresentation
	if presentation == nil || presentation.query == nil || presentation.syntax == nil || len(presentation.syntax.Targets) != 1 {
		return "", false, fmt.Errorf("stuntgres: missing rewrite qualification presentation")
	}
	root, ok := presentation.query.Root.(*plan.ProjectNode)
	if !ok || root == nil {
		return "", false, fmt.Errorf("stuntgres: invalid rewrite qualification frame")
	}
	r := NewQueryRenderer(presentation)
	r.Frame = root.Frame
	r.CurrentNames = true
	r.RewriteInquiry, r.RewriteRelation = true, renderer.Relation
	r.Presentation = Presentation{Deparse: true, Rule: true, Policy: true, Pretty: renderer.Pretty, Path: renderer.Path, Output: renderer.Output, NonstandardStrings: renderer.NonstandardStrings, TemporaryOwner: renderer.TemporaryOwner}
	names := ruleRangeTable{used: make(map[string]int)}
	if renderer.Relation != nil {
		names.used[renderer.Relation.Name()] = 0
		names.pseudos[0] = renderer.Relation.Name()
	}
	r.ruleTables = map[uint32]ruleRangeTable{root.Frame: names}
	r.ruleRootNames = &names
	text, err := r.ExpressionSQL(ctx, renderer.View, presentation.syntax.Targets[0], analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
	return strings.TrimSpace(text), false, err
}
