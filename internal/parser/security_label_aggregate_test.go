package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestSecurityLabelOrderedSetModifiers(t *testing.T) {
	for _, pair := range [][2]string{
		{"1", "1"},
		{"01", "1"},
		{"CAST(1 AS int4)", "1::int4"},
		{"1::int4", "CAST(1 AS int4)"},
		{"abs(1)", "(abs(1))"},
		{"(abs(1))", "abs(1)"},
		{"abs(ALL 1)", "abs(1)"},
		{"abs(1)", "abs(ALL 1)"},
		{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)", "abs (1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)"},
		{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()", "abs (1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ( )"},
		{"CAST(1 AS s.ty)", `CAST(1 AS s."ty")`},
		{`CAST(1 AS s."ty")`, "CAST(1 AS s.ty)"},
		{"((SELECT 1) UNION SELECT 2)", "(SELECT 1 UNION SELECT 2)"},
		{"(SELECT 1 UNION SELECT 2)", "((SELECT 1) UNION SELECT 2)"},
		{"abs(1)+2", "abs ( 1 ) + 2"},
		{"CAST(abs(1) AS int4)+2", "CAST( abs ( 1 ) AS int4 ) + 2"},
		{"abs(1)::int4+2", "abs ( 1 ) :: int4 + 2"},
		{"abs(abs(1)+2)", "abs ( abs ( 1 ) + 2 )"},
		{"(SELECT 1)", "( SELECT 1 )"},
		{"(VALUES (1))", "( VALUES ( 1 ) )"},
		{"$1+2", "$1 + 2"},
	} {
		q := "SECURITY LABEL ON AGGREGATE a(VARIADIC int4(" + pair[0] + ") ORDER BY VARIADIC int4(" + pair[1] + ")) IS NULL"
		statements, err := Parse(q)
		if err != nil { t.Fatal(q, err) }
		label := statements[0].(*SecurityLabel)
		for _, argument := range label.Routine.Arguments {
			for _, modifier := range argument.Type.Modifiers {
				if modifier.syntax != nil { t.Fatal("modifier syntax escaped validation", q) }
			}
		}
		if err := VisitQueryExpressions(label, func(Expr) error { t.Fatal("utility modifier visited", q); return nil }); err != nil { t.Fatal(err) }
	}
	for _, tc := range []struct{ query, position string }{
		{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(1) ORDER BY VARIADIC int4(2)) IS NULL", "57"},
		{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(abs(1)+2) ORDER BY VARIADIC int4(abs(1)+3)) IS NULL", "64"},
		{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(int4(1)) ORDER BY VARIADIC int4(1::int4)) IS NULL", "63"},
		{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(abs(1)) ORDER BY VARIADIC int4((abs(2)))) IS NULL", "62"},
		{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(1::int4) ORDER BY VARIADIC int4(1::int8)) IS NULL", "63"},
		{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(1+1) ORDER BY VARIADIC int4(2)) IS NULL", "59"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", Position: tc.position}
		if len(statements) != 0 || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) { t.Fatalf("%s: statements=%#v error=%#v want=%#v", tc.query, statements, diagnostic, want) }
	}
	for _, tc := range []struct {
		pair [2]string
		same, signature bool
	}{
		{pair: [2]string{"CAST(1 AS decimal)", "CAST(1 AS numeric)"}, same: true},
		{pair: [2]string{"CAST(1 AS integer)", "CAST(1 AS pg_catalog.int4)"}, same: true},
		{pair: [2]string{"CAST(1 AS dec)", "CAST(1 AS pg_catalog.numeric)"}, same: true},
		{pair: [2]string{"CAST(1 AS decimal(4,1)[])", "CAST(1 AS pg_catalog.numeric(4,1)[])"}, same: true},
		{pair: [2]string{"CAST(1 AS smallint)", "CAST(1 AS pg_catalog.int2)"}, same: true},
		{pair: [2]string{"CAST(1 AS bigint)", "CAST(1 AS pg_catalog.int8)"}, same: true},
		{pair: [2]string{"CAST(1 AS boolean)", "CAST(1 AS pg_catalog.bool)"}, same: true},
		{pair: [2]string{"CAST(1 AS real)", "CAST(1 AS pg_catalog.float4)"}, same: true},
		{pair: [2]string{"CAST(1 AS double precision)", "CAST(1 AS pg_catalog.float8)"}, same: true},
		{pair: [2]string{"CAST(1 AS integer)", `CAST(1 AS "pg_catalog"."int4")`}, same: true},
		{pair: [2]string{"CAST(1 AS int4(CAST(1 AS decimal)))", "CAST(1 AS int4(CAST(1 AS numeric)))"}, same: true},
		{pair: [2]string{"CAST(1 AS int4)", "CAST(1 AS integer)"}},
		{pair: [2]string{`CAST(1 AS "integer")`, "CAST(1 AS integer)"}},
		{pair: [2]string{"CAST(1 AS pg_catalog.integer)", "CAST(1 AS integer)"}},
		{pair: [2]string{`CAST(1 AS s."Ty")`, "CAST(1 AS s.ty)"}},
		{pair: [2]string{`CAST(1 AS pg_catalog."INT4")`, "CAST(1 AS integer)"}},
		{pair: [2]string{"CAST(1 AS decimal(4,1))", "CAST(1 AS numeric(4,2))"}},
		{pair: [2]string{"CAST(1 AS numeric[])", "CAST(1 AS numeric[][])"}},
		{pair: [2]string{"CAST(1 AS numeric[])", "CAST(1 AS numeric[3])"}},
		{pair: [2]string{"CAST(1 AS numeric[])", "CAST(1 AS numeric[0])"}},
		{pair: [2]string{"CAST(1 AS numeric)", "CAST(1 AS numeric[])"}},
		{pair: [2]string{"CAST(1 AS int4(CAST(1 AS decimal)))", "CAST(1 AS int4(CAST(2 AS numeric)))"}},
		{pair: [2]string{"SETOF integer", "SETOF pg_catalog.int4"}, same: true, signature: true},
		{pair: [2]string{"SETOF integer", "integer"}, signature: true},
		{pair: [2]string{"t.v%TYPE", "t.v%TYPE"}, same: true, signature: true},
		{pair: [2]string{"t.v%TYPE", "t.w%TYPE"}, signature: true},
		{pair: [2]string{"t.v%TYPE", "t.v"}, signature: true},
		{pair: [2]string{"SETOF t.v%TYPE", "t.v%TYPE"}, signature: true},
		{pair: [2]string{"nchar", "pg_catalog.bpchar(1)"}, same: true, signature: true},
		{pair: [2]string{"national char", "pg_catalog.bpchar(1)"}, same: true, signature: true},
		{pair: [2]string{"national character", "pg_catalog.bpchar(1)"}, same: true, signature: true},
		{pair: [2]string{"nchar(3)", "pg_catalog.bpchar(3)"}, same: true, signature: true},
		{pair: [2]string{"national char(3)", "pg_catalog.bpchar(3)"}, same: true, signature: true},
		{pair: [2]string{"national character(3)", "pg_catalog.bpchar(3)"}, same: true, signature: true},
		{pair: [2]string{"nchar varying", "pg_catalog.varchar"}, same: true, signature: true},
		{pair: [2]string{"national char varying", "pg_catalog.varchar"}, same: true, signature: true},
		{pair: [2]string{"national character varying", "pg_catalog.varchar"}, same: true, signature: true},
		{pair: [2]string{"nchar varying(3)", "pg_catalog.varchar(3)"}, same: true, signature: true},
		{pair: [2]string{"national char varying(3)", "pg_catalog.varchar(3)"}, same: true, signature: true},
		{pair: [2]string{"national character varying(3)", "pg_catalog.varchar(3)"}, same: true, signature: true},
		{pair: [2]string{"nchar[]", "pg_catalog.bpchar(1)[]"}, same: true, signature: true},
		{pair: [2]string{"national character varying(3)[]", "pg_catalog.varchar(3)[]"}, same: true, signature: true},
		{pair: [2]string{"nchar", "pg_catalog.bpchar"}, signature: true},
		{pair: [2]string{"nchar", "pg_catalog.bpchar(2)"}, signature: true},
		{pair: [2]string{"nchar", "nchar varying"}, signature: true},
		{pair: [2]string{"nchar", `"nchar"`}, signature: true},
		{pair: [2]string{"nchar", "pg_catalog.nchar"}, signature: true},
		{pair: [2]string{"national character", `"national"`}, signature: true},
		{pair: [2]string{"nchar varying", "pg_catalog.varchar(3)"}, signature: true},
		{pair: [2]string{"nchar varying", `"nchar varying"`}, signature: true},
		{pair: [2]string{"s.nchar", `s."nchar"`}, same: true, signature: true},
		{pair: [2]string{"s.national(1)", `s."national"(1)`}, same: true, signature: true},
		{pair: [2]string{`s."Nchar"`, "s.nchar"}, signature: true},
		{pair: [2]string{"CAST(1 AS nchar)", "CAST(1 AS pg_catalog.bpchar(1))"}, same: true},
		{pair: [2]string{"CAST(1 AS national character varying(3))", "CAST(1 AS pg_catalog.varchar(3))"}, same: true},
		{pair: [2]string{"CAST(1 AS nchar)", `CAST(1 AS "nchar")`}},
		{pair: [2]string{"CAST(1 AS nchar)", "CAST(1 AS pg_catalog.bpchar(2))"}},
	} {
		for _, pair := range [][2]string{tc.pair, {tc.pair[1], tc.pair[0]}} {
			left, right := pair[0], pair[1]
			if !tc.signature { left, right = "int4("+left+")", "int4("+right+")" }
			query := "SECURITY LABEL ON AGGREGATE a(VARIADIC " + left + " ORDER BY VARIADIC " + right + ") IS NULL"
			statements, err := Parse(query)
			if tc.same {
				if err != nil || len(statements) != 1 { t.Fatalf("%s: statements=%#v error=%v", query, statements, err) }
				label := statements[0].(*SecurityLabel)
				for _, argument := range label.Routine.Arguments {
					for _, modifier := range argument.Type.Modifiers {
						if modifier.syntax != nil { t.Fatal("recursive type syntax escaped validation", query) }
					}
				}
			} else {
				position := strconv.Itoa(len([]rune("SECURITY LABEL ON AGGREGATE a(VARIADIC "+left+" ORDER BY ")) + 1)
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", Position: position}
				var diagnostic *sqlstate.Error
				if len(statements) != 0 || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) { t.Fatalf("%s: statements=%#v error=%#v want=%#v", query, statements, diagnostic, want) }
			}
		}
	}
}

