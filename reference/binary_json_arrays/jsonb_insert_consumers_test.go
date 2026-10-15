package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbInsertStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		`CREATE TABLE jbi_owned(id int,src jsonb,d jsonb DEFAULT jsonb_insert('[0]','{0}','9'),g jsonb GENERATED ALWAYS AS (jsonb_insert(src,'{0}','9',true)) STORED,CHECK(g IS NULL OR jsonb_array_length(g)>0))`,
		`CREATE VIEW jbi_before AS SELECT id,jsonb_insert(replacement => '9',path => '{0}',jsonb_in => src) AS changed FROM jbi_owned`,
		`CREATE VIEW jbi_after AS SELECT id,jsonb_insert(src,'{0}','9',insert_after => true) AS changed FROM jbi_owned`,
		`CREATE VIEW jbi_variadic AS SELECT id,jsonb_insert(src,'{0}',VARIADIC '9'::jsonb) AS changed FROM jbi_owned`,
		`CREATE INDEX jbi_owned_idx ON jbi_owned ((jsonb_insert(src,'{0}','9')))`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO jbi_owned(id,src) VALUES(1,'[0]'),(2,NULL)", 2))
	check := fixture.Select("SELECT id,src,d,g FROM jbi_owned ORDER BY id", []string{"id", "src", "d", "g"}, []string{"INT4", "JSONB", "JSONB", "JSONB"}, [][]any{
		{int64(1), "[0]", "[9, 0]", "[0, 9]"}, {int64(2), nil, "[9, 0]", nil},
	})
	jsonbSetBoth(t, p, b, c, check)
	jsonbInsertViews(t, p, b, c, false)
	jsonbSetBoth(t, p, b, c, fixture.Select(`SELECT id FROM jbi_owned WHERE jsonb_insert(src,'{0}','9')='[9,0]' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	return check
}

func jsonbInsertViews(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, future bool) {
	t.Helper()
	for _, tc := range []struct{ name, first, third string }{
		{"jbi_before", "[9, 0]", "[9, 1]"}, {"jbi_after", "[0, 9]", "[1, 9]"}, {"jbi_variadic", "[9, 0]", "[9, 1]"},
	} {
		want := [][]any{{int64(1), tc.first}, {int64(2), nil}}
		if future {
			want = append(want, []any{int64(3), tc.third})
		}
		jsonbSetBoth(t, p, b, c, fixture.Select("SELECT id,changed FROM "+tc.name+" ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, want))
	}
}

func jsonbInsertAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jbi_source(id int,v jsonb)", 0))
	run(fixture.Exec(`INSERT INTO jbi_source VALUES(1,'{}'),(2,'{"a":1}')`, 2))
	run(fixture.Exec("CREATE TABLE jbi_target(id int,v jsonb)", 0))
	run(fixture.Exec("INSERT INTO jbi_target VALUES(0,'99')", 1))
	run(fixture.Exec("CREATE INDEX jbi_target_idx ON jbi_target ((jsonb_insert('{}','{safe}',v)))", 0))
	q := "INSERT INTO jbi_target SELECT id,jsonb_insert(v,'{a}','9') FROM jbi_source ORDER BY id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	s := fixture.ErrorDiagnostic(q, jsonbInsertCollision())
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	run(fixture.Select("SELECT id,v FROM jbi_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), "99"}}))
	run(fixture.Select("SELECT id,v FROM jbi_source ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), "{}"}, {int64(2), `{"a": 1}`}}))
	run(fixture.Select(`SELECT id FROM jbi_target WHERE jsonb_insert('{}','{safe}',v)='{"safe":99}' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
	run(fixture.Exec("UPDATE jbi_source SET v='{}' WHERE id=2", 1))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2))
	run(fixture.Select("SELECT id,v FROM jbi_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), "99"}, {int64(1), `{"a": 9}`}, {int64(2), `{"a": 9}`}}))
}

func TestReferenceJSONBInsertConsumersAndAtomicity(t *testing.T) {
	jsonbInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbInsertStored(t, p, b, c)
		s := fixture.Select("SELECT jsonb_insert(src,'{0}','9') AS changed,count(*) AS n FROM jbi_owned GROUP BY jsonb_insert(src,'{0}','9')", []string{"changed", "n"}, []string{"JSONB", "INT8"}, [][]any{{"[9, 0]", int64(1)}, {nil, int64(1)}})
		s.Bag = true
		jsonbSetBoth(t, p, b, c, s)
		jsonbSetBoth(t, p, b, c, fixture.Select("SELECT CASE WHEN src IS NULL THEN jsonb_insert('[1]','{0}','9') ELSE jsonb_insert(src,'{0}','9',true) END AS changed FROM jbi_owned ORDER BY id", []string{"changed"}, []string{"JSONB"}, [][]any{{"[0, 9]"}, {"[9, 1]"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select("UPDATE jbi_owned SET src=$1 WHERE id=1 RETURNING src,g", []string{"src", "g"}, []string{"JSONB", "JSONB"}, [][]any{{"[1]", "[1, 9]"}}, "[1]")))
		jsonbSetBoth(t, p, b, c, fixture.Select(`SELECT id FROM jbi_owned WHERE jsonb_insert(src,'{0}','9')='[9,1]' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE jbi_owned SET src='[0]' WHERE id=1", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		jsonbInsertAtomic(t, p, b, c)
	})
}
