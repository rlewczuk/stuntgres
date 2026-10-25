package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func queryJoinsFixture(t *testing.T, q string) (*Engine, *plan.Query, *exec.Source) {
	t.Helper()
	e, query, source, _ := queryJoinsAdmittedFixture(t, q, false)
	return e, query, source
}

// Explicit admission allows immutable captured rows to outlive catalog replacement
// without bypassing the replacement reservation held by implicit statements.
func queryJoinsAdmittedFixture(t *testing.T, q string, explicit bool) (*Engine, *plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := statement(t, q)
	e := New()
	for _, sql := range []string{"CREATE TABLE a(l TEXT,k INT,q INT)", "CREATE TABLE b(r TEXT,q INT,k INT)", "CREATE TABLE c(k INT,c TEXT)", "CREATE TABLE wide(k BIGINT)", "CREATE TABLE chars(k VARCHAR(3))", "CREATE TABLE longer(k VARCHAR(5))", "CREATE TABLE same(k VARCHAR(3))", "CREATE TABLE unused(v INT)", "INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", "INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", "INSERT INTO c VALUES(1,'c'),(1,'c')", "INSERT INTO wide VALUES(1)", "INSERT INTO chars VALUES('x')", "INSERT INTO longer VALUES('x')", "INSERT INTO same VALUES('x')"} {
		run(t, e, sql)
	}
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var scope *StatementScope
	var source *exec.Source
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if source != nil {
			source.Close()
		}
		if scope != nil && !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
		done()
	}
	t.Cleanup(release)
	if explicit {
		if _, err = e.Begin(op, session.TxOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	if err == nil {
		b, err = Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	if err == nil {
		// Standalone stages supply contexts, but still need live admission authority.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, b.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	return e, b.Query, source, release
}

func TestQueryJoinsMergedMetadataAndFrames(t *testing.T) {
	for _, q := range []string{"SELECT k,a.k,wide.k FROM a JOIN wide USING(k)", "SELECT k,a.k,wide.k FROM wide JOIN a USING(k)"} {
		_, query, source := queryJoinsFixture(t, q)
		schema := query.Root.Schema()
		if schema[0].Usage.Type.ID() != sqltype.Int8 || schema[1].Usage.Type.ID() != sqltype.Int4 || schema[2].Usage.Type.ID() != sqltype.Int8 || schema[0].OriginTable != schema[2].OriginTable || schema[0].OriginAttribute != schema[2].OriginAttribute || schema[0].OriginTable == schema[1].OriginTable {
			t.Fatal(q, schema)
		}
		result, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if err != nil || n != 2 {
			t.Fatal(result, n, err)
		}
		result.Close()
	}
	for _, tc := range []struct {
		q      string
		typmod int32
		direct bool
	}{
		{"SELECT k FROM chars JOIN same USING(k)", 7, true},
		{"SELECT k FROM chars JOIN longer USING(k)", -1, false},
	} {
		_, query, source := queryJoinsFixture(t, tc.q)
		column := query.Root.Schema()[0]
		if column.Usage.Typmod != tc.typmod || (column.OriginTable != 0) != tc.direct {
			t.Fatal(tc, column)
		}
		result, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if err != nil || n != 1 {
			t.Fatal(result, n, err)
		}
		result.Close()
	}
	e, query, source := queryJoinsFixture(t, "SELECT a.k,b.k,k FROM a JOIN b USING(k)")
	join := query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
	if len(join.Schema()) != 6 || len(query.Root.Schema()) != 3 {
		t.Fatal(join.Schema(), query.Root.Schema())
	}
	if source.Sources[0].View.Data() != source.Sources[1].View.Data() || len(source.View.Data().Committed) != 2 {
		t.Fatal("not one selected multi-table view")
	}
	unused := catalogRef(t, e, source.View.Catalog(), "unused")
	if _, ok := source.View.Data().Committed[unused.Address]; ok {
		t.Fatal("unrelated root captured")
	}
	project := query.Root.(*plan.ProjectNode)
	for i, want := range []int{1, 5, 1} {
		if project.Expressions[i].(plan.ColumnExpr).Slot != want {
			t.Fatal(project.Expressions)
		}
	}
}

func TestQueryJoinsErrorsCancellationAndCleanup(t *testing.T) {
	for _, mode := range []string{"reject_cancel", "admitted_error", "rejected_error", "late_target", "merge_error", "second_open", "bad_width", "bad_usage"} {
		t.Run(mode, func(t *testing.T) {
			e, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b ON a.k=b.k")
			children := append([]*exec.Source(nil), source.Sources...)
			project := query.Root.(*plan.ProjectNode)
			join := project.Child.(*plan.JoinNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("join provider sentinel")
			want := error(sentinel)
			calls := 0
			boolType, _ := e.CatalogRead().LookupType(sqltype.Bool)
			switch mode {
			case "reject_cancel":
				want = context.Canceled
				join.Predicate = queryProbe(boolType.Usage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if calls == 3 {
						cancel()
					}
					return sqltype.Boolean(boolType, false), nil
				})
			case "admitted_error", "rejected_error":
				join.Predicate = queryProbe(boolType.Usage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if calls == 3 {
						return sqltype.Value{}, sentinel
					}
					return sqltype.Boolean(boolType, mode == "admitted_error"), nil
				})
			case "late_target":
				original := project.Expressions[0]
				project.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if calls == 3 {
						return sqltype.Value{}, sentinel
					}
					return original.Eval(c)
				})
			case "merge_error":
				usage := project.Expressions[0].ResultUsage()
				join.QuerySchema = append(join.QuerySchema, catalog.Column{Usage: usage})
				join.Merges = []expr.Expr{queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if calls == 3 {
						return sqltype.Value{}, sentinel
					}
					return c.Row[1], nil
				})}
			case "second_open":
				join.Right.(*plan.SourceNode).Occurrence = len(children)
			case "bad_width":
				join.LeftWidth++
			case "bad_usage":
				join.Predicate = queryProbe(boolType.Usage(), func(c *exec.Context) (sqltype.Value, error) { return c.Row[1], nil })
			}
			rows, n, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil || n != 0 || err == nil {
				t.Fatal(rows, n, err)
			}
			if mode == "bad_width" || mode == "bad_usage" || mode == "second_open" {
				var diagnostic *sqlstate.Error
				if errors.As(err, &diagnostic) {
					t.Fatal("invariant error acquired SQLSTATE", diagnostic)
				}
			} else if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			for _, child := range children {
				if child.View != nil || child.Committed != nil || child.Own != nil {
					t.Fatal("orphan child capture", child)
				}
			}
			if source.Sources != nil {
				t.Fatal("orphan source bundle")
			}
		})
	}
}

