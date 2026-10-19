package functions

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type IndexPropertyKind uint8

const (
	AccessMethodProperty IndexPropertyKind = iota
	WholeIndexProperty
	IndexColumnProperty
)

func (k IndexPropertyKind) name() string {
	switch k {
	case AccessMethodProperty:
		return "pg_indexam_has_property"
	case WholeIndexProperty:
		return "pg_index_has_property"
	case IndexColumnProperty:
		return "pg_index_column_has_property"
	}
	return ""
}
func (k IndexPropertyKind) count() int {
	if k == IndexColumnProperty {
		return 3
	}
	if k == AccessMethodProperty || k == WholeIndexProperty {
		return 2
	}
	return 0
}
func (k IndexPropertyKind) argument(i int) sqltype.ID {
	if i == 0 {
		if k == AccessMethodProperty {
			return sqltype.Oid
		}
		return sqltype.Regclass
	}
	if k == IndexColumnProperty && i == 1 {
		return sqltype.Int4
	}
	return sqltype.Text
}

type IndexProperty struct {
	expr.Info
	Kind     IndexPropertyKind
	Args     [3]expr.Expr
	oidPlan  sqltype.CoercionPlan
	oidUsage sqltype.Usage
}

func (IndexProperty) ResultNullability(site expr.ResultSite) (nullable, known bool) {
	if site == expr.ResultSelect {
		return true, true
	}
	return false, false
}

func (e IndexProperty) children() []expr.Expr { return e.Args[:e.Kind.count()] }

func bindIndexProperty(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	return BindIndexPropertyStructure(b, raw, true)
}

