package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Source-derived contracts: PostgreSQL REL_18_6 be-fsstubs.c, inv_api.c,
// pg_proc.dat and pg_largeobject.c; docs/18/lo-funcs.html and lo-implementation.html.
func largeObjectScenario(t *testing.T, schemas []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step), bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"og_object_reader", "og_object_other"})
			for _, schema := range schemas {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer func() {
						if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
							t.Error(err)
						}
						if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
							t.Error(err)
						}
					}()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						s = largeObjectBackendStep(s, b)
						if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
							t.FailNow()
						}
					}
					run(fixture.Exec("CREATE ROLE og_object_reader", 0))
					run(fixture.Exec("CREATE ROLE og_object_other", 0))
					body(t, p, b, c, run, prepared)
				})
			}
		})
	}
}

func largeObjectCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn, oids ...uint32) {
	t.Helper()
	if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
		t.Error(err)
	}
	if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Error(err)
	}
	for _, oid := range oids {
		q := fmt.Sprintf("SELECT lo_unlink(oid) FROM pg_catalog.pg_largeobject_metadata WHERE oid=%d::oid", oid)
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Error("large object cleanup", q, err)
		}
	}
}

func largeObjectValue(expression, typ string, value any, args ...any) fixture.Step {
	return fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{typ}, [][]any{{value}}, args...)
}

func largeObjectError(query, state, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: state, Message: message}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func largeObjectMissing(query string, oid uint32, args ...any) fixture.Step {
	return largeObjectError(query, "42704", fmt.Sprintf("large object %d does not exist", oid), args...)
}

func largeObjectBytes(oid uint32, hex string) fixture.Step {
	return largeObjectValue(fmt.Sprintf("encode(lo_get(%d::oid),'hex')", oid), "TEXT", hex)
}

func largeObjectPut(oid uint32, offset int64, hex string) fixture.Step {
	return largeObjectValue(fmt.Sprintf("lo_put(%d::oid,%d::bigint,decode('%s','hex'))", oid, offset, hex), "VOID", "")
}

// lib/pq exposes OID as decimal octets, native as int64. Assert both raw
// codecs through RunStep while retaining independently specified numeric IDs.
func largeObjectBackendStep(s fixture.Step, b fixture.Backend) fixture.Step {
	if !b.PG || s.Rows == nil {
		return s
	}
	rows := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		rows[i] = append([]any(nil), row...)
		for j, typ := range s.Types {
			if typ == "OID" {
				if oid, ok := row[j].(int64); ok {
					rows[i][j] = fmt.Sprint(oid)
				}
			}
		}
	}
	s.Rows = rows
	return s
}
