package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func queryCoreFixture(t *testing.T, sql string) (*Engine, *plan.Query, *exec.Source) {
	t.Helper()
	// Parsing may terminate the test; never do it while holding e.mu, since
	// deferred session teardown needs that mutex before WaitEnd can return.
	ast := statement(t, sql)
	e := New()
	run(t, e, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
	run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	captured := run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	source, captureErr := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	captured.Close()
	t.Cleanup(source.Close)
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	from := ast.(*parser.Select).From
	if alias, ok := from.(*parser.AliasReference); ok {
		from = alias.Input
	}
	ref, err := e.ResolveReference(auth, from.(*parser.TableReference).Name)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e, b.Query, source
}

type queryProbeExpression struct {
	expr.Info
	eval func(*exec.Context) (sqltype.Value, error)
}

func (e queryProbeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.eval(evalContext(ctx))
}
func queryProbe(usage sqltype.Usage, eval func(*exec.Context) (sqltype.Value, error)) expr.Expr {
	return queryProbeExpression{Info: expr.Info{Usage: usage, Pos: parser.Position{Offset: -1}}, eval: eval}
}

func TestQueryCoreEagerFinalization(t *testing.T) {
	t.Run("later admitted target failure", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
		project := query.Root.(*plan.ProjectNode)
		usage := project.Schema()[0].Usage
		sentinel := errors.New("later target")
		calls := 0
		project.Expressions[0] = queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
			calls++
			if calls == 3 {
				return sqltype.Value{}, sentinel
			}
			return c.Row[0], nil
		})
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if rows != nil || count != 0 || err != sentinel || calls != 3 || source.View != nil {
			t.Fatal(rows, count, err, calls, source.View)
		}
	})
	t.Run("rejected rows do not evaluate targets", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE f ORDER BY id")
		project := query.Root.(*plan.ProjectNode)
		calls := 0
		project.Expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
			calls++
			id, _ := c.Row[0].Integer()
			if id == 2 || id == 3 {
				return sqltype.Value{}, errors.New("rejected target evaluated")
			}
			return c.Row[0], nil
		})
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if err != nil || count != 2 || calls != 2 {
			t.Fatal(count, calls, err)
		}
		defer rows.Close()
		if source.View != nil {
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
			project := query.Root.(*plan.ProjectNode)
			calls := 0
			if phase == "rejected scan" {
				typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
				filter := project.Child.(*plan.FilterNode)
				filter.Predicate = queryProbe(typ.Usage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					cancel()
					return sqltype.Boolean(typ, false), nil
				})
			} else {
				project.Expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if phase == "projection" || calls == 2 {
						cancel()
					}
					return c.Row[0], nil
				})
			}
			rows, _, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil || err != context.Canceled || calls == 0 || source.View != nil {
				t.Fatal(rows, err, calls, source.View)
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
			source.Committed = append([]storage.RowVersion(nil), source.Committed...)
			for i, version := range source.Committed {
				source.Committed[i].Values = append([]sqltype.Value(nil), version.Values...)
				source.Committed[i].Values[1] = value
			}
			ctx := &queryCountingContext{Context: context.Background(), remaining: 10}
			rows, _, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil || err != context.Canceled || source.View != nil {
				t.Fatal(rows, err, source.View)
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
	err := exec.MergeQueryIndices(context.Background(), []int{3, 2, 1, 0}, func(a, b int) (int, error) { calls++; return 0, sentinel })
	if err != sentinel || calls != 1 {
		t.Fatal(err, calls)
	}
	t.Run("type comparison failure prevents publication", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id")
		query.Root.(*plan.ProjectNode).Child.(*plan.SortNode).Keys[0].Comparison = sqltype.Comparison{}
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if rows != nil || count != 0 || err == nil || err.Error() != "invalid comparison values" || source.View != nil {
			t.Fatal(rows, count, err, source.View)
		}
	})
	for _, q := range []string{"SELECT v FROM t ORDER BY v", "SELECT v FROM t ORDER BY v DESC"} {
		_, query, source := queryCoreFixture(t, q)
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
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
			rows, _, err := exec.Finalize(context.Background(), query.Root, source, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := rows.(*exec.FinalRows)
			if r.Source != source || r.Selection != nil {
				t.Fatal("direct-column fast path lost")
			}
			other := independentFinalRows(rows)
			defer other.Close()
			forged := &exec.FinalRows{Self: r, Source: source}
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
			if source.View != nil || r.Values != nil || r.Selection != nil {
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
	anonymous, err := anonymousResult([]catalog.Column{{Name: "value", Usage: typ.Usage()}}, nil, 0)
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
			project := query.Root.(*plan.ProjectNode)
			stage, err := exec.OpenNode(context.Background(), &exec.State{Source: source}, project.Child)
			if err != nil {
				t.Fatal(err)
			}
			var cursor exec.RowCursor = &stage
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
			if stage.Source != nil || stage.Selection != nil || stage.Expressions != nil || source.View == nil {
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
