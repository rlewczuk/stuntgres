package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func rowLockPublicViewFixture(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn, context.Context) {
	t.Helper()
	db, a, b := transactionConnections(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20),(3,30)", "CREATE VIEW v AS SELECT id,v FROM t WHERE id<3", "CREATE VIEW w AS SELECT id,v+1 AS val FROM v"} {
		if _, err := a.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return db, a, b, ctx
}
func rowLockPublicViewBlocked(t *testing.T, x transactionExecutor, q string, prepared bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result, err := transactionExec(ctx, x, q, prepared)
	if !errors.Is(err, context.DeadlineExceeded) || result != nil {
		t.Fatal("view conflict", q, result, err)
	}
}
func rowLockPublicValues(t *testing.T, rows *sql.Rows, name, typ string, want ...int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{name}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ || types[0].ScanType() != reflect.TypeFor[int64]() {
		t.Fatal("metadata", types, err)
	}
	transactionRowValues(t, rows, want...)
}
func rowLockPublicSelect(t *testing.T, ctx context.Context, x transactionExecutor, q string, prepared bool, name, typ string, want ...int64) {
	t.Helper()
	rows, closeQuery, err := transactionQuery(ctx, x, q, prepared)
	if err != nil {
		t.Fatal(q, err)
	}
	defer closeQuery()
	rowLockPublicValues(t, rows, name, typ, want...)
	if err := closeQuery(); err != nil {
		t.Fatal(err)
	}
}
func TestRowLockViews(t *testing.T) {
	for _, clause := range rowLockPublicClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b, ctx := rowLockPublicViewFixture(t)
				holder, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				rows, closeQuery, err := transactionQuery(ctx, holder, "SELECT val FROM w WHERE id=1 "+clause, prepared)
				if err != nil {
					t.Fatal(err)
				}
				defer closeQuery()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].Name() != "val" || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal(types, err)
				}
				rowLockPublicValues(t, rows, "val", "INT4", 11)
				closeQuery()
				rowLockPublicSelect(t, ctx, b, "SELECT val FROM w WHERE id=1", prepared, "val", "INT4", 11)
				transactionAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=2", prepared, 1)
				transactionAffected(t, ctx, b, "UPDATE t SET v=v+1 WHERE id=3", prepared, 1)
				if clause == "FOR KEY SHARE" {
					transactionAffected(t, ctx, b, "UPDATE t SET v=40 WHERE id=1", prepared, 1)
				} else {
					rowLockPublicViewBlocked(t, b, "UPDATE t SET v=40 WHERE id=1", prepared)
				}
				rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				transactionAffected(t, ctx, b, "DELETE FROM t WHERE id=1", prepared, 1)
			})
		}
	}
}
func TestRowLockViewProvenance(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b, ctx := rowLockPublicViewFixture(t)
			for _, q := range []string{"CREATE TABLE u(id INT)", "INSERT INTO u VALUES(1),(1),(4)", "CREATE VIEW joined AS SELECT t.v FROM t JOIN u ON t.id=u.id", "CREATE VIEW zero_width AS SELECT FROM t WHERE id=1"} {
				if _, err := a.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			transactionSelect(t, ctx, holder, "SELECT v FROM joined FOR SHARE", prepared, 10, 10)
			rowLockPublicViewBlocked(t, b, "DELETE FROM u WHERE id=1", prepared)
			rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
			transactionAffected(t, ctx, b, "UPDATE u SET id=5 WHERE id=4", prepared, 1)
			holder.Rollback()
			holder, err = a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			rows, closeQuery, err := transactionQuery(ctx, holder, "SELECT FROM zero_width FOR KEY SHARE", prepared)
			if err != nil {
				t.Fatal(err)
			}
			defer closeQuery()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 0 {
				t.Fatal(columns, err)
			}
			count := 0
			for rows.Next() {
				count++
				if err := rows.Scan(); err != nil {
					t.Fatal(err)
				}
			}
			if err := rows.Err(); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			closeQuery()
			rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
		})
	}
}
func TestRowLockViewDiagnostics(t *testing.T) {
	for _, clause := range rowLockPublicClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				_, a, b, ctx := rowLockPublicViewFixture(t)
				for _, tc := range []struct{ name, body, suffix string }{{"grouped", "SELECT id FROM t GROUP BY id", "GROUP BY clause"}, {"having_view", "SELECT count(*) AS n FROM t HAVING TRUE", "HAVING clause"}, {"aggregate_view", "SELECT count(*) AS n FROM t", "aggregate functions"}} {
					transactionAffected(t, ctx, a, "CREATE VIEW "+tc.name+" AS "+tc.body, false, 0)
					rows, closeQuery, err := transactionQuery(ctx, b, "SELECT * FROM "+tc.name+" "+clause, prepared)
					if closeQuery != nil {
						closeQuery()
					}
					if rows != nil {
						rows.Close()
						t.Fatal("grouped error cursor")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: clause + " is not allowed with " + tc.suffix})
				}
				transactionAffected(t, ctx, a, "CREATE VIEW stored AS SELECT id,v FROM t "+clause, false, 0)
				reader, err := b.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Rollback()
				stmt, err := reader.PrepareContext(ctx, "SELECT v FROM stored WHERE FALSE")
				if err != nil {
					t.Fatal("readonly Prepare", err)
				}
				defer stmt.Close()
				rows, err := stmt.QueryContext(ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("readonly error cursor")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25006", Message: "cannot execute SELECT in a read-only transaction"})
			})
		}
	}
}
func TestRowLockViewStored(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db, a, b, ctx := rowLockPublicViewFixture(t)
			transactionAffected(t, ctx, a, "CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", false, 0)
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			transactionSelect(t, ctx, holder, "SELECT v FROM stored WHERE id=1 FOR KEY SHARE", prepared, 10)
			rowLockPublicViewBlocked(t, b, "UPDATE t SET v=99 WHERE id=1", prepared)
			transactionAffected(t, ctx, b, "UPDATE t SET v=21 WHERE id=2", prepared, 1)
			holder.Rollback()
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "FOR UPDATE") {
				t.Fatal("lost stored clause", dump.String())
			}
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			holder, err = a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			transactionSelect(t, ctx, holder, "SELECT v FROM stored WHERE id=1", prepared, 10)
			rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
		})
	}
}
func TestRowLockViewAggregation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b, ctx := rowLockPublicViewFixture(t)
			transactionAffected(t, ctx, a, "CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", false, 0)
			transactionAffected(t, ctx, a, "CREATE VIEW grouped AS SELECT count(*) AS n FROM stored WHERE id<3", false, 0)
			transactionAffected(t, ctx, a, "CREATE VIEW ordinary_group AS SELECT count(*) AS n FROM t", false, 0)
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			rowLockPublicSelect(t, ctx, holder, "SELECT n FROM grouped", prepared, "n", "INT8", 2)
			rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
			rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=2", prepared)
			transactionAffected(t, ctx, b, "UPDATE t SET v=31 WHERE id=3", prepared, 1)
			holder.Rollback()
			holder, err = a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			rowLockPublicSelect(t, ctx, holder, "SELECT ordinary_group.n FROM stored CROSS JOIN ordinary_group WHERE stored.id=1", prepared, "n", "INT8", 3)
			rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
			transactionAffected(t, ctx, b, "UPDATE t SET v=22 WHERE id=2", prepared, 1)
		})
	}
}
func TestRowLockVirtualCatalogBoundary(t *testing.T) {
	for _, clause := range rowLockPublicClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				_, a, b, ctx := rowLockPublicViewFixture(t)
				for _, catalog := range []struct{ source, suffix string }{{"pg_catalog.pg_class", "class"}, {"pg_catalog.pg_rewrite", "rewrite"}, {"pg_catalog.pg_views", "views"}} {
					direct, nested, stored := "virtual_"+catalog.suffix, "nested_"+catalog.suffix, "stored_"+catalog.suffix
					transactionAffected(t, ctx, a, "CREATE VIEW "+direct+" AS SELECT * FROM "+catalog.source, false, 0)
					transactionAffected(t, ctx, a, "CREATE VIEW "+nested+" AS SELECT * FROM "+direct, false, 0)
					transactionAffected(t, ctx, a, "CREATE VIEW "+stored+" AS SELECT * FROM "+direct+" "+clause, false, 0)
					for _, q := range []string{"SELECT * FROM " + catalog.source + " WHERE FALSE " + clause, "SELECT * FROM " + nested + " WHERE FALSE " + clause, "SELECT * FROM " + stored + " WHERE FALSE", "SELECT t.id FROM t CROSS JOIN " + nested + " WHERE FALSE " + clause} {
						rows, closeQuery, err := transactionQuery(ctx, b, q, prepared)
						if closeQuery != nil {
							closeQuery()
						}
						if rows != nil {
							rows.Close()
							t.Fatal("virtual error cursor")
						}
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
					}
				}
				transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1", false, 10)
			})
		}
	}
}