func TestQueryJoinsRejectedPairAllocation(t *testing.T) {
	_, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b ON FALSE")
	defer source.Close()
	join := query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
	calls := 0
	usage := join.Predicate.ResultUsage()
	join.Predicate = queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
		calls++
		return sqltype.Boolean(usage.Type, false), nil
	})
	// Repeat immutable input occurrences without changing width or output count.
	right := source.Sources[1]
	original := append([]storage.RowVersion(nil), right.Committed...)
	measure := func(n int) uint64 {
		right.Committed = make([]storage.RowVersion, n)
		for i := range right.Committed {
			right.Committed[i] = original[i%len(original)]
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for range 5 {
			calls = 0
			stage, err := exec.OpenJoinNode(context.Background(), &exec.State{Source: source}, join)
			if err != nil {
				t.Fatal(err)
			}
			drainErr := stage.Drain(context.Background())
			count := stage.Count()
			closeErr := stage.Close()
			if drainErr != nil || closeErr != nil || count != 0 || calls != source.Sources[0].Count()*n {
				t.Fatal("rejected product was not fully consumed", n, calls, count, drainErr, closeErr)
			}
			if stage.Source != nil || stage.Produce != nil || stage.Finish != nil {
				t.Fatal("rejected-product stage retained resources", stage)
			}
		}
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	small, large := measure(4), measure(4096)
	if large > small+8192 {
		t.Fatal("rejected-pair allocations scale with product", small, large)
	}
	_, single, singleSource := queryCoreFixture(t, "SELECT id FROM t")
	rows, n, err := exec.Finalize(context.Background(), single.Root, singleSource, nil)
	if err != nil || n != 4 {
		t.Fatal(rows, n, err)
	}
	final := rows.(*exec.FinalRows)
	if final.Source != singleSource || final.Values != nil || final.Source.Flat != nil || final.Source.Anonymous != nil {
		t.Fatal("single-source direct scan copied tuples", final)
	}
	rows.Close()
}

func TestQueryJoinsZeroWidthAndHeldRoots(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE a(v INT)", "CREATE TABLE b(v INT)", "INSERT INTO a VALUES(1),(2)", "INSERT INTO b VALUES(1),(2),(3)", "ALTER TABLE a DROP COLUMN v", "ALTER TABLE b DROP COLUMN v"} {
		run(t, e, q)
	}
	result := run(t, e, "SELECT * FROM a CROSS JOIN b")
	if result.Count != 6 || len(result.Rows.Schema()) != 0 {
		t.Fatal(result)
	}
	for range 6 {
		if present, err := result.Rows.Next(context.Background(), nil); !present || err != nil {
			t.Fatal(present, err)
		}
	}
	if present, err := result.Rows.Next(context.Background(), nil); present || err != nil {
		t.Fatal(present, err)
	}
	result.Rows.Close()
	for _, replacement := range []string{"append", "reset", "restore"} {
		for _, projection := range []string{"a.k,b.k", "a.k+1,b.k+1"} {
			e, query, source, release := queryJoinsAdmittedFixture(t, "SELECT "+projection+" FROM a JOIN b USING(k)", false)
			rows, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
			if err != nil || n != 4 {
				t.Fatal(rows, n, err)
			}
			// Final rows are detached; no live stage still needs the admission.
			release()
			schema := append([]catalog.Column(nil), rows.Schema()...)
			switch replacement {
			case "append":
				run(t, e, "INSERT INTO b VALUES('new',9,1)")
			case "reset":
				err = e.Reset(context.Background())
			case "restore":
				err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE a(k INT); CREATE TABLE b(k INT); INSERT INTO a VALUES(8); INSERT INTO b VALUES(8)"))
			}
			if err != nil || !reflect.DeepEqual(rows.Schema(), schema) {
				t.Fatal(err, rows.Schema(), schema)
			}
			scratch := make([]sqltype.Value, 2)
			want := int64(1)
			if projection == "a.k+1,b.k+1" {
				want = 2
			}
			for range 4 {
				present, err := rows.Next(context.Background(), scratch)
				l, _ := scratch[0].Integer()
				r, _ := scratch[1].Integer()
				if !present || err != nil || l != want || r != want {
					t.Fatal(present, err, l, r, want)
				}
			}
			rows.Close()
			if replacement == "append" {
				fresh := run(t, e, "SELECT a.k FROM a JOIN b USING(k)")
				if fresh.Count != 6 {
					t.Fatal(fresh)
				}
				fresh.Rows.Close()
			}
			if replacement == "restore" {
				fresh := run(t, e, "SELECT a.k FROM a JOIN b USING(k)")
				if fresh.Count != 1 {
					t.Fatal(fresh)
				}
				fresh.Rows.Close()
			}
		}
	}
}

