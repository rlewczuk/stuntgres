package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These tests inspect bound providers, not public SQL results. The fixture's SQL
// only supplies binding authority; no expression is evaluated by these tests.
func conditionalDefaultClosureBind(t *testing.T) func(*testing.T, string) expr.Expr {
	t.Helper()
	e, op, scope := strictNullUnitFixture(t)
	return func(t *testing.T, text string) expr.Expr {
		t.Helper()
		node, _, _ := strictNullUnitBind(t, e, op, scope, text)
		return node
	}
}

func conditionalDefaultClosureWant(t *testing.T, node expr.Expr, timeClosed, uuidClosed bool) {
	t.Helper()
	if got := closedTransactionTimeDefault(node); got != timeClosed {
		t.Errorf("transaction-time closure of %T = %v, want %v", node, got, timeClosed)
	}
	if got := closedUUIDDefault(node); got != uuidClosed {
		t.Errorf("UUID closure of %T = %v, want %v", node, got, uuidClosed)
	}
}

func conditionalDefaultClosureACL(t *testing.T, node expr.Expr, kind functions.ACLKind) functions.ACLValue {
	t.Helper()
	acl, ok := node.(functions.ACLValue)
	if !ok || acl.Kind != kind {
		t.Fatalf("bound provider = %T, want ACL kind %v", node, kind)
	}
	return acl
}

func TestConditionalDefaultClosureACLScalars(t *testing.T) {
	bind := conditionalDefaultClosureBind(t)
	for _, text := range []string{
		"acldefault('r', CASE WHEN clock_timestamp() IS NOT NULL THEN 10::oid ELSE 10::oid END)",
		"makeaclitem(10,10,lower(timeofday()),false)",
		"makeaclitem(10,10,'SELECT',CASE WHEN clock_timestamp() IS NOT NULL THEN false ELSE false END)",
		"acldefault('r',10) @> makeaclitem(10,10,lower(timeofday()),false)",
		"acldefault('r',CASE WHEN clock_timestamp() IS NOT NULL THEN 10::oid ELSE 10::oid END) @> makeaclitem(10,10,'SELECT',false)",
		"CASE WHEN TRUE THEN makeaclitem(10,10,lower(timeofday()),false) ELSE makeaclitem(10,10,'SELECT',false) END",
	} {
		t.Run(text, func(t *testing.T) {
			node := bind(t, text)
			if node.Properties().Volatility != expr.Volatile {
				t.Fatal("positive fixture lost selected volatility", node.Properties())
			}
			conditionalDefaultClosureWant(t, node, true, false)
		})
	}

	// Each written slot independently carries a genuinely bound nonclosed
	// child. Rejection must not depend on which argument is evaluated first.
	for _, family := range []struct {
		name string
		kind functions.ACLKind
		args []string
		sql  func([]string) string
	}{
		{"default", functions.ACLDefault, []string{"'r'", "10::oid"}, func(args []string) string { return "acldefault(" + strings.Join(args, ",") + ")" }},
		{"make", functions.ACLMake, []string{"10::oid", "10::oid", "'SELECT'::text", "false"}, func(args []string) string { return "makeaclitem(" + strings.Join(args, ",") + ")" }},
		{"contains", functions.ACLContains, []string{"acldefault('r',10)", "makeaclitem(10,10,'SELECT',false)"}, func(args []string) string { return args[0] + " @> " + args[1] }},
	} {
		for slot := range family.args {
			t.Run(fmt.Sprintf("%s/slot%d", family.name, slot), func(t *testing.T) {
				if family.kind == functions.ACLDefault && slot == 0 {
					node := conditionalDefaultClosureACL(t, bind(t, "acldefault($1::\"char\",10)"), family.kind)
					conditionalDefaultClosureWant(t, node, false, false)
					// Internal CHAR is only contextual input, not a general
					// CASE result type. Keep the genuine bound parameter child
					// but deliberately make root metadata inconsistent to
					// isolate recursion from the separately tested global gate.
					node.Property.ParameterDependent = false
					conditionalDefaultClosureWant(t, node, false, true)
				} else {
					args := append([]string(nil), family.args...)
					value := args[slot]
					args[slot] = "CASE WHEN random() > 0 THEN " + value + " ELSE " + value + " END"
					node := conditionalDefaultClosureACL(t, bind(t, family.sql(args)), family.kind)
					conditionalDefaultClosureWant(t, node, false, false)
				}
			})
		}
	}
	// The first arm is decisive at runtime, but the other arm is still written
	// into the recipe and must be closed during admission inspection.
	conditionalDefaultClosureWant(t, bind(t, "makeaclitem(10,10,'SELECT',CASE WHEN TRUE THEN false ELSE CASE WHEN random() > 0 THEN false ELSE false END END)"), false, false)
}

