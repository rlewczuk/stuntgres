package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMergeInsertBinding(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT DEFAULT 8,g INT GENERATED ALWAYS AS(v+1) STORED)", "CREATE TABLE source(id INT,v INT)", "INSERT INTO t VALUES(1,10,DEFAULT)", "INSERT INTO source VALUES(1,7),(2,8),(3,9)"} {
		sessionSQL(t, s, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED AND s.id=2 THEN INSERT(v,id) VALUES(s.v,s.id) WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)")
	if !reflect.DeepEqual(b.Targets, []int{1, 0}) || !reflect.DeepEqual(b.UsedColumns, []int{0}) || len(b.Merge.Arms) != 2 {
		t.Fatal("authority manifests", b.Targets, b.UsedColumns, b.Merge)
	}
	first, second := &b.Merge.Arms[0].Input, &b.Merge.Arms[1].Input
	if !reflect.DeepEqual(first.Targets, []int{1, 0}) || !reflect.DeepEqual(second.Targets, []int{0}) || first.Insert.Defaults != nil || len(second.Insert.Defaults) != 3 || second.Insert.Defaults[1] == nil {
		t.Fatal("independent arm schedules", first, second)
	}
	data := target.View.Data()
	if inputs.View.Data() != data || len(data.Committed) != 2 {
		t.Fatal("target/source did not share frontier")
	}
	r, err := executeMerge(op.Ctx, scope, b, target, inputs, nil, "public")
	if err != nil || r.Count != 2 || r.Rows != nil || len(scope.stage.rows[b.Ref.Address]) != 2 {
		t.Fatal("completed producer", r, err, scope.stage.rows)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, s, "SELECT v FROM t ORDER BY id", 10, 8, 8)
	transactionRows(t, s, "SELECT g FROM t ORDER BY id", 11, 9, 9)
	lockEmpty(t, e)
}

type mergeInsertTestCursor struct {
	schema                []catalog.Column
	rows                  [][]sqltype.Value
	position, closed      int
	nextError, closeError error
	beforeNext            func()
	beforeClose           func()
}

func (c *mergeInsertTestCursor) Schema() []catalog.Column { return c.schema }
func (c *mergeInsertTestCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if c.beforeNext != nil {
		c.beforeNext()
	}
	if c.position == len(c.rows) {
		return false, c.nextError
	}
	copy(dst, c.rows[c.position])
	c.position++
	return true, nil
}
func (c *mergeInsertTestCursor) Close() error {
	c.closed++
	if c.beforeClose != nil {
		c.beforeClose()
	}
	return c.closeError
}

func TestMergeInsertPublication(t *testing.T) {
	for _, phase := range []string{"complete", "late_source", "close", "primary_wins_close"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT)")
			op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING (VALUES(7),(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)")
			typ := b.Relation.Columns[0].Usage.Type
			value, _ := sqltype.Integer(typ, 7)
			primary, cleanup := errors.New("late source"), errors.New("required Close")
			cursor := &mergeInsertTestCursor{schema: b.Merge.Source.Schema(), rows: [][]sqltype.Value{{value}, {value}}}
			if phase == "late_source" || phase == "primary_wins_close" {
				cursor.nextError = primary
			}
			if phase == "close" || phase == "primary_wins_close" {
				cursor.closeError = cleanup
			}
			state := &exec.State{Source: inputs, Input: scope.input, Output: scope.output}
			rows, err := dml.AssembleMergeRows(scope.writeHost(), scope.mergeInput(op.Ctx, b, target, nil, nil, "public"), cursor, state, false)
			if cursor.closed != 1 || len(scope.stage.rows) != 0 {
				t.Fatal("producer published or did not close exactly once", cursor.closed, scope.stage.rows)
			}
			if phase == "complete" {
				if err != nil || len(rows.Values) != 2 {
					t.Fatal("bag occurrences", rows, err)
				}
				if err := dml.StageMergeRows(scope.writeHost(), rows, b.Ref); err != nil {
					t.Fatal(err)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			} else {
				want := primary
				if phase == "close" {
					want = cleanup
				}
				if !errors.Is(err, want) || rows != nil {
					t.Fatal("atomic failure", rows, err, want)
				}
				scope.Finish(err)
			}
			op.Done()
			target.Close()
			inputs.Close()
			if phase == "complete" {
				transactionRows(t, s, "SELECT id FROM t", 7, 7)
			} else {
				transactionRows(t, s, "SELECT id FROM t")
			}
			lockEmpty(t, e)
		})
	}
}

