package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInsertSelectDescription(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT,j BIGINT,b BOOL)")
	run(t, e, "CREATE TABLE s(i INT,b BOOL)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, tc := range []struct {
		q  string
		id sqltype.ID
	}{
		{"INSERT INTO t(i) SELECT $1", sqltype.Int4},
		{"INSERT INTO t(b,j) SELECT $1=1,$1 FROM s", sqltype.Int4},
		{"INSERT INTO t(j) VALUES($1),(2147483648) ORDER BY 1", sqltype.Int8},
	} {
		desc, err := e.Describe(op, statement(t, tc.q))
		if err != nil {
			t.Fatal(tc.q, err)
		}
		if len(desc.Results) != 0 || desc.Parameters.Count != 1 || desc.Parameters.Types[0].Type.ID() != tc.id || desc.Parameters.Types[0].Typmod != -1 {
			t.Fatal(tc.q, desc)
		}
	}
	_, err = e.Describe(op, statement(t, "INSERT INTO t(i,b) SELECT $1,$1"))
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "30"}
	var got *sqlstate.Error
	if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
		t.Fatal(got, err, want)
	}
	r := run(t, e, "INSERT INTO t (SELECT)")
	if r.Count != 1 || r.Rows != nil {
		t.Fatal(r)
	}
	r = run(t, e, "INSERT INTO t SELECT FROM s")
	if r.Count != 0 {
		t.Fatal(r)
	}
}

func TestInsertSelectCommonTypes(t *testing.T) {
	e := New()
	usage := func(id sqltype.ID) sqltype.Usage {
		typ, ok := e.CatalogRead().LookupType(id)
		if !ok {
			t.Fatal(id)
		}
		return typ.Usage()
	}
	nodes := func(ids ...sqltype.ID) [][]expr.Expr {
		rows := make([][]expr.Expr, len(ids))
		for i, id := range ids {
			u := sqltype.Usage{}
			if id != sqltype.Unknown {
				u = usage(id)
			}
			rows[i] = []expr.Expr{queryProbe(u, nil)}
		}
		return rows
	}
	for _, tc := range []struct {
		ids  []sqltype.ID
		want sqltype.ID
	}{
		{[]sqltype.ID{sqltype.Unknown, sqltype.Unknown}, sqltype.Text},
		{[]sqltype.ID{sqltype.Int2, sqltype.Int4}, sqltype.Int4},
		{[]sqltype.ID{sqltype.Int8, sqltype.Int4}, sqltype.Int8},
		{[]sqltype.ID{sqltype.Int4, sqltype.Int8}, sqltype.Int8},
		{[]sqltype.ID{sqltype.Numeric, sqltype.Int4}, sqltype.Numeric},
		{[]sqltype.ID{sqltype.Int8, sqltype.Numeric}, sqltype.Numeric},
		{[]sqltype.ID{sqltype.Float4, sqltype.Numeric}, sqltype.Float4},
		{[]sqltype.ID{sqltype.Numeric, sqltype.Float8}, sqltype.Float8},
		{[]sqltype.ID{sqltype.Float8, sqltype.Int8}, sqltype.Float8},
		{[]sqltype.ID{sqltype.Unknown, sqltype.UUID}, sqltype.UUID},
		{[]sqltype.ID{sqltype.Time, sqltype.Time}, sqltype.Time},
		{[]sqltype.ID{sqltype.Timestamp, sqltype.Timestamp}, sqltype.Timestamp},
		{[]sqltype.ID{sqltype.Cidr, sqltype.Inet}, sqltype.Inet},
		{[]sqltype.ID{sqltype.Inet, sqltype.Cidr}, sqltype.Inet},
		{[]sqltype.ID{sqltype.Unknown, sqltype.Cidr}, sqltype.Cidr},
	} {
		got, err := commonValuesUsage(nodes(tc.ids...), nil, 0, usage(sqltype.Text))
		if err != nil || got.Type.ID() != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	// These fixtures independently represent B, N, S, U and D respectively;
	// every distinct pair must fail, independently of row order.
	for _, a := range []sqltype.ID{sqltype.Bool, sqltype.Int4, sqltype.Text, sqltype.UUID, sqltype.Time} {
		for _, b := range []sqltype.ID{sqltype.Bool, sqltype.Int4, sqltype.Text, sqltype.UUID, sqltype.Time} {
			if a == b {
				continue
			}
			_, err := commonValuesUsage(nodes(a, b), nil, 0, usage(sqltype.Text))
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "42804" {
				t.Fatal(a, b, err)
			}
		}
	}
	// PostgreSQL's numeric candidates have this independently fixed directional
	// precedence; assert every admitted pair in both row orders.
	numeric := []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8}
	for i, a := range numeric {
		for j, b := range numeric {
			want := a
			if j > i {
				want = b
			}
			got, err := commonValuesUsage(nodes(a, b), nil, 0, usage(sqltype.Text))
			if err != nil || got.Type.ID() != want {
				t.Fatal(a, b, got, err, want)
			}
		}
	}
}

