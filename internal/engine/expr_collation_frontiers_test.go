package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type rowOperandEffect struct {
	expr.Info
	value   sqltype.Value
	order   *[]int
	id      int
	failure error
}

func (e rowOperandEffect) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	*e.order = append(*e.order, e.id)
	return e.value, e.failure
}

func TestExprCollationRowOperandDemand(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	boolean, _ := view.LookupType(sqltype.Bool)
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	comparison, err := sqltype.BindComparison(integer, integer, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareLess, integer, integer, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("reached later left operand")
	for _, tc := range []struct {
		name        string
		left, right sqltype.Value
		want        []int
		null        bool
		fail        bool
	}{
		{"decisive", one, two, []int{0, 1}, false, false},
		{"null", sqltype.Null(integer), two, []int{0, 1}, true, false},
		{"reached", one, one, []int{0, 1, 2}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []int
			probe := func(id int, value sqltype.Value, err error) expr.Expr {
				return rowOperandEffect{expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, value, &order, id, err}
			}
			row := plan.RowComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Operator: "<", Ordering: []sqltype.Comparison{comparison, comparison}, Comparisons: []plan.ComparisonExpr{
				{Info: expr.Info{Usage: boolean.Usage()}, Left: probe(0, tc.left, nil), Right: probe(1, tc.right, nil), Predicate: predicate, Operator: "<"},
				{Info: expr.Info{Usage: boolean.Usage()}, Left: probe(2, one, failure), Right: probe(3, two, errors.New("later right operand")), Predicate: predicate, Operator: "<"},
			}}
			value, err := row.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
			if !reflect.DeepEqual(order, tc.want) {
				t.Fatal("row pair operand order/demand", order, tc.want, err)
			}
			if tc.fail {
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || value.IsNull() != tc.null {
				t.Fatal(value, err)
			}
			if !tc.null {
				truth, err := value.Boolean()
				if err != nil || !truth {
					t.Fatal(value, err)
				}
			}
		})
	}
}

