package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// int4mi is deliberately not a valid comparator across all INT4 values. Its
// real overflow distinguishes registered support execution from builtin fallback.
func TestReferenceOperatorClassComparisonConsumption(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		for _, tc := range []struct {
			name, fn string
			oid      int64
		}{{"safe", "btint4cmp", 351}, {"subtract", "int4mi", 181}} {
			run(fixture.Exec(`CREATE OPERATOR FAMILY public.class_compare_`+tc.name+` USING btree`, 0))
			run(fixture.Exec(classCreate("public.class_compare_"+tc.name, "public.class_compare_"+tc.name, tc.fn), 0))
			run(fixture.Select(`SELECT p.amproc::oid::bigint FROM pg_amproc p JOIN pg_opclass c ON c.opcfamily=p.amprocfamily WHERE c.opcname=$1 AND p.amprocnum=1`, []string{"amproc"}, []string{"INT8"}, [][]any{{tc.oid}}, "class_compare_"+tc.name))
			run(fixture.Exec(`CREATE TABLE class_compare_`+tc.name+`(id integer,n integer)`, 0))
			run(fixture.Exec(`INSERT INTO class_compare_`+tc.name+` VALUES(1,2),(2,1),(3,NULL),(4,2)`, 4))
			run(fixture.Exec(`CREATE INDEX class_compare_`+tc.name+`_idx ON class_compare_`+tc.name+`(n public.class_compare_`+tc.name+`)`, 0))
			q := `SELECT id FROM class_compare_` + tc.name + ` WHERE n >= $1 ORDER BY n,id`
			held := classHeld(t, p, conn, q)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}, 2))
			run(fixture.Exec(`INSERT INTO class_compare_`+tc.name+` VALUES(5,0),(6,3)`, 2))
			s := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(6)}}, 2)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		t.Run("overflow_build", func(t *testing.T) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			r(fixture.Exec(`CREATE TABLE class_compare_build(n integer)`, 0))
			r(fixture.Exec(`INSERT INTO class_compare_build VALUES(-2147483648),(2147483647)`, 2))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(`CREATE INDEX class_compare_failed ON class_compare_build(n public.class_compare_subtract)`, fixture.Diagnostic{Code: "22003", Message: `integer out of range`})
				s.Prepared, s.ErrorStage = prepared, "execute"
				r(s)
				r(fixture.Select(`SELECT relname FROM pg_class WHERE relname='class_compare_failed'`, []string{"relname"}, []string{"NAME"}, nil))
				r(fixture.Select(`SELECT n FROM class_compare_build ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(-2147483648)}, {int64(2147483647)}}))
			}
			r(fixture.Exec(`CREATE INDEX class_compare_built ON class_compare_build(n public.class_compare_safe)`, 0))
		})
		t.Run("overflow_insert", func(t *testing.T) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			r(fixture.Exec(`CREATE TABLE class_compare_insert(n integer)`, 0))
			r(fixture.Exec(`INSERT INTO class_compare_insert VALUES(-2147483648)`, 1))
			r(fixture.Exec(`CREATE INDEX class_compare_maintained ON class_compare_insert(n public.class_compare_subtract)`, 0))
			q := `SELECT n FROM class_compare_insert WHERE n=$1`
			held := classHeld(t, p, conn, q)
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(`INSERT INTO class_compare_insert VALUES(2147483647)`, fixture.Diagnostic{Code: "22003", Message: `integer out of range`})
				s.Prepared, s.ErrorStage = prepared, "execute"
				r(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(-2147483648)}}, int64(-2147483648)))
				r(fixture.Select(`SELECT n FROM class_compare_insert ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(-2147483648)}}))
			}
			r(fixture.Exec(`DROP INDEX class_compare_maintained`, 0))
			r(fixture.Exec(`CREATE INDEX class_compare_maintained ON class_compare_insert(n public.class_compare_safe)`, 0))
			r(fixture.Exec(`INSERT INTO class_compare_insert VALUES(2147483647)`, 1))
			r(fixture.Select(`SELECT n FROM class_compare_insert ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(-2147483648)}, {int64(2147483647)}}))
		})
	})
}