func BindIndexPropertyStructure(b Binder, raw parser.FunctionExpr, foldNull bool) (expr.Expr, error) {
	if b.DefaultConsumer() {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	var kind IndexPropertyKind
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "pg_indexam_has_property":
		kind = AccessMethodProperty
	case "pg_index_has_property":
		kind = WholeIndexProperty
	case "pg_index_column_has_property":
		kind = IndexColumnProperty
	default:
		return nil, fmt.Errorf("stuntgres: invalid index property function")
	}
	var operands [3]Argument
	var scratch [3]string
	names := scratch[:]
	if len(raw.Args) > 3 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
		if len(raw.Args) <= 100 {
			names[i] = "unknown"
			if typ := o.Usage.Type; typ != nil {
				names[i] = typ.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	names = names[:len(raw.Args)]
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Variadic || len(raw.Args) != kind.count() {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	// Decide signature compatibility before converting unknown literals.
	var plans [3]sqltype.CoercionPlan
	for i := range raw.Args {
		if typ := operands[i].Usage.Type; typ != nil {
			plans[i], err = sqltype.PlanCoercion(operands[i].Expression.ResultUsage(), b.Type(kind.argument(i)).Usage(), sqltype.Implicit)
			if err != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
	}
	n := IndexProperty{Kind: kind}
	properties := expr.Properties{Volatility: expr.Stable}
	null := false
	for i := range raw.Args {
		target := b.Type(kind.argument(i)).Usage()
		if target.Type.ContainsRegclass() {
			if err := b.CaptureTypeInput(target.Type); err != nil {
				return nil, err
			}
		}
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan := plans[i]
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil {
				return nil, err
			}
		}
		n.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
		if literal, ok := b.ConstantValue(arg); ok && literal.IsNull() {
			null = true
		}
	}
	var formal [3]sqltype.ID
	for i := range raw.Args {
		formal[i] = kind.argument(i)
	}
	n.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), CatalogRoutineProvider, kind.name(), formal[:kind.count()]...)
	if err != nil {
		return nil, err
	}
	if foldNull && null {
		return b.Literal(sqltype.Null(n.Usage.Type), raw.Pos), nil
	}
	if kind != AccessMethodProperty {
		n.oidUsage = b.Type(sqltype.Oid).Usage()
		n.oidPlan, err = sqltype.PlanCoercion(n.Args[0].ResultUsage(), n.oidUsage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	return n, nil
}

func validateIndexPropertyExpression(e IndexProperty) error {
	if e.Kind.count() == 0 || e.Usage.Type == nil || e.Usage != e.Usage.Type.Usage() || e.Usage.Type.ID() != sqltype.Bool {
		return fmt.Errorf("stuntgres: invalid stored index property signature")
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range e.Args {
		if i >= e.Kind.count() {
			if arg != nil {
				return fmt.Errorf("stuntgres: invalid stored index property arity")
			}
			continue
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != e.Kind.argument(i) {
			return fmt.Errorf("stuntgres: invalid stored index property argument")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if properties != e.Properties() {
		return fmt.Errorf("stuntgres: invalid stored index property properties")
	}
	if e.Kind == AccessMethodProperty {
		if e.oidUsage != (sqltype.Usage{}) || e.oidPlan != (sqltype.CoercionPlan{}) {
			return fmt.Errorf("stuntgres: invalid AM property normalization")
		}
	} else if e.oidPlan == (sqltype.CoercionPlan{}) || e.oidUsage.Type == nil || e.oidUsage != e.oidUsage.Type.Usage() || e.oidUsage.Type.ID() != sqltype.Oid {
		return fmt.Errorf("stuntgres: invalid index property normalization descriptor")
	}
	return nil
}

func validateStoredIndexPropertyExpression(e IndexProperty) error {
	if err := validateIndexPropertyExpression(e); err != nil {
		return err
	}
	if e.Kind != AccessMethodProperty {
		plan, err := sqltype.PlanCoercion(e.Args[0].ResultUsage(), e.oidUsage, sqltype.Implicit)
		if err != nil || plan != e.oidPlan {
			return fmt.Errorf("stuntgres: invalid stored index property normalization")
		}
	}
	return nil
}

func (e IndexProperty) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := validateIndexPropertyExpression(e); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i := range e.Kind.count() {
		arg := e.Args[i]
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if v.Type() != arg.ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid index property argument value")
		}
		values[i] = v
		null = null || v.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	identity := values[0]
	if e.Kind != AccessMethodProperty {
		var err error
		identity, err = e.oidPlan.Apply(c.FunctionContext(), identity, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		if err != nil {
			return sqltype.Value{}, err
		}
		if identity.Type() != e.oidUsage.Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid normalized index property identity")
		}
	}
	oid, err := identity.OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	property, err := values[e.Kind.count()-1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	// ASCII names only; Unicode case folding must not invent builtin aliases.
	property = indexPropertyName(property)
	var result, known bool
	if e.Kind == AccessMethodProperty {
		result, known = indexAMProperty(oid, property)
	} else {
		runtime, failure := catalogInquiryRuntime(c)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		var ordinal sqltype.Value
		if e.Kind == IndexColumnProperty {
			ordinal = values[1]
		}
		result, known, err = runtime.IndexProperty(e.Kind, oid, ordinal, property)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if !known {
		return sqltype.Null(e.Usage.Type), nil
	}
	return sqltype.Boolean(e.Usage.Type, result), nil
}

func indexPropertyName(name string) string {
	for i := range len(name) {
		if name[i] >= 128 {
			return ""
		}
	}
	for _, property := range [...]string{"can_order", "can_unique", "can_multi_col", "can_exclude", "can_include", "clusterable", "index_scan", "bitmap_scan", "backward_scan", "asc", "desc", "nulls_first", "nulls_last", "orderable", "distance_orderable", "returnable", "search_array", "search_nulls"} {
		if strings.EqualFold(name, property) {
			return property
		}
	}
	return ""
}

func indexAMProperty(oid uint32, property string) (bool, bool) {
	var order, unique, multi, exclude, include bool
	switch oid {
	case 403:
		order, unique, multi, exclude, include = true, true, true, true, true
	case 405:
		exclude = true
	case 783:
		multi, exclude, include = true, true, true
	case 2742, 3580:
		multi = true
	case 4000:
		exclude, include = true, true
	default:
		return false, false
	}
	switch property {
	case "can_order":
		return order, true
	case "can_unique":
		return unique, true
	case "can_multi_col":
		return multi, true
	case "can_exclude":
		return exclude, true
	case "can_include":
		return include, true
	}
	return false, false
}
