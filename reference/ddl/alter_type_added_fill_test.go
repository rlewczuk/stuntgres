package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type addedFillRun func(fixture.Step)

type addedFillSession struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	prepared bool
	name     string
}

func addedFillScenarios(t *testing.T, cases []string, body func(*addedFillSession, string)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, scenario := range cases {
				t.Run(scenario, func(t *testing.T) {
					for _, route := range []string{"direct", "prepared"} {
						t.Run(route, func(t *testing.T) {
							conn, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { _ = conn.Close() })
							name := "fill_" + scenario + "_" + route
							if scenario == "empty" && route == "direct" {
								name = "grind_latency"
							}
							body(&addedFillSession{t, p, b, conn, route == "prepared", name}, scenario)
						})
					}
				})
			}
		})
	}
}

func (s *addedFillSession) run(db fixture.Executor) addedFillRun {
	return func(step fixture.Step) {
		step.Prepared = s.prepared
		fixture.RunStep(s.t, s.p.Ctx, db, s.b.Recorder, step)
	}
}

func (s *addedFillSession) begin() (*sql.Tx, addedFillRun) {
	s.t.Helper()
	tx, err := s.conn.BeginTx(s.p.Ctx, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = tx.Rollback() })
	return tx, s.run(tx)
}

func (s *addedFillSession) commit(tx *sql.Tx) {
	s.t.Helper()
	if err := tx.Commit(); err != nil {
		s.t.Fatal(err)
	}
}

func addedFillExec(run addedFillRun, query string, affected int64) {
	run(fixture.Exec(query, affected))
}

// Catalog witnesses assert the declared type, nullability and presence of the
// persistent default; omitted INSERTs separately prove that default's value.
func addedFillColumn(run addedFillRun, table, column, typ string) {
	run(fixture.Select("SELECT data_type::text AS data_type, is_nullable::text AS is_nullable, column_default IS NOT NULL AS has_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = '"+table+"' AND column_name = '"+column+"'", []string{"data_type", "is_nullable", "has_default"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{typ, "NO", true}}))
}

func addedFillAbsent(run addedFillRun, table, column string) {
	run(fixture.Select("SELECT column_name::text AS column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = '"+table+"' AND column_name = '"+column+"'", []string{"column_name"}, []string{"TEXT"}, [][]any{}))
}

