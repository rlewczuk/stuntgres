package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainDefaultPrecedence(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.d_base AS integer DEFAULT 11",
			"CREATE DOMAIN domain_s.d_inner AS domain_s.d_base DEFAULT 22",
			"CREATE DOMAIN domain_s.d_inherit AS domain_s.d_inner",
			"CREATE DOMAIN domain_s.d_outer AS domain_s.d_inner DEFAULT 33",
			"CREATE DOMAIN domain_s.d_null AS domain_s.d_inner DEFAULT NULL",
			"CREATE DOMAIN domain_s.d_plain AS integer NULL NULL",
			"CREATE DOMAIN domain_s.d_numeric AS numeric(5,2) DEFAULT 12.345",
			"CREATE DOMAIN domain_s.d_varchar AS varchar(3) DEFAULT 'abc'",
			"CREATE TABLE domain_s.defaults (id integer DEFAULT 0,a domain_s.d_base,b domain_s.d_inner,c domain_s.d_inherit,d domain_s.d_outer,e domain_s.d_outer DEFAULT 44,f domain_s.d_outer DEFAULT NULL,g domain_s.d_null,h domain_s.d_plain,n domain_s.d_numeric,v domain_s.d_varchar,z integer DEFAULT NULL)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.defaults DEFAULT VALUES", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.defaults(id) VALUES(1)", 1)))
		run(fixture.Exec("INSERT INTO domain_s.defaults VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1))
		expected := [][]any{}
		for _, id := range []int64{0, 1, 2} {
			expected = append(expected, []any{id, int64(11), int64(22), int64(22), int64(33), int64(44), nil, nil, nil, "12.35", "abc", nil})
		}
		s := fixture.Select("SELECT id,a,b,c,d,e,f,g,h,n,v,z FROM domain_s.defaults ORDER BY id", []string{"id", "a", "b", "c", "d", "e", "f", "g", "h", "n", "v", "z"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "NUMERIC", "VARCHAR", "INT4"}, expected)
		run(s)
		run(fixture.Prepared(s))
		q := "INSERT INTO domain_s.defaults(id,d) VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, v := range []int64{7, 8} {
			fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(q, 1, v, v))
		}
		run(fixture.Select("SELECT id,d,b FROM domain_s.defaults WHERE id>2 ORDER BY id", []string{"id", "d", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(7), int64(22)}, {int64(8), int64(8), int64(22)}}))
		run(fixture.Exec("INSERT INTO domain_s.defaults(id,d) VALUES(9,NULL)", 1))
		run(fixture.Select("SELECT d,NULL::domain_s.d_outer AS cast_null FROM domain_s.defaults WHERE id=9", []string{"d", "cast_null"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef ad JOIN pg_attribute a ON a.attrelid=ad.adrelid AND a.attnum=ad.adnum WHERE ad.adrelid='domain_s.defaults'::regclass AND a.attname='z'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceDomainDefaultBuiltinInheritance(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE DOMAIN domain_s.inherited_time AS information_schema.time_stamp", 0))
		run(fixture.Exec("CREATE TABLE domain_s.default_times(id integer,a information_schema.time_stamp,b domain_s.inherited_time,c domain_s.inherited_time DEFAULT '2001-02-03 04:05:06+00',d domain_s.inherited_time DEFAULT NULL)", 0))
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for i, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO domain_s.default_times(id) VALUES($1)", 1, int64(i))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, tx, r, s)
			s = fixture.Select("SELECT a=CURRENT_TIMESTAMP(2) AS a,b=CURRENT_TIMESTAMP(2) AS b,c='2001-02-03 04:05:06+00'::timestamptz AS c,d IS NULL AS d FROM domain_s.default_times ORDER BY id", []string{"a", "b", "c", "d"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}})
			if i == 1 {
				s.Rows = append(s.Rows, []any{true, true, true, true})
			}
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, tx, r, s)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
}
