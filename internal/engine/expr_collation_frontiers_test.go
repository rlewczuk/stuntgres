package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
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
	if err := evaluationContext(c); err != nil {
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
			row := rowComparisonExpression{Info: expr.Info{Usage: boolean.Usage()}, operator: "<", ordering: []sqltype.Comparison{comparison, comparison}, comparisons: []comparisonExpression{
				{Info: expr.Info{Usage: boolean.Usage()}, left: probe(0, tc.left, nil), right: probe(1, tc.right, nil), predicate: predicate, operator: "<"},
				{Info: expr.Info{Usage: boolean.Usage()}, left: probe(2, one, failure), right: probe(3, two, errors.New("later right operand")), predicate: predicate, operator: "<"},
			}}
			value, err := row.Eval(&EvalContext{Context: context.Background()})
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
		project := viewOf(view.MustRelation(ref.Address)).query.Root.(*projectNode)
		unused, ok := project.expressions[1].(cachedOrderExpression)
		if !ok {
			t.Fatal("fixture has no real scheduled producer", tc.name, project.expressions[1])
		}
		selected, ok := project.expressions[2].(cachedOrderExpression)
		if !ok {
			t.Fatal("selected payload was not scheduled", tc.name)
		}
		producer := func(root LogicalNode, slot int) expr.Expr {
			for root != nil {
				switch n := root.(type) {
				case *sortNode:
					base := n.cacheWidth - len(n.outputs)
					if slot >= base && slot < base+len(n.outputs) {
						return n.outputs[slot-base]
					}
				case *evaluationNode:
					if slot >= n.base && slot < n.base+len(n.expressions) {
						return n.expressions[slot-n.base]
					}
				}
				root, _ = queryInputs(root)
			}
			t.Fatal("missing actual cache-slot producer", tc.name, slot)
			return nil
		}
		original := producer(project.child, unused.slot)
		id, _ := view.LookupType(sqltype.Int4)
		text, _ := view.LookupType(sqltype.Text)
		one, _ := sqltype.Integer(id, 1)
		ctx := &EvalContext{Context: context.Background(), Scope: 1, Frame: project.frame, Row: []sqltype.Value{one, sqltype.String(text, "A"), sqltype.String(text, "A")}}
		_, err := original.Eval(ctx)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("fixture lacks reached original conflict", tc.name, err)
		}
		demanded, err := storedProjectionDemand(project, []int{0, 2})
		if err != nil {
			t.Fatal(err)
		}
		value, err := producer(demanded.child, unused.slot).Eval(ctx)
		if err != nil || !value.IsNull() {
			t.Fatal("unused scheduled payload still demands provider", tc.name, value, err)
		}
		value, err = producer(demanded.child, selected.slot).Eval(ctx)
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
		shared.expressions = append([]expr.Expr(nil), project.expressions...)
		shared.expressions[2] = unused
		demanded, err = storedProjectionDemand(&shared, []int{0, 2})
		if err != nil {
			t.Fatal(err)
		}
		_, err = producer(demanded.child, unused.slot).Eval(ctx)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("shared live cache provider was pruned", tc.name, err)
		}
		// Retain the actual scheduler layout but make the later producer
		// consume an earlier slot. This exercises transitive cache authority,
		// not only directly referenced final projection slots.
		identity := func(expression expr.Expr) (expr.Expr, bool, error) { return expression, false, nil }
		chained, err := rewriteQueryExpressions(project.child, identity, func(node LogicalNode) (LogicalNode, error) {
			switch n := node.(type) {
			case *sortNode:
				base := n.cacheWidth - len(n.outputs)
				if selected.slot >= base && selected.slot < base+len(n.outputs) {
					copy := *n
					copy.outputs = append([]expr.Expr(nil), n.outputs...)
					copy.outputs[selected.slot-base] = unused
					return &copy, nil
				}
			case *evaluationNode:
				if selected.slot >= n.base && selected.slot < n.base+len(n.expressions) {
					copy := *n
					copy.expressions = append([]expr.Expr(nil), n.expressions...)
					copy.expressions[selected.slot-n.base] = unused
					return &copy, nil
				}
			}
			return node, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		transitive := *project
		transitive.child = chained
		demanded, err = storedProjectionDemand(&transitive, []int{0, 2})
		if err != nil {
			t.Fatal(err)
		}
		_, err = producer(demanded.child, unused.slot).Eval(ctx)
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
		generation := table.generations[table.attributes[tc.slot]]
		if got := generationHasTableOID(generation.expression); got != tc.want {
			t.Fatal("owner-dependent row traversal", tc.slot, got, tc.want)
		}
		if got := generationHasTableOID(generation.planned); got != tc.want {
			t.Fatal("planned owner-dependent row traversal", tc.slot, got, tc.want)
		}
	}
}
