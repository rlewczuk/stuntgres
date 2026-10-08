//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func referenceAlterNullabilityPeerRows(t *testing.T, rows *sql.Rows, native, commit bool) {
	t.Helper()
	defer rows.Close()
	names := []string{"a"}
	want := [][]int64{{7}, {8}}
	if !commit {
		names = []string{"a", "b"}
		want = [][]int64{{7, 9}}
	}
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal(gotNames, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range metadata {
		scan := reflect.TypeFor[int32]()
		nullable := false
		if native {
			scan = reflect.TypeFor[int64]()
			nullable = !commit
		}
		n, k := column.Nullable()
		if column.DatabaseTypeName() != "INT4" || column.ScanType() != scan || k != native || n != nullable {
			t.Fatal(column, n, k)
		}
	}
	got := make([][]int64, 0)
	for rows.Next() {
		row := make([]int64, len(names))
		dest := make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}

func TestReferenceAlterNullabilityReviewCombinedPeer(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
				native bool
			}{{"postgres", p.postgres, p.postgresRecord, false}, {"native", p.memory, p.memoryRecord, true}} {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
					holder, waiter := 0, 0
					var oid int64
					if !backend.native {
						holder = referenceLockPID(t, p.ctx, a)
						waiter = referenceLockPID(t, p.ctx, b)
						oid = referenceLockOID(t, p.ctx, "public.t")
					}
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					referenceRunStep(t, p.ctx, tx, backend.record, referenceExec("ALTER TABLE t DROP b, ALTER a SET NOT NULL", 0))
					referenceRunStep(t, p.ctx, tx, backend.record, referenceExec("INSERT INTO t VALUES(8)", 1))
					q := "SELECT * FROM t ORDER BY a"
					type observation struct {
						rows *sql.Rows
						err  error
					}
					done := make(chan observation, 1)
					if backend.native {
						blocked, cancel := context.WithTimeout(p.ctx, 50*time.Millisecond)
						rows, e := b.QueryContext(blocked, q)
						cancel()
						if rows != nil {
							rows.Close()
						}
						if !errors.Is(e, context.DeadlineExceeded) {
							t.Fatal("native peer was not blocked", e)
						}
					} else {
						go func() { rows, e := b.QueryContext(p.ctx, q); done <- observation{rows, e} }()
						referenceLockWaitBehind(t, p.ctx, waiter, holder, oid, "AccessShareLock")
						var pending string
						if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT query FROM pg_stat_activity WHERE pid=$1", waiter).Scan(&pending); err != nil || pending != q {
							t.Fatal("different queued query", pending, err)
						}
					}
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					var observed observation
					if backend.native {
						observed.rows, observed.err = b.QueryContext(p.ctx, q)
					} else {
						select {
						case observed = <-done:
						case <-p.ctx.Done():
							t.Fatal(p.ctx.Err())
						}
					}
					if observed.err != nil {
						t.Fatal(observed.err)
					}
					referenceAlterNullabilityPeerRows(t, observed.rows, backend.native, commit)
				})
			}
		})
	}
}