func TestQueryJoinsAdmittedFrameLayout(t *testing.T) {
	_, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b ON TRUE")
	join := query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
	left := append([]sqltype.Value(nil), source.Sources[0].Row(0)...)
	right := append([]sqltype.Value(nil), source.Sources[1].Row(0)...)
	stage, err := exec.OpenJoinNode(context.Background(), &exec.State{Source: source}, join)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stage.Count() != 16 {
		t.Fatal("joined occurrence count", stage.Count())
	}
	for i := range stage.Count() {
		frame := stage.Source.Row(i)
		wantLeft := source.Sources[0].Row(i / 4)
		wantRight := source.Sources[1].Row(i % 4)
		if len(frame) != len(wantLeft)+len(wantRight) || !reflect.DeepEqual(frame[:len(wantLeft)], wantLeft) || !reflect.DeepEqual(frame[len(wantLeft):], wantRight) {
			t.Fatal("joined frame lost child values", i, frame, wantLeft, wantRight)
		}
	}
	if !reflect.DeepEqual(left, source.Sources[0].Row(0)) || !reflect.DeepEqual(right, source.Sources[1].Row(0)) {
		t.Fatal("joined scratch mutated a retained child row")
	}
}

func TestQueryJoinsEmptyOperandStillPreflightsOtherChild(t *testing.T) {
	e, query, source := queryJoinsFixture(t, "SELECT a.k FROM a CROSS JOIN (b JOIN c ON b.k=int4($1::text))")
	children := append([]*exec.Source(nil), source.Sources...)
	source.Committed = nil
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	rows, n, err := exec.Finalize(context.Background(), query.Root, source, []sqltype.Value{sqltype.String(text, "oops")})
	state(t, err, "22P02")
	if rows != nil || n != 0 {
		t.Fatal("preflight failure exposed partial output", rows, n)
	}
	for _, child := range children {
		if child.View != nil || child.Committed != nil || child.Own != nil {
			t.Fatal("preflight orphan", child)
		}
	}
}

func TestQueryJoinsVisibilityReleaseAuthority(t *testing.T) {
	_, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b USING(k)")
	for _, child := range source.Sources {
		data := child.View.Data()
		retained := child.View.Retain()
		retained.Release()
		retained.Release()
		if child.View.Data() != data {
			t.Fatal("independent release invalidated source capture")
		}
	}
	rows, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || n != 4 {
		t.Fatal(rows, n, err)
	}
	rows.Close()
}
