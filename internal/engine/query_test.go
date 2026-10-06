package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func queryCoreFixture(t *testing.T, sql string) (*Engine, *BoundQuery, *querySource) {
	t.Helper()
	e := New()
	run(t, e, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
	run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	captured := run(t, e, "SELECT * FROM t").Rows.(*finalRows)
	source := captureQuerySource(captured.source.view, captured.source.ref)
	captured.Close()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	ast := statement(t, sql)
	ref, err := e.resolveReference(auth, ast.(*parser.Select).Name)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.close)
	return e, b.query, source
}

type queryProbeExpression struct {
	expressionInfo
	eval func(*EvalContext) (sqltype.Value, error)
}

func (e queryProbeExpression) Eval(c *EvalContext) (sqltype.Value, error) { return e.eval(c) }
func queryProbe(usage sqltype.Usage, eval func(*EvalContext) (sqltype.Value, error)) BoundExpr {
	return queryProbeExpression{expressionInfo: expressionInfo{usage: usage, pos: parser.Position{Offset: -1}}, eval: eval}
}

func TestQueryCoreEagerFinalization(t *testing.T) {
	t.Run("later admitted target failure", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
		project := query.Root.(*projectNode)
		usage := project.Schema()[0].Usage
		sentinel := errors.New("later target")
		calls := 0
		project.expressions[0] = queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
			calls++
			if calls == 3 {
				return sqltype.Value{}, sentinel
			}
			return c.Row[0], nil
		})
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if rows != nil || count != 0 || err != sentinel || calls != 3 || source.view != nil {
			t.Fatal(rows, count, err, calls, source.view)
		}
	})
	t.Run("rejected rows do not evaluate targets", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE f ORDER BY id")
		project := query.Root.(*projectNode)
		calls := 0
		project.expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
			calls++
			id, _ := c.Row[0].Integer()
			if id == 2 || id == 3 {
				return sqltype.Value{}, errors.New("rejected target evaluated")
			}
			return c.Row[0], nil
		})
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if err != nil || count != 2 || calls != 2 {
			t.Fatal(count, calls, err)
		}
		defer rows.Close()
		if source.view != nil {
			t.Fatal("computed output retained unnecessary input")
		}
		values := make([]sqltype.Value, 1)
		for _, want := range []int64{1, 4} {
			present, err := rows.Next(context.Background(), values)
			got, _ := values[0].Integer()
			if !present || err != nil || got != want {
				t.Fatal(present, got, err)
			}
		}
		if calls != 2 {
			t.Fatal("public Next evaluated expressions")
		}
	})
}

func TestQueryCoreCancellation(t *testing.T) {
	for _, phase := range []string{"rejected scan", "projection", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE f")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			project := query.Root.(*projectNode)
			calls := 0
			if phase == "rejected scan" {
				typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
				filter := project.child.(*filterNode)
				filter.predicate = queryProbe(typ.Usage(), func(c *EvalContext) (sqltype.Value, error) {
					calls++
					cancel()
					return sqltype.Boolean(typ, false), nil
				})
			} else {
				project.expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
					calls++
					if phase == "projection" || calls == 2 {
						cancel()
					}
					return c.Row[0], nil
				})
			}
			rows, _, err := finalizeQuery(ctx, query.Root, source, nil)
			if rows != nil || err != context.Canceled || calls == 0 || source.view != nil {
				t.Fatal(rows, err, calls, source.view)
			}
		})
	}
	for _, mode := range []string{"NULL", "tie"} {
		t.Run(mode, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY v")
			typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
			value := sqltype.Null(typ)
			if mode == "tie" {
				value, _ = sqltype.Integer(typ, 1)
			}
			// Own a new immutable captured prefix for this sort-key fixture.
			source.committed = append([]rowVersion(nil), source.committed...)
			for i, version := range source.committed {
				source.committed[i].values = append([]sqltype.Value(nil), version.values...)
				source.committed[i].values[1] = value
			}
			ctx := &queryCountingContext{Context: context.Background(), remaining: 10}
			rows, _, err := finalizeQuery(ctx, query.Root, source, nil)
			if rows != nil || err != context.Canceled || source.view != nil {
				t.Fatal(rows, err, source.view)
			}
		})
	}
}

// Deterministic cancellation, including comparisons taking NULL/tie shortcuts.
type queryCountingContext struct {
	context.Context
	remaining int
}

func (c *queryCountingContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestQueryCoreSortErrors(t *testing.T) {
	sentinel := errors.New("comparison")
	calls := 0
	err := mergeQueryIndices(context.Background(), []int{3, 2, 1, 0}, func(a, b int) (int, error) { calls++; return 0, sentinel })
	if err != sentinel || calls != 1 {
		t.Fatal(err, calls)
	}
	t.Run("type comparison failure prevents publication", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
		query.Root.(*projectNode).child.(*sortNode).comparison = sqltype.Comparison{}
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if rows != nil || count != 0 || err == nil || err.Error() != "invalid comparison values" || source.view != nil {
			t.Fatal(rows, count, err, source.view)
		}
	})
	for _, q := range []string{"SELECT v FROM t ORDER BY v", "SELECT v FROM t ORDER BY v DESC"} {
		_, query, source := queryCoreFixture(t, q)
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if err != nil || count != 4 {
			t.Fatal(err, count)
		}
		want := []any{int64(1), int64(1), int64(2), nil}
		if q == "SELECT v FROM t ORDER BY v DESC" {
			want = []any{nil, int64(2), int64(1), int64(1)}
		}
		values := make([]sqltype.Value, 1)
		for _, expected := range want {
			present, err := rows.Next(context.Background(), values)
			if !present || err != nil || !reflect.DeepEqual(testExport(values[0]), expected) {
				t.Fatal(present, values, err, expected)
			}
		}
		rows.Close()
	}
}

