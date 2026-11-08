package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceBtreeArrayDefault pins the released PostgreSQL 18.6 default
// btree array_ops class: every array key builds with the implicit class,
// orders and compares through the element comparator, and exposes the released
// catalog identity. The engine has no btree scan path, so ORDER BY and
// predicate rows witness the comparator while the catalog, UNIQUE and REINDEX
// witnesses prove the index itself.
func TestReferenceBtreeArrayDefault(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE grin_arr_t(id integer, targets uuid[])", 0))
		const create = "CREATE INDEX grin_arr_idx ON grin_arr_t(targets)"
		heldCreate := classHeld(t, p, conn, create)
		checkIndex := func() {
			t.Helper()
			run(fixture.Select("SELECT relkind FROM pg_class WHERE relname='grin_arr_idx'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}))
			run(fixture.Select("SELECT indclass::text FROM pg_index WHERE indexrelid='grin_arr_idx'::regclass", []string{"indclass"}, []string{"TEXT"}, [][]any{{"10000"}}))
		}
		run(fixture.Exec(create, 0))
		checkIndex()
		run(fixture.Exec("DROP INDEX grin_arr_idx", 0))
		run(fixture.Prepared(fixture.Exec(create, 0)))
		checkIndex()
		run(fixture.Exec("DROP INDEX grin_arr_idx", 0))
		fixture.RunStep(t, p.Ctx, heldCreate, b.Recorder, fixture.Exec(create, 0))
		checkIndex()

		run(fixture.Exec(`INSERT INTO grin_arr_t VALUES
			(1,'{}'),
			(2,'{00000000-0000-0000-0000-000000000001}'),
			(3,'{00000000-0000-0000-0000-000000000001,00000000-0000-0000-0000-000000000003}'),
			(4,'{00000000-0000-0000-0000-000000000001,NULL}'),
			(5,'{00000000-0000-0000-0000-000000000002}'),
			(6,'{ffffffff-ffff-ffff-ffff-ffffffffffff}'),
			(7,NULL)`, 7))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_t ORDER BY targets", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}}))
		// DESC defaults to NULLS FIRST, so the order is the exact reverse of
		// the ASC witness including SQL NULL.
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_t ORDER BY targets DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(6)}, {int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)}}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_t WHERE targets = ARRAY['00000000-0000-0000-0000-000000000001'::uuid]", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_t WHERE targets > ARRAY['00000000-0000-0000-0000-000000000002'::uuid]", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))

		t.Run("Metadata", func(t *testing.T) {
			run(fixture.Select("SELECT pg_get_indexdef('grin_arr_idx'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX grin_arr_idx ON public.grin_arr_t USING btree (targets)"}}))
			run(fixture.Select("SELECT oid::bigint,opcmethod::bigint,opcname,opcnamespace::bigint,opcowner::bigint,opcfamily::bigint,opcintype::bigint,opcdefault,opckeytype::bigint FROM pg_opclass WHERE oid=10000", []string{"oid", "opcmethod", "opcname", "opcnamespace", "opcowner", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"INT8", "INT8", "NAME", "INT8", "INT8", "INT8", "INT8", "BOOL", "INT8"}, [][]any{{int64(10000), int64(403), "array_ops", int64(11), int64(10), int64(397), int64(2277), true, int64(0)}}))
			run(fixture.Select("SELECT oid::bigint,amprocnum,amproclefttype::bigint,amprocrighttype::bigint,amproc::oid::bigint FROM pg_amproc WHERE amprocfamily=397 ORDER BY amprocnum", []string{"oid", "amprocnum", "amproclefttype", "amprocrighttype", "amproc"}, []string{"INT8", "INT2", "INT8", "INT8", "INT8"}, [][]any{{int64(10000), int64(1), int64(2277), int64(2277), int64(382)}}))
			run(fixture.Select("SELECT amopstrategy,amopopr::oid::bigint FROM pg_amop WHERE amopfamily=397 ORDER BY amopstrategy", []string{"amopstrategy", "amopopr"}, []string{"INT2", "INT8"}, [][]any{
				{int64(1), int64(1072)},
				{int64(2), int64(1074)},
				{int64(3), int64(1070)},
				{int64(4), int64(1075)},
				{int64(5), int64(1073)},
			}))
		})

		run(fixture.Exec("CREATE TABLE grin_arr_multi(user_id integer, template_id integer, targets uuid[])", 0))
		run(fixture.Exec("CREATE INDEX grin_arr_multi_idx ON grin_arr_multi(user_id, template_id, targets)", 0))
		run(fixture.Select("SELECT indclass::text FROM pg_index WHERE indexrelid='grin_arr_multi_idx'::regclass", []string{"indclass"}, []string{"TEXT"}, [][]any{{"1978 1978 10000"}}))
		run(fixture.Select("SELECT pg_get_indexdef('grin_arr_multi_idx'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX grin_arr_multi_idx ON public.grin_arr_multi USING btree (user_id, template_id, targets)"}}))

		run(fixture.Exec("CREATE DOMAIN grin_payload AS name[]", 0))
		run(fixture.Exec("CREATE TABLE grin_arr_domain(id integer, v grin_payload)", 0))
		run(fixture.Exec("CREATE INDEX grin_arr_domain_idx ON grin_arr_domain(v)", 0))
		run(fixture.Select("SELECT indclass::text FROM pg_index WHERE indexrelid='grin_arr_domain_idx'::regclass", []string{"indclass"}, []string{"TEXT"}, [][]any{{"10000"}}))
		run(fixture.Exec(`INSERT INTO grin_arr_domain VALUES(1,'{alpha,NULL}'),(2,'{beta}')`, 2))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM grin_arr_domain ORDER BY v", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))

		run(fixture.Exec("CREATE TABLE grin_arr_unique(v text[])", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX uqarr_v ON grin_arr_unique(v)", 0))
		run(fixture.Exec("INSERT INTO grin_arr_unique VALUES('{x}')", 1))
		duplicate := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "uqarr_v"`)
		duplicate.Detail = "Key (v)=({x}) already exists."
		duplicate.Schema, duplicate.Table, duplicate.Constraint = "public", "grin_arr_unique", "uqarr_v"
		run(fixture.ErrorDiagnostic("INSERT INTO grin_arr_unique VALUES('{x}')", duplicate))
		run(fixture.Exec("REINDEX INDEX uqarr_v", 0))
		run(fixture.Select("SELECT v FROM grin_arr_unique ORDER BY v", []string{"v"}, []string{"_TEXT"}, [][]any{{"{x}"}}))

		// json has no btree element comparator. Class resolution and index
		// build succeed on the empty table and the first entry needs no
		// comparison; the demand is raised when a second entry compares
		// against it, so the whole multi-row statement rolls back.
		run(fixture.Exec("CREATE TABLE grin_arr_json(id integer, pts json[])", 0))
		run(fixture.Exec("CREATE INDEX grin_arr_json_idx ON grin_arr_json(pts)", 0))
		run(fixture.Select("SELECT indclass::text FROM pg_index WHERE indexrelid='grin_arr_json_idx'::regclass", []string{"indclass"}, []string{"TEXT"}, [][]any{{"10000"}}))
		missing := fixture.ControlsReferenceDiagnostic("42883", "could not identify a comparison function for type json")
		const insertJSON = `INSERT INTO grin_arr_json VALUES(1,ARRAY['1'::json]),(2,ARRAY['2'::json])`
		run(fixture.ErrorDiagnostic(insertJSON, missing))
		run(fixture.Prepared(fixture.ErrorDiagnostic(insertJSON, missing)))
		run(fixture.Select("SELECT count(*) FROM grin_arr_json", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}