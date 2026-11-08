package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// The literal and bound-input diagnostics reuse the independently established
// enum producer contract. Each mutation executes once, including failures.
func TestReferenceEnumIndexLayoutGenerationUnsafe(t *testing.T) {
	for _, child := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			for _, parameter := range []bool{false, true} {
				t.Run(fmt.Sprintf("child=%t/prepared=%t/parameter=%t", child, prepared, parameter), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							c := enumLayoutCase{t, p, backend}
							owner, _ := fixture.LockConnections(t, p.Ctx, backend.DB)
							if child {
								c.exec(owner, "BEGIN", 0)
							}
							defer owner.ExecContext(p.Ctx, "ROLLBACK")
							c.exec(owner, enumLayoutCreateType, 0)
							c.exec(owner, enumLayoutCreateTable, 0)
							c.exec(owner, enumLayoutInsert, 1, "pending")
							c.exec(owner, enumLayoutCreateIndex, 0)
							id := c.identity(owner)
							if child {
								c.exec(owner, "SAVEPOINT child", 0)
							} else {
								c.exec(owner, "BEGIN", 0)
							}
							c.exec(owner, enumLayoutAddValue, 0)
							c.exec(owner, enumLayoutAddColumn, 0)
							c.check(owner, id, true)
							c.exec(owner, "SAVEPOINT producer", 0)
							q := `SELECT 'inhibited'::grind_indexed_enum`
							var args []any
							want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P04", Message: `unsafe use of new value "inhibited" of enum type grind_indexed_enum`, Hint: "New enum values must be committed before they can be used.", Position: "8"}
							phase := "execute"
							if prepared {
								phase = "prepare"
							}
							if parameter {
								q = enumLayoutInsert
								args = []any{"inhibited"}
								want.Position = ""
								want.Context = "unnamed portal parameter $1 = '...'"
								phase = "execute"
							}
							var stmt *sql.Stmt
							var err error
							actualPhase := "execute"
							if prepared {
								stmt, err = owner.PrepareContext(p.Ctx, q)
								if err != nil {
									actualPhase = "prepare"
								}
								if stmt != nil {
									defer stmt.Close()
								}
							}
							if err == nil {
								if parameter {
									var result sql.Result
									if stmt != nil {
										result, err = stmt.ExecContext(p.Ctx, args...)
									} else {
										result, err = owner.ExecContext(p.Ctx, q, args...)
									}
									if result != nil {
										t.Error("unsafe insertion exposed result")
									}
								} else {
									var rows *sql.Rows
									if stmt != nil {
										rows, err = stmt.QueryContext(p.Ctx)
									} else {
										rows, err = owner.QueryContext(p.Ctx, q)
									}
									if rows != nil {
										rows.Close()
										t.Error("unsafe query exposed rows")
									}
								}
							}
							if err == nil {
								t.Fatal("unsafe enum use succeeded")
							}
							if got := fixture.ExtractDiagnostic(t, err); got != want || actualPhase != phase {
								t.Errorf("phase=%s diagnostic=%#v; want phase=%s diagnostic=%#v", actualPhase, got, phase, want)
							}
							_, err = owner.ExecContext(p.Ctx, "SELECT 1")
							aborted := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
							if err == nil {
								t.Error("failed producer did not abort transaction")
							} else if got := fixture.ExtractDiagnostic(t, err); got != aborted {
								t.Errorf("aborted diagnostic=%#v; want %#v", got, aborted)
							}
							c.exec(owner, "ROLLBACK TO producer", 0)
							c.check(owner, id, true)
							c.rows(owner, true, [][]any{{"pending", nil}})
							// Recovery permits the old label and cannot leak the rejected new row.
							c.exec(owner, enumLayoutInsert, 1, "pending")
							c.exec(owner, `DELETE FROM grind_enum_indexed WHERE status='pending'`, 2)
							c.rows(owner, true, nil)
							c.exec(owner, "RELEASE producer", 0)
							if child {
								c.exec(owner, "ROLLBACK TO child", 0)
								c.check(owner, id, false)
								c.rows(owner, false, [][]any{{"pending"}})
								c.exec(owner, "RELEASE child", 0)
							}
							c.exec(owner, "COMMIT", 0)
							if child {
								c.check(owner, id, false)
							} else {
								c.check(owner, id, true)
								c.exec(owner, enumLayoutInsert, 1, "inhibited")
								c.rows(owner, true, [][]any{{"inhibited", nil}})
							}
						})
					}
				})
			}
		}
	}
}
