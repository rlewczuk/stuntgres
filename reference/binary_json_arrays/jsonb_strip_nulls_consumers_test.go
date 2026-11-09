package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBStripNullsConsumers(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_consumer(id int,v jsonb,flag bool)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbs_consumer VALUES(1,'{"a":null}',false),(2,'{"b":null}',true),(3,'[null,1]',true)`, 3))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id FROM jbs_consumer WHERE jsonb_strip_nulls(v,flag)::text='{}' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT jsonb_strip_nulls(v,flag)::text AS doc,count(*) AS n FROM jbs_consumer GROUP BY jsonb_strip_nulls(v,flag)::text ORDER BY doc", []string{"doc", "n"}, []string{"TEXT", "INT8"}, [][]any{{"[1]", int64(1)}, {"{}", int64(2)}}),
			fixture.Select("SELECT json_agg(jsonb_strip_nulls(v,flag) ORDER BY id) AS docs FROM jbs_consumer", []string{"docs"}, []string{"JSON"}, [][]any{{`[{}, {}, [1]]`}}),
			fixture.Select("SELECT id,(SELECT jsonb_strip_nulls(v,flag)) AS stripped FROM jbs_consumer ORDER BY id", []string{"id", "stripped"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), "{}"}, {int64(2), "{}"}, {int64(3), "[1]"}}),
			jsonbStripSelect(`SELECT CASE WHEN true THEN '{}'::jsonb ELSE jsonb_strip_nulls('{"x":null}') END AS stripped`, "{}"),
			jsonbStripSelect(`SELECT COALESCE('{}'::jsonb,jsonb_strip_nulls('{"x":null}')) AS stripped`, "{}"),
		} {
			jsonTypeBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceJSONBStripNullsJoin(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_target(id int,v jsonb)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_flag(id int,flag bool)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbs_target VALUES(1,'[null,1]'),(2,'{"a":null}')`, 2))
		jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbs_flag VALUES(1,true),(2,false)", 2))
		for _, expression := range []string{"jsonb_strip_nulls(l.v,r.flag)", "jsonb_strip_nulls(strip_in_arrays => r.flag,target => l.v)"} {
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT l.id,"+expression+" AS stripped FROM jbs_target l JOIN jbs_flag r ON l.id=r.id ORDER BY l.id", []string{"id", "stripped"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), "[1]"}, {int64(2), "{}"}}))
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT l.id FROM jbs_target l JOIN jbs_flag r ON "+expression+"::text='[1]' ORDER BY l.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_left(id int,v jsonb,flag bool)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_right(id int,doc text)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbs_left VALUES(1,'{"drop":null}',true),(2,'{"keep":1}',false)`, 2))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbs_right VALUES(10,'{}'),(20,'{"other": 2}')`, 2))
		for _, expression := range []string{"jsonb_strip_nulls(l.v,l.flag)", "jsonb_strip_nulls(strip_in_arrays => l.flag,target => l.v)"} {
			query := "SELECT l.id AS left_id,r.id AS right_id FROM jbs_left AS l FULL JOIN jbs_right AS r ON " + expression + "::text = r.doc ORDER BY left_id NULLS LAST,right_id"
			jsonTypeBoth(t, p, b, c, fixture.Select(query, []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {nil, int64(20)}}))
		}
	})
}
