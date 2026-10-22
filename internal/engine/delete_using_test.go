package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestDeleteUsingPhysicalIdentityAndDemand(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,label TEXT)", "INSERT INTO t VALUES(2,20),(2,20),(3,30)", "INSERT INTO s VALUES(2,'same'),(2,'same'),(3,'third')"} {
		sessionSQL(t, s, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "DELETE FROM t d USING s x WHERE d.id=x.k RETURNING d.id,d.v,x.label,new.id")
	data := target.View.Data()
	if len(b.QuerySources) != 1 || inputs == nil || inputs.View == nil || inputs.View.Data() != data {
		t.Fatal("incoherent command capture")
	}
	original := append([]storage.RowVersion(nil), target.Committed...)
	if original[0].Row == original[1].Row {
		t.Fatal("equal values lack distinct physical identities")
	}
	calls := 0
	out := b.Returning.Expressions[0]
	b.Returning.Expressions[0] = queryProbe(out.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if root := scope.stage.deletes[b.Ref.Address]; root == nil || len(root.Rows) != calls {
			t.Fatal("output precedes actual unique staging", calls, root)
		}
		return out.Eval(c)
	})
	r, err := dml.ExecuteDelete(scope.writeHost(), dml.DeleteInput{Binding: b, Source: target, Inputs: inputs})
	if r.Rows != nil {
		t.Cleanup(func() { r.Rows.Close() })
	}
	if err != nil || r.Count != 3 || calls != 3 {
		t.Fatal(r, err, calls)
	}
	got := tuples(outcomeResult(r))
	r.Rows.Close()
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(2), int64(20), "same", nil}, {int64(2), int64(20), "same", nil}, {int64(3), int64(30), "third", nil}}) {
		t.Fatal(got)
	}
	root := scope.stage.deletes[b.Ref.Address]
	for _, v := range original {
		candidate, ok := root.Rows[v.Row]
		if !ok || candidate.Storage != b.Storage || candidate.Ref != b.Ref || candidate.Version != v.Version {
			t.Fatal("lost deletion authority", candidate, v)
		}
	}
	target.Close()
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	if target.View != nil || inputs.Sources != nil || len(scope.querySources) != 0 {
		t.Fatal("capture leaked")
	}
	for i, v := range original {
		if testExport(v.Values[1]) != testExport(e.storage.Relations[b.Ref.Address].Versions[i].Values[1]) {
			t.Fatal("immutable predecessor changed")
		}
	}
	transactionRows(t, s, "SELECT id FROM t")
	transactionRows(t, s, "SELECT k FROM s ORDER BY k", 2, 2, 3)
	lockEmpty(t, e)
}

func TestDeleteUsingLateFailureAndCancellation(t *testing.T) {
	for _, phase := range []string{"late_output", "source_rejected", "candidate_rejected", "output_cancel", "publication_cancel"} {
		for _, explicit := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
				e := New()
				writer := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1),(2)", "INSERT INTO r VALUES(1),(2)"} {
					sessionSQL(t, peer, q)
				}
				var tx ids.TxRef
				if explicit {
					tx = transactionBegin(t, writer)
					sessionSQL(t, writer, "UPDATE t SET v=11 WHERE id=1")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, scope, b, target, inputs := updateFromBoundSources(t, e, writer, ctx, "DELETE FROM t USING s JOIN r ON s.k=r.k WHERE t.id=s.k RETURNING t.id")
				children := append([]*exec.Source(nil), inputs.Sources...)
				calls := 0
				sentinel := errors.New("late DELETE USING output failure")
				switch phase {
				case "source_rejected":
					join := b.Delete.Using.(*plan.JoinNode)
					usage := join.Predicate.ResultUsage()
					join.Predicate = queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
						calls++
						if calls == 3 {
							cancel()
						}
						return sqltype.Boolean(usage.Type, false), nil
					})
				case "candidate_rejected":
					usage := b.Predicate.ResultUsage()
					b.Predicate = queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
						calls++
						if calls == 3 {
							cancel()
						}
						return sqltype.Boolean(usage.Type, false), nil
					})
				default:
					out := b.Returning.Expressions[0]
					b.Returning.Expressions[0] = queryProbe(out.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						calls++
						if len(scope.stage.deletes[b.Ref.Address].Rows) != calls {
							t.Fatal("output before stage")
						}
						if calls == 2 {
							if phase == "late_output" {
								return sqltype.Value{}, sentinel
							}
							cancel()
							if phase == "output_cancel" {
								return sqltype.Value{}, context.Canceled
							}
						}
						return out.Eval(c)
					})
				}
				r, err := dml.ExecuteDelete(scope.writeHost(), dml.DeleteInput{Binding: b, Source: target, Inputs: inputs})
				if r.Rows != nil {
					t.Cleanup(func() { r.Rows.Close() })
				}
				target.Close()
				want := error(context.Canceled)
				wantCalls := 2
				if phase == "late_output" {
					want = sentinel
				}
				if phase == "source_rejected" || phase == "candidate_rejected" {
					wantCalls = 3
				}
				if !errors.Is(err, want) || r.Count != 0 || r.Rows != nil || calls != wantCalls {
					t.Fatal(r, err, calls)
				}
				if err := scope.Finish(err); !errors.Is(err, want) {
					t.Fatal(err)
				}
				op.Done()
				for _, child := range children {
					if child.View != nil || child.Committed != nil || child.Own != nil {
						t.Fatal("orphan child view")
					}
				}
				if target.View != nil || inputs.Sources != nil || len(scope.querySources) != 0 {
					t.Fatal("orphan command view")
				}
				if explicit {
					if writer.TransactionState() != session.TxAborted || writer.Transactions.Writes != nil || writer.Transactions.Deletes != nil {
						t.Fatal("private work survived abort")
					}
					transactionEnd(t, writer, tx, false)
				}
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
				lockEmpty(t, e)
			})
		}
	}
}

func TestDeleteUsingCapturedOccurrenceLatestTarget(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	bSession := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1,100),(2,200)"} {
		sessionSQL(t, a, q)
	}
	op, scope, b, target, inputs := updateFromBoundSources(t, e, bSession, context.Background(), "DELETE FROM t USING s WHERE t.id=s.k RETURNING old.v,s.n")
	sessionSQL(t, a, "UPDATE t SET v=11 WHERE id=1")
	sessionSQL(t, a, "UPDATE s SET n=999")
	sessionSQL(t, a, "INSERT INTO t VALUES(1,30)")
	sessionSQL(t, a, "INSERT INTO s VALUES(1,400)")
	r, err := dml.ExecuteDelete(scope.writeHost(), dml.DeleteInput{Binding: b, Source: target, Inputs: inputs})
	if r.Rows != nil {
		t.Cleanup(func() { r.Rows.Close() })
	}
	target.Close()
	if err != nil || r.Count != 2 {
		t.Fatal(r, err)
	}
	got := tuples(outcomeResult(r))
	r.Rows.Close()
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(11), int64(100)}, {int64(20), int64(200)}}) {
		t.Fatal(got)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, a, "SELECT v FROM t", 30)
	lockEmpty(t, e)
}