func TestSecurityLabelCallDecorations(t *testing.T) {
	for _, tc := range []struct { call, message, position string }{
		{`abs(1) WITHIN GROUP (ORDER BY 1) WITHIN GROUP (ORDER BY 1)`, `syntax error at or near "WITHIN"`, "62"},
		{`abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) FILTER (WHERE false)`, `syntax error at or near "FILTER"`, "82"},
		{`abs(1) OVER () OVER ()`, `syntax error at or near "OVER"`, "44"},
		{`abs(1) FILTER (WHERE true) WITHIN GROUP (ORDER BY 1)`, `syntax error at or near "WITHIN"`, "56"},
		{`abs(1) OVER () FILTER (WHERE true)`, `syntax error at or near "FILTER"`, "44"},
		{`abs(1) OVER () WITHIN GROUP (ORDER BY 1)`, `syntax error at or near "WITHIN"`, "44"},
		{`abs(1) WITHIN GROUP (ORDER BY 1) OVER () FILTER (WHERE true)`, `syntax error at or near "FILTER"`, "70"},
		{`abs(DISTINCT 1) WITHIN GROUP (ORDER BY 1)`, `cannot use DISTINCT with WITHIN GROUP`, "45"},
		{`f(VARIADIC x) WITHIN GROUP (ORDER BY 1)`, `cannot use VARIADIC with WITHIN GROUP`, "43"},
		{`abs(1 ORDER BY 1) WITHIN GROUP (ORDER BY 1)`, `cannot use multiple ORDER BY clauses with WITHIN GROUP`, "47"},
	} {
		query := "SECURITY LABEL ON TYPE int4(" + tc.call + ") IS NULL"
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position}
		if len(statements) != 0 || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) { t.Fatalf("%s: statements=%#v error=%#v want=%#v", query, statements, diagnostic, want) }
	}
	// Cover every subset in PostgreSQL's decoration order across selected,
	// checked, string, time, ordinary, qualified and aggregate call routes.
	for _, call := range []string{"abs(1)", "lower('x')", "format('x')", "now()", "array_reverse(1)", "f(1)", `"f"(1)`, "s.f(1)", "pg_catalog.abs(1)", "sum(1)"} {
		for _, suffix := range []string{
			"", " WITHIN GROUP (ORDER BY 1)", " FILTER (WHERE true)", " OVER ()",
			" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)",
			" WITHIN GROUP (ORDER BY 1) OVER ()", " FILTER (WHERE true) OVER ()",
			" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()",
		} {
			query := "SECURITY LABEL ON TYPE int4(" + call + suffix + ") IS NULL"
			if statements, err := Parse(query); err != nil || len(statements) != 1 {
				t.Fatalf("%s: statements=%#v error=%v", query, statements, err)
			}
		}
		for _, suffix := range []string{
			" FILTER (WHERE true) FILTER (WHERE false)",
			" OVER () OVER ()",
			" WITHIN GROUP (ORDER BY 1) WITHIN GROUP (ORDER BY 2)",
			" FILTER (WHERE true) WITHIN GROUP (ORDER BY 1)",
			" OVER () WITHIN GROUP (ORDER BY 1)",
			" OVER () FILTER (WHERE true)",
			" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) FILTER (WHERE false)",
			" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER () FILTER (WHERE false)",
			" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER () OVER ()",
			" WITHIN GROUP (ORDER BY)", " FILTER (WHERE)", " OVER (ORDER BY)",
		} {
			query := "SECURITY LABEL ON TYPE int4(" + call + suffix + ") IS NULL"
			statements, err := Parse(query)
			var diagnostic *sqlstate.Error
			if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" {
				t.Fatalf("malformed decorations admitted: %s: statements=%#v error=%v", query, statements, err)
			}
		}
	}
	for _, pair := range [][2]string{
		{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE false)"},
		{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)", "abs(1) WITHIN GROUP (ORDER BY 2) FILTER (WHERE true)"},
		{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER (PARTITION BY 1)"},
		{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER (ORDER BY 1)", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER (ORDER BY 2)"},
		{"abs(1 ORDER BY 1) FILTER (WHERE true)", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)"},
	} {
		query := "SECURITY LABEL ON AGGREGATE a(VARIADIC int4(" + pair[0] + ") ORDER BY VARIADIC int4(" + pair[1] + ")) IS NULL"
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		position := strconv.Itoa(strings.Index(query, ") ORDER BY VARIADIC") + len(") ORDER BY ") + 1)
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Position != position {
			t.Fatalf("modifier decoration distinction lost: %s: statements=%#v error=%v", query, statements, err)
		}
	}
	// The ordinary SELECTED_FUNC route already retained WITHIN/FILTER/OVER
	// for binder diagnostics before SECURITY LABEL; syntax admission is not
	// aggregate execution capability.
	for _, suffix := range []string{
		" WITHIN GROUP (ORDER BY 1)", " WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)",
		" WITHIN GROUP (ORDER BY 1) OVER ()", " WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()",
	} {
		ordinary := "SELECT abs(1)" + suffix
		for _, query := range []string{ordinary, "SECURITY LABEL ON TYPE int4(abs(1)) IS NULL; " + ordinary, ordinary + "; SECURITY LABEL ON TYPE int4(abs(1)) IS NULL"} {
			want := 1
			if query != ordinary {
				want = 2
			}
			if statements, err := Parse(query); err != nil || len(statements) != want {
				t.Fatalf("ordinary decoration syntax lost: %s: statements=%#v error=%v", query, statements, err)
			}
		}
	}
	// The transaction-time/array-transform route has a different historical
	// frontier: WITHIN is admitted for array transforms, but FILTER after it
	// is label-only. Preserve that guard in fresh parses and both script orders.
	for _, suffix := range []string{
		" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)",
		" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()",
	} {
		ordinary := "SELECT array_reverse(1)" + suffix
		for _, query := range []string{ordinary, "SECURITY LABEL ON TYPE int4(abs(1)) IS NULL; " + ordinary, ordinary + "; SECURITY LABEL ON TYPE int4(abs(1)) IS NULL"} {
			statements, err := Parse(query)
			var diagnostic *sqlstate.Error
			if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
				t.Fatalf("ordinary decoration frontier widened: %s: statements=%#v error=%v", query, statements, err)
			}
		}
	}
}

