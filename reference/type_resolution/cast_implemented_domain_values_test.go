package reference_test

import (
	"fmt"

	"stuntgres/reference/fixture"
)

// Source constraints are checked before the cast under test. The finite
// yes_or_no value domain cannot be replaced by arbitrary destination seeds.
// varchar(3) uses released compact inputs where an intersection exists (Julian
// j0, unbounded ranges, empty arrays, zero IPv6, NULL composite fields).
func castConstrainedStringValue(cell castMatrixCell) (castMatrixValue, bool) {
	source, target := cell.source, cell.target
	value := castMatrixValue{input: source.seed, output: source.output}
	if source.name != "cast_short" && source.name != "information_schema.yes_or_no" {
		return value, false
	}
	if !cell.contexts[2].admitted {
		return value, true
	}
	physical := castPhysicalFact(target)
	if source.name == "cast_short" {
		if len([]rune(target.seed)) <= 3 && target.name != "regclass" && target.name != "pg_node_tree" && castInventoryIdentity(target) != "record" {
			value.input, value.output = target.seed, target.output
			return value, true
		}
		value.input = "red"
		switch {
		case target.name == "cast_array_domain":
			value.input, value.output = "{1}", "{1}"
		case physical.element != "":
			value.input, value.output = "{}", "{}"
		case physical.kind == "r":
			value.input, value.output = "(,)", "(,)"
		case physical.kind == "m":
			value.input, value.output = "{}", "{}"
		case physical.kind == "c":
			value.input, value.output = "(,)", "(,)"
		default:
			switch physical.name {
			case "money":
				value.input, value.output = "1", "$1.00"
			case "bytea":
				value.input, value.output = `\x`, `\x`
			case "aclitem":
				value.input, value.output = "=", "=/postgres"
			case "refcursor":
				value.input, value.output = "red", "red"
			case "date":
				value.input, value.output = "j0", "4714-11-24 BC"
			case "timestamp":
				value.input, value.output = "j0", "4714-11-24 00:00:00 BC"
			case "timestamptz":
				value.input, value.output = "j0", "4714-11-24 00:00:00+00 BC"
			case "time":
				value.input, value.output = "0:0", "00:00:00"
			case "timetz":
				value.input, value.output = "0:0", "00:00:00+00"
			case "interval":
				value.input, value.output = "0", "00:00:00"
			case "inet":
				value.input, value.output = "::", "::"
			case "cidr":
				value.input, value.output = "::", "::/128"
			case "point":
				value.input, value.output = "0,0", "(0,0)"
			case "path", "polygon":
				value.input, value.output = "0,0", "((0,0))"
			case "cube":
				value.input, value.output = "(1)", "(1)"
			case "regclass":
				value.input, value.output = "x", "x"
			default:
				value.diagnostic = castConstrainedInputFailure(physical, value.input)
			}
		}
		if target.name == "earth" {
			value.diagnostic = &fixture.Diagnostic{Code: "23514", Message: `value for domain earth violates check constraint "on_surface"`, Schema: "public", DataType: "earth", Constraint: "on_surface"}
		}
	} else {
		value.input = "YES"
		switch physical.name {
		case "bool":
			value.output = "t"
		case "bytea":
			value.output = `\x594553`
		case "name", "text", "varchar", "bpchar", "citext", "refcursor":
			value.output = "YES"
		case `"char"`:
			value.output = "Y"
		case "tsvector", "tsquery":
			value.output = "'YES'"
		case "void":
			value.output = ""
		default:
			value.diagnostic = castConstrainedInputFailure(physical, value.input)
		}
	}
	return value, true
}