func TestConditionalDefaultClosureACLExclusion(t *testing.T) {
	bind := conditionalDefaultClosureBind(t)
	for _, text := range []string{
		"aclexplode(acldefault('r',10))",
		"aclexplode(acldefault('c',0))",
		"aclexplode(NULL::aclitem[])",
		"aclexplode(acl => acldefault('r',10))",
		"aclexplode(acldefault('r',CASE WHEN clock_timestamp() IS NOT NULL THEN 10::oid ELSE 10::oid END))",
	} {
		t.Run(text, func(t *testing.T) {
			node := conditionalDefaultClosureACL(t, bind(t, text), functions.ACLExplode)
			conditionalDefaultClosureWant(t, node, false, node.Properties().Volatility <= expr.Stable)
			// Root-volatility fallback on the UUID route intentionally ignores
			// even a volatile child; the time route always refuses the set kind.
			stable := node
			stable.Property.Volatility = expr.Stable
			conditionalDefaultClosureWant(t, stable, false, true)
			volatile := node
			volatile.Property.Volatility = expr.Volatile
			conditionalDefaultClosureWant(t, volatile, false, false)
		})
	}
}

func TestConditionalDefaultClosureDependenciesAndFallback(t *testing.T) {
	bind := conditionalDefaultClosureBind(t)
	conditionalDefaultClosureWant(t, nil, true, true)
	conditionalDefaultClosureWant(t, bind(t, "42"), true, true)
	conditionalDefaultClosureWant(t, bind(t, "random()"), false, false)
	stable := bind(t, "random()").(functions.Random)
	stable.Property.Volatility = expr.Stable
	conditionalDefaultClosureWant(t, stable, true, true)

	for _, text := range []string{
		"makeaclitem(10,10,'SELECT',false)",
		"makeaclitem(10,10,lower(timeofday()),false)",
	} {
		original := conditionalDefaultClosureACL(t, bind(t, text), functions.ACLMake)
		for _, volatility := range []expr.Volatility{expr.Stable, expr.Volatile} {
			for _, dependence := range []string{"row", "parameter", "stage"} {
				t.Run(fmt.Sprintf("%s/%d/%s", text, volatility, dependence), func(t *testing.T) {
					node := original
					node.Property.Volatility = volatility
					node.Property.RowDependent = dependence == "row"
					node.Property.ParameterDependent = dependence == "parameter"
					node.Property.StageDependent = dependence == "stage"
					conditionalDefaultClosureWant(t, node, false, false)
				})
			}
		}
	}
	// These predicates do not add a blanket domain-fact gate to scalar ACL.
	domain := conditionalDefaultClosureACL(t, bind(t, "makeaclitem(10,10,'SELECT',false)"), functions.ACLMake)
	domain.Property.DomainDependent = true
	domain.Property.RuntimeDomainConversion = true
	conditionalDefaultClosureWant(t, domain, true, true)

	bad := conditionalDefaultClosureACL(t, bind(t, "makeaclitem(10,10,'SELECT',CASE WHEN random() > 0 THEN false ELSE false END)"), functions.ACLMake)
	conditionalDefaultClosureWant(t, bad, false, false)
	// Deliberately inconsistent stable metadata distinguishes UUID's fallback
	// from time's recursive scalar policy; this is not a validity test.
	bad.Property.Volatility = expr.Stable
	conditionalDefaultClosureWant(t, bad, false, true)
	good := conditionalDefaultClosureACL(t, bind(t, "makeaclitem(10,10,lower(timeofday()),false)"), functions.ACLMake)
	good.Property.Volatility = expr.Stable
	conditionalDefaultClosureWant(t, good, true, true)
}

