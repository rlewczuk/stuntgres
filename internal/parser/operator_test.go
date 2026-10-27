package parser

import (
	"strings"
	"testing"
)

func TestUserOperatorOptionGrammar(t *testing.T) {
	for _, tc := range []struct {
		rhs   string
		kind  OperatorOptionArgumentKind
		value string
	}{
		{"", OperatorOptionNoArgument, ""},
		{"NONE", OperatorOptionNoneArgument, ""},
		{`"none"`, OperatorOptionTypeArgument, ""},
		{"'none'", OperatorOptionStringArgument, "none"},
		{"''", OperatorOptionStringArgument, ""},
		{"'pg_catalog.int4'", OperatorOptionStringArgument, "pg_catalog.int4"},
		{`'A."B"'`, OperatorOptionStringArgument, `A."B"`},
		{"TRUE", OperatorOptionStringArgument, "true"},
		{"FALSE", OperatorOptionStringArgument, "false"},
		{"ON", OperatorOptionStringArgument, "on"},
		{"NULL", OperatorOptionStringArgument, "null"},
		{"DEFAULT", OperatorOptionStringArgument, "default"},
		{"off", OperatorOptionTypeArgument, ""},
		{`"OFF"`, OperatorOptionTypeArgument, ""},
		{"pg_catalog.int4", OperatorOptionTypeArgument, ""},
		{"int4", OperatorOptionTypeArgument, ""},
		{"int4[]", OperatorOptionTypeArgument, ""},
		{"numeric(8,2)", OperatorOptionTypeArgument, ""},
		{"SETOF int4", OperatorOptionTypeArgument, ""},
		{"OPERATOR(/*x*/public.#=#)", OperatorOptionNameArgument, ""},
		{"public.#=#", OperatorOptionNameArgument, ""},
		{"#=#", OperatorOptionNameArgument, ""},
		{"0", OperatorOptionIntegerArgument, "0"},
		{"+1", OperatorOptionIntegerArgument, "+1"},
		{"-0", OperatorOptionIntegerArgument, "-0"},
		{"-1", OperatorOptionIntegerArgument, "-1"},
		{"0x1", OperatorOptionIntegerArgument, "0x1"},
		{"0o1", OperatorOptionIntegerArgument, "0o1"},
		{"0b1", OperatorOptionIntegerArgument, "0b1"},
		{"1_0", OperatorOptionIntegerArgument, "1_0"},
		{"2147483647", OperatorOptionIntegerArgument, "2147483647"},
		{"-2147483647", OperatorOptionIntegerArgument, "-2147483647"},
		{"2147483648", OperatorOptionFloatArgument, "2147483648"},
		{"-2147483648", OperatorOptionFloatArgument, "-2147483648"},
		{"9223372036854775808", OperatorOptionFloatArgument, "9223372036854775808"},
		{"1.0", OperatorOptionFloatArgument, "1.0"},
		{"+1e0", OperatorOptionFloatArgument, "+1e0"},
	} {
		for _, prefix := range []string{"CREATE OPERATOR public.#=# (", "ALTER OPERATOR public.#=# (int4,int4) SET ("} {
			q := prefix + "hashes"
			if tc.rhs != "" {
				q += " = " + tc.rhs
			}
			q += ")"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			var options []OperatorOption
			switch s := statements[0].(type) {
			case *CreateOperator:
				options = s.Options
			case *AlterOperator:
				options = s.Options
			}
			if len(options) != 1 {
				t.Fatal(q, options)
			}
			o := options[0]
			if o.Name != "hashes" || o.HasValue != (tc.rhs != "") || o.Kind != tc.kind || o.Value != tc.value || o.Pos.Offset != len(prefix) {
				t.Fatal("option kind/presence/payload", q, o)
			}
			if o.HasValue && (o.ValuePos.Offset != len(prefix)+len("hashes = ") || o.ValuePos.Character != o.ValuePos.Offset+1) {
				t.Fatal("RHS position", q, o)
			}
			if tc.rhs == "SETOF int4" && (!o.Type.Setof || o.Type.Pos.Offset != o.ValuePos.Offset+len("SETOF ")) {
				t.Fatal("SETOF/type positions", q, o)
			}
			if tc.rhs == "int4" || tc.rhs == "int4[]" {
				if o.Type.Name != "integer" || len(o.Type.Parts) != 1 || o.Type.Parts[0].Text != "int4" || o.Type.Parts[0].Pos != o.Type.Pos {
					t.Fatal("canonical type lost original name component", q, o)
				}
			}
			if tc.rhs == "numeric(8,2)" && len(o.Type.Modifiers) != 2 || tc.rhs == "int4[]" && len(o.Type.ArrayBounds) != 1 {
				t.Fatal("type decorations lost", q, o)
			}
			if tc.rhs == "OPERATOR(/*x*/public.#=#)" && (o.Reference.Parts[0].Pos.Offset != strings.Index(q, "public.#=#)") || o.Reference.Parts[1].Pos.Offset != strings.LastIndex(q, "#=#")) {
				t.Fatal("qualified option component positions", q, o)
			}
		}
	}
	for _, schema := range []string{"public", `"Op Schema"`, "text", "view", "interval"} {
		q := "CREATE OPERATOR public.#=# (LEFTARG = pg_catalog.int4, RIGHTARG = SETOF int4, FUNCTION = pg_catalog.int4eq, COMMUTATOR = " + schema + ".#=#, NEGATOR = OPERATOR(/*comment*/public.#!#), HASHES, MERGES = false)"
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		create := statements[0].(*CreateOperator)
		options := create.Options
		if len(options) != 7 || options[0].Type.Schema.Text != "pg_catalog" || options[0].Type.Name != "int4" || !options[1].Type.Setof || options[2].Type.Name != "int4eq" {
			t.Fatal("type/function option derivation", options)
		}
		parts := options[3].Reference.Parts
		if len(parts) != 2 || parts[0].Text != strings.Trim(schema, `"`) || parts[1].Text != "#=#" || parts[0].Pos.Offset != strings.LastIndex(q, schema+".#=#") {
			t.Fatal("qualified symbolic option", parts)
		}
		if options[4].Reference.Parts[1].Text != "#!#" || options[5].HasValue || options[5].Kind != OperatorOptionNoArgument || options[5].Value != "" || !options[6].HasValue || options[6].Kind != OperatorOptionStringArgument || options[6].Value != "false" {
			t.Fatal("qualified OPERATOR or flag options", options)
		}
	}
	for _, q := range []string{
		"ALTER OPERATOR public.#=# (int4,int4) SET (COMMUTATOR = public.#=#, RESTRICT = NONE)",
		"DROP OPERATOR public.#=# (int4,int4)",
		`SELECT operator, "operator" FROM r`,
		`SELECT "operator"(1)`,
		"SELECT OPERATOR(public.##) 3",
		"SELECT 2 OPERATOR(public.#=#) ANY (ARRAY[2,3])",
		"SELECT 2 OPERATOR(public.#=#) ALL (SELECT i FROM r)",
		// SETOF admission is confined to unquoted operator-option type input.
		`SELECT setof FROM r`,
		`CREATE OPERATOR public.## (RIGHTARG = "setof", FUNCTION = lower); SELECT setof FROM r`,
		`CREATE OPERATOR public.## (RIGHTARG = int4, FUNCTION = int4um); SELECT setof = setof FROM r`,
		"SELECT abs(2) #~# abs(3)",
		"SELECT abs(2 #~# 3)",
		"CREATE TABLE d(i INT DEFAULT (abs(2)))",
		"CREATE TABLE d(i INT DEFAULT abs(2) #~# 3)",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestUserOperatorQualifiedPrecedenceGrammar(t *testing.T) {
	statements, err := Parse("SELECT 2 OPERATOR(pg_catalog.*) 3 + 4")
	if err != nil {
		t.Fatal(err)
	}
	root := statements[0].(*Select).Targets[0].(OperatorExpr)
	if len(root.Name.Parts) != 2 || root.Name.Parts[1].Text != "*" || root.Args[1].(OperatorExpr).Name.Parts[0].Text != "+" {
		t.Fatal("qualified operator lost generic precedence", root)
	}
}

func TestUserOperatorQualifiedAssociativityGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT 2 OPERATOR(public.#~#) 3 OPERATOR(public.#/#) 4",
		"SELECT 2 #~# 3 OPERATOR(public.#/#) 4",
		"SELECT 2 OPERATOR(public.#~#) 3 #/# 4",
	} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		root := statements[0].(*Select).Targets[0].(OperatorExpr)
		if root.Name.Parts[len(root.Name.Parts)-1].Text != "#/#" {
			t.Fatal("generic operators are not left associative", q, root)
		}
		left, ok := root.Args[0].(OperatorExpr)
		if !ok || left.Name.Parts[len(left.Name.Parts)-1].Text != "#~#" {
			t.Fatal("generic left operand lost its operator", q, root)
		}
	}
	statements, err := Parse("SELECT OPERATOR(public.##) 2 + 3 OPERATOR(public.#~#) 4")
	if err != nil {
		t.Fatal(err)
	}
	root := statements[0].(*Select).Targets[0].(OperatorExpr)
	prefix, ok := root.Args[0].(OperatorExpr)
	if !ok || len(prefix.Args) != 1 || prefix.Name.Parts[1].Text != "##" {
		t.Fatal("qualified prefix lost generic precedence", root)
	}
	sum, ok := prefix.Args[0].(OperatorExpr)
	if !ok || sum.Name.Parts[0].Text != "+" {
		t.Fatal("arithmetic must bind inside generic prefix operand", prefix)
	}
}

