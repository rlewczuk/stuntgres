package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGLargeobjectSnapshots(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE public.pgl_snapshot_guard(x integer)", 0))
		run(fixture.Exec("INSERT INTO public.pgl_snapshot_guard VALUES(1)", 1))
		// FROM a real guard prevents scalar SQL-function inlining.
		run(fixture.Exec("CREATE FUNCTION public.pgl_page(oid) RETURNS bytea LANGUAGE SQL AS 'SELECT l.data FROM pg_catalog.pg_largeobject l CROSS JOIN public.pgl_snapshot_guard g WHERE l.loid=$1 AND l.pageno=0'", 0))
		run(pglValue("encode(public.pgl_page(lo_from_bytea(920101::oid,decode('00ff','hex'))),'hex')", "TEXT", "00ff"))
		run(pglPages(920101, 0, []byte{0, 255}))
		run(pglCreate(920102, "aa00"))
		updated := "CASE WHEN lo_put(920102::oid,0::bigint,decode('bbff','hex')) IS NULL THEN 0::oid ELSE 920102::oid END"
		run(fixture.Select("SELECT encode(l.data,'hex') AS outer_before,encode(public.pgl_page("+updated+"),'hex') AS body_value,encode(l.data,'hex') AS outer_after FROM pg_largeobject l WHERE l.loid=920102::oid AND l.pageno=0", []string{"outer_before", "body_value", "outer_after"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"aa00", "bbff", "aa00"}}))
		run(pglPages(920102, 0, []byte{187, 255}))
		// A scalar page subquery retains the outer snapshot even after the
		// VOLATILE body successfully reads its argument-created object.
		run(fixture.Select("SELECT encode(public.pgl_page(lo_from_bytea(920103::oid,decode('00ff','hex'))),'hex') AS body_created,(SELECT encode(data,'hex') FROM pg_largeobject WHERE loid=920103::oid AND pageno=0) AS outer_after", []string{"body_created", "outer_after"}, []string{"TEXT", "TEXT"}, [][]any{{"00ff", nil}}))
		run(pglPages(920103, 0, []byte{0, 255}))
		run(pglError("SELECT encode(public.pgl_page(lo_from_bytea(920104::oid,decode('00ff','hex'))),'hex') AS body_created,encode(lo_get(920104::oid),'hex') AS outer_after", "42704", "large object 920104 does not exist"))
		run(pglPages(920104))
		run(fixture.Select("SELECT count(*) AS v FROM pg_largeobject_metadata WHERE oid=920104::oid", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// Body evaluation itself fails after argument effects. No staged bytes
		// publish; the following outer execution sees the old immutable root.
		run(fixture.Exec("CREATE FUNCTION public.pgl_page_fail(oid) RETURNS bytea LANGUAGE SQL AS 'SELECT lo_get(920120::oid) FROM public.pgl_snapshot_guard'", 0))
		s := pglError("SELECT public.pgl_page_fail(CASE WHEN lo_put(920102::oid,0::bigint,decode('ccdd','hex')) IS NULL THEN 0::oid ELSE 920102::oid END)", "42704", "large object 920120 does not exist")
		// Complete SQL-function context confirmed by the pinned PostgreSQL oracle.
		s.Diagnostic.Context = "SQL function \"pgl_page_fail\" statement 1"
		run(s)
		run(pglPages(920102, 0, []byte{187, 255}))
	})
}