func TestSecurityLabelModifierCapabilityIsolation(t *testing.T) {
	// Selected-call arithmetic and casts are syntax here, not executable
	// capability. Exercise both guards independently of aggregate equality.
	for _, expression := range []string{
		"abs(1)+2",
		"(abs(1)+2)*3",
		"CAST(abs(1) AS int4)",
		"abs(1)::int4",
		"abs(abs(1)+2)",
		"abs(ALL 1)",
		"abs(DISTINCT 1)",
		"abs(1 ORDER BY 1)",
		"abs(ALL abs(1)+2)",
		"lower(ALL 'x')",
		"pg_catalog.abs(ALL 1)",
		"abs(1) WITHIN GROUP (ORDER BY 1)",
		"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)",
		"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()",
		"abs(ALL 1) FILTER (WHERE true) OVER ()",
	} {
		for _, target := range []string{
			"TYPE int4(" + expression + ")",
			"TYPE db.s.ty(" + expression + ")",
			"TYPE bit(" + expression + ")",
			"FUNCTION f(int4(" + expression + "))",
		} {
			query := "SECURITY LABEL ON " + target + " IS NULL"
			statements, err := Parse(query + "; SELECT abs(1)")
			if err != nil || len(statements) != 2 {
				t.Fatalf("%s: statements=%#v error=%v", query, statements, err)
			}
			label := statements[0].(*SecurityLabel)
			for _, modifier := range label.Type.Modifiers {
				if modifier.syntax != nil {
					t.Fatal("type modifier syntax escaped validation", query)
				}
			}
			if err := VisitQueryExpressions(label, func(Expr) error {
				t.Fatal("utility modifier visited", query)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// A fresh parse and the same script must retain the ordinary SQL
			// frontier; the utility flag must not leak through either path.
			for _, ordinary := range []string{
				"SELECT abs(1)+2",
				"SELECT CAST(abs(1) AS int4)",
				query + "; SELECT abs(1)+2",
				query + "; SELECT CAST(abs(1) AS int4)",
				"SELECT abs(ALL 1)",
				"SELECT abs(DISTINCT 1)",
				"SELECT abs(1 ORDER BY 1)",
				query + "; SELECT abs(ALL 1)",
				query + "; SELECT abs(DISTINCT 1)",
				query + "; SELECT abs(1 ORDER BY 1)",
				"SELECT abs(ALL 1); " + query,
				"SELECT abs(1)+2; " + query,
			} {
				statements, err := Parse(ordinary)
				var diagnostic *sqlstate.Error
				if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
					t.Fatalf("ordinary capability widened: %s: statements=%#v error=%v", ordinary, statements, err)
				}
			}
		}
	}
	for _, expression := range []string{
		"abs(ALL)",
		"abs(DISTINCT)",
		"abs(ALL 1,)",
		"abs(ALL 1 ORDER BY)",
		"abs(DISTINCT 1) WITHIN GROUP (ORDER BY 1)",
		"abs(VARIADIC x) WITHIN GROUP (ORDER BY 1)",
		"abs(1 ORDER BY 1) WITHIN GROUP (ORDER BY 1)",
	} {
		query := "SECURITY LABEL ON TYPE int4(" + expression + ") IS NULL"
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatalf("malformed modifier admitted: %s: statements=%#v error=%v", query, statements, err)
		}
	}
}