func TestReferenceAlterTypeAddedColumnFill(t *testing.T) {
	addedFillScenarios(t, []string{"empty", "assignment", "using", "mixed", "mixed_empty", "twice"}, func(s *addedFillSession, scenario string) {
		n := s.name
		outside := s.run(s.conn)
		populated := scenario != "empty" && scenario != "mixed_empty"
		if populated {
			addedFillExec(outside, "CREATE TABLE "+n+" (id integer PRIMARY KEY)", 0)
			addedFillExec(outside, "INSERT INTO "+n+" VALUES (1)", 1)
		}
		tx, run := s.begin()
		if populated {
			addedFillExec(run, "INSERT INTO "+n+" VALUES (2)", 1)
		} else {
			addedFillExec(run, "CREATE TABLE "+n+" (id integer PRIMARY KEY)", 0)
		}
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN latency bigint DEFAULT -1 NOT NULL", 0)
		alter := "ALTER TABLE " + n + " ALTER COLUMN latency TYPE FLOAT"
		using := scenario == "using" || scenario == "mixed" || scenario == "mixed_empty"
		if using {
			alter += " USING (latency::double precision + id)"
		}
		mixed := scenario == "mixed" || scenario == "mixed_empty"
		if mixed {
			alter += ", ADD COLUMN marker integer DEFAULT 7 NOT NULL"
		}
		addedFillExec(run, alter, 0)
		first := 1
		if populated {
			first = 3
		}
		addedFillExec(run, fmt.Sprintf("INSERT INTO %s(id) VALUES (%d)", n, first), 1)
		columns, types := []string{"id", "latency"}, []string{"INT4", "FLOAT8"}
		if scenario != "empty" {
			if !mixed {
				addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN marker integer DEFAULT 7 NOT NULL", 0)
			}
			columns = append(columns, "marker")
			types = append(types, "INT4")
			if scenario == "twice" {
				addedFillExec(run, "ALTER TABLE "+n+" ALTER COLUMN marker TYPE bigint USING marker::bigint", 0)
				types[2] = "INT8"
			}
			if mixed {
				addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN tail integer DEFAULT 9 NOT NULL", 0)
				columns = append(columns, "tail")
				types = append(types, "INT4")
			}
			if populated {
				addedFillExec(run, "INSERT INTO "+n+"(id) VALUES (4)", 1)
			}
		}
		last := first
		if populated {
			last = 4
		}
		witness := func(r addedFillRun, count int) {
			rows := make([][]any, 0, count)
			for id := 1; id <= count; id++ {
				value := float64(-1)
				if using && populated && id <= 2 {
					value += float64(id)
				}
				row := []any{int64(id), value}
				if len(columns) > 2 {
					row = append(row, int64(7))
				}
				if mixed {
					row = append(row, int64(9))
				}
				rows = append(rows, row)
			}
			query := "SELECT id,latency"
			if len(columns) > 2 {
				query += ",marker"
			}
			if mixed {
				query += ",tail"
			}
			r(fixture.Select(query+" FROM "+n+" ORDER BY id", columns, types, rows))
			typeRows := make([][]any, count)
			for i := range typeRows {
				typeRows[i] = []any{"double precision"}
			}
			r(fixture.Select("SELECT pg_typeof(latency)::text AS typ FROM "+n+" ORDER BY id", []string{"typ"}, []string{"TEXT"}, typeRows))
			addedFillColumn(r, n, "latency", "double precision")
			if len(columns) > 2 {
				markerType := "integer"
				if scenario == "twice" {
					markerType = "bigint"
				}
				addedFillColumn(r, n, "marker", markerType)
				for i := range typeRows {
					typeRows[i] = []any{markerType}
				}
				r(fixture.Select("SELECT pg_typeof(marker)::text AS typ FROM "+n+" ORDER BY id", []string{"typ"}, []string{"TEXT"}, typeRows))
			}
			if mixed {
				addedFillColumn(r, n, "tail", "integer")
			}
		}
		if scenario == "empty" {
			run(fixture.Select("SELECT id,latency,pg_typeof(latency)::text AS typ FROM "+n+" ORDER BY id", []string{"id", "latency", "typ"}, []string{"INT4", "FLOAT8", "TEXT"}, [][]any{{int64(1), float64(-1), "double precision"}}))
		}
		witness(run, last)
		s.commit(tx)
		witness(outside, last)
		addedFillExec(outside, fmt.Sprintf("INSERT INTO %s(id) VALUES (%d)", n, last+1), 1)
		witness(outside, last+1)
	})
}

