package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestRowLockOrderedCacheFrontier(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"t", "w", "stored"} {
			t.Run(source+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b, ctx := rowLockPublicViewFixture(t)
				transactionAffected(t, ctx, a, "CREATE VIEW stored AS SELECT id,v FROM t WHERE id<3 ORDER BY v+1,id FOR UPDATE", false, 0)
				expression := "v+1"
				want := []int64{11, 21}
				if source == "w" {
					expression = "val+1"
					want = []int64{12, 22}
				}
				query := "SELECT " + expression + " AS k FROM " + source + " WHERE id<3 ORDER BY k,id"
				if source != "stored" {
					query += " FOR UPDATE"
				}
				holder, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				rows, closeQuery, err := transactionQuery(ctx, holder, query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				defer closeQuery()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].Name() != "k" || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal(types, err)
				}
				rowLockPublicValues(t, rows, "k", "INT4", want...)
				closeQuery()
				rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
				holder.Rollback()
				transactionAffected(t, ctx, b, "UPDATE t SET v=30 WHERE id=1", false, 1)
				// A subsequent head is not an enqueue witness. Direct same-level
				// frozen rank is proved by engine and PostgreSQL waiter tests;
				// nested view placement differences are independently specified.
				rowLockPublicSelect(t, ctx, a, query, prepared, "k", "INT4", want[1], want[0]+20)
				transactionAffected(t, ctx, a, "CREATE SEQUENCE s", false, 0)
				transactionAffected(t, ctx, a, "UPDATE t SET v=10 WHERE id=1", false, 1)
				query = "SELECT nextval('s') AS n FROM " + source + " WHERE id=1 ORDER BY n,n"
				if source != "stored" {
					query += " FOR UPDATE"
				}
				rowLockPublicSelect(t, ctx, a, query, prepared, "n", "INT8", 1)
				rowLockPublicSelect(t, ctx, a, "SELECT currval('s') AS n", false, "n", "INT8", 1)
				transactionAffected(t, ctx, a, "CREATE TABLE target(v INT)", false, 0)
				_, err = transactionExec(ctx, a, "INSERT INTO target SELECT 1/(v-10) AS k FROM t WHERE id=1 ORDER BY k FOR UPDATE", prepared)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
				transactionSelect(t, ctx, b, "SELECT v FROM target", false)
				transactionAffected(t, ctx, b, "UPDATE t SET v=v WHERE id=1", false, 1)
			})
		}
	}
}

func TestRowLockOrderedCacheSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b, ctx := rowLockPublicViewFixture(t)
				reader, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Rollback()
				query := "SELECT val+1 AS k FROM w WHERE id<3 ORDER BY k,id"
				rowLockPublicSelect(t, ctx, reader, query, false, "k", "INT4", 12, 22)
				transactionAffected(t, ctx, b, "UPDATE t SET v=30 WHERE id=1", false, 1)
				if clause == "FOR KEY SHARE" {
					rowLockPublicSelect(t, ctx, reader, query+" "+clause, prepared, "k", "INT4", 12, 22)
				} else {
					rows, closeQuery, err := transactionQuery(ctx, reader, query+" "+clause, prepared)
					if rows != nil {
						rows.Close()
					}
					if closeQuery != nil {
						closeQuery()
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "40001", Message: "could not serialize access due to concurrent update"})
				}
			})
		}
	}
}