func insertSelectEvaluationFixture(t *testing.T, sql string) (binding, *exec.Source) {
	t.Helper()
	e, q, source := queryCoreFixture(t, sql)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	e.mu.Lock()
	relation := e.catalog.state.MustRelation(ref.Address)
	e.mu.Unlock()
	return binding{query: q, relation: relation, ref: ref, targets: []int{0}}, source
}
func TestInsertSelectEagerFailureAndCancellation(t *testing.T) {
	for _, phase := range []string{"late source", "rejected scan", "assignment", "before apply", "sorting"} {
		t.Run(phase, func(t *testing.T) {
			b, source := insertSelectEvaluationFixture(t, "SELECT id FROM t WHERE f ORDER BY id")
			project := b.query.Root.(*plan.ProjectNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("late source evaluation")
			calls := 0
			if phase == "rejected scan" {
				filter := project.Child.(*plan.SortNode).Child.(*plan.FilterNode)
				filter.Predicate = queryProbe(filter.Predicate.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					cancel()
					return sqltype.Boolean(filter.Predicate.ResultUsage().Type, false), nil
				})
			} else if phase != "sorting" {
				project.Expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if phase == "late source" && calls == 2 {
						return sqltype.Value{}, sentinel
					}
					if phase == "assignment" || (phase == "before apply" && calls == 2) {
						cancel()
					}
					return c.Row[0], nil
				})
			}
			var evaluationContext context.Context = ctx
			if phase == "sorting" {
				evaluationContext = &queryCountingContext{Context: ctx, remaining: 16}
			}
			rows, err := evaluateInsertQuery(evaluationContext, b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
			want := error(context.Canceled)
			if phase == "late source" {
				want = sentinel
			}
			if rows.values != nil || err != want || source.View != nil {
				t.Fatal(rows, err, want, source.View)
			}
			source.Close()
			source.Close()
		})
	}
}

type insertCloseCursor struct {
	exec.RowCursor
	closeErr error
	closes   int
	cancel   context.CancelFunc
}

func (c *insertCloseCursor) Close() error {
	c.closes++
	c.RowCursor.Close()
	if c.cancel != nil {
		c.cancel()
	}
	return c.closeErr
}
func TestInsertSelectRequiredClose(t *testing.T) {
	for _, primary := range []bool{false, true} {
		b, source := insertSelectEvaluationFixture(t, "SELECT id FROM t")
		stage, err := exec.OpenNode(context.Background(), &exec.State{Source: source}, b.query.Root)
		if err != nil {
			t.Fatal(err)
		}
		closeErr := errors.New("source close")
		cursor := &insertCloseCursor{RowCursor: &stage, closeErr: closeErr}
		ctx := context.Background()
		if primary {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cancelled
		}
		rows, err := assembleInsertRows(ctx, b, cursor, stage.Count(), "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil, false, nil, nil)
		want := error(closeErr)
		if primary {
			want = context.Canceled
		}
		if rows.values != nil || err != want || cursor.closes != 1 {
			t.Fatal(rows, err, cursor.closes)
		}
		source.Close()
	}
}

func TestInsertSelectCancellationOnRequiredClose(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT id FROM t")
	defer source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stage, err := exec.OpenNode(ctx, &exec.State{Source: source}, b.query.Root)
	if err != nil {
		t.Fatal(err)
	}
	closing := &insertCloseCursor{RowCursor: &stage, cancel: cancel}
	rows, err := assembleInsertRows(ctx, b, closing, stage.Count(), "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil, false, nil, nil)
	if !errors.Is(err, context.Canceled) || rows.values != nil || closing.closes != 1 {
		t.Fatal(rows, err, closing.closes)
	}
	source.Close()
}

func TestInsertSelectSourceLockIdentity(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		e := New()
		holder := sessionStart(t, e, nil)
		worker := sessionStart(t, e, nil)
		sessionSQL(t, holder, "CREATE TABLE t(i INT)")
		sessionSQL(t, holder, "CREATE TABLE s(i INT)")
		sessionSQL(t, holder, "INSERT INTO s VALUES(1),(1)")
		tx := transactionBegin(t, holder)
		sessionSQL(t, holder, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE")
		ctx, cancel := context.WithCancel(context.Background())
		done := lockAsync(e, worker, ctx, "INSERT INTO t SELECT i FROM s", false)
		lockPending(t, e, 1)
		target := catalogRef(t, e, e.CatalogRead(), "t")
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: target}]
		held := false
		if entry != nil {
			for _, mode := range entry.Holders {
				if mode.Mode == lock.RowExclusive {
					held = true
				}
			}
		}
		if !held {
			t.Fatal("target RX not retained while source AS waits")
		}
		if cancelWait {
			cancel()
			lockAwait(t, done, context.Canceled)
			transactionEnd(t, holder, tx, false)
			transactionRows(t, worker, "SELECT i FROM t")
		} else {
			transactionEnd(t, holder, tx, false)
			lockAwait(t, done, nil)
			transactionRows(t, worker, "SELECT i FROM t ORDER BY i", 1, 1)
			cancel()
		}
		lockEmpty(t, e)
	}
}