func TestQueryCoreResultOwnership(t *testing.T) {
	for _, ending := range []string{"EOF", "cancel", "width", "close"} {
		t.Run(ending, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT id FROM t")
			rows, _, err := finalizeQuery(context.Background(), query.Root, source, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := rows.(*finalRows)
			if r.source != source || r.selection != nil {
				t.Fatal("direct-column fast path lost")
			}
			other := independentFinalRows(rows)
			defer other.Close()
			forged := &finalRows{self: r, source: source}
			forged.Close()
			copied := *r
			copied.Close()
			values := make([]sqltype.Value, 1)
			present, err := rows.Next(context.Background(), values)
			if !present || err != nil {
				t.Fatal(present, err)
			}
			saved := values[0]
			var terminal error
			switch ending {
			case "EOF":
				for range 4 {
					present, err = rows.Next(context.Background(), values)
				}
				if present || err != nil {
					t.Fatal(present, err)
				}
			case "cancel":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				present, terminal = rows.Next(ctx, values)
				if present || terminal != context.Canceled {
					t.Fatal(present, terminal)
				}
			case "width":
				present, terminal = rows.Next(context.Background(), nil)
				if present || terminal == nil {
					t.Fatal("width invariant not enforced")
				}
			case "close":
				rows.Close()
			}
			if source.view != nil || r.values != nil || r.selection != nil {
				t.Fatal("terminal resources retained")
			}
			if len(rows.Schema()) != 1 || rows.Schema()[0].Name != "id" || !rows.Schema()[0].NullabilityKnown || rows.Schema()[0].Nullable {
				t.Fatal(rows.Schema())
			}
			if got, _ := saved.Integer(); got != 1 {
				t.Fatal("saved value invalidated")
			}
			present, err = rows.Next(context.Background(), values)
			if present || err != terminal {
				t.Fatal("terminal state changed", present, err, terminal)
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
			if present, err = rows.Next(context.Background(), values); present || err != nil {
				t.Fatal(present, err)
			}
			if present, err = other.Next(context.Background(), values); !present || err != nil {
				t.Fatal("independent owner invalidated", present, err)
			}
		})
	}
}

func TestQueryCoreZeroWidth(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(v INT)")
	run(t, e, "INSERT INTO t VALUES(1),(2),(3),(4)")
	run(t, e, "ALTER TABLE t DROP COLUMN v")
	r := run(t, e, "SELECT * FROM t")
	defer r.Rows.Close()
	if len(r.Rows.Schema()) != 0 || r.Count != 4 {
		t.Fatal(r)
	}
	for range 4 {
		if present, err := r.Rows.Next(context.Background(), nil); !present || err != nil {
			t.Fatal(present, err)
		}
	}
	if present, err := r.Rows.Next(context.Background(), nil); present || err != nil {
		t.Fatal(present, err)
	}
	run(t, e, "CREATE TABLE empty(v INT)")
	run(t, e, "ALTER TABLE empty DROP v")
	empty := run(t, e, "SELECT * FROM empty")
	defer empty.Rows.Close()
	if empty.Rows == nil || len(empty.Rows.Schema()) != 0 {
		t.Fatal(empty)
	}
	if present, err := empty.Rows.Next(context.Background(), nil); present || err != nil {
		t.Fatal(present, err)
	}
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	anonymous, err := anonymousResult([]ColumnDescriptor{{Name: "value", Usage: typ.Usage()}}, nil, 0)
	if err != nil || anonymous.Rows == nil || len(anonymous.Rows.Schema()) != 1 {
		t.Fatal(anonymous, err)
	}
	defer anonymous.Rows.Close()
	if present, err := anonymous.Rows.Next(context.Background(), make([]sqltype.Value, 1)); present || err != nil {
		t.Fatal(present, err)
	}
}

func TestQueryCoreStageContractsAndDescriptors(t *testing.T) {
	for _, q := range []string{"SELECT id FROM t", "SELECT id FROM t WHERE f", "SELECT id FROM t WHERE f ORDER BY id"} {
		t.Run(q, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, q)
			project := query.Root.(*projectNode)
			stage, err := openQueryNode(context.Background(), &queryState{source: source}, project.child)
			if err != nil {
				t.Fatal(err)
			}
			var cursor RowCursor = &stage
			if len(cursor.Schema()) != 3 {
				t.Fatal("hidden input schema lost", cursor.Schema())
			}
			output := project.Schema()[0]
			input := cursor.Schema()[0]
			if output != input || output.OriginAttribute != 1 || output.Nullable || !output.NullabilityKnown {
				t.Fatal(output, input)
			}
			values := make([]sqltype.Value, 3)
			got := []int64{}
			for {
				present, err := cursor.Next(context.Background(), values)
				if err != nil {
					t.Fatal(err)
				}
				if !present {
					break
				}
				id, _ := values[0].Integer()
				got = append(got, id)
			}
			want := []int64{1, 2, 3, 4}
			if q != "SELECT id FROM t" {
				want = []int64{1, 4}
			}
			gotBag, wantBag := map[int64]int{}, map[int64]int{}
			for _, id := range got {
				gotBag[id]++
			}
			for _, id := range want {
				wantBag[id]++
			}
			if !reflect.DeepEqual(gotBag, wantBag) {
				t.Fatal(got, want)
			}
			// EOF releases stage state, not the query owner's independent pin.
			if stage.source != nil || stage.selection != nil || stage.expressions != nil || source.view == nil {
				t.Fatal("stage ownership boundary")
			}
			if err := cursor.Close(); err != nil {
				t.Fatal(err)
			}
			if len(cursor.Schema()) != 3 {
				t.Fatal("stage Close erased schema")
			}
		})
	}
}
