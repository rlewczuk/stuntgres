package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func unaccentDictionaryExpression(t *testing.T, e *core.Engine, dictionary, input string) plan.InstalledTextExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	n, err := b.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unaccent"}}}, Args: []parser.Expr{literal(dictionary), literal(input)}})
	if err != nil {
		t.Fatal(err)
	}
	return n.(plan.InstalledTextExpr)
}

// Each context owns a real admitted statement. Callers finish it before catalog
// mutation and defer an error unwind so failed assertions cannot retain admission.
func unaccentDictionaryContext(t *testing.T, e *core.Engine, op *session.Operation) (*exec.Context, *core.StatementScope) {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	return &exec.Context{EvalStatement: exec.EvalStatement{
		Context: op.Context(), Host: core.HostOf(scope), Catalog: scope.ResolutionCatalog(),
		Input: scope.Input, Output: scope.Output,
	}}, scope
}

func unaccentDictionaryValue(t *testing.T, n expr.Expr, c *exec.Context, want string) {
	t.Helper()
	v, err := n.Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	text, err := v.Text()
	if err != nil || text != want || v.Type() != n.ResultUsage().Type {
		t.Fatal("typed dictionary result", text, err)
	}
}

type unaccentOrderedArgument struct {
	expr.Info
	value  sqltype.Value
	order  *[]string
	label  string
	cancel context.CancelFunc
	err    error
}

func (a unaccentOrderedArgument) Eval(expr.Context) (sqltype.Value, error) {
	*a.order = append(*a.order, a.label)
	if a.cancel != nil {
		a.cancel()
	}
	return a.value, a.err
}