func TestSecurityLabelModifierSyntaxProvenance(t *testing.T) {
	one := int32(1)
	for _, query := range []string{"SECURITY LABEL ON TYPE numeric[0] IS NULL", "SECURITY LABEL ON AGGREGATE a(VARIADIC numeric[0] ORDER BY VARIADIC numeric[0]) IS NULL"} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 { t.Fatalf("%s: statements=%#v error=%v", query, statements, err) }
		label := statements[0].(*SecurityLabel)
		raw := label.Type
		if label.Routine.Arguments != nil { raw = label.Routine.Arguments[0].Type }
		if !reflect.DeepEqual(raw.ArrayBounds, []int32{0}) { t.Fatalf("private array provenance escaped: %#v", raw) }
	}
	for _, tc := range []struct { left, right TypeName; same bool }{
		{TypeName{Name: "bit"}, TypeName{Name: "bit", System: true, Modifiers: []TypeModifier{{Integer: &one}}}, true},
		{TypeName{Name: "char"}, TypeName{Name: "bpchar", System: true, Modifiers: []TypeModifier{{Integer: &one}}}, true},
		{TypeName{Name: "bit"}, TypeName{Name: "bit", System: true}, false},
		{TypeName{Name: "varchar"}, TypeName{Name: "varchar", System: true}, true},
		{TypeName{Name: "integer", Setof: true}, TypeName{Name: "integer"}, false},
		{TypeName{Name: "v", PctType: true, Parts: []Identifier{{Text: "t"}, {Text: "v"}}}, TypeName{Name: "v", Parts: []Identifier{{Text: "t"}, {Text: "v"}}}, false},
		{TypeName{Name: "integer", ArrayBounds: []int32{0, 3}}, TypeName{Name: "integer", ArrayBounds: []int32{3, 0}}, false},
	} {
		for _, pair := range [][2]TypeName{{tc.left, tc.right}, {tc.right, tc.left}} {
			// Wrap beneath private syntax to exercise recursive, non-Interface access.
			left := TypeName{Name: "int4", Modifiers: []TypeModifier{{syntax: CastExpr{TypeName: pair[0]}}}}
			right := TypeName{Name: "int4", Modifiers: []TypeModifier{{syntax: CastExpr{TypeName: pair[1]}}}}
			if got := aggregateUtilitySameType(left, right); got != tc.same { t.Fatalf("raw type identity: %#v / %#v: got %v want %v", pair[0], pair[1], got, tc.same) }
		}
	}
	if aggregateUtilitySameSyntax(reflect.ValueOf(TypedLiteralExpr{TypeName: TypeName{Name: "bit"}}), reflect.ValueOf(TypedLiteralExpr{TypeName: TypeName{Name: "bit", System: true, Modifiers: []TypeModifier{{Integer: &one}}}})) {
		t.Fatal("ConstBit acquired Typename default length")
	}
	if aggregateUtilitySameSyntax(reflect.ValueOf(TypedLiteralExpr{TypeName: TypeName{Name: "char"}}), reflect.ValueOf(TypedLiteralExpr{TypeName: TypeName{Name: "bpchar", System: true, Modifiers: []TypeModifier{{Integer: &one}}}})) {
		t.Fatal("ConstCharacter acquired Typename default length")
	}
	for _, pair := range [][2]string{{"float(24)", "real"}, {"float(25)", "double precision"}} {
		query := "SECURITY LABEL ON AGGREGATE a(VARIADIC " + pair[0] + " ORDER BY VARIADIC " + pair[1] + ") IS NULL"
		if statements, err := Parse(query); err != nil || len(statements) != 1 { t.Fatalf("%s: statements=%#v error=%v", query, statements, err) }
	}
	// Exercise native-only fields directly as well as the source-level
	// regressions above. None of this metadata authorizes utility execution.
	for _, pair := range [][2]any{
		{FunctionExpr{}, FunctionExpr{
			directSelectedCall: true, ResolutionOnly: true, CompositeCandidate: true,
			StoredSQLRoutine: true, StoredProviderRoutine: true, StoredProviderSymbol: "native",
			All: true,
		}},
		{AggregateExpr{}, AggregateExpr{
			StoredAggregate: true, StoredProviderRoutine: true, StoredProviderSymbol: "native",
		}},
		{CastExpr{}, CastExpr{Syntax: PostfixCast, FrozenMethod: "io", FrozenContext: "explicit"}},
		{TypedLiteralExpr{}, TypedLiteralExpr{RequiresDomain: true}},
		{SQLJSONQueryExpr{}, SQLJSONQueryExpr{TableColumn: "native"}},
		{Select{}, Select{Parenthesized: true}},
		{ValuesQuery{}, ValuesQuery{rowEnd: 123}},
		{GroupClause{}, GroupClause{All: true}},
		{TypeName{Name: "ty"}, TypeName{Name: "ty", Quoted: true}},
	} {
		for _, operands := range [][2]any{pair, {pair[1], pair[0]}} {
			if !aggregateUtilitySameSyntax(reflect.ValueOf(operands[0]), reflect.ValueOf(operands[1])) {
				t.Fatalf("native provenance changes parsed identity: %#v / %#v", operands[0], operands[1])
			}
		}
	}
	// These are parsed syntax, not provenance or evaluated-value identity.
	for _, pair := range [][2]any{
		{CastExpr{}, CastExpr{Syntax: FunctionCast}},
		{CastExpr{Syntax: FunctionCast, Callee: "int4"}, CastExpr{Syntax: FunctionCast, Callee: "int8"}},
		{FunctionExpr{}, FunctionExpr{Syntax: SQLFunctionSyntax}},
		{FunctionExpr{}, FunctionExpr{StringSyntax: TrimStringSyntax}},
		{FunctionExpr{}, FunctionExpr{Distinct: true}},
		{FunctionExpr{}, FunctionExpr{Variadic: true}},
		{RowExpr{}, RowExpr{Explicit: true}},
		{WindowSpec{}, WindowSpec{Bare: true}},
		{JoinReference{}, JoinReference{Comma: true}},
		{TypeName{Name: "ty"}, TypeName{Name: "ty", System: true}},
		{GroupClause{}, GroupClause{Distinct: true}},
		{ParameterExpr{Number: 1}, ParameterExpr{Number: 2}},
	} {
		if aggregateUtilitySameSyntax(reflect.ValueOf(pair[0]), reflect.ValueOf(pair[1])) {
			t.Fatalf("parsed syntax distinction lost: %#v / %#v", pair[0], pair[1])
		}
	}
}
