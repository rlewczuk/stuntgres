package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleProtection(t *testing.T) {
	typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_dst", "CREATE TYPE public.tl_mood AS ENUM ('z')", "CREATE TABLE public.tl_row(keep integer)", "CREATE VIEW public.tl_view AS SELECT keep FROM public.tl_row", "CREATE MATERIALIZED VIEW public.tl_materialized AS SELECT keep FROM public.tl_row", "CREATE EXTENSION cube", "CREATE EXTENSION intarray", "CREATE EXTENSION citext", "CREATE EXTENSION earthdistance")
		for _, suffix := range []string{" RENAME TO forbidden", " SET SCHEMA tl_dst", " OWNER TO CURRENT_USER"} {
			want := fixture.ControlsReferenceDiagnostic("42809", "cannot alter array type tl_mood[]")
			want.Hint = "You can alter type tl_mood, which will alter the array type as well."
			typeLifecycleFailure(t, p, c, "ALTER TYPE public._tl_mood"+suffix, want)
			for _, name := range []string{"tl_row", "tl_view", "tl_materialized"} {
				want := fixture.ControlsReferenceDiagnostic("42809", name+" is a table's row type")
				want.Hint = "Use ALTER TABLE instead."
				typeLifecycleFailure(t, p, c, "ALTER TYPE public."+name+suffix, want)
			}
		}
		for _, tc := range []struct{ name, message, hint string }{
			{"public._tl_mood", "cannot drop type tl_mood[] because type tl_mood requires it", "You can drop type tl_mood instead."},
			{"public.tl_row", "cannot drop type tl_row because table tl_row requires it", "You can drop table tl_row instead."},
			{"public.tl_view", "cannot drop type tl_view because view tl_view requires it", "You can drop view tl_view instead."},
			{"public.tl_materialized", "cannot drop type tl_materialized because materialized view tl_materialized requires it", "You can drop materialized view tl_materialized instead."},
			{"public.cube", "cannot drop type cube because extension cube requires it", "You can drop extension cube instead."},
			{"public.query_int", "cannot drop type query_int because extension intarray requires it", "You can drop extension intarray instead."},
			{"public.citext", "cannot drop type citext because extension citext requires it", "You can drop extension citext instead."},
			{"public.earth", "cannot drop type earth because extension earthdistance requires it", "You can drop extension earthdistance instead."},
			{"pg_catalog.int4", "cannot drop type integer because it is required by the database system", ""},
			{"pg_catalog.int4range", "cannot drop type int4range because it is required by the database system", ""},
			{"pg_catalog.int4multirange", "cannot drop type int4multirange because it is required by the database system", ""},
		} {
			want := fixture.ControlsReferenceDiagnostic("2BP01", tc.message)
			want.Hint = tc.hint
			for _, suffix := range []string{" RESTRICT", " CASCADE"} {
				typeLifecycleFailure(t, p, c, "DROP TYPE "+tc.name+suffix, want)
			}
		}
		// Native's existing inventory has integer/numeric companion descriptors.
		// PostgreSQL additionally joins temporal companions; lookup-only temporal
		// identities do not imply native array descriptors or executable codecs.
		ranges := [][]any{{"int4range", "int4multirange", true, true}, {"int8range", "int8multirange", true, true}, {"numrange", "nummultirange", true, true}}
		if b.PG {
			ranges = [][]any{{"daterange", "datemultirange", true, true}, {"int4range", "int4multirange", true, true}, {"int8range", "int8multirange", true, true}, {"numrange", "nummultirange", true, true}, {"tsrange", "tsmultirange", true, true}, {"tstzrange", "tstzmultirange", true, true}}
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT r.typname,m.typname AS multirange,a.typelem=r.oid AS array_element,ma.typelem=m.oid AS multi_array_element FROM pg_range g JOIN pg_type r ON r.oid=g.rngtypid JOIN pg_type m ON m.oid=g.rngmultitypid JOIN pg_type a ON a.oid=r.typarray JOIN pg_type ma ON ma.oid=m.typarray ORDER BY r.typname", []string{"typname", "multirange", "array_element", "multi_array_element"}, []string{"NAME", "NAME", "BOOL", "BOOL"}, ranges))
		// Direct OWNER of multiranges differs from range-owner recursion. No
		// privileged mutation of pinned bootstrap types is needed for this proof.
		want := fixture.ControlsReferenceDiagnostic("42809", "cannot alter multirange type int4multirange")
		want.Hint = "You can alter type int4range, which will alter the multirange type as well."
		typeLifecycleFailure(t, p, c, "ALTER TYPE pg_catalog.int4multirange OWNER TO CURRENT_USER", want)
		domainIdentityDDL(t, p, c, r, "DROP VIEW public.tl_view", "DROP MATERIALIZED VIEW public.tl_materialized", "DROP TABLE public.tl_row", "DROP EXTENSION earthdistance", "DROP EXTENSION cube", "DROP EXTENSION intarray", "DROP EXTENSION citext", "DROP TYPE public.tl_mood")
		typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='public'::regnamespace::oid AND typname IN ('tl_row','_tl_row','tl_view','_tl_view','tl_materialized','_tl_materialized','tl_mood','_tl_mood','cube','_cube','query_int','_query_int','citext','_citext','earth','_earth')", 0)
	})
}
