package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumConsumers(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		both := func(s fixture.Step) { moneyIntervalSumBoth(t, p, conn, b, s) }
		run(fixture.Exec("CREATE TABLE mis_consumer(id INTEGER,g INTEGER,m MONEY,iv INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO mis_consumer VALUES (1,1,'1','1 mon'),(2,1,'2','30 days'),(3,2,'4','1 day')", 3))
		both(fixture.Select("SELECT sum(m),sum(m),sum(iv),sum(iv),avg(iv),count(iv) FROM mis_consumer WHERE g=1", []string{"sum", "sum", "sum", "sum", "avg", "count"}, []string{"MONEY", "MONEY", "INTERVAL", "INTERVAL", "INTERVAL", "INT8"}, [][]any{{"$3.00", "$3.00", "1 mon 30 days", "1 mon 30 days", "30 days", int64(2)}}))
		both(fixture.Select("SELECT g FROM mis_consumer GROUP BY g HAVING sum(m)>'3'::money ORDER BY sum(iv)", []string{"g"}, []string{"INT4"}, [][]any{{int64(2)}}))
		both(moneyIntervalSumPair("SELECT sum(m)+'1'::money AS m,sum(iv)+'1 microsecond'::interval AS iv FROM mis_consumer WHERE g=1", [][]any{{"$4.00", "1 mon 30 days 00:00:00.000001"}}))
		both(moneyIntervalSumPair("WITH totals AS (SELECT sum(m) AS m,sum(iv) AS iv FROM mis_consumer WHERE g=1) SELECT m,iv FROM totals", [][]any{{"$3.00", "1 mon 30 days"}}))
		both(moneyIntervalSumPair("SELECT q.m,q.iv FROM (SELECT sum(m) AS m,sum(iv) AS iv FROM mis_consumer WHERE g=1) AS q", [][]any{{"$3.00", "1 mon 30 days"}}))
		both(fixture.Select("SELECT outer_row.g,(SELECT sum(inner_row.m) FROM mis_consumer AS inner_row WHERE inner_row.g=outer_row.g) AS m,(SELECT sum(inner_row.iv) FROM mis_consumer AS inner_row WHERE inner_row.g=outer_row.g) AS iv FROM mis_consumer AS outer_row WHERE outer_row.id=1", []string{"g", "m", "iv"}, []string{"INT4", "MONEY", "INTERVAL"}, [][]any{{int64(1), "$3.00", "1 mon 30 days"}}))
		run(fixture.Exec("CREATE VIEW mis_grouped AS SELECT g,sum(m) AS m,sum(iv) AS iv FROM mis_consumer GROUP BY g", 0))
		both(fixture.Select("SELECT g,m,iv FROM mis_grouped ORDER BY g", []string{"g", "m", "iv"}, []string{"INT4", "MONEY", "INTERVAL"}, [][]any{{int64(1), "$3.00", "1 mon 30 days"}, {int64(2), "$4.00", "1 day"}}))
		run(fixture.Exec("CREATE TABLE mis_target(g INTEGER,m MONEY,iv INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO mis_target SELECT 0,sum(m),sum(iv) FROM mis_consumer WHERE FALSE", 1))
		run(fixture.Exec("INSERT INTO mis_target SELECT g,sum(m),sum(iv) FROM mis_consumer WHERE FALSE GROUP BY g", 0))
		run(fixture.Select("SELECT g,m,iv FROM mis_target", []string{"g", "m", "iv"}, []string{"INT4", "MONEY", "INTERVAL"}, [][]any{{int64(0), nil, nil}}))
		run(fixture.Exec("DELETE FROM mis_target", 1))
		run(fixture.Exec("INSERT INTO mis_target VALUES (99,'9','9 days')", 1))
		for _, c := range []struct{ values, code, message string }{
			{"(1,1,'1','1 day'),(2,2,'92233720368547758.07','1 day'),(3,2,'0.01','1 day')", "22003", "money out of range"},
			{"(1,1,'1','1 day'),(2,2,'1','2147483647 days'),(3,2,'1','1 day')", "22008", "interval out of range"},
		} {
			run(fixture.Exec("DELETE FROM mis_consumer", 3))
			run(fixture.Exec("INSERT INTO mis_consumer VALUES "+c.values, 3))
			q := "SELECT g,sum(m ORDER BY id),sum(iv ORDER BY id) FROM mis_consumer GROUP BY g ORDER BY sum(m ORDER BY id),sum(iv ORDER BY id),g"
			// A post-aggregate sort forces PostgreSQL to evaluate every group
			// before Query admission; native must eagerly reject without a cursor.
			moneyIntervalSumExecutionError(t, p, conn, b, q, c.code, c.message, true)
			moneyIntervalSumExecutionError(t, p, conn, b, "INSERT INTO mis_target "+q, c.code, c.message, false)
			both(fixture.Select("SELECT g,m,iv FROM mis_target", []string{"g", "m", "iv"}, []string{"INT4", "MONEY", "INTERVAL"}, [][]any{{int64(99), "$9.00", "9 days"}}))
		}
	})
}
