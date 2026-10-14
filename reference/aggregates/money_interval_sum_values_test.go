package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumValues(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		both := func(s fixture.Step) { moneyIntervalSumBoth(t, p, conn, b, s) }
		run(fixture.Exec("CREATE TABLE mis_values(id INTEGER,g INTEGER,m MONEY,iv INTERVAL)", 0))
		both(moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_values", [][]any{{nil, nil}}))
		both(fixture.Select("SELECT g,sum(m),sum(iv) FROM mis_values GROUP BY g", []string{"g", "sum", "sum"}, []string{"INT4", "MONEY", "INTERVAL"}, [][]any{}))
		run(fixture.Exec("INSERT INTO mis_values VALUES (1,1,'12.34','1 mon 2 days 03:04:05.000001'),(2,1,'56.78','2 mons 3 days 04:05:06.000002'),(3,1,NULL,NULL),(4,2,NULL,NULL)", 4))
		both(moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_values", [][]any{{"$69.12", "3 mons 5 days 07:09:11.000003"}}))
		both(moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_values WHERE FALSE", [][]any{{nil, nil}}))
		both(moneyIntervalSumPair("SELECT sum(NULL::money) AS m,sum(NULL::interval) AS iv FROM mis_values", [][]any{{nil, nil}}))
		s := fixture.Select("SELECT g,sum(m),sum(iv) FROM mis_values GROUP BY g", []string{"g", "sum", "sum"}, []string{"INT4", "MONEY", "INTERVAL"}, [][]any{{int64(1), "$69.12", "3 mons 5 days 07:09:11.000003"}, {int64(2), nil, nil}})
		s.Bag = true
		both(s)
		for _, c := range []struct{ values, m, iv string }{
			{"(1,1,'90071992547409.93','9007199254740993 microseconds'),(2,1,'0.01',NULL)", "$90,071,992,547,409.94", "2501999:47:34.740993"},
			{"(1,1,'92233720368547758.07','1 mon'),(2,1,'-92233720368547758.08','30 days')", "-$0.01", "1 mon 30 days"},
			{"(1,1,'1','1 day'),(2,1,'-1','24 hours')", "$0.00", "1 day 24:00:00"},
			{"(1,1,'1','1 mon 2 days 3 microseconds'),(2,1,'-1','-1 mon -2 days -3 microseconds')", "$0.00", "00:00:00"},
		} {
			run(fixture.Exec("DELETE FROM mis_values", 4))
			run(fixture.Exec("INSERT INTO mis_values VALUES "+c.values, 2))
			both(moneyIntervalSumPair("SELECT sum(m ORDER BY id) AS m,sum(iv ORDER BY id) AS iv FROM mis_values", [][]any{{c.m, c.iv}}))
			// Keep DELETE's independently specified count constant between cases.
			run(fixture.Exec("INSERT INTO mis_values VALUES (3,1,NULL,NULL),(4,1,NULL,NULL)", 2))
		}
		run(fixture.Exec("DELETE FROM mis_values", 4))
		run(fixture.Exec("INSERT INTO mis_values VALUES (1,1,'1','1 day'),(2,1,'1','1 day'),(3,1,'2','2 days'),(4,1,NULL,NULL)", 4))
		both(moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_values", [][]any{{"$4.00", "4 days"}}))
		both(moneyIntervalSumPair("SELECT sum(DISTINCT m ORDER BY m) AS m,sum(DISTINCT iv ORDER BY iv) AS iv FROM mis_values", [][]any{{"$3.00", "3 days"}}))
		both(moneyIntervalSumPair("SELECT sum(m) FILTER(WHERE id<3) AS m,sum(iv) FILTER(WHERE id<3) AS iv FROM mis_values", [][]any{{"$2.00", "2 days"}}))
		both(moneyIntervalSumPair("SELECT sum(m) FILTER(WHERE FALSE) AS m,sum(iv) FILTER(WHERE FALSE) AS iv FROM mis_values", [][]any{{nil, nil}}))
		both(fixture.Select("SELECT sum(1::smallint),sum(1::integer),sum(1::bigint),sum(1::numeric),sum(1::real),sum(1::double precision)", []string{"sum", "sum", "sum", "sum", "sum", "sum"}, []string{"INT8", "INT8", "NUMERIC", "NUMERIC", "FLOAT4", "FLOAT8"}, [][]any{{int64(1), int64(1), "1", "1", float64(1), float64(1)}}))
	})
}

func TestReferenceMoneyIntervalSumFeatureSmoke(t *testing.T) {
	moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mis_smoke(m MONEY,iv INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO mis_smoke VALUES ('12.34','1 mon 2 days 03:04:05.000001'),('56.78','2 mons 3 days 04:05:06.000002'),(NULL,NULL)", 3))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_smoke", [][]any{{"$69.12", "3 mons 5 days 07:09:11.000003"}}))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_smoke WHERE FALSE", [][]any{{nil, nil}}))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT sum('90071992547409.93'::money)+'0.01'::money AS m,sum('9007199254740993 microseconds'::interval) AS iv", [][]any{{"$90,071,992,547,409.94", "2501999:47:34.740993"}}))
		run(fixture.Exec("CREATE TABLE mis_smoke_calendar(m MONEY,iv INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO mis_smoke_calendar VALUES ('1','1 mon'),('-1','30 days')", 2))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_smoke_calendar", [][]any{{"$0.00", "1 mon 30 days"}}))
		run(fixture.Exec("DELETE FROM mis_smoke_calendar", 2))
		run(fixture.Exec("INSERT INTO mis_smoke_calendar VALUES ('1','1 day'),('-1','24 hours')", 2))
		moneyIntervalSumBoth(t, p, conn, b, moneyIntervalSumPair("SELECT sum(m) AS m,sum(iv) AS iv FROM mis_smoke_calendar", [][]any{{"$0.00", "1 day 24:00:00"}}))
		q := "SELECT sum($1::money) AS m,sum($2::interval) AS iv FROM mis_smoke_calendar"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			moneyIntervalSumWithArgs(moneyIntervalSumPair(q, [][]any{{"$2.00", "2 days"}}), "1", "1 day"),
			moneyIntervalSumWithArgs(moneyIntervalSumPair(q, [][]any{{nil, nil}}), nil, nil),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "money out of range"}, "92233720368547758.07", "1 day")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, moneyIntervalSumWithArgs(moneyIntervalSumPair(q, [][]any{{"$2.00", "2 days"}}), "1", "1 day"))
		moneyIntervalSumExecutionError(t, p, conn, b, "SELECT sum(m) FROM (SELECT '92233720368547758.07'::money AS m UNION ALL SELECT '0.01'::money) AS q", "22003", "money out of range", true)
		moneyIntervalSumExecutionError(t, p, conn, b, "SELECT sum(iv) FROM (SELECT '2147483647 months'::interval AS iv UNION ALL SELECT '1 month'::interval) AS q", "22008", "interval out of range", true)
	})
}