func TestUserOperatorConsumerAdmissionGrammar(t *testing.T) {
	for _, q := range []string{
		"CREATE TABLE operator(x INT)",
		"CREATE TABLE public.operator(x INT)",
		"CREATE TABLE t(operator INT)",
		"SELECT operator FROM operator",
		"SELECT abs(2) #~# abs(3)",
		"SELECT abs(-2) #~# abs(-3)*4",
		"SELECT abs(-2) OPERATOR(public.#~#) abs(-3)*4",
		"INSERT INTO t VALUES(abs(-2) #~# abs(-3)*4)",
		"SELECT abs(abs(2)) OPERATOR(public.+) abs(3)",
		"INSERT INTO t VALUES(abs(2) #~# abs(3))",
		"INSERT INTO t ((VALUES(abs(2) OPERATOR(public.+) abs(3)) ORDER BY 1 LIMIT 1))",
		"SELECT (num_nulls(NULL))::int FROM t",
		"SELECT (num_nulls(NULL))+1",
		"SELECT abs(num_nulls(NULL))",
		"SELECT current_setting(num_nulls(NULL))",
		"INSERT INTO t (VALUES(abs(1)) LIMIT 1) UNION ALL SELECT 2",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal("admitted operator/grouped/query context rejected", q, statements, err)
		}
	}
	for _, q := range []string{
		"SELECT num_nulls(NULL)::int FROM t",
		"SELECT CAST(num_nulls(NULL) AS int) FROM t",
		"SELECT num_nulls(NULL)+1 FROM t",
		"INSERT INTO t VALUES(abs(1))",
		"INSERT INTO t ((VALUES(abs(1)) ORDER BY 1 LIMIT 1))",
		"INSERT INTO t VALUES(num_nulls(NULL))",
	} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("selected call leaked into scalar consumer", q, statements, err)
		}
	}
	// The INSERT replay keeps original, absolute multibyte/script positions.
	for _, suffix := range []string{"", " LIMIT 1", " ORDER BY 1"} {
		keywordDiagnostic(t, "CREATE TABLE prefix(v INT); /*é*/ INSERT INTO t VALUES(abs(1))"+suffix,
			"42601", `syntax error at or near ")"`, "62")
	}
}
