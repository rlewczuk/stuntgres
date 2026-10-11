package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTransactionIsolationReadOnlyUtilityPrecedence(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			for _, utility := range []struct{ query, tag string }{
				{"ALTER TABLE target RENAME TO renamed", "ALTER TABLE"},
				{"COMMENT ON TABLE target IS 'forbidden'", "COMMENT"},
			} {
				for _, missing := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(utility.tag+map[bool]string{false: "/locked", true: "/missing"}[missing]+map[bool]string{false: "/direct", true: "/held_prepared"}[prepared], func(t *testing.T) {
							p := fixture.NewScenario(t)
							db := p.Memory
							if postgres {
								db = p.Postgres
							}
							a, b := fixture.LockConnections(t, p.Ctx, db)
							var err error
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
							fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
							fixture.IsolationReferenceAffected(t, ctx, b, "COMMENT ON TABLE t IS 'original'", false, 0)
							var before bytes.Buffer
							if !postgres {
								if err := stuntgres.Dump(ctx, p.Native, &before); err != nil {
									t.Fatal(err)
								}
							}
							name := "t"
							if missing {
								name = "missing"
							}
							query := strings.Replace(utility.query, "target", name, 1)
							var stmt *sql.Stmt
							if prepared {
								var err error
								stmt, err = a.PrepareContext(ctx, query)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
							}
							if !missing {
								fixture.IsolationReferenceAffected(t, ctx, b, "BEGIN", false, 0)
								defer b.ExecContext(context.Background(), "ROLLBACK")
								fixture.IsolationReferenceAffected(t, ctx, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
							}
							fixture.IsolationReferenceAffected(t, ctx, a, "SET lock_timeout TO '20ms'", false, 0)
							fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
							defer a.ExecContext(context.Background(), "ROLLBACK")
							if prepared {
								_, err = stmt.ExecContext(ctx)
							} else {
								_, err = a.ExecContext(ctx, query)
							}
							fixture.IsolationReferenceDiagnostic(t, err, fixture.IsolationReferenceWant("25006", "cannot execute "+utility.tag+" in a read-only transaction"))
							referenceIsolationError(t, a, "SELECT current_user", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false)
							fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
							if !missing {
								fixture.IsolationReferenceAffected(t, ctx, b, "ROLLBACK", false, 0)
							}
							fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t", false, 1)
							fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t", false, 1)
							if postgres {
								var comment string
								if err := b.QueryRowContext(ctx, "SELECT obj_description('public.t'::regclass,'pg_class')").Scan(&comment); err != nil || comment != "original" {
									t.Fatal("forbidden utility changed comment", comment, err)
								}
								var count int64
								if err := b.QueryRowContext(ctx, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('missing','renamed')").Scan(&count); err != nil || count != 0 {
									t.Fatal("forbidden utility changed catalog names", count, err)
								}
							} else {
								var after bytes.Buffer
								if err := stuntgres.Dump(ctx, p.Native, &after); err != nil {
									t.Fatal(err)
								}
								if after.String() != before.String() {
									t.Fatalf("forbidden utility changed persistent state:\nbefore:\n%safter:\n%s", before.String(), after.String())
								}
							}
						})
					}
				}
			}
		})
	}
}
