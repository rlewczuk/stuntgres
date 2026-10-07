package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestRowLockViewAggregateComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			query   string
			want    []int64
			blocked string
		}{
			{"SELECT count(*) AS n FROM stored WHERE id=1", []int64{1}, "1"},
			{"SELECT count(*) AS n FROM stored", []int64{3}, "2"},
			{"SELECT n FROM grouped ORDER BY id", []int64{1, 1, 1}, "3"},
			{"SELECT n FROM hidden", nil, "2"},
			{"SELECT n FROM grouped WHERE FALSE", nil, "2"},
			{"SELECT count(*) AS n FROM grouped", []int64{3}, "2"},
		} {
			t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b, ctx := rowLockPublicViewFixture(t)
				for _, q := range []string{"CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", "CREATE VIEW grouped AS SELECT id,count(*) AS n FROM stored GROUP BY id", "CREATE VIEW hidden AS SELECT count(*) AS n FROM stored HAVING FALSE"} {
					transactionAffected(t, ctx, a, q, false, 0)
				}
				holder, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				rows, closeQuery, err := transactionQuery(ctx, holder, tc.query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				defer closeQuery()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].Name() != "n" || types[0].DatabaseTypeName() != "INT8" {
					t.Fatal(types, err)
				}
				rowLockPublicValues(t, rows, "n", "INT8", tc.want...)
				closeQuery()
				rowLockPublicViewBlocked(t, b, "DELETE FROM t WHERE id="+tc.blocked, prepared)
				if tc.blocked == "1" {
					transactionAffected(t, ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
					transactionAffected(t, ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1)
				}
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				transactionAffected(t, ctx, b, "DELETE FROM t WHERE id="+tc.blocked, prepared, 1)
			})
		}
	}
}

func TestRowLockViewSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range rowLockPublicClauses {
			for _, aggregate := range []bool{false, true} {
				for _, deleted := range []bool{false, true} {
					t.Run(clause+map[bool]string{false: "/scalar", true: "/aggregate"}[aggregate]+map[bool]string{false: "/update", true: "/delete"}[deleted]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						_, a, b, ctx := rowLockPublicViewFixture(t)
						if aggregate {
							transactionAffected(t, ctx, a, "CREATE VIEW stored AS SELECT id,v FROM t WHERE id=1 "+clause, false, 0)
							transactionAffected(t, ctx, a, "CREATE VIEW grouped AS SELECT v,count(*) AS n FROM stored GROUP BY v", false, 0)
						}
						reader, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
						if err != nil {
							t.Fatal(err)
						}
						defer reader.Rollback()
						rowLockPublicSelect(t, ctx, reader, "SELECT val FROM w WHERE id=1", false, "val", "INT4", 11)
						change := "UPDATE t SET v=20 WHERE id=1"
						if deleted {
							change = "DELETE FROM t WHERE id=1"
						}
						transactionAffected(t, ctx, a, change, false, 1)
						query := "SELECT val FROM w WHERE val=11 " + clause
						value := int64(11)
						if aggregate {
							query = "SELECT v FROM grouped"
							value = 10
						}
						rows, closeQuery, err := transactionQuery(ctx, reader, query, prepared)
						if clause == "FOR KEY SHARE" && !deleted {
							if err != nil {
								t.Fatal(err)
							}
							defer closeQuery()
							name := "val"
							if aggregate {
								name = "v"
							}
							rowLockPublicValues(t, rows, name, "INT4", value)
							closeQuery()
						} else {
							if closeQuery != nil {
								closeQuery()
							}
							if rows != nil {
								rows.Close()
								t.Fatal("serialization error cursor")
							}
							typeCoreDiagnostic(t, err, stuntgres.Error{Code: "40001", Message: "could not serialize access due to concurrent update"})
							rows, closeQuery, err = transactionQuery(ctx, reader, "SELECT val FROM w", false)
							if closeQuery != nil {
								closeQuery()
							}
							if rows != nil {
								rows.Close()
								t.Fatal("aborted cursor")
							}
							typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						}
					})
				}
			}
		}
	}
}
