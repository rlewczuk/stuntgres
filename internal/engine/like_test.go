package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestLikeEvaluationAndProperties(t *testing.T) {
	var text, boolean *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
		if d.ID() == sqltype.Bool {
			boolean = d
		}
	}
	provider, err := sqltype.BindLike("~~", text.Usage(), text.Usage(), boolean.Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("demanded pattern child")
	left := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	right := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, order: &order, index: 1, err: failure}
	node := likeExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: left, right: right, provider: provider, operator: "~~"}
	if _, err := node.Eval(&EvalContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	p := likeProperties(columnExpression{expressionInfo: expressionInfo{properties: ExprProperties{RowDependent: true}}}, parameterExpression{expressionInfo: expressionInfo{properties: ExprProperties{ParameterDependent: true, StageDependent: true, Volatility: Stable}}})
	if p != (ExprProperties{RowDependent: true, ParameterDependent: true, StageDependent: true, Volatility: Stable}) {
		t.Fatal(p)
	}
}

func TestLikeStoredAndStageConsumers(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,CHECK(v LIKE 'a%' ESCAPE '!'))")
	run(t, e, "INSERT INTO t VALUES(1,'ab','A%','!'),(2,NULL,NULL,NULL)")
	for _, tc := range []struct {
		query string
		want  [][]any
	}{
		{"SELECT v ILIKE p ESCAPE e FROM t ORDER BY id", [][]any{{true}, {nil}}},
		{"SELECT CASE WHEN v LIKE 'a%' THEN TRUE ELSE FALSE END FROM t ORDER BY id", [][]any{{true}, {false}}},
		{"SELECT COUNT(*) FROM t WHERE v ILIKE p ESCAPE e", [][]any{{int64(1)}}},
		{"SELECT v LIKE 'a%',COUNT(*) FROM t GROUP BY v LIKE 'a%' ORDER BY v LIKE 'a%'", [][]any{{true, int64(1)}, {nil, int64(1)}}},
	} {
		if got := likeEngineTuples(t, run(t, e, tc.query)); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.query, got, tc.want)
		}
	}
}

func likeEngineTuples(t *testing.T, result Result) [][]any {
	t.Helper()
	cursor := independentFinalRows(result.Rows)
	defer cursor.Close()
	rows := make([][]any, 0)
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			return rows
		}
		row := make([]any, len(values))
		for i, value := range values {
			if value.IsNull() {
				continue
			}
			if value.Type().ID() == sqltype.Bool {
				row[i], err = value.Boolean()
			} else {
				row[i], err = value.Integer()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		rows = append(rows, row)
	}
}
