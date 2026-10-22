package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func familyBackends(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			body(t, p, backend)
		})
	}
}

func familyScenario(t *testing.T, body func(*testing.T, context.Context, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	familyBackends(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		body(t, p.Ctx, conn, func(s fixture.Step) {
			// lib/pq returns OID cells as text bytes; native returns int64.
			// Keep the released OID descriptor and assert each wire representation.
			if backend.PG {
				for _, row := range s.Rows {
					for i, typ := range s.Types {
						if typ == "OID" {
							if oid, ok := row[i].(int64); ok {
								row[i] = strconv.FormatInt(oid, 10)
							}
						}
					}
				}
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
		})
	})
}

const familyOperators = `SELECT a.amopstrategy,a.amoplefttype,a.amoprighttype,a.amopopr,
 a.amoppurpose,a.amopmethod,a.amopsortfamily
 FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily
 WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace
 ORDER BY a.amopstrategy`

func familyOperatorStep() fixture.Step {
	return fixture.Select(familyOperators,
		[]string{"amopstrategy", "amoplefttype", "amoprighttype", "amopopr", "amoppurpose", "amopmethod", "amopsortfamily"},
		[]string{"INT2", "OID", "OID", "OID", "CHAR", "OID", "OID"},
		[][]any{
			{int64(1), int64(23), int64(23), int64(97), "s", int64(403), int64(0)},
			{int64(2), int64(23), int64(23), int64(523), "s", int64(403), int64(0)},
			{int64(3), int64(23), int64(23), int64(96), "s", int64(403), int64(0)},
			{int64(4), int64(23), int64(23), int64(525), "s", int64(403), int64(0)},
			{int64(5), int64(23), int64(23), int64(521), "s", int64(403), int64(0)},
		})
}

const familyCreate = `CREATE OPERATOR FAMILY public.family_probe USING btree`
const familyPopulate = `ALTER OPERATOR FAMILY public.family_probe USING btree ADD
 OPERATOR 1 pg_catalog.<(integer,integer),
 OPERATOR 2 pg_catalog.<=(integer,integer),
 OPERATOR 3 pg_catalog.=(integer,integer),
 OPERATOR 4 pg_catalog.>=(integer,integer),
 OPERATOR 5 pg_catalog.>(integer,integer),
 FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`
const familySupport = `SELECT p.oid,p.amproclefttype,p.amprocrighttype,p.amprocnum,p.amproc
 FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily
 WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`

func TestReferenceOperatorFamilySmoke(t *testing.T) {
	familyScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(familyCreate, 0))
		run(fixture.Prepared(fixture.Exec(familyPopulate, 0)))
		run(familyOperatorStep())
		stmt, err := conn.PrepareContext(ctx, familySupport)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		read := func() int64 {
			rows, err := stmt.QueryContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			wantNames := []string{"oid", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"}
			wantTypes := []string{"OID", "OID", "OID", "INT2", "REGPROC"}
			if len(columns) != len(wantNames) {
				t.Fatalf("support columns: %v", columns)
			}
			for i, c := range columns {
				if c.Name() != wantNames[i] || c.DatabaseTypeName() != wantTypes[i] {
					t.Fatalf("column %d: %s %s", i, c.Name(), c.DatabaseTypeName())
				}
			}
			var oid int64
			if rows.Next() {
				var left, right, slot int64
				var proc string
				if err := rows.Scan(&oid, &left, &right, &slot, &proc); err != nil {
					t.Fatal(err)
				}
				if oid == 0 || left != 23 || right != 23 || slot != 1 || proc != "btint4cmp" {
					t.Fatalf("support: %d %d %d %d %q", oid, left, right, slot, proc)
				}
				if rows.Next() {
					t.Fatal("extra support row")
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return oid
		}
		old := read()
		if old == 0 {
			t.Fatal("missing initial support")
		}
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 1(integer)`, 0))
		if got := read(); got != 0 {
			t.Fatalf("removed support remains: %d", got)
		}
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
		if got := read(); got == 0 || got == old {
			t.Fatalf("recreated support identity: old=%d new=%d", old, got)
		}
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer)`, fixture.Diagnostic{
			Code: "42710", Message: `operator 1(integer,integer) already exists in operator family "public.family_probe"`,
		}))
		run(familyOperatorStep())
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, fixture.Diagnostic{
			Code: "42710", Message: `function 1(integer,integer) already exists in operator family "public.family_probe"`,
		}))
		run(fixture.Select(`SELECT p.amprocnum,p.amproc::oid FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"amprocnum", "amproc"}, []string{"INT2", "OID"}, [][]any{{int64(1), int64(351)}}))
		run(fixture.Select(`SELECT count(*) FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec(`CREATE TABLE family_probe_t(n integer)`, 0))
		run(fixture.Exec(`INSERT INTO family_probe_t VALUES(2),(1),(NULL),(2)`, 4))
		run(fixture.Exec(`CREATE INDEX family_probe_builtin_i ON family_probe_t USING btree(n)`, 0))
		run(fixture.Exec(`CREATE INDEX family_probe_builtin_h ON family_probe_t USING hash(n)`, 0))
		run(fixture.Select(`SELECT n FROM family_probe_t ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {nil}}))
		run(fixture.Exec(`DROP OPERATOR FAMILY public.family_probe USING btree`, 0))
		run(fixture.Exec(`INSERT INTO family_probe_t VALUES(0)`, 1))
		run(fixture.Exec(`DELETE FROM family_probe_t WHERE n=1`, 1))
		run(fixture.Select(`SELECT n FROM family_probe_t ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(2)}, {int64(2)}, {nil}}))
	})
}
