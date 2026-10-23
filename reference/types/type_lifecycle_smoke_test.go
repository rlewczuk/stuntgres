package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleFeatureSmoke(t *testing.T) {
	typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "CREATE TYPE tl_src.mood AS ENUM ('z','a')", "CREATE DOMAIN tl_src.positive AS integer DEFAULT 7 CHECK(VALUE>0)", "CREATE TYPE tl_src.pair AS(n integer,note text)", "CREATE EXTENSION cube WITH SCHEMA tl_src", "CREATE TABLE public.tl_smoke(keep integer,m tl_src.mood,d tl_src.positive,p tl_src.pair,a tl_src.positive[],c tl_src.cube)")
		run(fixture.Exec("INSERT INTO public.tl_smoke VALUES(11,'z',7,'(7,x)','[0:1]={7,NULL}','(1,2)')", 1))
		var ids []int64
		for _, name := range []string{"mood", "positive", "pair", "cube", "positive[]"} {
			ids = append(ids, domainAlterOID(t, p, c, "SELECT 'tl_src."+name+"'::regtype::oid"))
		}
		q := "SELECT keep,m::text,d,p::text,a::text,c::text FROM public.tl_smoke"
		held := typeLifecycleHeld(t, p, c, q)
		for _, name := range []string{"mood", "positive", "pair", "cube"} {
			domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src."+name+" RENAME TO moved_"+name, "ALTER TYPE tl_src.moved_"+name+" SET SCHEMA tl_dst")
		}
		for i, name := range []string{"mood", "positive", "pair", "cube", "positive[]"} {
			domainIdentitySameOID(t, p, c, "SELECT 'tl_dst.moved_"+name+"'::regtype::oid", ids[i])
		}
		s := fixture.Select(q, []string{"keep", "m", "d", "p", "a", "c"}, []string{"INT4", "TEXT", "INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(11), "z", int64(7), "(7,x)", "[0:1]={7,NULL}", "(1, 2)"}})
		fixture.RunStep(t, p.Ctx, held, r, s)
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type tl_dst.moved_mood because other objects depend on it")
		want.Detail = "column m of table tl_smoke depends on type tl_dst.moved_mood"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		typeLifecycleFailure(t, p, c, "DROP TYPE tl_dst.moved_mood RESTRICT", want)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(s)
		}
		*notices = nil
		run(fixture.Prepared(fixture.Exec("DROP TYPE tl_dst.moved_mood CASCADE", 0)))
		domainIdentityNotices(t, notices, typeLifecycleNotice("drop cascades to column m of table tl_smoke"))
		run(fixture.Select("SELECT keep,d,p::text,a::text,c::text FROM public.tl_smoke", []string{"keep", "d", "p", "a", "c"}, []string{"INT4", "INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(11), int64(7), "(7,x)", "[0:1]={7,NULL}", "(1, 2)"}}))
		typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='tl_dst'::regnamespace::oid AND typname IN ('moved_mood','_moved_mood')", 0)
		typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_attribute WHERE attrelid='public.tl_smoke'::regclass::oid AND attname='m' AND NOT attisdropped", 0)
	})
}
