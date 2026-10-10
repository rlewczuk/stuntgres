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
	case parser.BitStringValue:
		return "bit"
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

func failingRowDetail(row []sqltype.Value, output sqltype.OutputEnvironment, columns []ColumnDescriptor, visible []int) (string, error) {
	var b strings.Builder
	b.WriteString("Failing row contains (")
	if columns != nil {
		for i, index := range visible {
			if i != 0 {
				b.WriteString(", ")
			}
			b.WriteString(columns[index].Name)
		}
		b.WriteString(") = (")
	}
	count := len(row)
	if columns != nil {
		count = len(visible)
	}
	for i := range count {
		index := i
		if columns != nil {
			index = visible[i]
		}
		value := row[index]
		if i != 0 {
			b.WriteString(", ")
		}
		if value.IsNull() {
			b.WriteString("null")
			continue
		}
		text, err := value.Type().OutputWithEnvironment(context.Background(), value, output)
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
