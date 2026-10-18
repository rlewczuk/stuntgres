package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type indexPropertyKind uint8

const (
	accessMethodProperty indexPropertyKind = iota
	wholeIndexProperty
	indexColumnProperty
)

func (k indexPropertyKind) name() string {
	switch k {
	case accessMethodProperty:
		return "pg_indexam_has_property"
	case wholeIndexProperty:
		return "pg_index_has_property"
	case indexColumnProperty:
		return "pg_index_column_has_property"
	}
	return ""
}
func (k indexPropertyKind) count() int {
	if k == indexColumnProperty {
		return 3
	}
	if k == accessMethodProperty || k == wholeIndexProperty {
		return 2
	}
	return 0
}
func (k indexPropertyKind) argument(i int) sqltype.ID {
	if i == 0 {
		if k == accessMethodProperty {
			return sqltype.Oid
		}
		return sqltype.Regclass
	}
	if k == indexColumnProperty && i == 1 {
		return sqltype.Int4
	}
	return sqltype.Text
}

type indexPropertyExpression struct {
	expressionInfo
	kind     indexPropertyKind
	args     [3]BoundExpr
	oidPlan  sqltype.CoercionPlan
	oidUsage sqltype.Usage
}

func (e indexPropertyExpression) children() []BoundExpr { return e.args[:e.kind.count()] }

func (b *expressionBinder) bindIndexProperty(raw parser.FunctionExpr) (BoundExpr, error) {
	return b.bindIndexPropertyStructure(raw, true)
}

