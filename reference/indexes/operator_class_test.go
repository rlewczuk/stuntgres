package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Native runs first so Roundtrip replays actual emitted bytes on PostgreSQL.
func classBackends(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func classConn(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, func(fixture.Step)) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
}

const classItems = `OPERATOR 1 pg_catalog.<, OPERATOR 2 pg_catalog.<=, OPERATOR 3 pg_catalog.=, OPERATOR 4 pg_catalog.>=, OPERATOR 5 pg_catalog.>, FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`

func classCreate(name, family, comparator string) string {
	return `CREATE OPERATOR CLASS ` + name + ` FOR TYPE integer USING btree FAMILY ` + family + ` AS OPERATOR 1 pg_catalog.<, OPERATOR 2 pg_catalog.<=, OPERATOR 3 pg_catalog.=, OPERATOR 4 pg_catalog.>=, OPERATOR 5 pg_catalog.>, FUNCTION 1 pg_catalog.` + comparator + `(integer,integer)`
}

func classHeld(t *testing.T, p *fixture.Pair, conn *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("quiet Prepare", err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
}

func classRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
}

func TestReferenceOperatorClassFeatureSmoke(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec(`CREATE OPERATOR FAMILY public.class_smoke_family USING btree`, 0))
		q := classCreate("public.class_smoke", "public.class_smoke_family", "btint4cmp")
		heldDDL := classHeld(t, p, conn, q)
		run(fixture.Select(`SELECT count(*) FROM pg_opclass WHERE opcname='class_smoke'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, heldDDL, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Exec(`CREATE TABLE class_smoke_rows(id integer,n integer)`, 0))
		run(fixture.Exec(`INSERT INTO class_smoke_rows VALUES(1,2),(2,1),(3,NULL),(4,2)`, 4))
		run(fixture.Prepared(fixture.Exec(`CREATE INDEX class_smoke_index ON class_smoke_rows(n public.class_smoke)`, 0)))
		metadata := fixture.Select(`SELECT c.opcmethod::bigint,c.opcintype::bigint,c.opcdefault,c.opckeytype::bigint,c.opcfamily=f.oid,c.opcnamespace='public'::regnamespace,c.opcowner=f.opfowner FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcname='class_smoke' AND f.opfname='class_smoke_family'`, []string{"opcmethod", "opcintype", "opcdefault", "opckeytype", "?column?", "?column?", "?column?"}, []string{"INT8", "INT8", "BOOL", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(403), int64(23), false, int64(0), true, true, true}})
		classRoutes(t, p, b, conn, metadata)
		run(fixture.Select(`SELECT a.amopstrategy,a.amoplefttype::bigint,a.amoprighttype::bigint,a.amopopr::bigint,a.amoppurpose,a.amopmethod::bigint,a.amopsortfamily::bigint FROM pg_amop a JOIN pg_opclass c ON c.opcfamily=a.amopfamily WHERE c.opcname='class_smoke' ORDER BY a.amopstrategy`, []string{"amopstrategy", "amoplefttype", "amoprighttype", "amopopr", "amoppurpose", "amopmethod", "amopsortfamily"}, []string{"INT2", "INT8", "INT8", "INT8", "CHAR", "INT8", "INT8"}, [][]any{{int64(1), int64(23), int64(23), int64(97), "s", int64(403), int64(0)}, {int64(2), int64(23), int64(23), int64(523), "s", int64(403), int64(0)}, {int64(3), int64(23), int64(23), int64(96), "s", int64(403), int64(0)}, {int64(4), int64(23), int64(23), int64(525), "s", int64(403), int64(0)}, {int64(5), int64(23), int64(23), int64(521), "s", int64(403), int64(0)}}))
		run(fixture.Select(`SELECT a.amprocnum,a.amproclefttype::bigint,a.amprocrighttype::bigint,a.amproc::oid::bigint FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE c.opcname='class_smoke'`, []string{"amprocnum", "amproclefttype", "amprocrighttype", "amproc"}, []string{"INT2", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(23), int64(23), int64(351)}}))
		const selectSQL = `SELECT id,n FROM class_smoke_rows WHERE n >= $1 ORDER BY n,id`
		held := classHeld(t, p, conn, selectSQL)
		check := func(rows [][]any) {
			s := fixture.Select(selectSQL, []string{"id", "n"}, []string{"INT4", "INT4"}, rows, 2)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Select(`SELECT id,n FROM class_smoke_rows WHERE n >= 2 ORDER BY n,id`, []string{"id", "n"}, []string{"INT4", "INT4"}, rows))
		}
		check([][]any{{int64(1), int64(2)}, {int64(4), int64(2)}})
		run(fixture.Exec(`INSERT INTO class_smoke_rows VALUES(5,0),(6,3)`, 2))
		check([][]any{{int64(1), int64(2)}, {int64(4), int64(2)}, {int64(6), int64(3)}})
		run(fixture.Select(`SELECT id FROM class_smoke_rows ORDER BY n NULLS LAST,id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(2)}, {int64(1)}, {int64(4)}, {int64(6)}, {int64(3)}}))
	})
}
