package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Every mutation route owns a fresh scenario; LOs outlive public-schema reset.
func lmCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
						t.Error(err)
						return
					}
					if _, err := c.ExecContext(ctx, "SELECT lo_unlink(oid) FROM pg_largeobject_metadata WHERE oid IN (910201::oid,910202::oid,910203::oid,4294967295::oid)"); err != nil {
						t.Error(err)
					}
				}()
				body(t, p, b, c, prepared)
			})
		})
	}
}

func lmSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, columnType, args, events, level string) {
	t.Helper()
	loInstall(t, p, b, db)
	loRun(t, p, b, db, fixture.Exec("CREATE TABLE lm(id integer PRIMARY KEY,v "+columnType+",n integer CONSTRAINT lm_n_check CHECK(n>=0))", 0))
	loRun(t, p, b, db, fixture.Exec("CREATE TRIGGER lm_cleanup BEFORE "+events+" ON lm FOR EACH "+level+" EXECUTE FUNCTION lo_manage("+args+")", 0))
	lmCreate(t, p, b, db, 910201, "00ff")
	lmCreate(t, p, b, db, 910202, "6162")
	loRun(t, p, b, db, fixture.Exec("INSERT INTO lm VALUES(1,910201,0)", 1))
}

func lmCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, oid int64, hex string) {
	t.Helper()
	loRows(t, p, b, db, fixture.Select(fmt.Sprintf("SELECT lo_from_bytea(%d::oid,decode('%s','hex')) AS object", oid, hex), []string{"object"}, []string{"OID"}, [][]any{{oid}}))
}

func lmRun(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, prepared bool, s fixture.Step) {
	t.Helper()
	s.Prepared = prepared
	loRun(t, p, b, db, s)
}

func lmRows(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, want ...[]any) {
	t.Helper()
	if want == nil {
		want = [][]any{}
	}
	loRows(t, p, b, db, fixture.Select("SELECT id,v,n FROM lm ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "OID", "INT4"}, want))
}

func lmRow(v any, n int64) []any { return []any{int64(1), v, n} }

func lmBytes(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, oid int64, hex string) {
	t.Helper()
	loRun(t, p, b, db, fixture.Select(fmt.Sprintf("SELECT encode(lo_get(%d::oid),'hex') AS bytes", oid), []string{"bytes"}, []string{"TEXT"}, [][]any{{hex}}))
}

func lmError(q, code, message string) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(code, message))
	s.ErrorStage = "execute"
	return s
}

func lmMissing(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, oid int64) {
	t.Helper()
	s := lmError(fmt.Sprintf("SELECT lo_get(%d::oid)", oid), "42704", fmt.Sprintf("large object %d does not exist", oid))
	s.SelectRows = true
	loRun(t, p, b, db, s)
}
