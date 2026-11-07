package parser

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func selectedOperandTarget(t *testing.T, source string) Expr {
	t.Helper()
	statements, err := Parse("SELECT " + source)
	if err != nil || len(statements) != 1 {
		t.Fatal(source, statements, err)
	}
	query, ok := statements[0].(*Select)
	if !ok || len(query.Targets) != 1 {
		t.Fatal("lost scalar projection", statements)
	}
	return query.Targets[0]
}

// A compact structural description deliberately ignores source positions and
// nonsemantic parser markers; the positioned script test covers those separately.
func selectedOperandShape(t *testing.T, expression Expr) string {
	t.Helper()
	children := func(expressions []Expr) string {
		parts := make([]string, len(expressions))
		for i, child := range expressions {
			parts[i] = selectedOperandShape(t, child)
		}
		return strings.Join(parts, ",")
	}
	name := func(object ObjectName) string {
		parts := make([]string, len(object.Parts))
		for i, part := range object.Parts {
			parts[i] = part.Text
		}
		return strings.Join(parts, ".")
	}
	switch node := expression.(type) {
	case FunctionExpr:
		return name(node.Name) + "(" + children(node.Args) + ")"
	case OperatorExpr:
		return name(node.Name) + "(" + children(node.Args) + ")"
	case CastExpr:
		return "cast:" + node.TypeName.Name + "(" + selectedOperandShape(t, node.Child) + ")"
	case LogicalExpr:
		op := [...]string{LogicalAnd: "and", LogicalOr: "or", LogicalNot: "not"}[node.Op]
		args := []Expr{node.Left}
		if node.Right != nil {
			args = append(args, node.Right)
		}
		return op + "(" + children(args) + ")"
	case LiteralExpr:
		switch node.Value.Kind {
		case IntegerValue:
			return strconv.FormatInt(node.Value.Integer, 10)
		case StringValue:
			return strconv.Quote(node.Value.Text)
		case BooleanValue:
			return strconv.FormatBool(node.Value.Boolean)
		case NullValue:
			return "null"
		}
	case ParameterExpr:
		return fmt.Sprintf("$%d", node.Number)
	case ColumnExpr:
		return name(node.Name)
	case ArrayExpr:
		return "array(" + children(node.Elements) + ")"
	case SubscriptExpr:
		parts := []string{selectedOperandShape(t, node.Base)}
		for _, subscript := range node.Subscripts {
			if subscript.Slice {
				parts = append(parts, selectedOperandShape(t, subscript.Lower)+":"+selectedOperandShape(t, subscript.Upper))
			} else {
				parts = append(parts, selectedOperandShape(t, subscript.Upper))
			}
		}
		return "subscript(" + strings.Join(parts, ",") + ")"
	case FieldExpr:
		return "field:" + node.Field.Text + "(" + selectedOperandShape(t, node.Base) + ")"
	case RowExpr:
		return "row(" + children(node.Fields) + ")"
	}
	t.Fatalf("unexpected operand node: %T %#v", expression, expression)
	return ""
}