// Error literals follow the respective REL_18_6 input functions; no native
// admission/planner/codec is called to manufacture expected outcomes.
func castConstrainedInputFailure(target castImplementedType, input string) *fixture.Diagnostic {
	invalid := func(name string) *fixture.Diagnostic {
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("invalid input syntax for type %s: %q", name, input)}
	}
	if target.element != "" {
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("malformed array literal: %q", input), Detail: `Array value must start with "{" or dimension information.`}
	}
	if target.kind == "r" {
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("malformed range literal: %q", input), Detail: "Missing left parenthesis or bracket."}
	}
	if target.kind == "m" {
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("malformed multirange literal: %q", input), Detail: "Missing left brace."}
	}
	if target.kind == "c" {
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("malformed record literal: %q", input), Detail: "Missing left parenthesis."}
	}
	if target.kind == "e" {
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("invalid input value for enum %s: %q", target.name, input)}
	}
	switch target.name {
	case "int2", "int4", "int8", "float4", "float8", "numeric", "money", "oid", "tid", "xid", "xid8", "cid", "uuid", "pg_lsn", "pg_snapshot", "point", "lseg", "path", "box", "polygon", "line", "circle", "inet", "cidr", "macaddr", "macaddr8":
		return invalid(target.diagnostic)
	case "date", "time", "timetz", "timestamp", "timestamptz", "interval":
		name := target.diagnostic
		if target.name == "time" || target.name == "timestamp" {
			name = target.name
		}
		d := invalid(name)
		d.Code = "22007"
		return d
	case "bit", "varbit":
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("%q is not a valid binary digit", input[:1])}
	case "json", "jsonb":
		return &fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type json", Detail: fmt.Sprintf("Token %q is invalid.", input), Context: "JSON data, line 1: " + input}
	case "jsonpath":
		return &fixture.Diagnostic{Code: "42601", Message: "syntax error at end of jsonpath input"}
	case "cube":
		return &fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for cube", Detail: fmt.Sprintf("syntax error at or near %q", input[:1])}
	case "query_int":
		return &fixture.Diagnostic{Code: "42601", Message: "syntax error"}
	case "ean13":
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("invalid input syntax for EAN13 number: %q", input)}
	case "pg_node_tree":
		return &fixture.Diagnostic{Code: "0A000", Message: "cannot accept a value of type pg_node_tree"}
	case "cast_anonymous_record", "cast_deferred_record":
		return &fixture.Diagnostic{Code: "0A000", Message: "input of anonymous composite types is not implemented"}
	case "regclass":
		return &fixture.Diagnostic{Code: "42P01", Message: `relation "yes" does not exist`}
	case "regtype":
		return &fixture.Diagnostic{Code: "42704", Message: `type "yes" does not exist`}
	case "regproc":
		return &fixture.Diagnostic{Code: "42883", Message: `function "YES" does not exist`}
	case "regprocedure", "regoperator":
		return &fixture.Diagnostic{Code: "22P02", Message: "expected a left parenthesis"}
	case "regoper":
		return &fixture.Diagnostic{Code: "42883", Message: "operator does not exist: YES"}
	case "regrole":
		return &fixture.Diagnostic{Code: "42704", Message: `role "yes" does not exist`}
	case "regnamespace":
		return &fixture.Diagnostic{Code: "3F000", Message: `schema "yes" does not exist`}
	case "regcollation":
		return &fixture.Diagnostic{Code: "42704", Message: `collation "yes" for encoding "UTF8" does not exist`}
	case "regconfig":
		return &fixture.Diagnostic{Code: "42704", Message: `text search configuration "yes" does not exist`}
	case "regdictionary":
		return &fixture.Diagnostic{Code: "42704", Message: `text search dictionary "yes" does not exist`}
	case "aclitem":
		return &fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("unrecognized key word: %q", input), Hint: `ACL key word must be "group" or "user".`}
	}
	panic("missing constrained-source input fact for " + target.name)
}

func castConstrainedNumericValue(source castImplementedType, value castMatrixValue) castMatrixValue {
	if (source.name == "cast_positive" || source.name == "cast_nested") && (value.input == "0" || value.input == "-2") {
		if value.output == "0" || value.output == "-" {
			value.output = "1"
		}
		value.input = "1"
	}
	if source.name == "information_schema.cardinal_number" && value.input == "-2" {
		value.input = "1"
	}
	return value
}