func TestMergeInsertCancellation(t *testing.T) {
	for _, phase := range []string{"source", "probe", "when", "assignment", "default", "before_apply"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT,v INT DEFAULT 8)")
			sessionSQL(t, s, "INSERT INTO t VALUES(99,99)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, scope, b, target, inputs := updateFromBoundSources(t, e, s, ctx, "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN NOT MATCHED AND s.id>0 THEN INSERT(id) VALUES(s.id)")
			calls := 0
			wrap := func(original expr.Expr) expr.Expr {
				return queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					value, err := original.Eval(c)
					if calls == 2 {
						cancel()
					}
					return value, err
				})
			}
			switch phase {
			case "probe":
				b.Merge.On = wrap(b.Merge.On)
			case "when":
				b.Merge.Arms[0].Condition = wrap(b.Merge.Arms[0].Condition)
			case "assignment":
				b.Merge.Arms[0].Input.Expressions[0][0] = wrap(b.Merge.Arms[0].Input.Expressions[0][0])
			case "default":
				b.Merge.Arms[0].Input.Insert.Defaults[1] = wrap(b.Merge.Arms[0].Input.Insert.Defaults[1])
			}
			var result Result
			var err error
			if phase == "source" || phase == "before_apply" {
				value, _ := sqltype.Integer(b.Relation.Columns[0].Usage.Type, 1)
				cursor := &mergeInsertTestCursor{schema: b.Merge.Source.Schema(), rows: [][]sqltype.Value{{value}}}
				if phase == "source" {
					cursor.beforeNext = cancel
				} else {
					cursor.beforeClose = cancel
				}
				state := &exec.State{Source: inputs, Input: scope.input, Output: scope.output}
				_, err = dml.AssembleMergeRows(scope.writeHost(), scope.mergeInput(ctx, b, target, nil, nil, "public"), cursor, state, false)
				if cursor.closed != 1 {
					t.Fatal("canceled source not closed")
				}
				target.Close()
				inputs.Close()
			} else {
				result, err = executeMerge(ctx, scope, b, target, inputs, nil, "public")
			}
			if !errors.Is(err, context.Canceled) || result.Count != 0 || result.Rows != nil || len(scope.stage.rows) != 0 {
				t.Fatal("cancellation leaked publication", result, err, scope.stage.rows)
			}
			scope.Finish(err)
			op.Done()
			transactionRows(t, s, "SELECT id FROM t", 99)
			lockEmpty(t, e)
		})
	}
}

func TestMergeInsertColumnAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER merge_writer", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(id INT,v INT)"} {
		roleExecute(t, e, op, q)
	}
	owner := op.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("merge_writer")
	grant := func(table string, attribute int, mask authorization.Privileges) {
		ref := roleTableRef(t, e, table)
		ref.Address.SubID = int32(attribute)
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", 1, authorization.InsertPrivilege)
	grant("s", 1, authorization.SelectPrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION merge_writer")
	q := "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT(id) VALUES(s.id) WHEN NOT MATCHED THEN INSERT(v) VALUES(1)"
	roleError(t, e, op, q, "42501", "permission denied for table t", "")
	roleExecute(t, e, op, "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)")
	roleExecute(t, e, op, "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(id) VALUES(1)")
	roleError(t, e, op, "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)", "42501", "permission denied for table t", "")
	grant("t", 2, authorization.InsertPrivilege)
	grant("t", 1, authorization.InsertPrivilege|authorization.SelectPrivilege)
	roleExecute(t, e, op, q)
	grant("s", 1, 0)
	roleError(t, e, op, "MERGE INTO t USING s ON FALSE WHEN NOT MATCHED THEN INSERT(id) VALUES(1)", "42501", "permission denied for table s", "")
	lockEmpty(t, e)
}

func TestMergeInsertSystemCondition(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT)")
	sessionSQL(t, s, "CREATE TABLE source(id INT)")
	for _, name := range []string{"xmin", "ctid", "cmin", "xmax", "cmax"} {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Describe(op, statement(t, "MERGE INTO t USING source s ON FALSE WHEN NOT MATCHED AND s."+name+" IS NULL THEN INSERT VALUES(1)"))
		op.Done()
		var diagnostic interface{ SQLState() string }
		if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42P10" {
			t.Fatal(name, err)
		}
	}
	// Same-named ordinary derived columns are not physical system attributes.
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Describe(op, statement(t, "MERGE INTO t USING (VALUES(1)) s(xmin) ON FALSE WHEN NOT MATCHED AND s.xmin=1 THEN INSERT VALUES(s.xmin)"))
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	lockEmpty(t, e)
}
