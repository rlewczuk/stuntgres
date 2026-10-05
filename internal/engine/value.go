package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A bound occurrence contains only immutable analysis state, never arguments.
type boundValue struct {
	value sqltype.Value
	parameter int
	coercion sqltype.CoercionPlan
	convert bool
}

func (v boundValue) evaluate(ctx context.Context, frame []sqltype.Value) (sqltype.Value, error) {
	if v.parameter != 0 { return frame[v.parameter-1], nil }
	if v.convert { return v.coercion.Apply(ctx, v.value) }
	return v.value, ctx.Err()
}

func (e *Engine) bindValue(raw parser.Value, target sqltype.Usage, mode sqltype.CoercionContext) (boundValue, error) {
	bound := boundValue{}
	view := CatalogRead{owner: e, state: e.state}
	switch raw.Kind {
	case parser.NullValue:
		bound.value = sqltype.Null(target.Type)
		return bound, nil
	case parser.StringValue:
		var err error
		bound.value, err = target.Type.Input(context.Background(), raw.Text, -1)
		return bound, err
	case parser.IntegerValue:
		id := sqltype.Int4
		if raw.Integer < -2147483648 || raw.Integer > 2147483647 { id = sqltype.Int8 }
		d, _ := view.LookupType(id)
		bound.value, _ = sqltype.Integer(d, raw.Integer)
	case parser.BooleanValue:
		d, _ := view.LookupType(sqltype.Bool)
		bound.value = sqltype.Boolean(d, raw.Boolean)
	default:
		return bound, sqlError("0A000", "unsupported value")
	}
	// Operator admission is owned by the selected equality provider.
	if mode == sqltype.Implicit { return bound,nil }
	if bound.value.Type() == target.Type { return bound, nil }
	plan, err := sqltype.PlanCoercion(bound.value.Type().Usage(), target, mode)
	if err != nil { return bound, err }
	bound.coercion, bound.convert = plan, true
	return bound, nil
}

func literalTypeName(raw parser.Value) string {
	switch raw.Kind {
	case parser.IntegerValue:
		if raw.Integer < -2147483648 || raw.Integer > 2147483647 { return "bigint" }
		return "integer"
	case parser.BooleanValue:
		return "boolean"
	}
	return "unknown"
}

func operatorError(err error, column ColumnDescriptor, predicate *parser.Predicate) error {
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok { return err }
	copy := *diagnostic
	if copy.Code == "42883" {
		copy.Message = fmt.Sprintf("operator does not exist: %s = %s", strings.ToLower(column.Usage.Type.SQLName()), literalTypeName(predicate.Value))
		copy.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		copy.Position = strconv.Itoa(predicate.Pos.Character)
	} else if predicate.Value.Kind == parser.StringValue && (copy.Code == "22P02" || copy.Code == "22003") {
		copy.Position = strconv.Itoa(predicate.Value.Pos.Character)
	}
	return &copy
}

func failingRowDetail(row []sqltype.Value) (string, error) {
	var b strings.Builder
	b.WriteString("Failing row contains (")
	for i, value := range row {
		if i != 0 { b.WriteString(", ") }
		if value.IsNull() { b.WriteString("null"); continue }
		text, err := value.Type().Output(context.Background(), value)
		if err != nil { return "", err }
		if len(text) > 64 {
			n := 64
			for !utf8.RuneStart(text[n]) { n-- }
			b.WriteString(text[:n])
			b.WriteString("...")
		} else {
			b.WriteString(text)
		}
	}
	b.WriteString(").")
	return b.String(), nil
}
