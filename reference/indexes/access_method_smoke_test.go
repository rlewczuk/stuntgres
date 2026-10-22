package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent identity probes are repeated after replacement, never copied from
// source allocator output. The public SQL witness does not claim optimizer use;
// actual immutable root/support membership is a separate package-unit proof.
func amIndexIdentity(t *testing.T, ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, run func(fixture.Step), method, family, class, index string) {
	t.Helper()
	am := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_am WHERE amname=$1`, method)
	if am == 403 {
		t.Fatal("custom method lost SQL identity")
	}
	fam := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_opfamily WHERE opfname=$1 AND opfnamespace='public'::regnamespace AND opfmethod=$2::oid`, family, am)
	cl := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_opclass WHERE opcname=$1 AND opcnamespace='public'::regnamespace AND opcmethod=$2::oid`, class, am)
	idx := classRoundtripIdentity(t, ctx, db, `SELECT oid::bigint FROM pg_class WHERE relname=$1 AND relnamespace='public'::regnamespace`, index)
	run(fixture.Select(`SELECT relam::bigint FROM pg_class WHERE relname=$1 AND relnamespace='public'::regnamespace`, []string{"relam"}, []string{"INT8"}, [][]any{{am}}, index))
	run(fixture.Select(`SELECT opcmethod::bigint,opcfamily::bigint,opcdefault,opcintype::bigint FROM pg_opclass WHERE oid=$1::oid`, []string{"opcmethod", "opcfamily", "opcdefault", "opcintype"}, []string{"INT8", "INT8", "BOOL", "INT8"}, [][]any{{am, fam, true, int64(23)}}, cl))
	// The supported dependency authority proves the unique exact class link
	// without relying on the deliberately unprojected pg_index.indclass vector.
	run(fixture.Select(`SELECT refobjid::bigint,deptype,objsubid,refobjsubid FROM pg_depend WHERE classid='pg_class'::regclass AND objid=$1::oid AND refclassid='pg_opclass'::regclass`, []string{"refobjid", "deptype", "objsubid", "refobjsubid"}, []string{"INT8", "CHAR", "INT4", "INT4"}, [][]any{{cl, "n", int64(0), int64(0)}}, idx))
	rows := [][]any{}
	for i, op := range []int64{97, 523, 96, 525, 521} {
		rows = append(rows, []any{int64(i + 1), am, int64(23), int64(23), op})
	}
	run(fixture.Select(`SELECT amopstrategy,amopmethod::bigint,amoplefttype::bigint,amoprighttype::bigint,amopopr::bigint FROM pg_amop WHERE amopfamily=$1::oid ORDER BY amopstrategy`, []string{"amopstrategy", "amopmethod", "amoplefttype", "amoprighttype", "amopopr"}, []string{"INT2", "INT8", "INT8", "INT8", "INT8"}, rows, fam))
	run(fixture.Select(`SELECT amprocnum,amproc::oid::bigint FROM pg_amproc WHERE amprocfamily=$1::oid`, []string{"amprocnum", "amproc"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(351)}}, fam))
}

func TestReferenceAccessMethodBtreeSmoke(t *testing.T) {
	amBackends(t, []string{"am_smoke"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec(`CREATE ACCESS METHOD am_smoke TYPE INDEX HANDLER pg_catalog.bthandler`, 0))
		run(fixture.Exec(`CREATE TABLE am_smoke_rows(n integer)`, 0))
		run(fixture.Exec(`INSERT INTO am_smoke_rows VALUES(2),(1),(2),(NULL)`, 4))
		amError(t, p, b, conn, `CREATE INDEX am_smoke_i ON am_smoke_rows USING am_smoke(n)`, fixture.Diagnostic{Code: "42704", Message: `data type integer has no default operator class for access method "am_smoke"`, Hint: `You must specify an operator class for the index or define a default operator class for the data type.`})
		run(amCount(`SELECT count(*) FROM pg_class WHERE relname='am_smoke_i'`, 0))
		amError(t, p, b, conn, `CREATE INDEX am_smoke_i ON am_smoke_rows USING am_smoke(n pg_catalog.int4_ops)`, fixture.Diagnostic{Code: "42704", Message: `operator class "pg_catalog.int4_ops" does not exist for access method "am_smoke"`})
		run(amCount(`SELECT count(*) FROM pg_class WHERE relname='am_smoke_i'`, 0))
		run(fixture.Exec(`CREATE OPERATOR FAMILY public.am_smoke_family USING am_smoke`, 0))
		run(fixture.Prepared(fixture.Exec(amCreateClass("am_smoke", "am_smoke_family", "am_smoke_ops"), 0)))
		run(fixture.Prepared(fixture.Exec(`CREATE INDEX am_smoke_i ON am_smoke_rows USING am_smoke(n)`, 0)))
		const q = `SELECT n FROM am_smoke_rows ORDER BY n NULLS LAST`
		held := classHeld(t, p, conn, q)
		check := func(rows [][]any) {
			s := fixture.Select(q, []string{"n"}, []string{"INT4"}, rows)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			amIndexIdentity(t, p.Ctx, conn, run, "am_smoke", "am_smoke_family", "am_smoke_ops", "am_smoke_i")
		}
		check([][]any{{int64(1)}, {int64(2)}, {int64(2)}, {nil}})
		run(fixture.Exec(`INSERT INTO am_smoke_rows VALUES(0),(2),(NULL)`, 3))
		check([][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {nil}, {nil}})
		for _, s := range []fixture.Step{
			amCount(`SELECT count(*) FROM am_smoke_rows WHERE n=$1`, 3, 2),
			amCount(`SELECT count(*) FROM am_smoke_rows WHERE n IS NULL`, 2),
			fixture.Select(`SELECT n FROM am_smoke_rows WHERE n >= $1 AND n < $2 ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}, 0, 2),
			amProperties(amPropertySQL, [][]any{{true, true, true, true, true, nil}}, "am_smoke"),
			fixture.Select(`SELECT pg_index_has_property('am_smoke_i'::regclass,'index_scan') AS scan,pg_index_column_has_property('am_smoke_i'::regclass,1,'orderable') AS ordering`, []string{"scan", "ordering"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
		} {
			classRoutes(t, p, b, conn, s)
			h := classHeld(t, p, conn, s.Query)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
	})
}