func TestSelectedCallOperandsGrammar(t *testing.T) {
	// Arity and provider availability belong to binding. This matrix checks
	// that every scoped spelling retains its call/parameter syntax as a primary.
	for _, name := range []string{
		"cbrt", "degrees", "radians", "pi", "erf", "erfc", "gamma",
		"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible",
		"pg_ts_config_is_visible", "pg_ts_dict_is_visible", "pg_ts_parser_is_visible", "pg_ts_template_is_visible", "pg_statistics_obj_is_visible",
		"col_description", "obj_description", "shobj_description", "num_nulls", "num_nonnulls", "current_schemas",
		"random", "random_normal", "setseed", "min_scale", "round", "scale", "trim_scale", "trunc", "sign", "mod",
		"abs", "ceil", "ceiling", "div", "exp", "factorial", "floor", "gcd", "lcm",
		"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d", "cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand",
	} {
		for _, prefix := range []string{"", "pg_catalog."} {
			call := prefix + name + "($1)"
			for _, tc := range []struct{ source, shape string }{
				{call + "::text", "cast:text(" + call + ")"},
				{"CAST(" + call + " AS text)", "cast:text(" + call + ")"},
				{"1+" + call, "+(1," + call + ")"},
				{call + "+1", "+(" + call + ",1)"},
				{call + "=1", "=(" + call + ",1)"},
				{"1=" + call, "=(1," + call + ")"},
			} {
				if got := selectedOperandShape(t, selectedOperandTarget(t, tc.source)); got != tc.shape {
					t.Fatal(tc.source, got, tc.shape)
				}
			}
		}
	}
	for _, tc := range []struct{ source, shape string }{
		{"md5(random()::text || id::text)", "md5(||(cast:text(random()),cast:text(id)))"},
		{"abs(-round(-1)::int4)", "abs(-(cast:integer(round(-1))))"},
		{"floor(random()*10)", "floor(*(random(),10))"},
		{"normalize(abs(1))", "pg_catalog.normalize(abs(1))"},
		{"trim(abs(1) FROM abs(2))", "pg_catalog.btrim(abs(2),abs(1))"},
		{"overlay(abs(1) PLACING abs(2) FROM abs(3) FOR abs(4))", "pg_catalog.overlay(abs(1),abs(2),abs(3),abs(4))"},
		{"position(abs(1) IN abs(2))", "pg_catalog.position(abs(2),abs(1))"},
		{"substring(abs(1) FROM abs(2) FOR abs(3))", "pg_catalog.substring(abs(1),abs(2),abs(3))"},
		{"(current_schemas(false))[1]", "subscript(current_schemas(false),1)"},
		{"(ARRAY[10,20])[abs($1)]", "subscript(array(10,20),abs($1))"},
		{"(ARRAY[10,20])[abs($1):abs($2)]", "subscript(array(10,20),abs($1):abs($2))"},
		{"(ROW(abs(-1),2)).f1", "field:f1(row(abs(-1),2))"},
		{"(abs(-1)).missing", "field:missing(abs(-1))"},
	} {
		if got := selectedOperandShape(t, selectedOperandTarget(t, tc.source)); got != tc.shape {
			t.Fatal(tc.source, got, tc.shape)
		}
	}
	for _, source := range []string{"abs($1) COLLATE pg_catalog.\"C\"", "abs($1) AT TIME ZONE abs($2)", "abs($1) AT LOCAL", "extract(epoch FROM abs($1))"} {
		expression := selectedOperandTarget(t, source)
		switch node := expression.(type) {
		case CollateExpr:
			if selectedOperandShape(t, node.Child) != "abs($1)" || len(node.Name.Parts) != 2 || node.Name.Parts[1].Text != "C" {
				t.Fatal(source, node)
			}
		case TimeZoneExpr:
			if selectedOperandShape(t, node.Value) != "abs($1)" || (!node.Local && selectedOperandShape(t, node.Zone) != "abs($2)") {
				t.Fatal(source, node)
			}
		case FunctionExpr:
			if len(node.Args) != 2 || selectedOperandShape(t, node.Args[1]) != "abs($1)" {
				t.Fatal(source, node)
			}
		default:
			t.Fatalf("lost scalar annotation: %s: %T", source, node)
		}
	}
	for _, source := range []string{"pg_catalog.abs(x => $1)::text", "pg_catalog.abs(VARIADIC $1)::text", "pg_catalog.abs($1) WITHIN GROUP (ORDER BY $2) FILTER (WHERE $3) OVER (PARTITION BY $4)::text"} {
		cast := selectedOperandTarget(t, source).(CastExpr)
		call := cast.Child.(FunctionExpr)
		if len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog" || call.Name.Parts[1].Text != "abs" || len(call.Args) != 1 || call.Args[0].(ParameterExpr).Number != 1 {
			t.Fatal("lost qualified call/parameter", source, call)
		}
		if strings.Contains(source, "=>") && (len(call.ArgNames) != 1 || call.ArgNames[0].Text != "x") {
			t.Fatal("lost named argument", call)
		}
		if call.Variadic != strings.Contains(source, "VARIADIC") {
			t.Fatal("lost VARIADIC", call)
		}
		if strings.Contains(source, "WITHIN") && (!call.Within || len(call.Order) != 1 || call.Order[0].Expr.(ParameterExpr).Number != 2 || call.Filter.(ParameterExpr).Number != 3 || call.Window == nil || len(call.Window.Partition) != 1 || call.Window.Partition[0].(ParameterExpr).Number != 4) {
			t.Fatal("lost call decorations", call)
		}
	}
	for _, source := range []string{
		"INSERT INTO t(v) VALUES(1+abs($1)) RETURNING abs(v)::text,abs(v)*2",
		"UPDATE t SET v=1+abs($1) WHERE abs(v)+0=1 RETURNING abs(v)::text,abs(v)*2",
	} {
		statements, err := Parse(source)
		if err != nil || len(statements) != 1 {
			t.Fatal("selected mutation consumer rejected", source, statements, err)
		}
		var value Expr
		var returning *Returning
		switch statement := statements[0].(type) {
		case *Insert:
			value, returning = statement.Rows[0][0], statement.Returning
		case *Update:
			value, returning = statement.Assignments[0].Value, statement.Returning
			if got := selectedOperandShape(t, statement.Where); got != "=(+(abs(v),0),1)" {
				t.Fatal("lost mutation predicate", got)
			}
		}
		if got := selectedOperandShape(t, value); got != "+(1,abs($1))" {
			t.Fatal("lost mutation scalar", got)
		}
		if returning == nil || len(returning.Targets) != 2 || selectedOperandShape(t, returning.Targets[0]) != "cast:text(abs(v))" || selectedOperandShape(t, returning.Targets[1]) != "*(abs(v),2)" {
			t.Fatal("lost selected RETURNING expressions", returning)
		}
	}
	// This cutover does not extend positional arguments to arbitrary Boolean
	// expressions or promote DEFAULT to executable selected-provider authority.
	for _, source := range []string{"SELECT abs(TRUE AND FALSE)", "SELECT abs(1=1)", "CREATE TABLE t(v int DEFAULT abs(1))", "SELECT random()[1]"} {
		if statements, err := Parse(source); err == nil || len(statements) != 0 {
			t.Fatal("unrelated consumer boundary widened", source, statements, err)
		}
	}
}

