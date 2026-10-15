package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceCommitTimeStartup(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SET track_commit_timestamp=off", "SET track_commit_timestamp=on", "SET LOCAL track_commit_timestamp=off", "SET track_commit_timestamp=DEFAULT", "SET track_commit_timestamp='garbage'", "RESET track_commit_timestamp", "SELECT set_config('track_commit_timestamp','off',false)", "SELECT set_config('track_commit_timestamp','garbage',true)"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55P02", Message: `parameter "track_commit_timestamp" cannot be changed without restarting the server`})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
			}
		}
		fixture.LockExec(t, p.Ctx, a, "RESET ALL", 0)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT current_setting('track_commit_timestamp')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"on"}}))
		rows, err := a.QueryContext(p.Ctx, "SHOW ALL")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var name, value, description string
			if err := rows.Scan(&name, &value, &description); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if name == "track_commit_timestamp" {
				found = true
				if value != "on" || description != "Collects transaction commit time." {
					t.Fatalf("startup setting %q %q", value, description)
				}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if !found {
			t.Fatal("SHOW ALL omitted track_commit_timestamp")
		}
		b.DB.SetMaxIdleConns(0)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		if err := observer.Close(); err != nil {
			t.Fatal(err)
		}
		reopened := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, reopened, b.Recorder, fixture.Select("SHOW track_commit_timestamp", []string{"track_commit_timestamp"}, []string{"TEXT"}, [][]any{{"on"}}))
		// Constructor configuration is a native host API, not a PostgreSQL SET.
		if !b.PG {
			config := stuntgres.DriverConfig{TrackCommitTimestamp: true}
			configured := stuntgres.NewDriverWithConfig(config)
			config.TrackCommitTimestamp = false
			for _, tc := range []struct {
				driver *stuntgres.Driver
				want   string
			}{
				{stuntgres.NewDriver(), "off"}, {&stuntgres.Driver{}, "off"}, {configured, "on"},
			} {
				for _, key := range []string{"opaque-database", "track_commit_timestamp=on"} {
					connector, err := tc.driver.OpenConnector(key)
					if err != nil {
						t.Fatal(err)
					}
					db := sql.OpenDB(connector)
					var got string
					err = db.QueryRowContext(p.Ctx, "SHOW track_commit_timestamp").Scan(&got)
					db.Close()
					if err != nil || got != tc.want {
						t.Fatalf("startup %q got %q want %q: %v", key, got, tc.want, err)
					}
				}
			}
		}
	})
}

func TestReferenceCommitTimeMetadata(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q            string
				names, types []string
				values       [][]any
			}{
				{"SELECT pg_xact_commit_timestamp(NULL), pg_xact_commit_timestamp_origin(NULL)", []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin"}, []string{"TIMESTAMPTZ", "RECORD"}, [][]any{{nil, nil}}},
				{"SELECT pg_xact_commit_timestamp_origin('1'::xid)", []string{"pg_xact_commit_timestamp_origin"}, []string{"RECORD"}, [][]any{{"(,)"}}},
				{"SELECT (pg_xact_commit_timestamp_origin(NULL)).*", []string{"timestamp", "roident"}, []string{"TIMESTAMPTZ", "OID"}, [][]any{{nil, nil}}},
				{"SELECT * FROM pg_xact_commit_timestamp_origin('1'::xid)", []string{"timestamp", "roident"}, []string{"TIMESTAMPTZ", "OID"}, [][]any{{nil, nil}}},
				{"SELECT * FROM pg_xact_commit_timestamp_origin(NULL) AS f(stamp, origin)", []string{"stamp", "origin"}, []string{"TIMESTAMPTZ", "OID"}, [][]any{{nil, nil}}},
				{"SELECT (pg_last_committed_xact()).* LIMIT 0", []string{"xid", "timestamp", "roident"}, []string{"XID", "TIMESTAMPTZ", "OID"}, nil},
				{"SELECT * FROM pg_last_committed_xact() LIMIT 0", []string{"xid", "timestamp", "roident"}, []string{"XID", "TIMESTAMPTZ", "OID"}, nil},
				{"SELECT pg_last_committed_xact() LIMIT 0", []string{"pg_last_committed_xact"}, []string{"RECORD"}, nil},
			} {
				s := fixture.Select(tc.q, tc.names, tc.types, tc.values)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, c := range columns {
						want := reflect.TypeFor[any]()
						switch c.DatabaseTypeName() {
						case "TIMESTAMPTZ":
							want = reflect.TypeFor[time.Time]()
						case "XID":
							if !b.PG {
								want = reflect.TypeFor[[]byte]()
							}
						case "RECORD":
							if !b.PG {
								want = reflect.TypeFor[string]()
							}
						case "OID":
							if !b.PG {
								want = reflect.TypeFor[int64]()
							}
						}
						if c.ScanType() != want {
							t.Fatalf("%s ScanType %v want %v", c.DatabaseTypeName(), c.ScanType(), want)
						}
						if nullable, known := c.Nullable(); nullable || known {
							t.Fatalf("%s Nullable %v/%v", c.Name(), nullable, known)
						}
						if length, known := c.Length(); length != 0 || known {
							t.Fatalf("%s Length %d/%v", c.Name(), length, known)
						}
						if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
							t.Fatalf("%s DecimalSize %d/%d/%v", c.Name(), precision, scale, known)
						}
					}
				}
				s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
					if tc.types[column] != "RECORD" || value == nil {
						return false
					}
					if b.PG {
						v, ok := value.([]byte)
						if !ok || string(v) != "(,)" {
							t.Fatalf("pq record %T %v", value, value)
						}
					} else if v, ok := value.(string); !ok || v != "(,)" {
						t.Fatalf("native record %T %v", value, value)
					}
					return true
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
			}
		}
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE commit_time_metadata(v INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_metadata VALUES(1)", 1)
		r := commitTimeLast(t, p, observer)
		for _, zone := range []string{"UTC", "America/New_York"} {
			fixture.LockExec(t, p.Ctx, a, "SET TIME ZONE '"+zone+"'", 0)
			commitTimeLookup(t, p, a, r)
			var text string
			if err := a.QueryRowContext(p.Ctx, "SELECT pg_xact_commit_timestamp_origin($1)::text", r.xid).Scan(&text); err != nil {
				t.Fatal(err)
			}
			var expected string
			if err := a.QueryRowContext(p.Ctx, "SELECT ROW(pg_xact_commit_timestamp($1),'0'::oid)::text", r.xid).Scan(&expected); err != nil {
				t.Fatal(err)
			}
			if text != expected {
				t.Fatalf("record formatting %q want %q", text, expected)
			}
		}
	})
}