func TestExprCollationScheduledPayloadDemand(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, `CREATE TABLE expr_scheduled_t(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`)
	for _, tc := range []struct{ name, tail string }{{"expr_scheduled_sort", `ORDER BY id`}, {"expr_scheduled_bound", `LIMIT 1`}, {"expr_scheduled_both", `ORDER BY id LIMIT 1`}} {
		sessionSQL(t, owner, `CREATE VIEW `+tc.name+` AS SELECT id,a=b AS unused,a COLLATE "C"=b AS selected FROM expr_scheduled_t `+tc.tail)
		view := e.CatalogRead()
		ref := catalogRef(t, e, view, tc.name)
		project := viewOf(view.MustRelation(ref.Address)).query.Root.(*plan.ProjectNode)
		unused, ok := project.Expressions[1].(plan.CachedOrderExpr)
		if !ok {
			t.Fatal("fixture has no real scheduled producer", tc.name, project.Expressions[1])
		}
		selected, ok := project.Expressions[2].(plan.CachedOrderExpr)
		if !ok {
			t.Fatal("selected payload was not scheduled", tc.name)
		}
		producer := func(root plan.Node, slot int) expr.Expr {
			for root != nil {
				switch n := root.(type) {
				case *plan.SortNode:
					base := n.CacheWidth - len(n.Outputs)
					if slot >= base && slot < base+len(n.Outputs) {
						return n.Outputs[slot-base]
					}
				case *plan.EvaluationNode:
					if slot >= n.Base && slot < n.Base+len(n.Expressions) {
						return n.Expressions[slot-n.Base]
					}
				}
				root, _ = plan.QueryInputs(root)
			}
			t.Fatal("missing actual cache-slot producer", tc.name, slot)
			return nil
		}
		original := producer(project.Child, unused.Slot)
		id, _ := view.LookupType(sqltype.Int4)
		text, _ := view.LookupType(sqltype.Text)
		one, _ := sqltype.Integer(id, 1)
		ctx := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: project.Frame, Row: []sqltype.Value{one, sqltype.String(text, "A"), sqltype.String(text, "A")}}}
		_, err := original.Eval(ctx)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("fixture lacks reached original conflict", tc.name, err)
		}
		demanded, err := storedProjectionDemand(project, []int{0, 2})
		if err != nil {
			t.Fatal(err)
		}
		value, err := producer(demanded.Child, unused.Slot).Eval(ctx)
		if err != nil || !value.IsNull() {
			t.Fatal("unused scheduled payload still demands provider", tc.name, value, err)
		}
		value, err = producer(demanded.Child, selected.Slot).Eval(ctx)
		truth, boolErr := value.Boolean()
		if err != nil || boolErr != nil || !truth {
			t.Fatal("scheduled selected provider lost", tc.name, value, err, boolErr)
		}
		_, err = original.Eval(ctx)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("source scheduled producer mutated", tc.name, err)
		}
		// A retained alias of the same real cache slot keeps its provider,
		// even when the first output spelling of that slot is unreferenced.
		shared := *project
		shared.Expressions = append([]expr.Expr(nil), project.Expressions...)
		shared.Expressions[2] = unused
		demanded, err = storedProjectionDemand(&shared, []int{0, 2})
		if err != nil {
			t.Fatal(err)
		}
		_, err = producer(demanded.Child, unused.Slot).Eval(ctx)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("shared live cache provider was pruned", tc.name, err)
		}
		// Retain the actual scheduler layout but make the later producer
		// consume an earlier slot. This exercises transitive cache authority,
		// not only directly referenced final projection slots.
		identity := func(expression expr.Expr) (expr.Expr, bool, error) { return expression, false, nil }
		chained, err := plan.RewriteQueryExpressions(project.Child, identity, func(node plan.Node) (plan.Node, error) {
			switch n := node.(type) {
			case *plan.SortNode:
				base := n.CacheWidth - len(n.Outputs)
				if selected.Slot >= base && selected.Slot < base+len(n.Outputs) {
					copy := *n
					copy.Outputs = append([]expr.Expr(nil), n.Outputs...)
					copy.Outputs[selected.Slot-base] = unused
					return &copy, nil
				}
			case *plan.EvaluationNode:
				if selected.Slot >= n.Base && selected.Slot < n.Base+len(n.Expressions) {
					copy := *n
					copy.Expressions = append([]expr.Expr(nil), n.Expressions...)
					copy.Expressions[selected.Slot-n.Base] = unused
					return &copy, nil
				}
			}
			return node, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		transitive := *project
		transitive.Child = chained
		demanded, err = storedProjectionDemand(&transitive, []int{0, 2})
		if err != nil {
			t.Fatal(err)
		}
		_, err = producer(demanded.Child, unused.Slot).Eval(ctx)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("transitively live provider was pruned", tc.name, err)
		}
	}
}

func TestExprCollationGeneratedTableOIDReachability(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, `CREATE TABLE expr_row_generated(a INT,marker BIGINT,first_pair BOOL GENERATED ALWAYS AS ((tableoid::BIGINT,a)=(marker,a)) STORED,later_pair BOOL GENERATED ALWAYS AS ((a,tableoid::BIGINT)=(a,marker)) STORED,no_owner BOOL GENERATED ALWAYS AS ((a,a)=(a,a)) STORED)`)
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "expr_row_generated")
	table := view.MustRelation(ref.Address)
	for _, tc := range []struct {
		slot int
		want bool
	}{{2, true}, {3, true}, {4, false}} {
		generation := table.ColumnMeta.Generations[table.Attributes[tc.slot]]
		if got := generationHasTableOID(generation.Expression); got != tc.want {
			t.Fatal("owner-dependent row traversal", tc.slot, got, tc.want)
		}
		if got := generationHasTableOID(generation.Planned); got != tc.want {
			t.Fatal("planned owner-dependent row traversal", tc.slot, got, tc.want)
		}
	}
}
