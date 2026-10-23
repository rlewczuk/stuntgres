package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePosixActualCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					b.DB.SetMaxOpenConns(1)
					fixture.StringCoreControlExec(t, b.DB, `CREATE TABLE cancel_t(v TEXT,p TEXT,sentinel INT)`)
					defer func() {
						if _, err := b.DB.ExecContext(p.Ctx, `DROP TABLE IF EXISTS cancel_t`); err != nil {
							t.Error("cancel fixture cleanup", err)
						}
					}()
					result, err := b.DB.ExecContext(p.Ctx, `INSERT INTO cancel_t VALUES($1,'^(a|aa)*$',7)`, strings.Repeat("a", 8*1024*1024))
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					for _, query := range []string{`UPDATE cancel_t SET sentinel=99 WHERE v ~ p`, `UPDATE cancel_t SET sentinel=99 WHERE substring(v,p) IS NOT NULL`} {
						if b.PG {
							fixture.SessionCancelQuery(t, p.Ctx, conn, prepared, query, false)
						} else {
							var operation func(context.Context) error
							if prepared {
								stmt, err := conn.PrepareContext(p.Ctx, query)
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
							// Observe actual common matcher entry, not an unrelated coercion routine.
							likeReferenceCancelEntered(t, "sqltype.(*PGRegexProgram)", operation)
						}
						var sentinel int
						if err := conn.QueryRowContext(p.Ctx, `SELECT sentinel FROM cancel_t`).Scan(&sentinel); err != nil || sentinel != 7 {
							t.Fatal("partial canceled UPDATE", sentinel, err)
						}
					}
					result, err = conn.ExecContext(p.Ctx, `UPDATE cancel_t SET sentinel=8 WHERE 'a' ~ 'a'`)
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					var sentinel int
					if err := conn.QueryRowContext(p.Ctx, `SELECT sentinel FROM cancel_t`).Scan(&sentinel); err != nil || sentinel != 8 {
						t.Fatal("recovery", sentinel, err)
					}
					if _, err := conn.ExecContext(p.Ctx, `DROP TABLE cancel_t`); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestReferencePosixStatementTimeout(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			b.DB.SetMaxOpenConns(1)
			// Cleanup SQL cannot reliably finish inside the expired session's
			// 10ms limit under whole-suite scheduling contention. Retire that
			// idle physical connection instead; committed table state survives
			// reconnect, so the sentinel still proves autocommit atomicity.
			retireTimedSession := func() {
				b.DB.SetMaxIdleConns(0)
				b.DB.SetMaxIdleConns(1)
			}
			fixture.StringCoreControlExec(t, b.DB, `CREATE TABLE timeout_t(v TEXT,p TEXT,sentinel INT)`)
			defer func() {
				retireTimedSession()
				if _, err := b.DB.ExecContext(context.Background(), `DROP TABLE IF EXISTS timeout_t`); err != nil {
					t.Error("timeout fixture cleanup", err)
				}
			}()
			r, err := b.DB.ExecContext(p.Ctx, `INSERT INTO timeout_t VALUES($1,'^(a|aa)*$',7)`, strings.Repeat("a", 8*1024*1024))
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			for _, prepared := range []bool{false, true} {
				func() {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := conn.Close(); err != nil {
							t.Error("timeout connection close", err)
						}
					}()
					const query = `UPDATE timeout_t SET sentinel=99 WHERE v ~ p`
					var executor fixture.Executor = conn
					if prepared {
						// Preparation is not the cancellation witness. Finish it
						// before arming the deadline on this physical session.
						stmt, err := conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := stmt.Close(); err != nil {
								t.Error("timeout statement close", err)
							}
						}()
						executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`SET statement_timeout='10ms'`, 0))
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "57014", Message: "canceling statement due to statement timeout"})
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				}()
				retireTimedSession()
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT sentinel FROM timeout_t`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`UPDATE timeout_t SET sentinel=8 WHERE 'a' ~ 'a'`, 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT sentinel FROM timeout_t`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(8)}}))
		})
	}
}
