package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeMetadata(t *testing.T) {
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE cube_metadata(id integer NOT NULL,v cube,n cube NOT NULL)", 0))
		run(fixture.Exec("CREATE TABLE cube_vacant(v cube NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO cube_metadata VALUES (1,'(3,1),(1,4)','()'),(2,NULL,'-0')", 2))
		for _, tc := range []struct {
			q        string
			columns  []string
			nullable []bool
			rows     [][]any
		}{
			{"SELECT v,n FROM cube_metadata ORDER BY id", []string{"v", "n"}, []bool{true, false}, [][]any{{"(3, 1),(1, 4)", "()"}, {nil, "(-0)"}}},
			{"SELECT v,n FROM cube_metadata WHERE id=99", []string{"v", "n"}, []bool{true, false}, [][]any{}},
			{"SELECT v AS alias FROM cube_metadata WHERE id=2", []string{"alias"}, []bool{true}, [][]any{{nil}}},
			{"SELECT v FROM cube_vacant", []string{"v"}, []bool{false}, [][]any{}},
			{"SELECT NULL::cube AS v", []string{"v"}, nil, [][]any{{nil}}},
			{"SELECT '1'::cube AS v", []string{"v"}, nil, [][]any{{"(1)"}}},
		} {
			for _, prepared := range []bool{false, true} {
				s := cubeSelect(b, tc.q, tc.columns, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, tc.columns, tc.nullable) }
				run(s)
			}
		}
		q := "SELECT v,n FROM cube_metadata WHERE id=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			id   int
			rows [][]any
		}{{1, [][]any{{"(3, 1),(1, 4)", "()"}}}, {2, [][]any{{nil, "(-0)"}}}, {99, [][]any{}}} {
			s := cubeSelect(b, q, []string{"v", "n"}, tc.rows, tc.id)
			var retained []*sql.ColumnType
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				retained = cubeMetadata(t, b.PG, rows, s.Columns, []bool{true, false})
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			cubeColumnMetadata(t, b.PG, retained, s.Columns, []bool{true, false})
		}
		var collation any = int64(0)
		if b.PG {
			collation = "0"
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT t.typname,t.typtype,t.typlen,t.typbyval,t.typcategory,t.typispreferred,t.typcollation,t.typtypmod,t.typalign,t.typstorage FROM pg_type t WHERE t.oid=('cube'::regtype)::oid", []string{"typname", "typtype", "typlen", "typbyval", "typcategory", "typispreferred", "typcollation", "typtypmod", "typalign", "typstorage"}, []string{"NAME", "CHAR", "INT2", "BOOL", "CHAR", "BOOL", "OID", "INT4", "CHAR", "CHAR"}, [][]any{{"cube", "b", int64(-1), false, "U", false, collation, int64(-1), "d", "p"}}),
			fixture.Select("SELECT a.attname,a.atttypid=('cube'::regtype)::oid AS same_type,a.attnotnull FROM pg_attribute a WHERE a.attrelid='cube_metadata'::regclass::oid AND a.attnum>1 AND NOT a.attisdropped ORDER BY a.attnum", []string{"attname", "same_type", "attnotnull"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"v", true, false}, {"n", true, true}}),
			fixture.Select("SELECT a.typname,a.typelem=t.oid AS element_identity FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=('cube'::regtype)::oid", []string{"typname", "element_identity"}, []string{"NAME", "BOOL"}, [][]any{{"_cube", true}}),
			fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1247 AND objid=('cube[]'::regtype)::oid AND refclassid=1247 AND refobjid=('cube'::regtype)::oid", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}),
			fixture.Select("SELECT d.deptype,e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_type'::regclass::oid AND d.objid=('cube'::regtype)::oid AND d.refclassid='pg_extension'::regclass::oid", []string{"deptype", "extname"}, []string{"CHAR", "NAME"}, [][]any{{"e", "cube"}}),
			fixture.Select("SELECT p.proname,p.prorettype=('cube'::regtype)::oid AS returns_cube,p.proisstrict,p.provolatile,p.proparallel FROM pg_proc p WHERE p.oid IN ((SELECT typinput::oid FROM pg_type WHERE oid=('cube'::regtype)::oid),(SELECT typoutput::oid FROM pg_type WHERE oid=('cube'::regtype)::oid)) ORDER BY p.proname", []string{"proname", "returns_cube", "proisstrict", "provolatile", "proparallel"}, []string{"NAME", "BOOL", "BOOL", "CHAR", "CHAR"}, [][]any{{"cube_in", true, true, "i", "s"}, {"cube_out", false, true, "i", "s"}}),
			// Resolve exact argument identities through regprocedure rather than
			// requiring the deliberately bounded pg_proc projection to add proargtypes.
			fixture.Select("SELECT t.typinput::oid='public.cube_in(cstring)'::regprocedure::oid AS input_signature,t.typoutput::oid='public.cube_out(public.cube)'::regprocedure::oid AS output_signature,p.pronargs AS input_arguments,o.pronargs AS output_arguments,o.prorettype='cstring'::regtype::oid AS returns_cstring FROM pg_type t JOIN pg_proc p ON p.oid=t.typinput JOIN pg_proc o ON o.oid=t.typoutput WHERE t.oid=('cube'::regtype)::oid", []string{"input_signature", "output_signature", "input_arguments", "output_arguments", "returns_cstring"}, []string{"BOOL", "BOOL", "INT2", "INT2", "BOOL"}, [][]any{{true, true, int64(1), int64(1), true}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		// PostgreSQL 1.5 really installs binary handlers. Native deliberately
		// exposes only implemented text codecs, not phantom executable members.
		if b.PG {
			run(fixture.Select("SELECT r.proname AS receive,s.proname AS send FROM pg_type t JOIN pg_proc r ON r.oid=t.typreceive JOIN pg_proc s ON s.oid=t.typsend WHERE t.oid=('cube'::regtype)::oid", []string{"receive", "send"}, []string{"NAME", "NAME"}, [][]any{{"cube_recv", "cube_send"}}))
		} else {
			run(fixture.Select("SELECT typreceive::oid,typsend::oid FROM pg_type WHERE oid=('cube'::regtype)::oid", []string{"typreceive", "typsend"}, []string{"OID", "OID"}, [][]any{{int64(0), int64(0)}}))
			run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('cube_recv','cube_send')", []string{"proname"}, []string{"NAME"}, [][]any{}))
		}
	})
}
