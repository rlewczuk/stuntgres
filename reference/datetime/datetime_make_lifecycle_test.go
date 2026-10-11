package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeMakeStrictSlots(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, function := range []struct {
			name, typ string
			count     int
		}{{"make_timestamp", "TIMESTAMP", 6}, {"make_timestamptz", "TIMESTAMPTZ", 6}, {"make_timestamptz", "TIMESTAMPTZ", 7}} {
			for slot := range function.count {
				t.Run(fmt.Sprintf("%s%d/slot%d", function.name, function.count, slot), func(t *testing.T) {
					args := []string{"2000", "1", "1", "0", "0", "0", "'UTC'"}
					args[slot] = "NULL"
					expression := function.name + "("
					for i := range function.count {
						if i > 0 {
							expression += ","
						}
						expression += args[i]
					}
					expression += ")"
					for _, prepared := range []bool{false, true} {
						s := fixture.Select("SELECT "+expression+" AS result", []string{"result"}, []string{function.typ}, [][]any{{nil}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				})
			}
		}
	})
}

func TestReferenceDatetimeMakeStableDefinitions(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_make_stable(id integer,z timestamptz DEFAULT make_timestamptz(2013,7,15,12,0,0),explicit timestamptz DEFAULT make_timestamptz(2013,7,15,12,0,0,'UTC'))", 0))
		run(fixture.Exec("CREATE VIEW dt_make_stable_view AS SELECT make_timestamptz(2013,7,15,12,0,0) AS z", 0))
		var definitionOnly bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &definitionOnly); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("INSERT INTO dt_make_stable(id) VALUES(1)", 1))
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		run(fixture.Exec("INSERT INTO dt_make_stable(id) VALUES(2)", 1))
		run(fixture.Select("SELECT z::text AS z FROM dt_make_stable_view", []string{"z"}, []string{"TEXT"}, [][]any{{"2013-07-15 12:00:00-04"}}))
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Select("SELECT id,z::text AS z,explicit::text AS explicit FROM dt_make_stable ORDER BY id", []string{"id", "z", "explicit"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "2013-07-15 12:00:00+00", "2013-07-15 12:00:00+00"}, {int64(2), "2013-07-15 16:00:00+00", "2013-07-15 12:00:00+00"}}))
		for _, explicit := range []string{"", ",'UTC'"} {
			d := datetimeMakeError("42P17", "generation expression is not immutable")
			run(fixture.ErrorDiagnostic("CREATE TABLE dt_make_bad_generated(z timestamptz GENERATED ALWAYS AS (make_timestamptz(2013,7,15,12,0,0"+explicit+")) STORED)", d))
			d = datetimeMakeError("42P17", "functions in index expression must be marked IMMUTABLE")
			run(fixture.ErrorDiagnostic("CREATE INDEX dt_make_bad_index ON dt_make_stable ((make_timestamptz(2013,7,15,12,0,0"+explicit+")))", d))
		}
		// A retained cursor's values and metadata belong to its Execute environment.
		rows, err := c.QueryContext(p.Ctx, "SELECT z FROM dt_make_stable ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TIMESTAMPTZ" {
			t.Fatalf("retained metadata %#v", metadata)
		}
		for i, want := range []time.Time{time.Date(2013, 7, 15, 12, 0, 0, 0, time.UTC), time.Date(2013, 7, 15, 16, 0, 0, 0, time.UTC)} {
			if !rows.Next() {
				t.Fatalf("missing retained row %d: %v", i, rows.Err())
			}
			var got time.Time
			if err := rows.Scan(&got); err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want) {
				t.Fatalf("retained instant %v want %v", got, want)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatalf("retained tail %v", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !b.PG {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(definitionOnly.Bytes())); err != nil {
				t.Fatal(err)
			}
			// Empty definitions must not contain a frozen UTC constructor value; target
			// Execute settings, not source Dump settings, govern restored defaults/view.
			run(fixture.Exec("INSERT INTO dt_make_stable(id) VALUES(3)", 1))
			run(fixture.Select("SELECT id,z::text AS z,explicit::text AS explicit FROM dt_make_stable ORDER BY id", []string{"id", "z", "explicit"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(3), "2013-07-15 12:00:00-04", "2013-07-15 08:00:00-04"}}))
			run(fixture.Select("SELECT z::text AS z FROM dt_make_stable_view", []string{"z"}, []string{"TEXT"}, [][]any{{"2013-07-15 12:00:00-04"}}))
		}
	})
}

func TestReferenceDatetimeMakeReturningAndTransactions(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_make_returning(id integer,n integer)", 0))
		run(fixture.Exec("INSERT INTO dt_make_returning VALUES(1,2),(2,3)", 2))
		run(fixture.Select("SELECT id,make_interval(days=>n) AS interval FROM dt_make_returning ORDER BY id", []string{"id", "interval"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "2 days"}, {int64(2), "3 days"}}))
		run(fixture.Select("UPDATE dt_make_returning SET n=n WHERE id=1 RETURNING id,make_interval(days=>n) AS interval", []string{"id", "interval"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "2 days"}}))
		run(fixture.Select("DELETE FROM dt_make_returning WHERE id=2 RETURNING make_date(2000,1,n)::text AS date", []string{"date"}, []string{"TEXT"}, [][]any{{"2000-01-03"}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.ErrorDiagnostic("UPDATE dt_make_returning SET n=make_date(2013,2,29)::text::integer", datetimeMakeError("22008", "date field value out of range: 2013-02-29")))
		d := datetimeMakeError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
		s := fixture.ErrorDiagnostic("SELECT id FROM dt_make_returning", d)
		s.SelectRows = true
		run(s)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT id,n FROM dt_make_returning ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	})
}
