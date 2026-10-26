package reference_test

import (
	"fmt"
	"strings"
)

// Independent released input signatures. Fixed builtin OIDs are stable; no
// native declaration or deparser is used to construct expected parameter cells.
func ipBuiltinDefaultRows(db, name string, oid int, defaults map[int]string) [][]any {
	signatures := map[int]string{
		3464: "int4 int4 int4 int4 int4 int4 float8", 3579: "jsonb _text jsonb bool", 3261: "json bool",
		4005: "jsonb jsonpath jsonb bool", 4006: "jsonb jsonpath jsonb bool", 4007: "jsonb jsonpath jsonb bool", 4008: "jsonb jsonpath jsonb bool", 4009: "jsonb jsonpath jsonb bool",
		3960: "anyelement json bool", 3961: "anyelement json bool", 4350: "text text", 4351: "text text", 6212: "float8 float8",
		3305: "jsonb _text jsonb bool", 5054: "jsonb _text jsonb bool text", 1268: "text bool", 6388: "anyarray", 6389: "anyarray bool", 6390: "anyarray bool bool",
	}
	var names []string
	for _, d := range ipBuiltinNames {
		if d.oid == oid {
			names = d.names
			break
		}
	}
	var rows [][]any
	identity := fmt.Sprintf("%s_%d", name, oid)
	for i, udt := range strings.Fields(signatures[oid]) {
		data := udt
		switch udt {
		case "int4":
			data = "integer"
		case "float8":
			data = "double precision"
		case "bool":
			data = "boolean"
		case "_text":
			data = "ARRAY"
		}
		var parameterName, def any
		if len(names) > 0 {
			parameterName = names[i]
		}
		if value, ok := defaults[i+1]; ok {
			def = value
		}
		rows = append(rows, ipRow(db, "pg_catalog", identity, i+1, parameterName, "IN", data, "pg_catalog", udt, def))
	}
	return rows
}