func (b *expressionBinder) bindIndexPropertyStructure(raw parser.FunctionExpr, foldNull bool) (BoundExpr, error) {
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	var kind indexPropertyKind
	switch raw.Name.Parts[len(raw.Name.Parts)-1].Text {
	case "pg_indexam_has_property":
		kind = accessMethodProperty
	case "pg_index_has_property":
		kind = wholeIndexProperty
	case "pg_index_column_has_property":
		kind = indexColumnProperty
	default:
		return nil, fmt.Errorf("stuntgres: invalid index property function")
	}
	var operands [3]comparisonOperand
	var scratch [3]string
	names := scratch[:]
	if len(raw.Args) > 3 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
		if len(raw.Args) <= 100 {
			names[i] = "unknown"
			if typ := o.descriptor(); typ != nil {
				names[i] = typ.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	names = names[:len(raw.Args)]
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Variadic || len(raw.Args) != kind.count() {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	// Decide signature compatibility before converting unknown literals.
	var plans [3]sqltype.CoercionPlan
	for i := range raw.Args {
		if typ := operands[i].descriptor(); typ != nil {
			plans[i], err = sqltype.PlanCoercion(operands[i].node.ResultUsage(), b.typ(kind.argument(i)).Usage(), sqltype.Implicit)
			if err != nil {
				return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
		}
	}
	n := indexPropertyExpression{kind: kind}
	properties := ExprProperties{Volatility: Stable}
	null := false
	for i := range raw.Args {
		target := b.typ(kind.argument(i)).Usage()
		if target.Type.ContainsRegclass() {
			if err := b.captureElementInput(); err != nil {
				return nil, err
			}
		}
		arg, err := b.finalizeOperand(operands[i], target.Type)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan := plans[i]
			arg = coercionExpression{b.info(target, raw.Args[i].SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit, false}
		}
		n.args[i] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
		if literal, ok := arg.(literalExpression); ok && literal.value.IsNull() {
			null = true
		}
	}
	var formal [3]sqltype.ID
	for i := range raw.Args {
		formal[i] = kind.argument(i)
	}
	n.expressionInfo, err = b.builtinRoutineInfo(b.info(b.typ(sqltype.Bool).Usage(), raw.Pos, properties), catalogRoutineProvider, kind.name(), formal[:kind.count()]...)
	if err != nil {
		return nil, err
	}
	if foldNull && null {
		return literalExpression{b.info(n.usage, raw.Pos, ExprProperties{}), sqltype.Null(n.usage.Type)}, nil
	}
	if kind != accessMethodProperty {
		n.oidUsage = b.typ(sqltype.Oid).Usage()
		n.oidPlan, err = sqltype.PlanCoercion(n.args[0].ResultUsage(), n.oidUsage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	return n, nil
}

func validateIndexPropertyExpression(e indexPropertyExpression) error {
	if e.kind.count() == 0 || e.usage.Type == nil || e.usage != e.usage.Type.Usage() || e.usage.Type.ID() != sqltype.Bool {
		return fmt.Errorf("stuntgres: invalid stored index property signature")
	}
	properties := ExprProperties{Volatility: Stable}
	for i, arg := range e.args {
		if i >= e.kind.count() {
			if arg != nil {
				return fmt.Errorf("stuntgres: invalid stored index property arity")
			}
			continue
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != e.kind.argument(i) {
			return fmt.Errorf("stuntgres: invalid stored index property argument")
		}
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if properties != e.Properties() {
		return fmt.Errorf("stuntgres: invalid stored index property properties")
	}
	if e.kind == accessMethodProperty {
		if e.oidUsage != (sqltype.Usage{}) || e.oidPlan != (sqltype.CoercionPlan{}) {
			return fmt.Errorf("stuntgres: invalid AM property normalization")
		}
	} else if e.oidPlan == (sqltype.CoercionPlan{}) || e.oidUsage.Type == nil || e.oidUsage != e.oidUsage.Type.Usage() || e.oidUsage.Type.ID() != sqltype.Oid {
		return fmt.Errorf("stuntgres: invalid index property normalization descriptor")
	}
	return nil
}

func validateStoredIndexPropertyExpression(e indexPropertyExpression) error {
	if err := validateIndexPropertyExpression(e); err != nil {
		return err
	}
	if e.kind != accessMethodProperty {
		plan, err := sqltype.PlanCoercion(e.args[0].ResultUsage(), e.oidUsage, sqltype.Implicit)
		if err != nil || plan != e.oidPlan {
			return fmt.Errorf("stuntgres: invalid stored index property normalization")
		}
	}
	return nil
}

func (e indexPropertyExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := validateIndexPropertyExpression(e); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range e.children() {
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
		return sqltype.Null(e.usage.Type), nil
	}
	identity := values[0]
	if e.kind != accessMethodProperty {
		var err error
		identity, err = e.oidPlan.Apply(c.Context, identity, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
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
	property, err := values[e.kind.count()-1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	// ASCII names only; Unicode case folding must not invent builtin aliases.
	property = indexPropertyName(property)
	var result, known bool
	if e.kind == accessMethodProperty {
		result, known = indexAMProperty(oid, property)
	} else {
		if c.liveScope() == nil || c.liveScope().commentCatalog.state == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing index property catalog snapshot")
		}
		relation, ok := c.liveScope().commentCatalog.state.descriptors[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}].(*relationDescriptor)
		if !ok || relation.sqlIndex == nil {
			return sqltype.Null(e.usage.Type), nil
		}
		if e.kind == wholeIndexProperty {
			switch relation.sqlIndex.method {
			case btreeMethod, hashMethod, gistMethod:
				switch property {
				case "clusterable":
					result, known = relation.sqlIndex.method != hashMethod, true
				case "backward_scan":
					result, known = relation.sqlIndex.method != gistMethod, true
				case "index_scan", "bitmap_scan":
					result, known = true, true
				}
			}
		} else {
			ordinal, err := values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			result, known = indexColumnCapability(relation.sqlIndex, ordinal, property)
		}
	}
	if !known {
		return sqltype.Null(e.usage.Type), nil
	}
	return sqltype.Boolean(e.usage.Type, result), nil
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

func indexColumnCapability(index *indexDefinition, ordinal int64, property string) (bool, bool) {
	if ordinal <= 0 || ordinal > int64(len(index.keys)+len(index.include)) {
		return false, false
	}
	if index.method == gistMethod {
		key := ordinal <= int64(len(index.keys))
		switch property {
		case "orderable":
			return false, true
		case "asc", "desc", "nulls_first", "nulls_last", "search_array":
			return false, key
		case "search_nulls":
			return key, key
		case "distance_orderable", "returnable":
			if !key {
				// GiST's property callback finds no opclass for INCLUDE,
				// before the generic nonkey-column fallback can run.
				return false, false
			}
			switch index.keys[ordinal-1].class.OID() {
			case 10017, 10078: // inet_ops has fetch; range_ops has no compress.
				// Neither admitted class supplies distance ordering.
				return property == "returnable", true
			}
		}
		return false, false
	}
	if index.method == hashMethod {
		switch property {
		case "asc", "desc", "nulls_first", "nulls_last", "orderable", "distance_orderable", "returnable", "search_array", "search_nulls":
			return false, true
		default:
			return false, false
		}
	}
	key := ordinal <= int64(len(index.keys))
	switch property {
	case "orderable":
		return key, true
	case "distance_orderable":
		return false, true
	case "returnable":
		return true, true
	case "search_array", "search_nulls":
		return key, key
	}
	if !key {
		return false, false
	}
	definition := index.keys[ordinal-1]
	switch property {
	case "asc":
		return !definition.desc, true
	case "desc":
		return definition.desc, true
	case "nulls_first":
		return definition.nullsFirst, true
	case "nulls_last":
		return !definition.nullsFirst, true
	}
	return false, false
}
