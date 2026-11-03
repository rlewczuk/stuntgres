package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimePipeline(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		srRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE st_calls START 1", 0))
		for _, indexed := range []bool{false,true} {
			if indexed { srRun(t, p, b, c, fixture.Exec("CREATE INDEX st_id_idx ON sr(id)", 0)) }
			for _, prepared := range []bool{false,true} {
				for _, argument := range []string{"0", "'Infinity'"} {
					srRun(t, p, b, c, fixture.Exec("ALTER SEQUENCE st_calls RESTART WITH 1", 0))
					s := srCount("SELECT count(*) AS n FROM sr TABLESAMPLE system_time("+argument+") WHERE nextval('st_calls')<0", 0)
					s.Prepared = prepared
					srRun(t, p, b, c, s)
					called, last := false, int64(1)
					if argument == "'Infinity'" { called, last = true, 10 }
					srRun(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM st_calls", []string{"last_value","is_called"}, []string{"INT8","BOOL"}, [][]any{{last,called}}))
				}
			}
		}
		srBoth(t, p, b, c, fixture.Select("SELECT id*2 AS doubled FROM ONLY public.sr AS sampled TABLESAMPLE public.system_time('Infinity') WHERE id>7 ORDER BY id DESC LIMIT 2", []string{"doubled"}, []string{"INT4"}, [][]any{{int64(20)},{int64(18)}}))
		srBoth(t, p, b, c, fixture.Select("SELECT sum(id) AS total,count(*) AS n FROM sr TABLESAMPLE system_time('Infinity')", []string{"total","n"}, []string{"INT8","INT8"}, [][]any{{int64(55),int64(10)}}))
		srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM sr AS a TABLESAMPLE system_time('Infinity') CROSS JOIN sr AS z TABLESAMPLE system_time(0)", 0))
		for _, argument := range []string{"NULL", "-1", "'NaN'"} {
			srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM sr TABLESAMPLE system_time("+argument+") WHERE false", 0))
			srBoth(t, p, b, c, fixture.Select("SELECT id,payload FROM sr TABLESAMPLE system_time("+argument+") LIMIT 0", srNames, srTypes, nil))
		}
		correlated := "SELECT limits.k,(SELECT count(*) FROM sr TABLESAMPLE system_time(limits.k)) AS n FROM (VALUES(0::float8),('Infinity'::float8),(0::float8)) AS limits(k) ORDER BY limits.k"
		srBoth(t, p, b, c, fixture.Select(correlated, []string{"k","n"}, []string{"FLOAT8","INT8"}, [][]any{{float64(0),int64(0)},{float64(0),int64(0)},{math.Inf(1),int64(10)}}))
		stInheritance(t, p, b, c)
	})
}

func stInheritance(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE st_parent(id integer NOT NULL,payload text)",
		"CREATE TABLE st_child() INHERITS(st_parent)",
		"CREATE TABLE st_partitioned(id integer NOT NULL,payload text) PARTITION BY RANGE(id)",
		"CREATE TABLE st_partition_a PARTITION OF st_partitioned FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE st_partition_b PARTITION OF st_partitioned FOR VALUES FROM(10) TO(20)",
	} { srRun(t, p, b, c, fixture.Exec(q, 0)) }
	srRun(t, p, b, c, fixture.Exec("INSERT INTO st_parent VALUES(1,'parent'),(2,'parent')", 2))
	srRun(t, p, b, c, fixture.Exec("INSERT INTO st_child VALUES(11,'child'),(12,'child')", 2))
	srRun(t, p, b, c, fixture.Exec("INSERT INTO st_partitioned VALUES(1,'a'),(2,'a'),(11,'b'),(12,'b')", 4))
	for _, item := range []struct { source string; visible [][]any }{
		{"st_parent", [][]any{{int64(1),"parent"},{int64(2),"parent"},{int64(11),"child"},{int64(12),"child"}}},
		{"ONLY st_parent", [][]any{{int64(1),"parent"},{int64(2),"parent"}}},
		{"st_partitioned", [][]any{{int64(1),"a"},{int64(2),"a"},{int64(11),"b"},{int64(12),"b"}}},
		{"ONLY st_partitioned", nil},
	} {
		q := "SELECT id,payload FROM "+item.source+" TABLESAMPLE system_time("
		srBoth(t, p, b, c, fixture.Select(q+"0)", srNames, srTypes, nil))
		srBoth(t, p, b, c, fixture.Select(q+"'Infinity') ORDER BY id", srNames, srTypes, item.visible))
		stSampleBoth(t, p, c, q+"0.25)", item.visible)
	}
}