func TestReferenceCommitTimePublication(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		var previous string
		for _, q := range []string{"CREATE TABLE commit_time_publication(v INT)", "INSERT INTO commit_time_publication VALUES(1)", "CREATE TABLE commit_time_initial AS SELECT v FROM commit_time_publication", "ALTER TABLE commit_time_publication ADD COLUMN extra INT"} {
			before := commitTimeClock(t, p, observer)
			if _, err := a.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(q, err)
			}
			after := commitTimeClock(t, p, observer)
			r := commitTimeLast(t, p, observer)
			commitTimeBounded(t, r, before, after)
			if r.xid == previous {
				t.Fatalf("publication reused previous commit: %s", q)
			}
			previous = r.xid
			commitTimeLookup(t, p, observer, r)
			commitTimeSame(t, commitTimeLast(t, p, a), r)
		}
		baseline := commitTimeLast(t, p, observer)
		_, err := a.ExecContext(p.Ctx, "CREATE TABLE commit_time_publication(v INT)")
		fixture.State(t, err, "42P07")
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v FROM commit_time_initial", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceCommitTimeReplay(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE commit_time_replay(v INT, committed TIMESTAMPTZ DEFAULT pg_xact_commit_timestamp('1'::xid))", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW commit_time_live AS SELECT * FROM pg_last_committed_xact()", 0)
		stmt, err := observer.PrepareContext(p.Ctx, "SELECT * FROM pg_last_committed_xact()")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, v := range []string{"1", "2"} {
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_replay(v) VALUES("+v+")", 1)
			r := commitTimeLast(t, p, a)
			var got commitTimeRecord
			if err := stmt.QueryRowContext(p.Ctx).Scan(&got.xid, &got.stamp, &got.origin); err != nil {
				t.Fatal(err)
			}
			commitTimeSame(t, got, r)
			if err := observer.QueryRowContext(p.Ctx, "SELECT * FROM commit_time_live").Scan(&got.xid, &got.stamp, &got.origin); err != nil {
				t.Fatal(err)
			}
			commitTimeSame(t, got, r)
		}
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT committed FROM commit_time_replay ORDER BY v", []string{"committed"}, []string{"TIMESTAMPTZ"}, [][]any{{nil}, {nil}}))
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE INDEX commit_time_volatile ON commit_time_replay ((pg_xact_commit_timestamp('1'::xid)))", "42P17", "functions in index expression must be marked IMMUTABLE"},
			{"CREATE TABLE commit_time_generated(v INT, stamp TIMESTAMPTZ GENERATED ALWAYS AS (pg_xact_commit_timestamp('1'::xid)) STORED)", "42P17", "generation expression is not immutable"},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
		}
	})
}
