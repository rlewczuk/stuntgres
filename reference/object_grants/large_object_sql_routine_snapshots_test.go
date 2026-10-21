package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Independent expectations: PostgreSQL18 xfunc-volatility.html and REL_18_6
// backend/executor/functions.c install a fresh snapshot for a VOLATILE SQL
// function's body query. FROM prevents scalar SQL-function inlining; the outer
// query keeps its own snapshot after the body returns or fails.
func TestReferenceLargeObjectSQLRoutineSnapshots(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer largeObjectCleanup(t, p, conn, 910081, 910082, 910083)
					run := func(s fixture.Step) {
						s.Prepared = prepared
						s = largeObjectBackendStep(s, backend)
						if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }) {
							t.FailNow()
						}
					}
					run(fixture.Exec("CREATE TABLE public.og_lo_snapshot_guard(x integer)", 0))
					run(fixture.Exec("INSERT INTO public.og_lo_snapshot_guard VALUES(1)", 1))
					run(fixture.Exec("CREATE FUNCTION public.og_lo_read_snapshot(oid) RETURNS bytea LANGUAGE SQL AS 'SELECT lo_get($1) FROM public.og_lo_snapshot_guard'", 0))

					// The outer argument creates the object. The non-inline body
					// query's fresh READ snapshot sees that command-visible candidate.
					run(largeObjectValue("encode(public.og_lo_read_snapshot(lo_from_bytea(910081::oid,decode('00ff','hex'))),'hex')", "TEXT", "00ff"))
					run(largeObjectBytes(910081, "00ff"))

					run(largeObjectValue("lo_from_bytea(910082::oid,decode('aa00','hex'))", "OID", int64(910082)))
					// The first read freezes the outer view. Put is demanded by the
					// wrapper argument; its nonnull VOID result selects the existing
					// OID. The body sees new bytes, then unwind restores the outer view.
					updated := "CASE WHEN lo_put(910082::oid,0::bigint,decode('bbff','hex')) IS NULL THEN 0::oid ELSE 910082::oid END"
					run(fixture.Select("SELECT encode(lo_get(910082::oid),'hex') AS outer_before,encode(public.og_lo_read_snapshot("+updated+"),'hex') AS body_value,encode(lo_get(910082::oid),'hex') AS outer_after", []string{"outer_before", "body_value", "outer_after"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"aa00", "bbff", "aa00"}}))
					run(largeObjectBytes(910082, "bbff"))

					// A successful body read must not leak its new-object visibility
					// into the surrounding query. Its later direct get fails, and the
					// entire statement (including argument creation) rolls back.
					run(largeObjectMissing("SELECT encode(public.og_lo_read_snapshot(lo_from_bytea(910083::oid,decode('00ff','hex'))),'hex') AS body_created,encode(lo_get(910083::oid),'hex') AS outer_after", 910083))
					run(largeObjectMissing("SELECT lo_get(910083::oid)", 910083))
					run(fixture.Select("SELECT count(*) AS v FROM pg_largeobject_metadata WHERE oid=910083::oid", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			}
		})
	}
}
