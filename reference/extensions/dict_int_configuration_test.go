package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func dictIntConfiguration(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TEXT SEARCH DICTIONARY dict_int_custom (TEMPLATE=public.intdict_template,maxlen=3,absval=true)",
		"CREATE TEXT SEARCH CONFIGURATION dict_int_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION dict_int_cfg ALTER MAPPING FOR int,uint WITH dict_int_custom",
		"CREATE TABLE dict_int_vectors(id int,v tsvector)",
	} {
		dictIntExec(t, p, b, c, q, 0)
	}
	dictIntExec(t, p, b, c, "INSERT INTO dict_int_vectors VALUES(1,to_tsvector('dict_int_cfg','-123456')),(2,to_tsvector('dict_int_cfg','42')),(3,NULL)", 3)
}

func dictIntMatches(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, ids ...int64) {
	t.Helper()
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	dictIntRun(t, p, b, e, fixture.Select("SELECT id FROM dict_int_vectors WHERE v @@ to_tsquery('dict_int_cfg',$1::text) ORDER BY id", []string{"id"}, []string{"INT4"}, rows, "-123999"))
}

func TestReferenceDictIntConfiguration(t *testing.T) {
	dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		dictIntExec(t, p, b, c, "CREATE EXTENSION dict_int", 0)
		dictIntConfiguration(t, p, b, c)
		dictIntLexemes(t, p, b, c, "{123}", "dict_int_custom", "-123456")
		dictIntSelect(t, p, b, c, "SELECT to_tsvector('dict_int_cfg',$1::text)::text AS vector,to_tsquery('dict_int_cfg',$2::text)::text AS query", []string{"vector", "query"}, []string{"TEXT", "TEXT"}, [][]any{{"'123':1", "'123'"}}, "-123456", "-123999")
		const query = "SELECT id FROM dict_int_vectors WHERE v @@ to_tsquery('dict_int_cfg',$1::text) ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		check := func(ids ...int64) {
			for _, e := range []fixture.Executor{c, held} {
				dictIntMatches(t, p, b, e, ids...)
			}
		}
		check(1)
		dictIntExec(t, p, b, c, "CREATE INDEX dict_int_vectors_gist ON dict_int_vectors USING gist(v)", 0)
		check(1)
		dictIntExec(t, p, b, c, "INSERT INTO dict_int_vectors VALUES(4,to_tsvector('dict_int_cfg','+123777'))", 1)
		check(1, 4)
		dictIntExec(t, p, b, c, "UPDATE dict_int_vectors SET v=to_tsvector('dict_int_cfg','42') WHERE id=1", 1)
		check(4)
		dictIntExec(t, p, b, c, "DELETE FROM dict_int_vectors WHERE id=4", 1)
		check()
		dictIntSelect(t, p, b, c, "SELECT id,v::text FROM dict_int_vectors ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "'42':1"}, {int64(2), "'42':1"}, {int64(3), nil}})
		dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dict_int_custom (rejectlong=true)", 0)
		dictIntExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION dict_int_cfg ALTER MAPPING FOR int,uint WITH dict_int_custom,pg_catalog.simple", 0)
		dictIntSelect(t, p, b, c, "SELECT to_tsvector('dict_int_cfg','123456 42')::text AS vector", []string{"vector"}, []string{"TEXT"}, [][]any{{"'42':2"}})
		dictIntExec(t, p, b, c, "INSERT INTO dict_int_vectors VALUES(5,to_tsvector('dict_int_cfg','123456 42'))", 1)
		dictIntSelect(t, p, b, c, "SELECT id FROM dict_int_vectors WHERE v @@ to_tsquery('dict_int_cfg','42') ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(5)}})
		check()
		dictIntLexemes(t, p, b, c, "{}", "dict_int_custom", "123456")
	})
}

func TestReferenceDictIntFeatureSmoke(t *testing.T) {
	dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		dictIntExec(t, p, b, c, "CREATE EXTENSION dict_int", 0)
		held := dictIntHeld(t, p, c)
		defer held.Statement.Close()
		for _, tc := range []struct {
			options, input string
			want           any
		}{
			{"maxlen=3", "123456", "{123}"},
			{"rejectlong=true", "123456", "{}"},
			{"rejectlong=false,absval=false", "-123", "{-12}"},
			{"absval=true", "-123", "{123}"},
		} {
			dictIntExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.intdict ("+tc.options+")", 0)
			for _, e := range []fixture.Executor{c, held} {
				dictIntLexemes(t, p, b, e, tc.want, "public.intdict", tc.input)
			}
		}
		for _, e := range []fixture.Executor{c, held} {
			dictIntLexemes(t, p, b, e, nil, "public.intdict", nil)
		}
		dictIntConfiguration(t, p, b, c)
		dictIntExec(t, p, b, c, "CREATE INDEX dict_int_vectors_gist ON dict_int_vectors USING gist(v)", 0)
		dictIntSelect(t, p, b, c, "SELECT to_tsvector('dict_int_cfg','-123456')::text AS vector,to_tsquery('dict_int_cfg','-123999')::text AS query", []string{"vector", "query"}, []string{"TEXT", "TEXT"}, [][]any{{"'123':1", "'123'"}})
		dictIntMatches(t, p, b, c, 1)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			dictIntSelect(t, p, b, c, "SELECT extname FROM pg_extension WHERE extname='dict_int'", []string{"extname"}, []string{"NAME"}, nil)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			dictIntMatches(t, p, b, c, 1)
			dictIntLexemes(t, p, b, held, "{123}", "public.intdict", "-123456")
			dictIntOptionText(t, p, b, c, "public.intdict", "maxlen = 3, rejectlong = 'false', absval = 'true'")
		}
	})
}
