package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectACLFeatureSmoke(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE acl_obj_smoke(a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO acl_obj_smoke VALUES (7,9)", 1))
		run(fixture.Exec("CREATE TABLE acl_obj_smoke_data(acl ACLITEM[])", 0))
		run(fixture.Exec("CREATE VIEW acl_obj_smoke_view AS SELECT pg_get_acl(1259,'acl_obj_smoke'::regclass,1) AS acl", 0))
		untouched := func() {
			t.Helper()
			run(fixture.Select("SELECT pg_get_acl(1259,'acl_obj_smoke'::regclass,0) AS whole,pg_get_acl(1259,'acl_obj_smoke'::regclass,2) AS b", []string{"whole", "b"}, []string{"_ACLITEM", "_ACLITEM"}, [][]any{{nil, nil}}))
		}
		untouched()
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'acl_obj_smoke'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		q := "SELECT pg_get_acl($1,$2,$3) AS acl"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		checkHeld := func(value any) {
			t.Helper()
			s := objectACLSelect(q, value)
			s.Args = []any{int64(1259), oid, int64(1)}
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, "_ACLITEM") }
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		checkHeld(nil)
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_smoke TO PUBLIC", 0))
		checkHeld("{=r/" + objectACLOwner(b) + "}")
		s := fixture.Select("SELECT unnest(pg_get_acl(1259,'acl_obj_smoke'::regclass,1)) AS acl", []string{"acl"}, []string{"ACLITEM"}, [][]any{{"=r/" + objectACLOwner(b)}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, "ACLITEM") }
		run(s)
		untouched()
		run(fixture.Exec("INSERT INTO acl_obj_smoke_data SELECT pg_get_acl(1259,'acl_obj_smoke'::regclass,1)", 1))
		run(objectACLSelect("SELECT pg_get_acl(3456,0,0) AS acl", nil))
		run(objectACLSelect("SELECT pg_get_acl(NULL,NULL,NULL) AS acl", nil))
		run(fixture.Exec("REVOKE SELECT(a) ON acl_obj_smoke FROM PUBLIC", 0))
		checkHeld(nil)
		untouched()
		data := objectACLSelect("SELECT acl FROM acl_obj_smoke_data", "{=r/"+objectACLOwner(b)+"}")
		recipe := objectACLSelect("SELECT acl FROM acl_obj_smoke_view", nil)
		recipe.CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, "_ACLITEM") }
		run(data)
		run(recipe)
		// The held numeric OID is deliberately not reused after replacement.
		// The stored regclass recipe must resolve the restored relation instead.
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(data)
			run(recipe)
			untouched()
		}
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_smoke TO PUBLIC", 0))
		objectACLRoundtripControls(t, p, b, c, "acl_obj_smoke")
		run(data)
	})
}
