package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently prescribed released-view display, not native-produced text.
const itDefinition = ` SELECT (current_database())::information_schema.sql_identifier AS trigger_catalog,
    (n.nspname)::information_schema.sql_identifier AS trigger_schema,
    (t.tgname)::information_schema.sql_identifier AS trigger_name,
    (em.text)::information_schema.character_data AS event_manipulation,
    (current_database())::information_schema.sql_identifier AS event_object_catalog,
    (n.nspname)::information_schema.sql_identifier AS event_object_schema,
    (c.relname)::information_schema.sql_identifier AS event_object_table,
    (rank() OVER (PARTITION BY (n.nspname)::information_schema.sql_identifier, (c.relname)::information_schema.sql_identifier, em.num, ((t.tgtype)::integer & 1), ((t.tgtype)::integer & 66) ORDER BY t.tgname))::information_schema.cardinal_number AS action_order,
    (
        CASE
            WHEN pg_has_role(c.relowner, 'USAGE'::text) THEN (regexp_match(pg_get_triggerdef(t.oid), '.{35,} WHEN \((.+)\) EXECUTE FUNCTION'::text))[1]
            ELSE NULL::text
        END)::information_schema.character_data AS action_condition,
    (SUBSTRING(pg_get_triggerdef(t.oid) FROM (POSITION(('EXECUTE FUNCTION'::text) IN (SUBSTRING(pg_get_triggerdef(t.oid) FROM 48))) + 47)))::information_schema.character_data AS action_statement,
    (
        CASE ((t.tgtype)::integer & 1)
            WHEN 1 THEN 'ROW'::text
            ELSE 'STATEMENT'::text
        END)::information_schema.character_data AS action_orientation,
    (
        CASE ((t.tgtype)::integer & 66)
            WHEN 2 THEN 'BEFORE'::text
            WHEN 64 THEN 'INSTEAD OF'::text
            ELSE 'AFTER'::text
        END)::information_schema.character_data AS action_timing,
    (t.tgoldtable)::information_schema.sql_identifier AS action_reference_old_table,
    (t.tgnewtable)::information_schema.sql_identifier AS action_reference_new_table,
    (NULL::name)::information_schema.sql_identifier AS action_reference_old_row,
    (NULL::name)::information_schema.sql_identifier AS action_reference_new_row,
    (NULL::timestamp with time zone)::information_schema.time_stamp AS created
   FROM pg_namespace n,
    pg_class c,
    pg_trigger t,
    ( VALUES (4,'INSERT'::text), (8,'DELETE'::text), (16,'UPDATE'::text)) em(num, text)
  WHERE ((n.oid = c.relnamespace) AND (c.oid = t.tgrelid) AND (((t.tgtype)::integer & em.num) <> 0) AND (NOT t.tgisinternal) AND (NOT pg_is_other_temp_schema(n.oid)) AND (pg_has_role(c.relowner, 'USAGE'::text) OR has_table_privilege(c.oid, 'INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER'::text) OR has_any_column_privilege(c.oid, 'INSERT, UPDATE, REFERENCES'::text)));`

func itBuiltinDefinition(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	for _, path := range []string{"public", "public,information_schema,pg_catalog"} {
		run(fixture.Exec("SET search_path="+path, 0))
		want := itDefinition
		if path != "public" {
			want = strings.ReplaceAll(want, "information_schema.", "")
		}
		for _, query := range []string{
			"SELECT pg_get_viewdef('information_schema.triggers'::regclass) AS definition",
			"SELECT definition FROM pg_catalog.pg_views WHERE schemaname='information_schema' AND viewname='triggers'",
			"SELECT view_definition AS definition FROM information_schema.views WHERE table_schema='information_schema' AND table_name='triggers'",
		} {
			s := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{want}})
			if strings.Contains(query, "view_definition AS") {
				s.Types = []string{"VARCHAR"}
			}
			run(s)
			run(fixture.Prepared(s))
			itHeld(t, p, c, r, query)(s)
		}
	}
	run(fixture.Exec("RESET search_path", 0))
}

func TestReferenceInfoTriggersAfter(t *testing.T) {
	itrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE TABLE public.it_after(id integer)", 0))
		run(fixture.Exec("CREATE FUNCTION public.it_after_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$", 0))
		var want [][]any
		for _, kind := range []struct{ names []string; timing, level string }{
			{[]string{"a", "z"}, "BEFORE", "ROW"},
			{[]string{"b", "y"}, "AFTER", "ROW"},
			{[]string{"c", "x"}, "AFTER", "STATEMENT"},
		} {
			for i, name := range kind.names {
				run(fixture.Exec("CREATE TRIGGER "+name+" "+kind.timing+" INSERT ON public.it_after FOR EACH "+kind.level+" EXECUTE FUNCTION public.it_after_fn()", 0))
				row := itrRow(database, "public", "it_after", name, "INSERT", int64(i+1), nil, "EXECUTE FUNCTION it_after_fn()")
				row[10], row[11] = kind.level, kind.timing
				want = append(want, row)
			}
		}
		run(fixture.Exec("CREATE TRIGGER trunc_only AFTER TRUNCATE ON public.it_after EXECUTE FUNCTION public.it_after_fn()", 0))
		run(fixture.Exec("CREATE TRIGGER mixed AFTER INSERT OR TRUNCATE ON public.it_after EXECUTE FUNCTION public.it_after_fn()", 0))
		row := itrRow(database, "public", "it_after", "mixed", "INSERT", 2, nil, "EXECUTE FUNCTION it_after_fn()")
		row[10], row[11] = "STATEMENT", "AFTER"
		want = append(want, row)
		// c, mixed, x are the independent AFTER STATEMENT INSERT group.
		for _, row := range want {
			if row[2] == "x" { row[7] = int64(3) }
		}
		s := itProjection(b.PG, "event_object_schema='public' AND event_object_table='it_after'", want)
		run(s)
		run(fixture.Prepared(s))
		itHeld(t, p, c, r, s.Query)(s)
	})
}
