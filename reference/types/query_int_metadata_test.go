package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryIntMetadata(t *testing.T) {
	queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		queryIntInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE qi_metadata(id integer NOT NULL,v query_int,n query_int NOT NULL)", 0))
		run(fixture.Exec("CREATE TABLE qi_vacant(v query_int NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO qi_metadata VALUES (1,'1&(2|3)','!1'),(2,NULL,'010')", 2))
		// Class IDs are stable catalog identities; object OIDs come from public
		// regtype lookup, including the non-storable automatic array identity.
		ownership := fixture.Select("SELECT deptype FROM pg_catalog.pg_depend WHERE classid=1247 AND objid=('query_int[]'::regtype)::oid AND refclassid=1247 AND refobjid=('query_int'::regtype)::oid", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}})
		run(ownership)
		run(fixture.Prepared(ownership))
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT v,n FROM qi_metadata ORDER BY id", [][]any{{"1 & ( 2 | 3 )", "!1"}, {nil, "8"}}},
			{"SELECT v,n FROM qi_metadata WHERE id=99", [][]any{}},
			{"SELECT v,n FROM qi_metadata WHERE id=2", [][]any{{nil, "8"}}},
		} {
			for _, prepared := range []bool{false, true} {
				s := queryIntSelect(b, tc.q, []string{"v", "n"}, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { queryIntMetadata(t, b.PG, rows, s.Columns, []bool{true, false}) }
				run(s)
			}
		}
		for _, tc := range []struct {
			q, name  string
			nullable []bool
			rows     [][]any
		}{
			{"SELECT v FROM qi_vacant", "v", []bool{false}, [][]any{}},
			{"SELECT NULL::query_int AS v", "v", nil, [][]any{{nil}}},
			{"SELECT '1'::query_int AS v", "v", nil, [][]any{{"1"}}},
			{"SELECT v AS alias FROM qi_metadata WHERE id=2", "alias", []bool{true}, [][]any{{nil}}},
		} {
			for _, prepared := range []bool{false, true} {
				s := queryIntSelect(b, tc.q, []string{tc.name}, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { queryIntMetadata(t, b.PG, rows, s.Columns, tc.nullable) }
				run(s)
			}
		}
		q := "SELECT v,n FROM qi_metadata WHERE id=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			id   int
			rows [][]any
		}{{1, [][]any{{"1 & ( 2 | 3 )", "!1"}}}, {2, [][]any{{nil, "8"}}}, {99, [][]any{}}} {
			s := queryIntSelect(b, q, []string{"v", "n"}, tc.rows, tc.id)
			var retained []*sql.ColumnType
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				retained = queryIntMetadata(t, b.PG, rows, s.Columns, []bool{true, false})
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			queryIntColumnMetadata(t, b.PG, retained, s.Columns, []bool{true, false})
		}
		q = "SELECT t.oid,t.typname,n.nspname,t.typtype,t.typlen,t.typbyval,t.typcategory,t.typispreferred,t.typcollation,t.typtypmod,t.typarray,t.typinput,t.typoutput FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.oid=('query_int'::regtype)::oid"
		var oid, length, collation, modifier, array int64
		var name, namespace, kind, category, input, output string
		var byValue, preferred bool
		if err := c.QueryRowContext(p.Ctx, q).Scan(&oid, &name, &namespace, &kind, &length, &byValue, &category, &preferred, &collation, &modifier, &array, &input, &output); err != nil {
			t.Fatal(err)
		}
		if oid <= 0 || array <= 0 || oid == array || name != "query_int" || namespace != "public" || kind != "b" || length != -1 || byValue || category != "U" || preferred || collation != 0 || modifier != -1 || input != "bqarr_in" || output != "bqarr_out" {
			t.Fatalf("query_int catalog oid=%d array=%d name=%q namespace=%q kind=%q length=%d byValue=%v category=%q preferred=%v collation=%d modifier=%d input=%q output=%q", oid, array, name, namespace, kind, length, byValue, category, preferred, collation, modifier, input, output)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a.attname,a.atttypid=('query_int'::regtype)::oid AS same_type,a.attnotnull FROM pg_attribute a WHERE a.attrelid='qi_metadata'::regclass::oid AND a.attnum>1 AND NOT a.attisdropped ORDER BY a.attnum", []string{"attname", "same_type", "attnotnull"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"v", true, false}, {"n", true, true}}),
			fixture.Select("SELECT a.typname,a.typelem=t.oid AS element_identity FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=('query_int'::regtype)::oid", []string{"typname", "element_identity"}, []string{"NAME", "BOOL"}, [][]any{{"_query_int", true}}),
			fixture.Select("SELECT d.deptype,e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_type'::regclass::oid AND d.objid=('query_int'::regtype)::oid AND d.refclassid='pg_extension'::regclass::oid", []string{"deptype", "extname"}, []string{"CHAR", "NAME"}, [][]any{{"e", "intarray"}}),
			fixture.Select("SELECT p.proname,p.prorettype=('query_int'::regtype)::oid AS returns_query,p.proisstrict,p.provolatile,p.proparallel FROM pg_proc p WHERE p.oid IN ((SELECT typinput::oid FROM pg_type WHERE oid=('query_int'::regtype)::oid),(SELECT typoutput::oid FROM pg_type WHERE oid=('query_int'::regtype)::oid)) ORDER BY p.proname", []string{"proname", "returns_query", "proisstrict", "provolatile", "proparallel"}, []string{"NAME", "BOOL", "BOOL", "CHAR", "CHAR"}, [][]any{{"bqarr_in", true, true, "i", "s"}, {"bqarr_out", false, true, "i", "s"}}),
		} {
			run(s)
		}
	})
}
