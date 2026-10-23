package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAlterProviders(t *testing.T) {
	t.Run("released scalar and window", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "ra_provider")
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE SCHEMA ra_provider", "CREATE VIEW ra_abs_view AS SELECT pg_catalog.abs(-4::integer) AS v")
			// pg_catalog is not reset by the fixture. Register each inverse as soon
			// as its forward operation succeeds, including on assertion failure.
			routineExec(t, p, b, c, "ALTER FUNCTION pg_catalog.abs(integer) RENAME TO ra_abs")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION pg_catalog.ra_abs(integer) RENAME TO abs"); err != nil {
					t.Error(err)
				}
			}()
			routineExec(t, p, b, c, "ALTER FUNCTION pg_catalog.ra_abs(integer) SET SCHEMA ra_provider")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION ra_provider.ra_abs(integer) SET SCHEMA pg_catalog"); err != nil {
					t.Error(err)
				}
			}()
			routineExec(t, p, b, c, "ALTER FUNCTION pg_catalog.row_number() RENAME TO ra_row_number")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION pg_catalog.ra_row_number() RENAME TO row_number"); err != nil {
					t.Error(err)
				}
			}()
			routineExec(t, p, b, c, "ALTER ROUTINE pg_catalog.ra_row_number() SET SCHEMA ra_provider")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION ra_provider.ra_row_number() SET SCHEMA pg_catalog"); err != nil {
					t.Error(err)
				}
			}()
			routineValue(t, p, b, c, "SELECT ra_provider.ra_abs(-4::integer) AS v", "v", "INT4", int64(4))
			routineValue(t, p, b, c, "SELECT pg_catalog.abs(-5::bigint) AS v", "v", "INT8", int64(5))
			routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM ra_abs_view", "v", "INT4", int64(4), true, true)
			routineAlterRows(t, p, b, c, "SELECT ra_provider.ra_row_number() OVER(ORDER BY x) AS v FROM(VALUES(2),(3)) s(x) ORDER BY x", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}})
			routineAlterError(t, p, b, c, "ALTER FUNCTION pg_catalog.abs(integer) RENAME TO absent", "42883", "function pg_catalog.abs(integer) does not exist")
			routineAlterError(t, p, b, c, "ALTER FUNCTION pg_catalog.row_number() RENAME TO absent", "42883", "function pg_catalog.row_number() does not exist")
		})
	})
	t.Run("owned aggregate and support references", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "ra_aggregate")
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE SCHEMA ra_aggregate", "CREATE FUNCTION ra_step(bigint,integer) RETURNS bigint RETURN $1+$2", "CREATE AGGREGATE ra_sum(integer) (SFUNC=ra_step,STYPE=bigint,INITCOND='0')", "CREATE VIEW ra_sum_view AS SELECT ra_sum(x) AS v FROM(VALUES(2),(3)) s(x)")
			oid := routineAlterOID(t, p, c, "ra_sum")
			routineExec(t, p, b, c, "ALTER FUNCTION ra_sum(integer) RENAME TO ra_sum_new", "ALTER ROUTINE ra_sum_new(integer) SET SCHEMA ra_aggregate", "ALTER FUNCTION ra_step(bigint,integer) RENAME TO ra_step_new")
			routineAlterSameOID(t, p, c, "ra_sum_new", oid)
			routineValueWithNativeNullability(t, p, b, c, "SELECT ra_aggregate.ra_sum_new(x) AS v FROM(VALUES(2),(3)) s(x)", "v", "INT8", int64(5), true, true)
			routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM ra_sum_view", "v", "INT8", int64(5), true, true)
		})
	})
	t.Run("installed scalar and aggregate members", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "ra_extension")
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS pgcrypto CASCADE; DROP EXTENSION IF EXISTS intagg CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			routineExec(t, p, b, c, "CREATE SCHEMA ra_extension", "CREATE EXTENSION pgcrypto SCHEMA public", "CREATE EXTENSION intagg SCHEMA public VERSION '1.1'",
				"CREATE VIEW ra_digest_view AS SELECT encode(digest('abc'::text,'sha256'::text),'hex') AS v")
			var digestOID, aggOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'digest(text,text)'::regprocedure::oid").Scan(&digestOID); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT 'int_array_aggregate(integer)'::regprocedure::oid").Scan(&aggOID); err != nil {
				t.Fatal(err)
			}
			routineExec(t, p, b, c, "ALTER FUNCTION digest(text,text) RENAME TO ra_digest", "ALTER ROUTINE ra_digest(text,text) SET SCHEMA ra_extension", "ALTER FUNCTION int_array_aggregate(integer) RENAME TO ra_collect", "ALTER FUNCTION ra_collect(integer) SET SCHEMA ra_extension")
			routineAlterSameOID(t, p, c, "ra_digest", digestOID)
			routineAlterSameOID(t, p, c, "ra_collect", aggOID)
			routineAlterRows(t, p, b, c, "SELECT encode(ra_extension.ra_digest('abc'::text,'sha256'::text),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}})
			routineAlterRows(t, p, b, c, "SELECT v FROM ra_digest_view", []string{"v"}, []string{"TEXT"}, [][]any{{"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}})
			routineAlterRows(t, p, b, c, "SELECT ra_extension.ra_collect(x ORDER BY x)::text AS v FROM(VALUES(2),(3)) s(x)", []string{"v"}, []string{"TEXT"}, [][]any{{"{2,3}"}})
			routineAlterRows(t, p, b, c, "SELECT e.extname,n.nspname FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_namespace n ON n.oid=e.extnamespace WHERE p.proname IN ('ra_digest','ra_collect') ORDER BY e.extname", []string{"extname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"intagg", "public"}, {"pgcrypto", "public"}})
			routineAlterRows(t, p, b, c, "SELECT encode(digest('abc'::bytea,'sha256'::text),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}})
		})
	})
	t.Run("installed cube routine identity", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_cube_owner"})
		fixture.SchemaReferenceCleanup(t, "ra_cube_source", "ra_cube_dest")
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS public.ra_cube_ordered CASCADE; DROP EXTENSION IF EXISTS cube CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			routineExec(t, p, b, c, "CREATE ROLE ra_cube_owner",
				"CREATE SCHEMA ra_cube_source AUTHORIZATION ra_cube_owner",
				"CREATE SCHEMA ra_cube_dest AUTHORIZATION ra_cube_owner",
				"CREATE EXTENSION cube SCHEMA ra_cube_source",
				"CREATE VIEW ra_cube_view AS SELECT ra_cube_source.cube_dim('(2,3)'::ra_cube_source.cube) AS dim,ra_cube_source.cube(2::double precision)::text AS point")
			routineExec(t, p, b, c, "CREATE VIEW ra_cube_spatial_view AS SELECT ra_cube_source.cube_overlap('(1)'::ra_cube_source.cube,'(1)'::ra_cube_source.cube) AS hit,ra_cube_source.cube_distance('(1)'::ra_cube_source.cube,'(2)'::ra_cube_source.cube) AS distance")
			routineExec(t, p, b, c, "CREATE VIEW ra_cube_spatial_operator_view AS SELECT '(1)'::ra_cube_source.cube OPERATOR(ra_cube_source.&&) '(1)'::ra_cube_source.cube AS hit,'(1)'::ra_cube_source.cube OPERATOR(ra_cube_source.<->) '(2)'::ra_cube_source.cube AS distance")
			routineExec(t, p, b, c,
				"SET search_path=ra_cube_source,public,pg_catalog",
				"CREATE TABLE public.ra_cube_ordered(id integer,v ra_cube_source.cube)",
				"CREATE INDEX ra_cube_ordered_idx ON public.ra_cube_ordered(v)",
				"CREATE VIEW public.ra_cube_compare_view AS SELECT ra_cube_source.cube_eq(v,'(2)'::ra_cube_source.cube) AS same,ra_cube_source.cube_cmp(v,'(2)'::ra_cube_source.cube) AS ordering,CASE v WHEN '(2)'::ra_cube_source.cube THEN 1 ELSE 0 END AS branch FROM public.ra_cube_ordered ORDER BY id",
				"CREATE VIEW public.ra_cube_compare_operator_view AS SELECT v OPERATOR(ra_cube_source.=) '(2)'::ra_cube_source.cube AS same,v OPERATOR(ra_cube_source.<) '(2)'::ra_cube_source.cube AS earlier FROM public.ra_cube_ordered ORDER BY id")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.ra_cube_ordered VALUES(1,'(2)'),(2,'(1)'),(3,'(2)')", 3))
			var equalOID, compareOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ra_cube_source.cube_eq(ra_cube_source.cube,ra_cube_source.cube)'::regprocedure::oid,'ra_cube_source.cube_cmp(ra_cube_source.cube,ra_cube_source.cube)'::regprocedure::oid").Scan(&equalOID, &compareOID); err != nil {
				t.Fatal(err)
			}
			checkComparison := func(conn *sql.Conn, backend fixture.Backend) {
				routineAlterRows(t, p, backend, conn, "SELECT same,ordering,branch FROM public.ra_cube_compare_view", []string{"same", "ordering", "branch"}, []string{"BOOL", "INT4", "INT4"}, [][]any{{true, int64(0), int64(1)}, {false, int64(-1), int64(0)}, {true, int64(0), int64(1)}})
				routineAlterRows(t, p, backend, conn, "SELECT same,earlier FROM public.ra_cube_compare_operator_view", []string{"same", "earlier"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {false, true}, {true, false}})
				routineAlterRows(t, p, backend, conn, "SELECT id FROM public.ra_cube_ordered ORDER BY v,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}})
				routineAlterRows(t, p, backend, conn, "SELECT v::text AS v,count(*) AS n FROM public.ra_cube_ordered GROUP BY v ORDER BY v", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"(1)", int64(1)}, {"(2)", int64(2)}})
			}
			checkComparison(c, b)
			var overlapOID, distanceOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ra_cube_source.cube_overlap(ra_cube_source.cube,ra_cube_source.cube)'::regprocedure::oid,'ra_cube_source.cube_distance(ra_cube_source.cube,ra_cube_source.cube)'::regprocedure::oid").Scan(&overlapOID, &distanceOID); err != nil {
				t.Fatal(err)
			}
			routineAlterRows(t, p, b, c, "SELECT hit,distance FROM ra_cube_spatial_view", []string{"hit", "distance"}, []string{"BOOL", "FLOAT8"}, [][]any{{true, float64(1)}})
			routineAlterRows(t, p, b, c, "SELECT hit,distance FROM ra_cube_spatial_operator_view", []string{"hit", "distance"}, []string{"BOOL", "FLOAT8"}, [][]any{{true, float64(1)}})
			var pointOID, dimOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ra_cube_source.cube(double precision)'::regprocedure::oid,'ra_cube_source.cube_dim(ra_cube_source.cube)'::regprocedure::oid").Scan(&pointOID, &dimOID); err != nil {
				t.Fatal(err)
			}
			routineAlterRows(t, p, b, c, "SELECT dim,point FROM ra_cube_view", []string{"dim", "point"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "(2)"}})
			routineExec(t, p, b, c,
				"ALTER FUNCTION ra_cube_source.cube(double precision) RENAME TO ra_cube_point",
				"ALTER ROUTINE ra_cube_source.ra_cube_point(double precision) SET SCHEMA ra_cube_dest",
				"ALTER FUNCTION ra_cube_dest.ra_cube_point(double precision) OWNER TO ra_cube_owner",
				"ALTER FUNCTION ra_cube_source.cube_dim(ra_cube_source.cube) RENAME TO ra_cube_dims",
				"ALTER ROUTINE ra_cube_source.ra_cube_dims(ra_cube_source.cube) SET SCHEMA ra_cube_dest",
				"ALTER FUNCTION ra_cube_dest.ra_cube_dims(ra_cube_source.cube) OWNER TO ra_cube_owner")
			routineExec(t, p, b, c,
				"ALTER FUNCTION ra_cube_source.cube_overlap(ra_cube_source.cube,ra_cube_source.cube) RENAME TO ra_cube_overlaps",
				"ALTER ROUTINE ra_cube_source.ra_cube_overlaps(ra_cube_source.cube,ra_cube_source.cube) SET SCHEMA ra_cube_dest",
				"ALTER FUNCTION ra_cube_dest.ra_cube_overlaps(ra_cube_source.cube,ra_cube_source.cube) OWNER TO ra_cube_owner",
				"ALTER FUNCTION ra_cube_source.cube_distance(ra_cube_source.cube,ra_cube_source.cube) RENAME TO ra_cube_distance",
				"ALTER ROUTINE ra_cube_source.ra_cube_distance(ra_cube_source.cube,ra_cube_source.cube) SET SCHEMA ra_cube_dest",
				"ALTER FUNCTION ra_cube_dest.ra_cube_distance(ra_cube_source.cube,ra_cube_source.cube) OWNER TO ra_cube_owner")
			routineExec(t, p, b, c,
				"ALTER FUNCTION ra_cube_source.cube_eq(ra_cube_source.cube,ra_cube_source.cube) RENAME TO ra_cube_equal",
				"ALTER ROUTINE ra_cube_source.ra_cube_equal(ra_cube_source.cube,ra_cube_source.cube) SET SCHEMA ra_cube_dest",
				"ALTER FUNCTION ra_cube_dest.ra_cube_equal(ra_cube_source.cube,ra_cube_source.cube) OWNER TO ra_cube_owner",
				"ALTER FUNCTION ra_cube_source.cube_cmp(ra_cube_source.cube,ra_cube_source.cube) RENAME TO ra_cube_compare",
				"ALTER ROUTINE ra_cube_source.ra_cube_compare(ra_cube_source.cube,ra_cube_source.cube) SET SCHEMA ra_cube_dest",
				"ALTER FUNCTION ra_cube_dest.ra_cube_compare(ra_cube_source.cube,ra_cube_source.cube) OWNER TO ra_cube_owner")
			routineAlterSameOID(t, p, c, "ra_cube_point", pointOID)
			routineAlterSameOID(t, p, c, "ra_cube_dims", dimOID)
			routineAlterSameOID(t, p, c, "ra_cube_overlaps", overlapOID)
			routineAlterSameOID(t, p, c, "ra_cube_distance", distanceOID)
			routineAlterSameOID(t, p, c, "ra_cube_equal", equalOID)
			routineAlterSameOID(t, p, c, "ra_cube_compare", compareOID)
			check := func(conn *sql.Conn, backend fixture.Backend) {
				routineAlterRows(t, p, backend, conn, "SELECT dim,point FROM ra_cube_view", []string{"dim", "point"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "(2)"}})
				routineAlterRows(t, p, backend, conn, "SELECT ra_cube_dest.ra_cube_dims('(2,3)'::ra_cube_source.cube) AS dim,ra_cube_dest.ra_cube_point(2::double precision)::text AS point", []string{"dim", "point"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "(2)"}})
				routineAlterRows(t, p, backend, conn, "SELECT ra_cube_dest.ra_cube_dims(ra_cube_source.cube(1::double precision,4::double precision)) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
				routineAlterRows(t, p, backend, conn, "SELECT hit,distance FROM ra_cube_spatial_view", []string{"hit", "distance"}, []string{"BOOL", "FLOAT8"}, [][]any{{true, float64(1)}})
				routineAlterRows(t, p, backend, conn, "SELECT hit,distance FROM ra_cube_spatial_operator_view", []string{"hit", "distance"}, []string{"BOOL", "FLOAT8"}, [][]any{{true, float64(1)}})
				routineAlterRows(t, p, backend, conn, "SELECT ra_cube_dest.ra_cube_overlaps('(1)'::ra_cube_source.cube,'(1)'::ra_cube_source.cube) AS hit,ra_cube_dest.ra_cube_distance('(1)'::ra_cube_source.cube,'(2)'::ra_cube_source.cube) AS distance", []string{"hit", "distance"}, []string{"BOOL", "FLOAT8"}, [][]any{{true, float64(1)}})
				routineAlterRows(t, p, backend, conn, "SELECT '(1)'::ra_cube_source.cube OPERATOR(ra_cube_source.&&) '(1)'::ra_cube_source.cube AS hit,'(1)'::ra_cube_source.cube OPERATOR(ra_cube_source.<->) '(2)'::ra_cube_source.cube AS distance", []string{"hit", "distance"}, []string{"BOOL", "FLOAT8"}, [][]any{{true, float64(1)}})
				checkComparison(conn, backend)
				routineAlterRows(t, p, backend, conn, "SELECT ra_cube_dest.ra_cube_equal('(1)'::ra_cube_source.cube,'(1)'::ra_cube_source.cube) AS same,ra_cube_dest.ra_cube_compare('(1)'::ra_cube_source.cube,'(2)'::ra_cube_source.cube) AS ordering", []string{"same", "ordering"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(-1)}})
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO public.ra_cube_ordered VALUES(4,'(0)')", 1))
				routineAlterRows(t, p, backend, conn, "SELECT id FROM public.ra_cube_ordered ORDER BY v,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(2)}, {int64(1)}, {int64(3)}})
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DELETE FROM public.ra_cube_ordered WHERE id=4", 1))
				routineAlterRows(t, p, backend, conn, "SELECT proname,proowner='ra_cube_owner'::regrole AS owned FROM pg_proc WHERE proname IN ('ra_cube_compare','ra_cube_dims','ra_cube_distance','ra_cube_equal','ra_cube_overlaps','ra_cube_point') ORDER BY proname", []string{"proname", "owned"}, []string{"NAME", "BOOL"}, [][]any{{"ra_cube_compare", true}, {"ra_cube_dims", true}, {"ra_cube_distance", true}, {"ra_cube_equal", true}, {"ra_cube_overlaps", true}, {"ra_cube_point", true}})
			}
			check(c, b)
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(c, b)
			if p.Postgres != nil {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				defer func() {
					if _, err := pg.ExecContext(p.Ctx, "DROP TABLE IF EXISTS public.ra_cube_ordered CASCADE; DROP EXTENSION IF EXISTS cube CASCADE; DROP SCHEMA IF EXISTS ra_cube_source CASCADE; DROP SCHEMA IF EXISTS ra_cube_dest CASCADE; DROP ROLE IF EXISTS ra_cube_owner"); err != nil {
						t.Error(err)
					}
				}()
				if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE ra_cube_owner"); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual renamed Cube member replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			}
		})
	})
	t.Run("existing procedural declaration identity", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_pl_owner"})
		fixture.SchemaReferenceCleanup(t, "ra_pl_source", "ra_pl_dest")
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE ROLE ra_pl_owner",
				"CREATE SCHEMA ra_pl_source AUTHORIZATION ra_pl_owner",
				"CREATE SCHEMA ra_pl_dest AUTHORIZATION ra_pl_owner",
				"CREATE FUNCTION ra_pl_source.ra_pl_f(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer := x; BEGIN RETURN n+10; END$$",
				"CREATE FUNCTION ra_pl_source.ra_pl_f(text) RETURNS integer RETURN length($1)",
				"CREATE VIEW public.ra_pl_saved AS SELECT ra_pl_source.ra_pl_f(1) AS v")
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ra_pl_source.ra_pl_f(integer)'::regprocedure::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			routineAlterRows(t, p, b, c, "SELECT v FROM public.ra_pl_saved", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}})
			routineExec(t, p, b, c,
				"ALTER FUNCTION ra_pl_source.ra_pl_f(integer) RENAME TO ra_pl_changed",
				"ALTER ROUTINE ra_pl_source.ra_pl_changed(integer) SET SCHEMA ra_pl_dest",
				"ALTER FUNCTION ra_pl_dest.ra_pl_changed(integer) OWNER TO ra_pl_owner")
			routineAlterSameOID(t, p, c, "ra_pl_changed", oid)
			check := func(conn *sql.Conn, backend fixture.Backend) {
				routineAlterRows(t, p, backend, conn, "SELECT v FROM public.ra_pl_saved", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}})
				routineAlterRows(t, p, backend, conn, "SELECT ra_pl_dest.ra_pl_changed(2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}})
				routineAlterRows(t, p, backend, conn, "SELECT ra_pl_source.ra_pl_f('abc'::text) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}})
				routineAlterRows(t, p, backend, conn, "SELECT proowner='ra_pl_owner'::regrole AS owned FROM pg_proc WHERE proname='ra_pl_changed'", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
				if backend.PG {
					// Native pg_proc's bounded surface does not expose prolang.
					routineAlterRows(t, p, backend, conn, "SELECT l.lanname FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.proname='ra_pl_changed'", []string{"lanname"}, []string{"NAME"}, [][]any{{"plpgsql"}})
				}
			}
			check(c, b)
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				check(c, b)
			}
			if p.Postgres != nil {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				defer func() {
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS ra_pl_source CASCADE; DROP SCHEMA IF EXISTS ra_pl_dest CASCADE; DROP ROLE IF EXISTS ra_pl_owner"); err != nil {
						t.Error(err)
					}
				}()
				if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE ra_pl_owner"); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual renamed procedural declaration replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			}
		})
	})
}
