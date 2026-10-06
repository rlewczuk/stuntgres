package engine

import (
	"context"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func literalTypeName(raw parser.Value) string {
	switch raw.Kind {
	case parser.NumericValue:
		return "numeric"
	case parser.IntegerValue:
		if raw.Integer < -2147483648 || raw.Integer > 2147483647 {
			return "bigint"
		}
		return "integer"
	case parser.BooleanValue:
		return "boolean"
	}
	return "unknown"
}

func failingRowDetail(row []sqltype.Value) (string, error) {
	var b strings.Builder
	b.WriteString("Failing row contains (")
	for i, value := range row {
		if i != 0 {
			b.WriteString(", ")
		}
		if value.IsNull() {
			b.WriteString("null")
			continue
		}
		text, err := value.Type().Output(context.Background(), value)
		if err != nil {
			return "", err
		}
		if len(text) > 64 {
			n := 64
			for !utf8.RuneStart(text[n]) {
				n--
			}
			b.WriteString(text[:n])
			b.WriteString("...")
		} else {
			b.WriteString(text)
		}
	}
	b.WriteString(").")
	return b.String(), nil
}
