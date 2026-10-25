package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from PostgreSQL18 catalog-pg-largeobject, lo-implementation
// and REL_18_6 inv_api.c, not from the embedded implementation.
func pglScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"pgl_owner", "pgl_reader"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer func() {
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON pg_catalog.pg_largeobject FROM pgl_owner,pgl_reader", "REVOKE SELECT(loid,pageno,data) ON pg_catalog.pg_largeobject FROM pgl_owner,pgl_reader", "SELECT lo_unlink(oid) FROM pg_largeobject_metadata WHERE oid>=920101::oid AND oid<=920120::oid"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error("cleanup", q, err)
							}
						}
					}()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						s = pglBackendStep(s, b)
						if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
							t.FailNow()
						}
					}
					run(fixture.Exec("CREATE ROLE pgl_owner", 0))
					run(fixture.Exec("CREATE ROLE pgl_reader", 0))
					body(t, p, b, c, run)
				})
			}
		})
	}
}

func pglBackendStep(s fixture.Step, b fixture.Backend) fixture.Step {
	if !b.PG || s.Rows == nil {
		return s
	}
	rows := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		rows[i] = append([]any(nil), row...)
		for j, typ := range s.Types {
			if typ == "OID" {
				if v, ok := row[j].(int64); ok {
					rows[i][j] = fmt.Sprint(v)
				}
			}
		}
	}
	s.Rows = rows
	return s
}

func pglValue(expr, typ string, value any) fixture.Step {
	return fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{value}})
}
func pglCreate(oid int, hex string) fixture.Step {
	return pglValue(fmt.Sprintf("lo_from_bytea(%d::oid,decode('%s','hex'))", oid, hex), "OID", int64(oid))
}
func pglPut(oid int, offset int64, hex string) fixture.Step {
	return pglValue(fmt.Sprintf("lo_put(%d::oid,%d::bigint,decode('%s','hex'))", oid, offset, hex), "VOID", "")
}
func pglError(q, code, message string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}
func pglPages(oid int, pages ...any) fixture.Step {
	rows := make([][]any, 0, len(pages)/2)
	for i := 0; i < len(pages); i += 2 {
		rows = append(rows, []any{int64(oid), int64(pages[i].(int)), string(pages[i+1].([]byte))})
	}
	s := fixture.Select(fmt.Sprintf("SELECT loid,pageno,data FROM pg_catalog.pg_largeobject WHERE loid=%d::oid ORDER BY pageno", oid), []string{"loid", "pageno", "data"}, []string{"OID", "INT4", "BYTEA"}, rows)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column != 2 {
			return false
		}
		v, ok := value.([]byte)
		if !ok || !bytes.Equal(v, []byte(rows[row][2].(string))) {
			t.Fatalf("raw page %d: %T %x, want %x", row, value, value, rows[row][2])
		}
		return true
	}
	return s
}