func TestUnaccentDictionaryExpressionIdentity(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	view := e.CatalogRead()
	n := unaccentDictionaryExpression(t, e, "public.unaccent", "Hôtel")
	c, scope := unaccentDictionaryContext(t, e, op)
	defer scope.Finish(context.Canceled)
	if n.Procedure != view.MustProcedure(ext.Members[0]) || n.RoutineReference != n.Procedure || n.Ref.Address != ext.Members[0] || n.Procedure.Signature != catalog.UnaccentDictionarySignature || !analyze.ValidInstalledText(n) || exec.PlannedVolatility(n) != expr.Stable || plan.GenerationImmutable(n) {
		t.Fatal("binary installed authority", n)
	}
	text, _ := view.LookupType(sqltype.Text)
	dictionary, _ := view.LookupType(sqltype.Regdictionary)
	if n.Dictionary == nil || n.Dictionary.ResultUsage().Type != dictionary || n.Arg.ResultUsage().Type != text || n.Usage.Type != text {
		t.Fatal("formal descriptors")
	}
	unaccentDictionaryValue(t, n, c, "Hotel")
	var children []expr.Expr
	plan.ExpressionChildren(n, func(x expr.Expr) { children = append(children, x) })
	if !reflect.DeepEqual(children, []expr.Expr{n.Dictionary, n.Arg}) || !analyze.SameGroupExpression(n, n) {
		t.Fatal("ordered children", children)
	}
	other := unaccentDictionaryExpression(t, e, "pg_catalog.simple", "Hôtel")
	if analyze.SameGroupExpression(n, other) {
		t.Fatal("dictionary ignored in group equality")
	}
	refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
	if err != nil || len(refs) != 2 || !slices.Contains(refs, ext.Members[0]) || !slices.Contains(refs, ext.Members[5]) {
		t.Fatal("binary dependency graph", refs, err)
	}
	// The selected builtin simple dictionary is pinned: generic dependency
	// capture omits it, as well as the unselected installed dictionary, while
	// retaining the actual installed procedure.
	pruned, err := stored.ExpressionDependRefs(context.Background(), view, other)
	if err != nil || len(pruned) != 1 || !slices.Contains(pruned, ext.Members[0]) || slices.Contains(pruned, ext.Members[5]) || slices.Contains(pruned, ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}) {
		t.Fatal("dictionary dependency pruning", pruned, err)
	}
	rendered, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, n)
	if err != nil || !strings.Contains(rendered, "unaccent") || !strings.Contains(rendered, ",") || !strings.Contains(rendered, "Hôtel") {
		t.Fatal("binary stored rendering", rendered, err)
	}
	var visited []expr.Expr
	rebuilt, _, err := stored.MapExpressionChildren(view, n, func(x expr.Expr) (expr.Expr, bool, error) { visited = append(visited, x); return x, false, nil })
	if err != nil || !reflect.DeepEqual(visited, children) || !reflect.DeepEqual(rebuilt, n) {
		t.Fatal("ordered identity rebuild", visited, err)
	}
	retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || !reflect.DeepEqual(retagged, n) {
		t.Fatal("retag retained authority", err)
	}
	replaced, changed, err := stored.MapExpressionChildren(view, n, func(x expr.Expr) (expr.Expr, bool, error) {
		if x == n.Arg {
			return plan.LiteralExpr{Info: expr.InfoOf(x), Value: sqltype.String(text, "Æsir")}, true, nil
		}
		return x, false, nil
	})
	if err != nil || !changed {
		t.Fatal("changed-child reconstruction", err)
	}
	r := replaced.(plan.InstalledTextExpr)
	if r.Procedure != n.Procedure || r.Ref != n.Ref || r.Dictionary != n.Dictionary || !analyze.ValidInstalledText(r) || n.Arg != children[1] {
		t.Fatal("reconstruction discarded captured authority or changed source")
	}
	unaccentDictionaryValue(t, r, c, "AEsir")
	simplified, _, err := exec.SimplifyConditional(c, n, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := simplified.(plan.InstalledTextExpr); !ok {
		t.Fatal("STABLE call result folded", simplified)
	}

	foreign := engine.New()
	foreignText, _ := foreign.CatalogRead().LookupType(sqltype.Text)
	foreignDictionary, _ := foreign.CatalogRead().LookupType(sqltype.Regdictionary)
	for name, corrupt := range map[string]func(*plan.InstalledTextExpr){
		"missing_dictionary":    func(x *plan.InstalledTextExpr) { x.Dictionary = nil },
		"wrong_dictionary_type": func(x *plan.InstalledTextExpr) { x.Dictionary = x.Arg },
		"foreign_dictionary": func(x *plan.InstalledTextExpr) {
			x.Dictionary = plan.LiteralExpr{Info: expr.Info{Usage: foreignDictionary.Usage()}, Value: sqltype.Null(foreignDictionary)}
		},
		"foreign_text_and_result": func(x *plan.InstalledTextExpr) {
			x.Arg = plan.LiteralExpr{Info: expr.Info{Usage: foreignText.Usage()}, Value: sqltype.Null(foreignText)}
			x.Usage.Type = foreignText
		},
		"swapped_result": func(x *plan.InstalledTextExpr) { x.Usage.Type = dictionary },
		"wrong_signature": func(x *plan.InstalledTextExpr) {
			p := *x.Procedure
			p.Signature = catalog.UnaccentTextSignature
			x.Procedure = &p
			x.RoutineReference = &p
		},
		"wrong_provider": func(x *plan.InstalledTextExpr) {
			p := *x.Procedure
			p.Implementation = extension.StockFuzzystrmatch
			x.Procedure = &p
			x.RoutineReference = &p
		},
		"forged_provider": func(x *plan.InstalledTextExpr) {
			p := *x.Procedure
			p.Implementation = &extension.UnaccentProvider{}
			x.Procedure = &p
			x.RoutineReference = &p
		},
		"forged_cohort": func(x *plan.InstalledTextExpr) {
			p := *x.Procedure
			o := *p.Origin
			i := *o.Installation
			o.Installation = &i
			p.Origin = &o
			x.Procedure = &p
			x.RoutineReference = &p
		},
		"missing_carrier": func(x *plan.InstalledTextExpr) { x.RoutineReference = nil },
		"wrong_reference": func(x *plan.InstalledTextExpr) { x.Ref.Address.SubID = 1 },
		"forged_descriptor_kind": func(x *plan.InstalledTextExpr) {
			p := *x.Procedure
			p.RoutineKind = catalog.ProcedureRoutine
			x.Procedure = &p
			x.RoutineReference = &p
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := n
			bad.Arg = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}
			corrupt(&bad)
			if analyze.ValidInstalledText(bad) {
				t.Fatal("forgery admitted")
			}
			if _, err := bad.Eval(c); err == nil {
				t.Fatal("forged NULL evaluated")
			}
			if _, _, err := exec.SimplifyConditional(c, bad, false); err == nil {
				t.Fatal("forged NULL planned")
			}
			if _, err := stored.ExpressionDependRefs(context.Background(), view, bad); err == nil {
				t.Fatal("forged dependency capture admitted")
			}
			if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, bad); err == nil {
				t.Fatal("forgery rendered")
			}
		})
	}
	stale := n
	stale.Ref.Epoch++
	if _, err := stale.Eval(c); err == nil {
		t.Fatal("stale procedure reference evaluated")
	}
	stale.Ref = foreign.Catalog.Domain.Ref(view.Epoch(), n.Ref.Address)
	if _, err := stale.Eval(c); err == nil {
		t.Fatal("foreign procedure reference evaluated")
	}
	var order []string
	dv, err := n.Dictionary.Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	ordered := n
	ordered.Dictionary = unaccentOrderedArgument{Info: expr.Info{Usage: dictionary.Usage()}, value: dv, order: &order, label: "dictionary"}
	ordered.Arg = unaccentOrderedArgument{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, value: sqltype.String(text, "Hôtel"), order: &order, label: "text"}
	unaccentDictionaryValue(t, ordered, c, "Hotel")
	if !reflect.DeepEqual(order, []string{"dictionary", "text"}) || exec.PlannedVolatility(ordered) != expr.Volatile {
		t.Fatal("single ordered evaluation/volatility", order)
	}
	inputError := errors.New("operand input failed")
	for _, first := range []bool{true, false} {
		order = nil
		darg := unaccentOrderedArgument{Info: expr.Info{Usage: dictionary.Usage()}, value: dv, order: &order, label: "dictionary"}
		targ := unaccentOrderedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, label: "text"}
		want := []string{"dictionary", "text"}
		if first {
			darg.err = inputError
			want = want[:1]
		} else {
			targ.err = inputError
		}
		ordered.Dictionary, ordered.Arg = darg, targ
		if _, err := ordered.Eval(c); !errors.Is(err, inputError) || !reflect.DeepEqual(order, want) {
			t.Fatal("operand error source order", order, err)
		}
	}
	for _, nullDictionary := range []bool{false, true} {
		order = nil
		dvalue, tvalue := dv, sqltype.Null(text)
		if nullDictionary {
			dvalue, tvalue = sqltype.Null(dictionary), sqltype.String(text, "Hôtel")
		}
		ordered.Dictionary = unaccentOrderedArgument{Info: expr.Info{Usage: dictionary.Usage()}, value: dvalue, order: &order, label: "dictionary"}
		ordered.Arg = unaccentOrderedArgument{Info: expr.Info{Usage: text.Usage()}, value: tvalue, order: &order, label: "text"}
		v, err := ordered.Eval(c)
		if err != nil || !v.IsNull() || v.Type() != text || !reflect.DeepEqual(order, []string{"dictionary", "text"}) {
			t.Fatal("strict ordered evaluation", order, v, err)
		}
	}
	missing, err := sqltype.RegTSValue(dictionary, ^uint32(0))
	if err != nil {
		t.Fatal(err)
	}
	strict := n
	strict.Dictionary = plan.LiteralExpr{Info: expr.Info{Usage: dictionary.Usage()}, Value: missing}
	strict.Arg = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}
	if v, err := strict.Eval(c); err != nil || !v.IsNull() || v.Type() != text {
		t.Fatal("NULL acquired nonexistent dictionary", v, err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	// Start with live operation authority, then cancel before evaluation.
	prior := op.Ctx
	cancelled, cancel := context.WithCancel(prior)
	op.Ctx = cancelled
	defer func() { op.Ctx = prior; cancel() }()
	cancelledContext, cancelledScope := unaccentDictionaryContext(t, e, op)
	defer cancelledScope.Finish(context.Canceled)
	cancel()
	_, err = ordered.Eval(cancelledContext)
	if err = cancelledScope.Finish(err); !errors.Is(err, context.Canceled) {
		t.Fatal("ordinary cancellation", err)
	}
	op.Ctx = prior

	// A genuine ispell dictionary has two distinct lexemes. The installed
	// wrapper must acquire that exact callback and take its first, not run stock
	// unaccent, concatenate outputs, or authorize only an unaccent template.
	e.TextSearchFiles = functions.NewDictionaryFiles(map[string]string{"unit.dict": "root/A\nrooting/B\n", "unit.affix": "SFX A Y 1\nSFX A 0 ing .\n"})
	enginetest.RoleExecute(t, e, op, "CREATE TEXT SEARCH DICTIONARY binary_multi (TEMPLATE=pg_catalog.ispell,DictFile=unit,AffFile=unit)")
	multi := unaccentDictionaryExpression(t, e, "binary_multi", "rooting")
	multiContext, multiScope := unaccentDictionaryContext(t, e, op)
	defer multiScope.Finish(context.Canceled)
	mv := e.CatalogRead()
	a := mv.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ext.TargetNamespace, Name: "binary_multi"})
	lexemes, err := mv.MustTSDictionary(a).Runtime.Lexize(context.Background(), "rooting")
	if err != nil || len(lexemes.Lexemes) != 2 || lexemes.Lexemes[0].Text != "rooting" || lexemes.Lexemes[1].Text != "root" {
		t.Fatal("real multi-lexeme callback", lexemes, err)
	}
	unaccentDictionaryValue(t, multi, multiContext, "rooting")
	if err := multiScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	// Missing selected OID is an acquisition error, not a name fallback.
	enginetest.RoleExecute(t, e, op, "DROP TEXT SEARCH DICTIONARY binary_multi")
	missingContext, missingScope := unaccentDictionaryContext(t, e, op)
	defer missingScope.Finish(context.Canceled)
	_, err = multi.Eval(missingContext)
	err = missingScope.Finish(err)
	extensionUnitError(t, err, "XX000")
	// Cancellation after child evaluation must cross the genuine host/acquisition
	// path as an ordinary context error, never a fabricated SQLSTATE.
	operationContext, stop := context.WithCancel(prior)
	op.Ctx = operationContext
	defer func() { op.Ctx = prior; stop() }()
	operationEvaluation, operationScope := unaccentDictionaryContext(t, e, op)
	defer operationScope.Finish(context.Canceled)
	order = nil
	ordered.Dictionary = unaccentOrderedArgument{Info: expr.Info{Usage: dictionary.Usage()}, value: dv, order: &order, label: "dictionary"}
	ordered.Arg = unaccentOrderedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "Hôtel"), order: &order, label: "text", cancel: stop}
	_, err = ordered.Eval(operationEvaluation)
	if err = operationScope.Finish(err); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(order, []string{"dictionary", "text"}) {
		t.Fatal("canceled acquisition", order, err)
	}
}
