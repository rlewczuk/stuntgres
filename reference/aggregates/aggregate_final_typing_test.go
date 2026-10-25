package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateFinalTyping(t *testing.T) {
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateFinalSetup(t, p, b, c)
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.prorettype=25::oid AS result_type,a.aggtranstype=23::oid AS state_type,a.aggfinalfn=f.oid AS final,a.aggnumdirectargs,a.aggfinalextra,a.aggfinalmodify::text AS modify,p.proisstrict FROM (SELECT oid,prorettype,proisstrict FROM pg_proc WHERE proname='fa_sum') p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggfinalfn WHERE f.proname='fa_final'", []string{"result_type", "state_type", "final", "aggnumdirectargs", "aggfinalextra", "modify", "proisstrict"}, []string{"BOOL", "BOOL", "BOOL", "INT2", "BOOL", "TEXT", "BOOL"}, [][]any{{true, true, true, int64(0), false, "r", false}}))
		createAggregateRun(t, p, b, c,
			fixture.Exec(`CREATE DOMAIN fa_result AS text COLLATE "C"`, 0),
			fixture.Exec("CREATE FUNCTION fa_domain_final(integer) RETURNS fa_result LANGUAGE SQL AS $$SELECT ('total='||$1::text)::fa_result$$", 0),
			fixture.Exec("CREATE FUNCTION fa_text_step(integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$", 0),
			fixture.Exec("CREATE AGGREGATE fa_domain(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_domain_final)", 0),
			fixture.Exec("CREATE AGGREGATE fa_text(text) (SFUNC=fa_text_step,STYPE=int4,INITCOND='0',FINALFUNC=fa_final)", 0),
			fixture.Exec("CREATE AGGREGATE fa_domain_text(text) (SFUNC=fa_text_step,STYPE=int4,INITCOND='0',FINALFUNC=fa_domain_final)", 0),
			fixture.Exec(`CREATE TABLE fa_collation_input(id integer,p text COLLATE "POSIX",d text)`, 0),
			fixture.Exec("INSERT INTO fa_collation_input VALUES(1,'a','b')", 1))
		for _, tc := range []struct{ name, expression, source, collation, typ, value string }{
			{"fa_type_default", "fa_sum(x)", "fa_input", "default", "text", "total=6"},
			{"fa_type_domain", "fa_domain(x)", "fa_input", "C", "fa_result", "total=6"},
			{"fa_type_c", `fa_text(d COLLATE "C")`, "fa_collation_input", "C", "text", "total=1"},
			{"fa_type_posix", "fa_text(p)", "fa_collation_input", "POSIX", "text", "total=1"},
			{"fa_type_modifiers", `fa_text(p ORDER BY d COLLATE "C") FILTER(WHERE (d COLLATE "C")<>'skip')`, "fa_collation_input", "POSIX", "text", "total=1"},
			{"fa_type_domain_default", "fa_domain_text(d)", "fa_collation_input", "default", "fa_result", "total=1"},
		} {
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE VIEW "+tc.name+" AS SELECT "+tc.expression+" AS v FROM "+tc.source, 0))
			createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT v::text AS v FROM "+tc.name, tc.value))
			attribute := "SELECT attcollation,atttypid FROM pg_attribute WHERE attrelid='" + tc.name + "'::regclass AND attname='v'"
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT c.collname,a.atttypid='"+tc.typ+"'::regtype AS result_type FROM ("+attribute+") a JOIN pg_collation c ON c.oid=a.attcollation", []string{"collname", "result_type"}, []string{"NAME", "BOOL"}, [][]any{{tc.collation, true}}))
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.prorettype='fa_result'::regtype AS result_type,a.aggtranstype=23::oid AS state_type FROM (SELECT oid,prorettype FROM pg_proc WHERE proname='fa_domain') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"result_type", "state_type"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		domain := aggregateFinalText("SELECT fa_domain(x) AS v FROM fa_input", "total=6")
		domain.CheckMetadata = aggregateFinalMetadata(b.PG, false)
		createAggregateBoth(t, p, b, c, domain)
		stored := aggregateFinalText("SELECT v FROM fa_type_domain", "total=6")
		stored.CheckMetadata = aggregateFinalMetadata(b.PG, true)
		createAggregateBoth(t, p, b, c, stored)
		// Without a final routine, native aggregate output retains known nullable
		// state metadata; pq reports unknown nullability on both routes.
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE AGGREGATE fa_state_only(text) (SFUNC=pg_catalog.textcat,STYPE=text,INITCOND='')", 0),
			fixture.Exec("CREATE VIEW fa_state_view AS SELECT fa_state_only(x::text ORDER BY id) AS v FROM fa_input", 0))
		const query = "SELECT fa_state_only(x::text ORDER BY id) AS v FROM fa_input WHERE id<=$1"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		for _, tc := range []struct {
			limit int64
			value string
		}{{3, "123"}, {0, ""}, {1, "1"}, {3, "123"}} {
			step := aggregateFinalText(query, tc.value, tc.limit)
			step.CheckMetadata = aggregateFinalMetadata(b.PG, true)
			createAggregateBoth(t, p, b, c, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		stateView := aggregateFinalText("SELECT v FROM fa_state_view", "123")
		stateView.CheckMetadata = aggregateFinalMetadata(b.PG, true)
		createAggregateBoth(t, p, b, c, stateView)
	})
}

