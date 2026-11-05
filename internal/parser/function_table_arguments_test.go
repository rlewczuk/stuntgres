package parser

import (
	"strings"
	"testing"
)

func TestFunctionTableCallArguments(t *testing.T) {
	for _, name := range []string{"carrier_scalar", "carrier_schema.carrier_scalar", "pg_catalog.pg_typeof", "pg_catalog.lower"} {
		for _, spec := range []struct {
			args string
			star, distinct, all, variadic bool
			count, order int
			label string
		}{
			{"*", true, false, false, false, 0, 0, ""},
			{"DISTINCT 1", false, true, false, false, 1, 0, ""},
			{"ALL 1 ORDER BY 1 DESC", false, false, true, false, 1, 1, ""},
			{"1 ORDER BY 1", false, false, false, false, 1, 1, ""},
			{"v=>1", false, false, false, false, 1, 0, "v"},
			{"VARIADIC ARRAY[1]", false, false, false, true, 1, 0, ""},
		} {
			query := "SELECT * FROM "+name+"("+spec.args+")"
			statements, err := Parse(query)
			if err != nil { t.Fatal(query, err) }
			from, ok := statements[0].(*Select).From.(*FunctionTable)
			if !ok || len(from.Calls) != 1 { t.Fatal("missing generic source carrier", query, statements) }
			call := from.Calls[0]
			parts := strings.Split(name, ".")
			if len(call.Name.Parts) != len(parts) { t.Fatal("lost qualified function name", call.Name) }
			for i, part := range parts {
				if call.Name.Parts[i].Text != part { t.Fatal("changed function identity", name, call.Name) }
			}
			if call.Star != spec.star || call.Distinct != spec.distinct || call.All != spec.all || call.Variadic != spec.variadic || len(call.Args) != spec.count || len(call.Order) != spec.order || call.Pos.Character != 15 || call.Name.Parts[len(call.Name.Parts)-1].Text == "" {
				t.Fatal("lost ordinary FROM argument syntax/provenance", query, call)
			}
			if spec.label != "" && (len(call.ArgNames) != 1 || call.ArgNames[0].Text != spec.label) { t.Fatal("lost named argument", call) }
			if spec.order != 0 && call.Order[0].Pos.Character <= call.Pos.Character { t.Fatal("lost ORDER location", call) }
		}
	}
	for _, query := range []string{
		"SELECT * FROM carrier_scalar(DISTINCT)",
		"SELECT * FROM carrier_scalar(1 ORDER BY)",
		"SELECT * FROM carrier_scalar(1) FILTER (WHERE true)",
		"SELECT * FROM carrier_scalar(1) OVER ()",
	} {
		if _, err := Parse(query); err == nil { t.Fatal("malformed/FROM-only decoration unexpectedly admitted", query) }
	}
}
