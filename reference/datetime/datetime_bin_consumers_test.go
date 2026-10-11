package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeBinQueryConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE db_rows(id integer,s timestamp,z timestamptz,o timestamp)", 0))
	run(fixture.Exec("INSERT INTO db_rows VALUES(1,TIMESTAMP '2000-01-01 00:02:29.999999',TIMESTAMPTZ '2000-01-01 00:02:29.999999+00',TIMESTAMP '2000-01-01 00:02:30'),(2,TIMESTAMP '2000-01-01 00:02:30',TIMESTAMPTZ '2000-01-01 00:02:30+00',TIMESTAMP '2000-01-01 00:02:30'),(3,TIMESTAMP '2000-01-01 00:17:29.999999',TIMESTAMPTZ '2000-01-01 00:17:29.999999+00',TIMESTAMP '2000-01-01 00:02:30'),(4,TIMESTAMP '2000-01-01 00:17:30',TIMESTAMPTZ '2000-01-01 00:17:30+00',TIMESTAMP '2000-01-01 00:02:30')", 4))
	for _, s := range []fixture.Step{
		fixture.Select("SELECT date_bin('15 minutes',s,o)::text AS bucket,count(*) AS n FROM db_rows GROUP BY date_bin('15 minutes',s,o) ORDER BY date_bin('15 minutes',s,o)", []string{"bucket", "n"}, []string{"TEXT", "INT8"}, [][]any{{"1999-12-31 23:47:30", int64(1)}, {"2000-01-01 00:02:30", int64(2)}, {"2000-01-01 00:17:30", int64(1)}}),
		fixture.Select("SELECT count(date_bin('15 minutes',s,o)) AS n,min(date_bin('15 minutes',s,o))::text AS first FROM db_rows", []string{"n", "first"}, []string{"INT8", "TEXT"}, [][]any{{int64(4), "1999-12-31 23:47:30"}}),
		fixture.Select("SELECT id,date_bin('15 minutes',z,o::timestamptz) AS bucket FROM db_rows WHERE date_bin('15 minutes',s,o)=TIMESTAMP '2000-01-01 00:02:30' ORDER BY date_bin('15 minutes',z,o::timestamptz),id", []string{"id", "bucket"}, []string{"INT4", "TIMESTAMPTZ"}, [][]any{{int64(2), datetimeTruncInstant("2000-01-01T00:02:30Z")}, {int64(3), datetimeTruncInstant("2000-01-01T00:02:30Z")}}),
		fixture.Select("SELECT a.id,b.id FROM db_rows a JOIN db_rows b ON date_bin('15 minutes',a.s,a.o)=date_bin('15 minutes',b.s,b.o) ORDER BY a.id,b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}),
		fixture.Select("SELECT CASE WHEN id=2 THEN date_bin('15 minutes',s,o) ELSE COALESCE(NULL::timestamp,date_bin('15 minutes',s,o)) END AS result FROM db_rows WHERE id=2 OR id=3 ORDER BY id", []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant("2000-01-01T00:02:30Z")}, {datetimeTruncInstant("2000-01-01T00:02:30Z")}}),
		fixture.Select("SELECT date_bin('15 minutes',s,o) AS bucket,count(date_bin('15 minutes',z,o::timestamptz)) AS n FROM db_rows GROUP BY date_bin('15 minutes',s,o) HAVING count(date_bin('15 minutes',z,o::timestamptz))=2 ORDER BY bucket", []string{"bucket", "n"}, []string{"TIMESTAMP", "INT8"}, [][]any{{datetimeTruncInstant("2000-01-01T00:02:30Z"), int64(2)}}),
	} {
		datetimeExtractBoth(t, p, b, c, s)
	}
	run(fixture.Exec("CREATE TABLE db_write(id integer,s timestamp,z timestamptz)", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO db_write SELECT id,date_bin('15 minutes',s,o),date_bin('15 minutes',z,o::timestamptz) FROM db_rows WHERE id=1", 1)))
	run(fixture.Select("SELECT s,z FROM db_write", []string{"s", "z"}, []string{"TIMESTAMP", "TIMESTAMPTZ"}, [][]any{{datetimeTruncInstant("1999-12-31T23:47:30Z"), datetimeTruncInstant("1999-12-31T23:47:30Z")}}))
	run(fixture.Select("UPDATE db_write SET s=TIMESTAMP '2000-01-01 00:17:29.999999' WHERE id=1 RETURNING date_bin('15 minutes',s,TIMESTAMP '2000-01-01 00:02:30') AS result", []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant("2000-01-01T00:02:30Z")}}))
	run(fixture.Prepared(fixture.Select("DELETE FROM db_write WHERE id=1 RETURNING date_bin('15 minutes',z,TIMESTAMPTZ '2000-01-01 00:02:30+00') AS result", []string{"result"}, []string{"TIMESTAMPTZ"}, [][]any{{datetimeTruncInstant("1999-12-31T23:47:30Z")}})))
	run(fixture.Select("SELECT count(*) FROM db_write", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}