func TestConditionalDefaultClosurePolicyPrecedence(t *testing.T) {
	bind := conditionalDefaultClosureBind(t)
	for _, test := range []struct {
		sql  string
		time bool
		uuid bool
	}{
		{"gen_random_uuid()", false, true},
		{"uuidv4()", false, true},
		{"uuidv7()", false, true},
		{"now()", true, true},
		{"transaction_timestamp()", true, true},
		{"statement_timestamp()", true, true},
		{"clock_timestamp()", true, false},
		{"timeofday()", true, false},
		{"lower(timeofday())", true, false},
		{"coalesce(uuidv4(),uuidv4())", false, true},
		{"coalesce(random(),random())", false, false},
		{"uuidv7(INTERVAL '1 day')", false, true},
		{"uuidv7(CASE WHEN now() IS NOT NULL THEN INTERVAL '1 day' ELSE INTERVAL '0' END)", false, true},
		{"uuidv7(CASE WHEN clock_timestamp() IS NOT NULL THEN INTERVAL '1 day' ELSE INTERVAL '0' END)", false, false},
		{"uuidv7(CASE WHEN uuidv4() IS NOT NULL THEN INTERVAL '1 day' ELSE INTERVAL '0' END)", false, false},
	} {
		t.Run(test.sql, func(t *testing.T) {
			conditionalDefaultClosureWant(t, bind(t, test.sql), test.time, test.uuid)
		})
	}
	clock := bind(t, "clock_timestamp()").(functions.TransactionTime)
	for _, mutate := range []func(*functions.TransactionTime){
		func(n *functions.TransactionTime) { n.Kind = 0 },
		func(n *functions.TransactionTime) { n.Usage = bind(t, "'text'::text").ResultUsage() },
		func(n *functions.TransactionTime) { n.Property.Volatility = expr.Stable },
	} {
		invalid := clock
		mutate(&invalid)
		if closedTransactionTimeDefault(invalid) {
			t.Error("invalid intrinsic time leaf admitted", invalid)
		}
	}

	// These real calendar providers are transparent only to UUID closure. A
	// selected UUID in a typed CASE condition makes their root genuinely volatile.
	stamp := "CASE WHEN uuidv4() IS NULL THEN TIMESTAMPTZ '2000-01-01 UTC' ELSE TIMESTAMPTZ '2000-01-02 UTC' END"
	for _, text := range []string{
		"date_trunc('day'," + stamp + ")",
		"date_bin(INTERVAL '1 day'," + stamp + ",TIMESTAMPTZ '2000-01-01 UTC')",
		"date_add(" + stamp + ",INTERVAL '1 day')",
	} {
		t.Run(text, func(t *testing.T) {
			node := bind(t, text)
			switch node.(type) {
			case functions.DatetimeTrunc, functions.DatetimeBin, functions.DatetimeZoneCalendar:
			default:
				t.Fatalf("calendar provider = %T", node)
			}
			conditionalDefaultClosureWant(t, node, false, true)
		})
	}

	e, _ := uuidOsspUnitInstalled(t)
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	node, err := b.bindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_generate_v4"}}}})
	if err != nil {
		t.Fatal(err)
	}
	installed := node.(installedUUIDOsspFunctionExpression)
	conditionalDefaultClosureWant(t, installed, false, true)
	badShape := installed
	badShape.RoutineReference = nil
	conditionalDefaultClosureWant(t, badShape, false, false)
	staged := installed
	staged.Property.StageDependent = true
	if !validInstalledUUIDOsspShape(staged) {
		t.Fatal("fixture must retain valid installed shape despite stage dependence")
	}
	conditionalDefaultClosureWant(t, staged, false, false)
}

func TestConditionalDefaultClosureInspection(t *testing.T) {
	bind := conditionalDefaultClosureBind(t)
	type snapshot struct {
		info     expr.Info
		children []expr.Expr
	}
	capture := func(node expr.Expr) []snapshot {
		var saved []snapshot
		var visit func(expr.Expr)
		visit = func(node expr.Expr) {
			entry := snapshot{info: expr.InfoOf(node)}
			boundExpressionChildren(node, func(child expr.Expr) { entry.children = append(entry.children, child) })
			saved = append(saved, entry)
			for _, child := range entry.children {
				visit(child)
			}
		}
		visit(node)
		return saved
	}
	for _, test := range []struct {
		sql  string
		time bool
		uuid bool
	}{
		// Invalid dynamic privilege text must remain undemanded by inspection.
		{"makeaclitem(10,10,lower(timeofday()),false)", true, false},
		{"pg_sleep(600)", true, false},
		{"uuidv7(INTERVAL '1 day')", false, true},
	} {
		t.Run(test.sql, func(t *testing.T) {
			node := bind(t, test.sql)
			before := capture(node)
			var aclBefore functions.ACLValue
			if acl, ok := node.(functions.ACLValue); ok {
				aclBefore = acl
			}
			// Neither predicate takes an evaluation context or runtime authority.
			for range 3 {
				conditionalDefaultClosureWant(t, node, test.time, test.uuid)
			}
			if !reflect.DeepEqual(before, capture(node)) {
				t.Error("inspection changed retained metadata or children")
			}
			if acl, ok := node.(functions.ACLValue); ok && !reflect.DeepEqual(aclBefore, acl) {
				t.Error("inspection changed ACL Info/Args or written annotations")
			}
		})
	}
}

func TestConditionalDefaultClosureSQLRoutineConsumer(t *testing.T) {
	bind := conditionalDefaultClosureBind(t)
	for _, test := range []struct {
		sql   string
		valid bool
	}{
		{"makeaclitem(10,10,lower(timeofday()),false)", true},
		{"makeaclitem(10,10,'SELECT',CASE WHEN random() > 0 THEN false ELSE false END)", false},
	} {
		t.Run(test.sql, func(t *testing.T) {
			node := conditionalDefaultClosureACL(t, bind(t, test.sql), functions.ACLMake)
			valid, containsSQL := sqlRoutineDefaultClosure(node)
			if valid != test.valid || containsSQL || closedSQLRoutineDefault(node) {
				t.Fatalf("ACL fallback = (%v,%v), want (%v,false), never a SQL-routine default", valid, containsSQL, test.valid)
			}
			for _, dependence := range []string{"row", "parameter", "stage"} {
				dependent := node
				dependent.Property.Volatility = expr.Stable
				dependent.Property.RowDependent = dependence == "row"
				dependent.Property.ParameterDependent = dependence == "parameter"
				dependent.Property.StageDependent = dependence == "stage"
				valid, containsSQL := sqlRoutineDefaultClosure(dependent)
				if valid || containsSQL || closedSQLRoutineDefault(dependent) {
					t.Error("dependent ACL rescued by stable SQL-routine bypass", dependence, valid, containsSQL)
				}
			}
		})
	}
}
