package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDateRangeEnvironments(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v daterange)", 0))
		run(fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
		run(fixture.Exec("INSERT INTO r VALUES (1,'[01/02/03,01/03/03)'),(2,'[2000-02-29,2000-03-02)'),(3,'[0001-12-31 BC,0001-01-01)')", 3))
		query := "INSERT INTO r VALUES ($1,'[01/02/03,01/03/03)')"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		run(fixture.Exec("SET DateStyle = 'ISO, DMY'", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, 4))
		run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 5, "[01/02/03,01/03/03)")))
		for _, tc := range []struct{ style string; rows [][]any }{
			{"ISO, DMY", [][]any{{int64(1), "[2003-01-02,2003-01-03)"}, {int64(2), "[2000-02-29,2000-03-02)"}, {int64(3), `["0001-12-31 BC",0001-01-01)`}, {int64(4), "[2003-01-02,2003-01-03)"}, {int64(5), "[2003-02-01,2003-03-01)"}}},
			{"SQL, DMY", [][]any{{int64(1), "[02/01/2003,03/01/2003)"}, {int64(2), "[29/02/2000,02/03/2000)"}, {int64(3), `["31/12/0001 BC",01/01/0001)`}, {int64(4), "[02/01/2003,03/01/2003)"}, {int64(5), "[01/02/2003,01/03/2003)"}}},
			{"German, DMY", [][]any{{int64(1), "[02.01.2003,03.01.2003)"}, {int64(2), "[29.02.2000,02.03.2000)"}, {int64(3), `["31.12.0001 BC",01.01.0001)`}, {int64(4), "[02.01.2003,03.01.2003)"}, {int64(5), "[01.02.2003,01.03.2003)"}}},
			{"ISO, MDY", [][]any{{int64(1), "[2003-01-02,2003-01-03)"}, {int64(2), "[2000-02-29,2000-03-02)"}, {int64(3), `["0001-12-31 BC",0001-01-01)`}, {int64(4), "[2003-01-02,2003-01-03)"}, {int64(5), "[2003-02-01,2003-03-01)"}}},
		} {
			run(fixture.Exec("SET DateStyle = '"+tc.style+"'", 0))
			for _, prepared := range []bool{false, true} {
				s := rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, tc.rows, []bool{false, true})
				s.Prepared = prepared
				run(s)
			}
		}
	}
}

// The scalar DATE owner supplies the transaction's day independently. Expected
// successors use civil arithmetic, never a second wall-clock read or parity.
func TestReferenceDateRangeRelativeCapture(t *testing.T) {
	for _, zone := range []string{"UTC", "+12:00"} {
		t.Run(zone, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer conn.Close()
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE baseline(v date)", 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,v daterange)", 0))
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil { t.Fatal(err) }
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO baseline VALUES ('today')", 1))
				var scalar any
				if err := tx.QueryRowContext(p.Ctx, "SELECT v FROM baseline").Scan(&scalar); err != nil { t.Fatal(err) }
				day := time.Time{}
				switch value := scalar.(type) {
				case time.Time: day = value
				case string: day, err = time.Parse("2006-01-02", value)
				case []byte: day, err = time.Parse("2006-01-02", string(value))
				default: t.Fatalf("DATE baseline transport %T", scalar)
				}
				if err != nil { t.Fatal(err) }
				day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
				iso := func(delta int) string { return day.AddDate(0, 0, delta).Format("2006-01-02") }
				query := "INSERT INTO r VALUES ($1,'[today,tomorrow)')"
				stmt, err := tx.PrepareContext(p.Ctx, query)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: query}
				for _, k := range []int{1, 2} { fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, k)) }
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO r VALUES (3,'[yesterday,today]')", 1))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, 4, "[today,tomorrow)")))
				want := [][]any{{int64(1), fmt.Sprintf("[%s,%s)", iso(0), iso(1))}, {int64(2), fmt.Sprintf("[%s,%s)", iso(0), iso(1))}, {int64(3), fmt.Sprintf("[%s,%s)", iso(-1), iso(1))}, {int64(4), fmt.Sprintf("[%s,%s)", iso(0), iso(1))}}
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "DATERANGE"}, want, []bool{false, true}))
				if err := stmt.Close(); err != nil { t.Fatal(err) }
				if err := tx.Commit(); err != nil { t.Fatal(err) }
			}
		})
	}
}

