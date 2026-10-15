package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectACLSignatures(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE acl_obj_signature(a INT)", 0))
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_signature TO PUBLIC", 0))
		for _, expression := range []string{
			"pg_get_acl(1259::oid,'acl_obj_signature'::regclass,1::integer)",
			"pg_catalog.pg_get_acl(1259,'acl_obj_signature'::regclass,1)",
			`"pg_get_acl"('1259','acl_obj_signature'::regclass,'1')`,
			"pg_get_acl(objsubid => 1, objid => 'acl_obj_signature'::regclass, classid => 1259)",
			"pg_get_acl(1259, objsubid => 1, objid => 'acl_obj_signature'::regclass)",
			"pg_get_acl(objid := 'acl_obj_signature'::regclass, classid := 1259, objsubid := 1)",
			"pg_get_acl(1259,'acl_obj_signature'::regclass,VARIADIC 1)",
		} {
			t.Run(expression, func(t *testing.T) {
				objectACLRun(t, p, b, c, objectACLSelect("SELECT "+expression+" AS acl", "{=r/"+objectACLOwner(b)+"}"))
			})
		}
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'acl_obj_signature'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		s := objectACLSelect("SELECT pg_get_acl($1,$2,$3) AS acl", "{=r/"+objectACLOwner(b)+"}")
		s.Args = []any{int64(1259), oid, int64(1)}
		run(s)
		s = objectACLSelect("SELECT pg_get_acl(objsubid => $1, classid => $2, objid => $3) AS acl", "{=r/"+objectACLOwner(b)+"}")
		s.Args = []any{int64(1), int64(1259), oid}
		run(s)
		// Scalar composition, joins, grouping and stored recipes all share the
		// typed expression path; no aclexplode/acldefault implementation is needed.
		run(fixture.Select("SELECT (pg_get_acl(1259,'acl_obj_signature'::regclass,1))[1] AS acl", []string{"acl"}, []string{"ACLITEM"}, [][]any{{"=r/" + objectACLOwner(b)}}))
		run(objectACLSelect("SELECT CASE WHEN TRUE THEN pg_get_acl(1259,'acl_obj_signature'::regclass,1) ELSE NULL END AS acl", "{=r/"+objectACLOwner(b)+"}"))
		run(objectACLSelect("SELECT COALESCE(pg_get_acl(1259,0,0),pg_get_acl(1259,'acl_obj_signature'::regclass,1)) AS acl", "{=r/"+objectACLOwner(b)+"}"))
		run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_signature'::regclass,v) AS acl FROM (VALUES (1),(1)) AS x(v) GROUP BY v", "{=r/"+objectACLOwner(b)+"}"))
		run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_signature'::regclass,x.v) AS acl FROM (VALUES (1)) AS x(v) JOIN (VALUES (1)) AS y(v) ON x.v=y.v", "{=r/"+objectACLOwner(b)+"}"))
		run(fixture.Select("SELECT count(pg_get_acl(1259,'acl_obj_signature'::regclass,v)) AS n FROM (VALUES (1),(2)) AS x(v)", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT pg_get_acl(1259,'acl_obj_signature'::regclass,v) AS acl,row_number() OVER (ORDER BY v) AS n FROM (VALUES (1),(2)) AS x(v) ORDER BY v", []string{"acl", "n"}, []string{"_ACLITEM", "INT8"}, [][]any{{"{=r/" + objectACLOwner(b) + "}", int64(1)}, {nil, int64(2)}}))
		run(fixture.Exec("CREATE VIEW acl_obj_signature_view AS SELECT pg_get_acl(1259,'acl_obj_signature'::regclass,1) AS acl", 0))
		run(objectACLSelect("SELECT acl FROM acl_obj_signature_view", "{=r/"+objectACLOwner(b)+"}"))
	})
}

func TestReferenceObjectACLNulls(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		for _, args := range []string{"NULL,0,0", "1259,NULL,0", "1259,0,NULL", "NULL,NULL,NULL", "999999,NULL,1", "NULL,1,1"} {
			run(objectACLSelect("SELECT pg_get_acl("+args+") AS acl", nil))
		}
		for _, args := range [][]any{{nil, int64(0), int64(0)}, {int64(1259), nil, int64(0)}, {int64(1259), int64(0), nil}, {nil, nil, nil}} {
			s := objectACLSelect("SELECT pg_get_acl($1,$2,$3) AS acl", nil)
			s.Args = args
			run(s)
		}
		run(fixture.Select("SELECT unnest(pg_get_acl(NULL,NULL,NULL)) AS acl", []string{"acl"}, []string{"ACLITEM"}, nil))
		run(fixture.Select("SELECT (pg_get_acl(NULL,NULL,NULL))[1] AS acl", []string{"acl"}, []string{"ACLITEM"}, [][]any{{nil}}))
		// A strict function does not suppress demanded argument expression errors.
		q := "SELECT pg_get_acl(NULL,0,1/0) AS acl"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