func TestSelectedCallOperandsPrecedence(t *testing.T) {
	for _, tc := range []struct{ source, shape string }{
		{"1+abs(-2)*3", "+(1,*(abs(-2),3))"},
		{"(1+abs(-2))*3", "*(+(1,abs(-2)),3)"},
		{"-abs(-2)^2", "^(-(abs(-2)),2)"},
		{"+abs(-2)^2", "^(+(abs(-2)),2)"},
		{"abs(-2)^3^2", "^(^(abs(-2),3),2)"},
		{"abs(-2)::text || 'x'", "||(cast:text(abs(-2)),\"x\")"},
		{"CAST(abs(-2)+1 AS text)", "cast:text(+(abs(-2),1))"},
		{"NOT abs(-1)+1=2 OR abs(-2)*2=4 AND FALSE", "or(not(=(+(abs(-1),1),2)),and(=(*(abs(-2),2),4),false))"},
		{"TRUE OR abs(-2)*2=4 AND FALSE", "or(true,and(=(*(abs(-2),2),4),false))"},
		{"(TRUE OR abs(-2)*2=4) AND FALSE", "and(or(true,=(*(abs(-2),2),4)),false)"},
		{"abs(1) #~# abs(2)*3", "#~#(abs(1),*(abs(2),3))"},
		{"abs(1) OPERATOR(public.+) abs(2)*3", "public.+(abs(1),*(abs(2),3))"},
		{"abs(1) #~# abs(2) #/# abs(3)", "#/#(#~#(abs(1),abs(2)),abs(3))"},
	} {
		if got := selectedOperandShape(t, selectedOperandTarget(t, tc.source)); got != tc.shape {
			t.Fatal(tc.source, got, tc.shape)
		}
	}
}

func TestSelectedCallOperandsScriptPositions(t *testing.T) {
	const source = "CREATE TABLE prefix(v INT); /*é*/ INSERT INTO t VALUES(abs($1)::text); SELECT 1+pg_catalog.\"ABS\"(x => $2)::int4"
	statements, err := Parse(source)
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	assertPosition := func(position Position, token string) {
		t.Helper()
		offset := strings.Index(source, token)
		want := Position{Offset: offset, Character: utf8.RuneCountInString(source[:offset]) + 1}
		if position != want {
			t.Fatal(token, position, want)
		}
	}
	insert := statements[1].(*Insert)
	cast := insert.Rows[0][0].(CastExpr)
	call := cast.Child.(FunctionExpr)
	assertPosition(insert.Pos, "INSERT")
	assertPosition(cast.Pos, "::text")
	assertPosition(call.Pos, "abs(")
	assertPosition(call.Name.Parts[0].Pos, "abs(")
	assertPosition(call.Args[0].SourcePosition(), "$1")
	product := statements[2].(*Select).Targets[0].(OperatorExpr)
	assertPosition(product.Pos, "+pg_catalog")
	cast = product.Args[1].(CastExpr)
	call = cast.Child.(FunctionExpr)
	assertPosition(cast.Pos, "::int4")
	assertPosition(call.Pos, "pg_catalog")
	assertPosition(call.Name.Parts[0].Pos, "pg_catalog")
	assertPosition(call.Name.Parts[1].Pos, "\"ABS\"")
	assertPosition(call.ArgNames[0].Pos, "x =>")
	assertPosition(call.Args[0].SourcePosition(), "$2")
	if call.Name.Parts[1].Text != "ABS" || call.Args[0].(ParameterExpr).Number != 2 {
		t.Fatal("lost quoted spelling/parameter", call)
	}
	for _, prefix := range []string{"/*é*/ ", "CREATE TABLE prefix(v INT); /*é*/ INSERT INTO t VALUES(abs(1)+2); "} {
		query := prefix + "SELECT abs(1,)"
		position := strconv.Itoa(utf8.RuneCountInString(query[:strings.LastIndex(query, ")")]) + 1)
		keywordDiagnostic(t, query, "42601", `syntax error at or near ")"`, position)
		query = prefix + "SELECT random()::text WHERE"
		keywordDiagnostic(t, query, "42601", "syntax error at end of input", strconv.Itoa(utf8.RuneCountInString(query)+1))
	}
}
