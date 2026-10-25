package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSmokeSearchPathSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "sp1", "sp2", "path,comma")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			db := backend.DB
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			exec := func(c *sql.Conn, q string) {
				t.Helper()
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				fixture.RunStep(t, ctx, c, backend.Recorder, fixture.Exec(q, count))
			}
			value := func(c *sql.Conn, q string, want any) {
				t.Helper()
				var got any
				if err := c.QueryRowContext(ctx, q).Scan(&got); err != nil {
					t.Fatal(q, err)
				}
				if expected, ok := want.(time.Time); ok && backend.PG {
					actual, valid := got.(time.Time)
					if !valid || !actual.Equal(expected) {
						t.Fatalf("%s: %#v want %#v", q, got, want)
					}
				} else if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: %#v want %#v", q, got, want)
				}
			}
			for _, q := range []string{"CREATE SCHEMA sp1", "CREATE SCHEMA sp2", "CREATE TABLE sp1.t(id pg_catalog.int4)", "CREATE TABLE sp2.t(id INT)", "INSERT INTO sp1.t VALUES(7)", "INSERT INTO sp2.t VALUES(9)", "SET search_path TO missing,sp1,sp1,sp2"} {
				exec(a, q)
			}
			exec(b, "SET search_path TO sp1,sp2")
			table, err := a.PrepareContext(ctx, "SELECT id FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer table.Close()
			tableFrame := fixture.HeldStatement{Executor: a, Statement: table, SQL: "SELECT id FROM t"}
			fixture.RunStep(t, ctx, tableFrame, backend.Recorder, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			exec(a, "SET search_path TO sp2,sp1")
			fixture.RunStep(t, ctx, tableFrame, backend.Recorder, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
			value(a, "SELECT id FROM sp1.t", int64(7))
			value(b, "SELECT id FROM t", int64(7))
			for _, tc := range []struct {
				q    string
				want any
			}{
				{"SELECT pg_catalog.num_nulls(NULL,7)", int64(1)},
				{"SELECT pg_catalog.current_setting('search_path')", "sp2, sp1"},
				{"SELECT CAST(id AS pg_catalog.int4) FROM sp1.t", int64(7)},
				{"SELECT pg_catalog.int4(id) FROM sp1.t", int64(7)},
				{"SELECT pg_catalog.count(*) FROM sp1.t", int64(1)},
				{"SELECT id OPERATOR(pg_catalog.-) 2 + 3 FROM sp1.t", int64(2)},
				{"SELECT id OPERATOR(pg_catalog.=) 7 FROM sp1.t", true},
			} {
				value(a, tc.q, tc.want)
			}
			exec(a, "CREATE SEQUENCE sp1.seq")
			value(a, "SELECT pg_catalog.nextval('sp1.seq')", int64(1))
			value(a, "SELECT pg_catalog.set_config('search_path','sp1',false)", "sp1")
			exec(a, "SET search_path TO pg_temp,sp1")
			exec(a, "CREATE TEMP SEQUENCE temp_seq")
			var temp string
			if err := a.QueryRowContext(ctx, "SELECT current_schema()").Scan(&temp); err != nil || !strings.HasPrefix(temp, "pg_temp_") {
				t.Fatal(temp, err)
			}
			exec(a, "SET search_path TO sp1,pg_temp,pg_temp")
			value(a, "SELECT pg_catalog.num_nulls(NULL)", int64(1))
			exec(a, `CREATE SCHEMA "path,comma"`)
			exec(a, `SET search_path TO "path,comma"`)
			exec(a, "SET search_path TO sp2")
			_, err = a.ExecContext(ctx, "SELECT set_config('search_path','sp1,',false)")
			if err := (fixture.MigrationSmoke{T: t}).TransactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid value for parameter "search_path": "sp1,"`, Detail: "List syntax is invalid."}); err != nil {
				t.Fatal(err)
			}
			exec(a, "SET search_path TO missing,sp2,sp1")
			exec(a, "CREATE TABLE created(id INT)")
			exec(a, "CREATE TABLE sp1.defaults(id INT DEFAULT (7 OPERATOR(pg_catalog.+) 1))")
			exec(a, "INSERT INTO sp1.defaults DEFAULT VALUES")
			exec(a, "CREATE VIEW sp1.v AS SELECT id OPERATOR(pg_catalog.+) 1 AS n FROM sp1.t")
			value(a, "SELECT n FROM sp1.v", int64(8))
			exec(a, "CREATE VIEW sp1.math_path AS SELECT pg_catalog.round(1.25,1) AS n FROM sp1.t")
			checkMath := func(rows *sql.Rows) {
				t.Helper()
				defer rows.Close()
				metadata, err := rows.ColumnTypes()
				if err != nil || len(metadata) != 1 || metadata[0].Name() != "n" || metadata[0].DatabaseTypeName() != "NUMERIC" || (!backend.PG && metadata[0].ScanType() != reflect.TypeFor[string]()) || (backend.PG && metadata[0].ScanType() != reflect.TypeFor[any]()) {
					t.Fatal("persisted math metadata", metadata, err)
				}
				if !rows.Next() {
					t.Fatal("missing persisted math row", rows.Err())
				}
				var raw any
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				fixture.AssertRawValue(t, backend.PG, "NUMERIC", raw)
				text := raw
				if backend.PG {
					text = string(raw.([]byte))
				}
				if text != "1.3" {
					t.Fatal("persisted math value", raw, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra persisted math row", rows.Err())
				}
			}
			const mathQuery = "SELECT n FROM sp1.math_path"
			mathRows, err := a.QueryContext(ctx, mathQuery)
			if err != nil {
				t.Fatal(err)
			}
			checkMath(mathRows)
			mathHeld, err := a.PrepareContext(ctx, mathQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer mathHeld.Close()
			mathRows, err = (fixture.HeldStatement{Executor: a, Statement: mathHeld, SQL: mathQuery}).QueryContext(ctx, mathQuery)
			if err != nil {
				t.Fatal(err)
			}
			checkMath(mathRows)
			exec(a, "SET search_path TO sp1")
			exec(a, "CREATE TABLE sp1.temporal(t pg_catalog.time(3),ts pg_catalog.timestamp(3))")
			exec(a, "INSERT INTO sp1.temporal VALUES('12:34:56.123789','2000-01-01 12:34:56.123789')")
			checkTemporal := func(rows *sql.Rows) {
				t.Helper()
				defer rows.Close()
				metadata, err := rows.ColumnTypes()
				if err != nil || len(metadata) != 2 {
					t.Fatal(metadata, err)
				}
				for i, expected := range []struct {
					name, typ string
					scan      reflect.Type
				}{{"t", "TIME", reflect.TypeFor[string]()}, {"ts", "TIMESTAMP", reflect.TypeFor[time.Time]()}} {
					scan := expected.scan
					if backend.PG {
						scan = reflect.TypeFor[time.Time]()
					}
					if metadata[i].Name() != expected.name || metadata[i].DatabaseTypeName() != expected.typ || metadata[i].ScanType() != scan {
						t.Fatal("temporal metadata", metadata[i], expected)
					}
				}
				if !rows.Next() {
					t.Fatal("missing temporal row", rows.Err())
				}
				var clock, timestamp any
				if err := rows.Scan(&clock, &timestamp); err != nil {
					t.Fatal(err)
				}
				fixture.AssertRawValue(t, backend.PG, "TIME", clock)
				clockText := clock
				if backend.PG {
					clockValue := clock.(time.Time)
					if !clockValue.Equal(time.Date(0, 1, 1, 12, 34, 56, 124000000, time.UTC)) {
						t.Fatal("PostgreSQL TIME value", clockValue)
					}
					clockText = clockValue.Format("15:04:05.000")
				}
				timestampValue, ok := timestamp.(time.Time)
				wantTimestamp := time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)
				timestampMatches := ok && timestampValue.Equal(wantTimestamp)
				if !backend.PG {
					timestampMatches = reflect.DeepEqual(timestamp, wantTimestamp)
				}
				if clockText != "12:34:56.124" || !timestampMatches {
					t.Fatal("temporal values", clock, timestamp)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra temporal row", rows.Err())
				}
			}
			const temporalQuery = "SELECT t,ts FROM sp1.temporal"
			temporalRows, err := a.QueryContext(ctx, temporalQuery)
			if err != nil {
				t.Fatal(err)
			}
			checkTemporal(temporalRows)
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				exec(a, "SET search_path TO sp2,sp1")
				freshConnector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-fresh-temporal")
				if err != nil {
					t.Fatal(err)
				}
				fresh := sql.OpenDB(freshConnector)
				defer fresh.Close()
				freshSession, err := fresh.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer freshSession.Close()
				exec(freshSession, "SET search_path TO pg_catalog")
				if err := stuntgres.Restore(ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				restoredTemporal, err := freshSession.PrepareContext(ctx, temporalQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer restoredTemporal.Close()
				freshMath, err := freshSession.PrepareContext(ctx, mathQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer freshMath.Close()
				for _, path := range []string{"pg_catalog", "sp2"} {
					exec(freshSession, "SET search_path TO "+path)
					rows, err := freshSession.QueryContext(ctx, temporalQuery)
					if err != nil {
						t.Fatal(err)
					}
					checkTemporal(rows)
					rows, err = (fixture.HeldStatement{Executor: freshSession, Statement: restoredTemporal, SQL: temporalQuery}).QueryContext(ctx, temporalQuery)
					if err != nil {
						t.Fatal(err)
					}
					checkTemporal(rows)
					rows, err = freshSession.QueryContext(ctx, mathQuery)
					if err != nil {
						t.Fatal(err)
					}
					checkMath(rows)
					rows, err = (fixture.HeldStatement{Executor: freshSession, Statement: freshMath, SQL: mathQuery}).QueryContext(ctx, mathQuery)
					if err != nil {
						t.Fatal(err)
					}
					checkMath(rows)
				}
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			value(a, "SELECT id FROM sp1.t", int64(7))
			value(a, "SELECT id FROM sp2.t", int64(9))
			value(a, "SELECT id FROM sp1.defaults", int64(8))
			value(a, "SELECT n FROM sp1.v", int64(8))
			mathRows, err = a.QueryContext(ctx, mathQuery)
			if err != nil {
				t.Fatal(err)
			}
			checkMath(mathRows)
			mathRows, err = (fixture.HeldStatement{Executor: a, Statement: mathHeld, SQL: mathQuery}).QueryContext(ctx, mathQuery)
			if err != nil {
				t.Fatal(err)
			}
			checkMath(mathRows)
			if !backend.PG {
				value(a, "SELECT t FROM sp1.temporal", "12:34:56.124")
			} else {
				value(a, "SELECT t FROM sp1.temporal", time.Date(0, 1, 1, 12, 34, 56, 124000000, time.UTC))
			}
			value(a, "SELECT ts FROM sp1.temporal", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC))
			exec(a, "INSERT INTO sp1.temporal VALUES(TIME '12:34:56.125789',TIMESTAMP '2000-01-01 12:34:56.125789')")
			if !backend.PG {
				value(a, "SELECT t FROM sp1.temporal WHERE t='12:34:56.126'", "12:34:56.126")
			} else {
				value(a, "SELECT t FROM sp1.temporal WHERE t='12:34:56.126'", time.Date(0, 1, 1, 12, 34, 56, 126000000, time.UTC))
			}
			value(a, "SELECT ts FROM sp1.temporal WHERE t='12:34:56.126'", time.Date(2000, 1, 1, 12, 34, 56, 126000000, time.UTC))
			if !backend.PG {
				if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil {
					t.Fatal("invalid restore succeeded")
				}
				value(a, "SELECT id FROM sp1.t", int64(7))
			}
		})
	}
}
