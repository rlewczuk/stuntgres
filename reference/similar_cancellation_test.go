package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReferenceSimilarActualCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					b.db.SetMaxOpenConns(1)
					stringCoreControlExec(t, b.db, `CREATE TABLE t(v TEXT,p TEXT,sentinel INT)`)
					// Valid regex workload observed active through pinned PostgreSQL admission.
					// Finishing before the observation barrier remains an explicit failure.
					input := strings.Repeat("a", 8*1024*1024)
					result, err := b.db.ExecContext(p.ctx, `INSERT INTO t VALUES($1,'(a|aa)*',7)`, input)
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					const query = `UPDATE t SET sentinel=99 WHERE v SIMILAR TO p`
					if b.pg {
						referenceSessionCancelQuery(t, p.ctx, conn, prepared, query, false)
					} else {
						var operation func(context.Context) error
						if prepared {
							stmt, err := conn.PrepareContext(p.ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							operation = func(ctx context.Context) error {
								r, err := stmt.ExecContext(ctx)
								if r != nil {
									return errors.New("canceled UPDATE exposed result")
								}
								return err
							}
						} else {
							operation = func(ctx context.Context) error {
								r, err := conn.ExecContext(ctx, query)
								if r != nil {
									return errors.New("canceled UPDATE exposed result")
								}
								return err
							}
						}
						// Observe the actual compiled SQL-regex matcher, not unrelated coercion
						// or another sqltype provider before admitting cancellation.
						likeReferenceCancelEntered(t, "sqltype.(*SimilarProgram)", operation)
					}
					var sentinel int
					if err := conn.QueryRowContext(p.ctx, `SELECT sentinel FROM t`).Scan(&sentinel); err != nil || sentinel != 7 {
						t.Fatal("canceled regex published UPDATE", sentinel, err)
					}
					result, err = conn.ExecContext(p.ctx, `UPDATE t SET sentinel=8 WHERE 'a' SIMILAR TO 'a'`)
					if err != nil {
						t.Fatal("recovery", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					if err := conn.QueryRowContext(p.ctx, `SELECT sentinel FROM t`).Scan(&sentinel); err != nil || sentinel != 8 {
						t.Fatal("recovery publication", sentinel, err)
					}
				})
			}
		})
	}
}
