package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SQLJSON is a SQL/JSON constructor without its query form: JSON_ARRAY,
// JSON_OBJECT, JSON, JSON_SCALAR or JSON_SERIALIZE. Its RETURNING clause is
// an enclosing engine coercion, and the engine renders its SQL because the
// arguments display through their formatted-input coercions.
type SQLJSON struct {
	expr.Info
	Raw  parser.SQLJSONExpr
	Args []expr.Expr
	Plan sqltype.SQLJSONPlan
}

// SQLJSONKind maps a constructor's folded name to its plan kind; 0 is not a
// constructor.
func SQLJSONKind(name string) sqltype.SQLJSONKind {
	switch name {
	case "json_array":
		return sqltype.SQLJSONArray
	case "json_object":
		return sqltype.SQLJSONObject
	case "json":
		return sqltype.SQLJSONParse
	case "json_scalar":
		return sqltype.SQLJSONScalar
	case "json_serialize":
		return sqltype.SQLJSONSerialize
	}
	return 0
}

// NewSQLJSON binds the constructor plan for the arguments' usages.
func NewSQLJSON(info expr.Info, raw parser.SQLJSONExpr, args []expr.Expr) (SQLJSON, error) {
	var small [8]sqltype.Usage
	usages := small[:min(len(args), len(small))]
	if len(args) > len(small) {
		usages = make([]sqltype.Usage, len(args))
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindSQLJSON(SQLJSONKind(raw.Kind), info.Usage, usages, raw.Absent, raw.Unique && raw.Kind != "json")
	if err != nil {
		return SQLJSON{}, err
	}
	return SQLJSON{Info: info, Raw: raw, Args: args, Plan: plan}, nil
}

func (e SQLJSON) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var small [8]sqltype.Value
	values := small[:min(len(e.Args), len(small))]
	if len(e.Args) > len(small) {
		values = make([]sqltype.Value, len(e.Args))
	}
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.Plan.Apply(ctx, values, c.FunctionJSONOutput())
}

func (e SQLJSON) Children() []expr.Expr { return e.Args }

// Rebuild rebinds the constructor plan for the new children.
func (e SQLJSON) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Args) {
		return nil, fmt.Errorf("stuntgres: invalid SQL/JSON constructor children")
	}
	return NewSQLJSON(info, e.Raw, children)
}

// IntrinsicVolatility is the bound volatility, which already includes the
// output-dependent inputs (composites, dates, timestamps) that make it STABLE.
func (e SQLJSON) IntrinsicVolatility() expr.Volatility { return e.Property.Volatility }

func (e SQLJSON) ValidateStoredProvider() error { return nil }

func (e SQLJSON) Render() (expr.Rendering, error) {
	return expr.Rendering{
		Tree:   fmt.Sprintf("SQLJSON %s absent %t unique %t", e.Raw.Kind, e.Raw.Absent, e.Raw.Unique),
		Inputs: e.Args,
	}, nil
}

// JSONPredicate is IS [NOT] JSON [VALUE|ARRAY|OBJECT|SCALAR] [WITH UNIQUE KEYS].
type JSONPredicate struct {
	expr.Info
	Child       expr.Expr
	Kind        string
	Unique, Not bool
}

func (e JSONPredicate) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := e.Child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	truth, err := sqltype.JSONPredicate(ctx, value, strings.ToUpper(e.Kind), e.Unique)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Not {
		truth = !truth
	}
	return sqltype.Boolean(e.Usage.Type, truth), nil
}

func (e JSONPredicate) Children() []expr.Expr { return []expr.Expr{e.Child} }

func (e JSONPredicate) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid IS JSON children")
	}
	e.Info, e.Child = info, children[0]
	return e, nil
}

func (e JSONPredicate) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e JSONPredicate) ValidateStoredProvider() error { return nil }

// Render displays the predicate parenthesized; the child shows no implicit
// coercions and no operator parent.
func (e JSONPredicate) Render() (expr.Rendering, error) {
	return expr.Rendering{
		Tree:   fmt.Sprintf("IS JSON %s unique %t not %t", e.Kind, e.Unique, e.Not),
		Inputs: []expr.Expr{e.Child},
		Form:   expr.FormTemplate,
		Args:   []expr.RenderArg{{Expr: e.Child, HideImplicit: true}},
		Pieces: []expr.Piece{expr.TextPiece("("), expr.ArgPiece(0), expr.TextPiece(JSONPredicateSuffix(e.Kind, e.Unique, e.Not))},
	}, nil
}

// JSONPredicateSuffix is the predicate's SQL after its operand, including
// the closing parenthesis.
func JSONPredicateSuffix(kind string, unique, not bool) string {
	text := " IS "
	if not {
		text += "NOT "
	}
	text += "JSON"
	if kind != "" {
		text += " " + strings.ToUpper(kind)
	}
	if unique {
		text += " WITH UNIQUE KEYS"
	}
	return text + ")"
}
