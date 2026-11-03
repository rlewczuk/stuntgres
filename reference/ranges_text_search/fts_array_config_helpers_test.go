package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// All scenarios use the same pinned-session lifetime and public fixture.
func ftsArrayConfigScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer ftsConvertRestore(t, p, c)()
			body(t, p, b, c)
		})
	}
}

func ftsArrayConfigRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func ftsArrayConfigBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	ftsArrayConfigRun(t, p, b, e, s)
	ftsArrayConfigRun(t, p, b, e, fixture.Prepared(s))
}

func ftsArrayConfigValue(q, typ string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"result"}, []string{typ}, [][]any{{want}}, args...)
}

func ftsArrayConfigVector(input string, want any, args ...any) fixture.Step {
	return ftsArrayConfigValue("SELECT array_to_tsvector("+input+") AS result", "TSVECTOR", want, args...)
}

func ftsArrayConfigCurrent(name string, oid int64) fixture.Step {
	return fixture.Select("SELECT get_current_ts_config() AS cfg,get_current_ts_config()::oid::bigint AS id",
		[]string{"cfg", "id"}, []string{"REGCONFIG", "INT8"}, [][]any{{name, oid}})
}

// Build array input independently of SQL serialization/vector construction. A
// nil element is SQL NULL; every non-null element is double-quoted and escaped.
func ftsArrayConfigArray(elements ...*string) string {
	var out strings.Builder
	out.WriteByte('{')
	for i, v := range elements {
		if i != 0 {
			out.WriteByte(',')
		}
		if v == nil {
			out.WriteString("NULL")
			continue
		}
		out.WriteByte('"')
		for _, ch := range *v {
			if ch == '\\' || ch == '"' {
				out.WriteByte('\\')
			}
			out.WriteRune(ch)
		}
		out.WriteByte('"')
	}
	out.WriteByte('}')
	return out.String()
}

func ftsArrayConfigErrorRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step, analysis bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			step := s
			step.Prepared, step.SelectRows, step.ErrorStage = prepared, query, "execute"
			if prepared && analysis {
				step.ErrorStage = "prepare"
			}
			t.Run(fmt.Sprintf("prepared=%t/query=%t", prepared, query), func(t *testing.T) {
				ftsArrayConfigRun(t, p, b, e, step)
			})
		}
	}
}

// The actual catalog row is authority for generated IDs, never an allocator
// constant or a value learned from the function under test.
func ftsArrayConfigOID(t *testing.T, p *fixture.Pair, c *sql.Conn, schema, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_ts_config WHERE cfgnamespace=$1::regnamespace AND cfgname=$2", schema, name).Scan(&oid); err != nil || oid <= 0 {
		t.Fatal("configuration descriptor", schema, name, oid, err)
	}
	return oid
}
