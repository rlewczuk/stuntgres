package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeConstructorCatalog(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cube_construct_catalog_ns")
	cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA cube_construct_catalog_ns", 0))
		run(fixture.Exec("CREATE EXTENSION cube SCHEMA cube_construct_catalog_ns", 0))
		const ns = "cube_construct_catalog_ns"
		// Use real regprocedure resolution for every formal identity; native's
		// deliberately bounded pg_proc need not grow a proargtypes projection.
		for _, tc := range []struct {
			name, arguments, result string
			nargs                   int64
		}{
			{"cube", "double precision", ns + ".cube", 1},
			{"cube", "double precision,double precision", ns + ".cube", 2},
			{"cube", "double precision[]", ns + ".cube", 1},
			{"cube", "double precision[],double precision[]", ns + ".cube", 2},
			{"cube", ns + ".cube,double precision", ns + ".cube", 2},
			{"cube", ns + ".cube,double precision,double precision", ns + ".cube", 3},
			{"cube_dim", ns + ".cube", "integer", 1},
			{"cube_is_point", ns + ".cube", "boolean", 1},
		} {
			signature := ns + "." + tc.name + "(" + tc.arguments + ")"
			q := "SELECT p.proname,p.pronargs,p.prorettype=" + cubeLiteral(tc.result) + "::regtype::oid AS result_identity,p.proisstrict,p.provolatile,p.proparallel,n.nspname,p.proowner=e.extowner AS same_owner,d.deptype FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.refclassid=3079 JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid=" + cubeLiteral(signature) + "::regprocedure::oid AND e.extname='cube'"
			s := fixture.Select(q, []string{"proname", "pronargs", "result_identity", "proisstrict", "provolatile", "proparallel", "nspname", "same_owner", "deptype"}, []string{"NAME", "INT2", "BOOL", "BOOL", "CHAR", "CHAR", "NAME", "BOOL", "CHAR"}, [][]any{{tc.name, tc.nargs, true, true, "i", "s", ns, true, "e"}})
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Select("SELECT t.typinput::oid='cube_construct_catalog_ns.cube_in(cstring)'::regprocedure::oid AS input_signature,t.typoutput::oid='cube_construct_catalog_ns.cube_out(cube_construct_catalog_ns.cube)'::regprocedure::oid AS output_signature,a.typelem=t.oid AS element_identity FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='cube_construct_catalog_ns.cube'::regtype::oid", []string{"input_signature", "output_signature", "element_identity"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		if !b.PG {
			// Pin the complete installed constructor/comparison/spatial/coordinate inventory.
			s := fixture.Select("SELECT p.proname,p.pronargs FROM pg_proc p WHERE p.pronamespace='cube_construct_catalog_ns'::regnamespace", []string{"proname", "pronargs"}, []string{"NAME", "INT2"}, [][]any{{"cube_in", int64(1)}, {"cube_out", int64(1)}, {"cube_eq", int64(2)}, {"cube_ne", int64(2)}, {"cube_lt", int64(2)}, {"cube_le", int64(2)}, {"cube_gt", int64(2)}, {"cube_ge", int64(2)}, {"cube_cmp", int64(2)}, {"cube", int64(1)}, {"cube", int64(2)}, {"cube", int64(1)}, {"cube", int64(2)}, {"cube", int64(2)}, {"cube", int64(3)}, {"cube_dim", int64(1)}, {"cube_is_point", int64(1)}, {"cube_overlap", int64(2)}, {"cube_contains", int64(2)}, {"cube_contained", int64(2)}, {"cube_distance", int64(2)}, {"distance_taxicab", int64(2)}, {"distance_chebyshev", int64(2)}, {"cube_ll_coord", int64(2)}, {"cube_ur_coord", int64(2)}, {"cube_coord", int64(2)}, {"cube_coord_llur", int64(2)}})
			s.Bag = true
			run(s)
			run(fixture.Select("SELECT typreceive::oid,typsend::oid FROM pg_type WHERE oid='cube_construct_catalog_ns.cube'::regtype::oid", []string{"typreceive", "typsend"}, []string{"OID", "OID"}, [][]any{{int64(0), int64(0)}}))
			operators := fixture.Select("SELECT oprname FROM pg_operator WHERE oprnamespace='cube_construct_catalog_ns'::regnamespace", []string{"oprname"}, []string{"NAME"}, [][]any{{"="}, {"<>"}, {"<"}, {"<="}, {">"}, {">="}, {"&&"}, {"@>"}, {"<@"}, {"<->"}, {"<#>"}, {"<=>"}, {"->"}, {"~>"}})
			operators.Bag = true
			run(operators)
		}
	})
}
