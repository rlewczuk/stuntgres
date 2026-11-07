package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnGeneratedSkipAndReadd(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(g.t, g.p, g.b.PG, &notices)
		n := *g
		n.c = c
		n.b.Recorder = rec
		n.exec("CREATE TABLE g(a integer)", 0)
		n.exec("INSERT INTO g VALUES(2)", 1)
		n.exec("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a+1) STORED", 0)
		var oid, attr int64
		if err := c.QueryRowContext(g.p.Ctx, "SELECT oid,adnum FROM pg_attrdef WHERE adrelid='g'::regclass").Scan(&oid, &attr); err != nil || oid <= 0 || attr != 2 {
			g.t.Fatal(oid, attr, err)
		}
		for _, def := range []string{"b nonexistent_type GENERATED ALWAYS AS(missing+1) STORED", "b integer GENERATED ALWAYS AS(random()) VIRTUAL", "b integer GENERATED ALWAYS AS(a/0) STORED", "b integer DEFAULT 1 GENERATED ALWAYS AS(a+1) STORED"} {
			notices = nil
			n.exec("ALTER TABLE g ADD COLUMN IF NOT EXISTS "+def, 0)
			n.notices(notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "b" of relation "g" already exists, skipping`}})
			n.run(fixture.Select("SELECT * FROM g", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
			n.run(fixture.Select("SELECT oid=$1::oid AS same_oid,adnum=$2::smallint AS same_attr,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='g'::regclass", []string{"same_oid", "same_attr", "pg_get_expr"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{true, true, "(a + 1)"}}, oid, attr))
			n.kinds("g", []any{"b", "s"})
		}
		q := "/* é */ ALTER TABLE g ADD IF NOT EXISTS b integer GENERATED ALWAYS AS(a+) STORED"
		d := addGeneratedDiagnostic("42601", `syntax error at or near ")"`)
		d.Position = addGeneratedPosition(q, ") STORED")
		stage := "execute"
		if g.prepared {
			stage = "prepare"
		}
		n.fail(q, d, false, stage)
		n.exec("ALTER TABLE g DROP b", 0)
		n.exec("ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a+2) STORED", 0)
		n.run(fixture.Select("SELECT oid<>$1::oid AS fresh_oid,oid>0 AS valid_oid,adnum>$2::smallint AS fresh_attr,pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='g'::regclass", []string{"fresh_oid", "valid_oid", "fresh_attr", "pg_get_expr"}, []string{"BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, true, "(a + 2)"}}, oid, attr))
		n.run(fixture.Select("SELECT oid FROM pg_attrdef WHERE oid=$1::oid", []string{"oid"}, []string{"OID"}, nil, oid))
		n.run(fixture.Select("SELECT objid FROM pg_depend WHERE classid='pg_attrdef'::regclass AND objid=$1::oid", []string{"objid"}, []string{"OID"}, nil, oid))
		n.run(fixture.Select("SELECT * FROM g", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}}))
		n.posts()
		n.exec("ALTER TABLE posts DROP search", 0)
		n.exec("ALTER TABLE posts ADD COLUMN search tsvector GENERATED ALWAYS AS("+addGeneratedSearch+") STORED", 0)
		n.postsRows(false)
		n.gin()
	})
}
