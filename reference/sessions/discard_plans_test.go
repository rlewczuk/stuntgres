package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDiscardCategoryPrecision(t *testing.T) {
	for _, category := range []string{"PLANS", "SEQUENCES", "TEMP", "ALL"} {
		t.Run(category, func(t *testing.T) {
			discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				discardSeed(t, p, b, a, peer)
				discardExec(t, p, b, a, "DISCARD "+category)
				discardPeerIntact(t, p, b, peer)
				if category == "ALL" {
					sqlPreparedStep(t, p, b, a, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
				} else {
					discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
					discardScalar(t, p, b, a, "SHOW application_name", "application_name", "TEXT", "discard_7")
				}
				if category == "TEMP" || category == "ALL" {
					discardMissingTable(t, p, b, a, "discard_t")
				} else {
					discardScalar(t, p, b, a, "SELECT v FROM discard_t", "v", "INT4", int64(7))
				}
				if category == "SEQUENCES" || category == "ALL" {
					discardMissingMemory(t, p, b, a, "discard_s")
				} else {
					discardScalar(t, p, b, a, "SELECT currval('discard_s')", "currval", "INT8", int64(1))
				}
				discardScalar(t, p, b, peer, "SELECT pg_try_advisory_lock(81001::bigint)", "pg_try_advisory_lock", "BOOL", category == "ALL")
			})
		})
	}
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		discardExec(t, p, b, a, "CREATE TABLE discard_cursor(v int)")
		sqlPreparedStep(t, p, b, a, fixture.Exec("INSERT INTO discard_cursor VALUES(1),(2),(3)", 3))
		discardExec(t, p, b, a, "BEGIN")
		discardExec(t, p, b, a, "DECLARE c CURSOR FOR SELECT v FROM discard_cursor ORDER BY v")
		for i, category := range []string{"PLANS", "SEQUENCES", "TEMP"} {
			discardScalar(t, p, b, a, "FETCH 1 FROM c", "v", "INT4", int64(i+1))
			discardExec(t, p, b, a, "DISCARD "+category)
		}
		sqlPreparedStep(t, p, b, a, fixture.Select("FETCH ALL FROM c", []string{"v"}, []string{"INT4"}, [][]any{}))
		discardExec(t, p, b, a, "ROLLBACK")
	})
}

func discardTimestamp(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) time.Time {
	t.Helper()
	var value time.Time
	if err := c.QueryRowContext(p.Ctx, q).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReferenceDiscardPlansRetainedCaptures(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		discardExec(t, p, b, a, "PREPARE p AS SELECT TIMESTAMP 'now' AS captured")
		stmt, err := a.PrepareContext(p.Ctx, "SELECT $1::int AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		temporal, err := a.PrepareContext(p.Ctx, "SELECT TIMESTAMP 'now' AS captured")
		if err != nil {
			t.Fatal(err)
		}
		defer temporal.Close()
		readTemporal := func() time.Time {
			rows, err := temporal.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "captured" {
				t.Fatal(columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TIMESTAMP" {
				t.Fatal(types, err)
			}
			if !rows.Next() {
				t.Fatal("missing temporal row", rows.Err())
			}
			var value time.Time
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra temporal row", rows.Err())
			}
			return value
		}
		goOld := readTemporal()
		step := fixture.Select("SELECT $1::int AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		step.Args = []any{7}
		held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: step.Query}
		sqlPreparedStep(t, p, b, held, step)
		old := discardTimestamp(t, p, a, "EXECUTE p")
		if again := discardTimestamp(t, p, a, "EXECUTE p"); !again.Equal(old) {
			t.Fatal("retained temporal capture changed without DISCARD")
		}
		if again := readTemporal(); !again.Equal(goOld) {
			t.Fatal("Go temporal capture changed without DISCARD")
		}
		for _, boundary := range []string{"standalone", "rollback", "savepoint"} {
			if boundary != "standalone" {
				discardExec(t, p, b, a, "BEGIN")
			}
			if boundary == "savepoint" {
				discardExec(t, p, b, a, "SAVEPOINT s")
			}
			discardExec(t, p, b, a, "DISCARD PLANS")
			if boundary == "savepoint" {
				discardExec(t, p, b, a, "ROLLBACK TO s")
			}
			if boundary != "standalone" {
				discardExec(t, p, b, a, "ROLLBACK")
			}
			lower := discardTimestamp(t, p, a, "SELECT CURRENT_TIMESTAMP::timestamp")
			fresh := discardTimestamp(t, p, a, "EXECUTE p")
			upper := discardTimestamp(t, p, a, "SELECT CURRENT_TIMESTAMP::timestamp")
			if fresh.Before(lower) || fresh.After(upper) {
				t.Fatalf("capture %v outside fresh transaction bounds [%v,%v]", fresh, lower, upper)
			}
			if !fresh.After(old) {
				t.Fatal("DISCARD did not recapture temporal literal")
			}
			old = fresh
			goFresh := readTemporal()
			goUpper := discardTimestamp(t, p, a, "SELECT CURRENT_TIMESTAMP::timestamp")
			if goFresh.Before(lower) || goFresh.After(goUpper) || !goFresh.After(goOld) {
				t.Fatalf("Go capture %v outside new bounds [%v,%v], old %v", goFresh, lower, goUpper, goOld)
			}
			goOld = goFresh
			sqlPreparedStep(t, p, b, held, step)
		}
	})
}