func TestReferenceAlterTypeAddedColumnFillTimestamp(t *testing.T) {
	addedFillScenarios(t, []string{"timestamp_empty", "timestamp_populated"}, func(s *addedFillSession, scenario string) {
		n := s.name
		outside := s.run(s.conn)
		addedFillExec(outside, "SET TIME ZONE 'UTC'", 0)
		populated := scenario == "timestamp_populated"
		if populated {
			addedFillExec(outside, "CREATE TABLE "+n+" (id integer PRIMARY KEY)", 0)
			addedFillExec(outside, "INSERT INTO "+n+" VALUES (1)", 1)
		}
		tx, run := s.begin()
		if populated {
			addedFillExec(run, "INSERT INTO "+n+" VALUES (2)", 1)
		} else {
			addedFillExec(run, "CREATE TABLE "+n+" (id integer PRIMARY KEY)", 0)
		}
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN last_used_at timestamp NOT NULL DEFAULT '0001-01-01 00:00:00+00:00'", 0)
		year1 := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
		modern := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
		before := [][]any{}
		if populated {
			addedFillExec(run, "UPDATE "+n+" SET last_used_at = TIMESTAMP '2024-02-03 04:05:06' WHERE id=2", 1)
			before = [][]any{{int64(1), year1, "timestamp without time zone"}, {int64(2), modern, "timestamp without time zone"}}
		}
		run(fixture.Select("SELECT id,last_used_at,pg_typeof(last_used_at)::text AS typ FROM "+n+" ORDER BY id", []string{"id", "last_used_at", "typ"}, []string{"INT4", "TIMESTAMP", "TEXT"}, before))
		addedFillColumn(run, n, "last_used_at", "timestamp without time zone")
		addedFillExec(run, "ALTER TABLE ONLY "+n+" ALTER COLUMN last_used_at SET DATA TYPE timestamptz USING last_used_at::timestamp AT TIME ZONE 'UTC', ALTER COLUMN last_used_at SET DEFAULT '0001-01-01 00:00:00+00:00'::timestamptz", 0)
		first, last := 1, 1
		if populated {
			first, last = 3, 4
		}
		addedFillExec(run, fmt.Sprintf("INSERT INTO %s(id) VALUES (%d)", n, first), 1)
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN marker integer DEFAULT 7 NOT NULL", 0)
		if populated {
			addedFillExec(run, "INSERT INTO "+n+"(id) VALUES (4)", 1)
		}
		witness := func(r addedFillRun, count int) {
			rows := make([][]any, count)
			for id := 1; id <= count; id++ {
				instant, rendered := year1, "0001-01-01 00:00:00+00"
				if populated && id == 2 {
					instant, rendered = modern, "2024-02-03 04:05:06+00"
				}
				rows[id-1] = []any{int64(id), instant, rendered, "timestamp with time zone", int64(7)}
			}
			r(fixture.Select("SELECT id,last_used_at,last_used_at::text AS rendered,pg_typeof(last_used_at)::text AS typ,marker FROM "+n+" ORDER BY id", []string{"id", "last_used_at", "rendered", "typ", "marker"}, []string{"INT4", "TIMESTAMPTZ", "TEXT", "TEXT", "INT4"}, rows))
			addedFillColumn(r, n, "last_used_at", "timestamp with time zone")
			addedFillColumn(r, n, "marker", "integer")
		}
		witness(run, last)
		s.commit(tx)
		witness(outside, last)
		addedFillExec(outside, fmt.Sprintf("INSERT INTO %s(id) VALUES (%d)", n, last+1), 1)
		witness(outside, last+1)
	})
}

func TestReferenceAlterTypeAddedColumnFillFeatureSmoke(t *testing.T) {
	addedFillScenarios(t, []string{"smoke"}, func(s *addedFillSession, _ string) {
		n := s.name
		outside := s.run(s.conn)
		addedFillExec(outside, "SET TIME ZONE 'UTC'", 0)
		addedFillExec(outside, "CREATE TABLE "+n+" (id integer PRIMARY KEY)", 0)
		addedFillExec(outside, "INSERT INTO "+n+" VALUES (1)", 1)
		tx, run := s.begin()
		addedFillExec(run, "INSERT INTO "+n+" VALUES (2)", 1)
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN latency bigint DEFAULT -1 NOT NULL", 0)
		addedFillExec(run, "ALTER TABLE "+n+" ALTER COLUMN latency TYPE FLOAT USING (latency::double precision + id)", 0)
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN marker integer DEFAULT 7 NOT NULL", 0)
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN last_used_at timestamp NOT NULL DEFAULT '0001-01-01 00:00:00+00:00'", 0)
		addedFillExec(run, "ALTER TABLE ONLY "+n+" ALTER COLUMN last_used_at SET DATA TYPE timestamptz USING last_used_at::timestamp AT TIME ZONE 'UTC', ALTER COLUMN last_used_at SET DEFAULT '0001-01-01 00:00:00+00:00'::timestamptz", 0)
		addedFillExec(run, "INSERT INTO "+n+"(id) VALUES (3)", 1)
		witness := func(r addedFillRun, count int) {
			rows := make([][]any, count)
			for id := 1; id <= count; id++ {
				v := float64(-1)
				if id <= 2 {
					v += float64(id)
				}
				rows[id-1] = []any{int64(id), v, int64(7), time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)}
			}
			r(fixture.Select("SELECT id,latency,marker,last_used_at FROM "+n+" ORDER BY id", []string{"id", "latency", "marker", "last_used_at"}, []string{"INT4", "FLOAT8", "INT4", "TIMESTAMPTZ"}, rows))
			addedFillColumn(r, n, "latency", "double precision")
			addedFillColumn(r, n, "marker", "integer")
			addedFillColumn(r, n, "last_used_at", "timestamp with time zone")
		}
		witness(run, 3)
		s.commit(tx)
		witness(outside, 3)
		addedFillExec(outside, "INSERT INTO "+n+"(id) VALUES (4)", 1)
		witness(outside, 4)
	})
}