func TestReferenceAggregateFinalExtra(t *testing.T) {
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION fa_pair_step(integer,integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+coalesce($2,0)$$", 0),
			fixture.Exec("CREATE FUNCTION fa_extra_final(integer,integer,text) RETURNS text LANGUAGE SQL AS $$SELECT 'total='||$1::text||';nulls='||(($2 IS NULL) AND ($3 IS NULL))::text$$", 0),
			fixture.Exec("CREATE AGGREGATE fa_extra(integer,text) (SFUNC=fa_pair_step,STYPE=int4,INITCOND='0',FINALFUNC=fa_extra_final,FINALFUNC_EXTRA)", 0),
			fixture.Exec("CREATE FUNCTION fa_tick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$", 0),
			fixture.Exec("CREATE FUNCTION fa_final(integer) RETURNS text LANGUAGE SQL AS $$SELECT 'total='||$1::text$$", 0),
			fixture.Exec("CREATE AGGREGATE fa_zero(*) (SFUNC=fa_tick,STYPE=int4,INITCOND='0',FINALFUNC=fa_final,FINALFUNC_EXTRA)", 0))
		for _, tc := range []struct{ suffix, value string }{{"", "total=6;nulls=true"}, {" WHERE FALSE", "total=0;nulls=true"}} {
			createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_extra(x,y) AS v FROM(VALUES(1,'a'::text),(2,'b'::text),(3,'c'::text)) s(x,y)"+tc.suffix, tc.value))
		}
		const q = "SELECT fa_extra($1,$2) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			x          int64
			text, want string
		}{{4, "not-null", "total=4;nulls=true"}, {9, "different", "total=9;nulls=true"}, {4, "not-null", "total=4;nulls=true"}} {
			step := aggregateFinalText(q, tc.want, tc.x, tc.text)
			createAggregateBoth(t, p, b, c, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_zero(*) AS v FROM(VALUES(1),(2),(3)) s(x)", "total=3"))
		createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_zero(*) AS v FROM(VALUES(1)) s(x) WHERE FALSE", "total=0"))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggnumdirectargs,a.aggfinalextra FROM (SELECT oid FROM pg_proc WHERE proname='fa_extra') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"aggnumdirectargs", "aggfinalextra"}, []string{"INT2", "BOOL"}, [][]any{{int64(0), true}}))
		for _, collation := range []string{"C", "POSIX"} {
			name := "fa_extra_" + collation
			createAggregateRun(t, p, b, c, fixture.Exec(`CREATE VIEW "`+name+`" AS SELECT fa_extra(x,y COLLATE "`+collation+`") AS v FROM(VALUES(1,'a'::text),(2,'b'::text)) s(x,y)`, 0))
			createAggregateBoth(t, p, b, c, aggregateFinalText(`SELECT v FROM "`+name+`"`, "total=3;nulls=true"))
			attribute := `SELECT attcollation FROM pg_attribute WHERE attrelid='"` + name + `"'::regclass AND attname='v'`
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT c.collname FROM ("+attribute+") a JOIN pg_collation c ON c.oid=a.attcollation", []string{"collname"}, []string{"NAME"}, [][]any{{collation}}))
		}
	})
}