func TestInsertSelectIndependentACL(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER worker")
	roleExecute(t, e, op, "CREATE TABLE t(i INT)")
	roleExecute(t, e, op, "CREATE TABLE s(i INT,v INT,b BOOL)")
	roleExecute(t, e, op, "INSERT INTO s VALUES(1,2,TRUE)")
	target := roleTableRef(t, e, "t")
	source := roleTableRef(t, e, "s")
	worker, _ := e.Roles().Lookup("worker")
	grantor := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(ref ids.ObjectRef, mask authorization.Privileges) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: worker.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant(target, authorization.InsertPrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION worker")
	for _, q := range []string{"INSERT INTO t SELECT i FROM s", "INSERT INTO t SELECT 1 FROM s WHERE FALSE"} {
		roleError(t, e, op, q, "42501", "permission denied for table s", "")
	}
	for _, sub := range []int32{1, 3} {
		ref := source
		ref.Address.SubID = sub
		grant(ref, authorization.SelectPrivilege)
	}
	roleError(t, e, op, "INSERT INTO t SELECT i FROM s WHERE b ORDER BY v", "42501", "permission denied for table s", "")
	ref := source
	ref.Address.SubID = 2
	grant(ref, authorization.SelectPrivilege)
	if r := roleExecute(t, e, op, "INSERT INTO t SELECT i FROM s WHERE b ORDER BY v"); r.Count != 1 || r.Rows != nil {
		t.Fatal(r)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "ALTER TABLE s RENAME COLUMN i TO renamed")
	roleExecute(t, e, op, "ALTER TABLE s DROP v")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION worker")
	if r := roleExecute(t, e, op, "INSERT INTO t SELECT renamed FROM s WHERE b"); r.Count != 1 {
		t.Fatal(r)
	}
	grant(target, 0)
	roleError(t, e, op, "INSERT INTO t SELECT renamed FROM s", "42501", "permission denied for table t", "")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	r := roleExecute(t, e, op, "SELECT i FROM t")
	if len(tuples(r)) != 2 {
		t.Fatal(r)
	}
}

func TestInsertSelectSelfTransaction(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(i INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1),(1),(2)")
	if r := sessionSQL(t, a, "INSERT INTO t SELECT * FROM t"); r.Count != 3 {
		t.Fatal(r)
	}
	transactionRows(t, b, "SELECT i FROM t ORDER BY i", 1, 1, 1, 1, 2, 2)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(3)")
	if r := sessionSQL(t, a, "INSERT INTO t SELECT * FROM t"); r.Count != 7 {
		t.Fatal(r)
	}
	transactionRows(t, b, "SELECT i FROM t ORDER BY i", 1, 1, 1, 1, 2, 2)
	transactionRows(t, a, "SELECT i FROM t ORDER BY i", 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3)
	transactionEnd(t, a, tx, true)
	transactionRows(t, b, "SELECT i FROM t ORDER BY i", 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3)
}
