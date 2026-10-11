package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityDefaultsViewsAndControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"identity_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap, database := "postgres", "reference/"+strings.TrimSuffix(t.Name(), "/"+backend.Name)
			database = database[:min(len(database), 63)]
			var system any
			if backend.PG {
				bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(fixture.Exec("CREATE ROLE identity_reader", 0))
			if backend.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO identity_reader", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION identity_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Exec("CREATE TABLE identity_default(id INT,who TEXT DEFAULT current_user,session_name TEXT DEFAULT session_user)", 0))
			run(fixture.Exec("CREATE VIEW identity_view AS SELECT current_user AS effective,system_user AS auth FROM identity_default", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET ROLE identity_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			var current, session string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_user,session_user").Scan(&current, &session); err != nil || current != "identity_reader" || session != bootstrap {
				t.Fatal(current, session, err)
			}
			run(fixture.Exec("INSERT INTO identity_default(id) VALUES(1)", 1))
			const query = "SELECT effective,auth FROM identity_view"
			held, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			assert := func(want string) {
				var auth any
				if err := held.QueryRowContext(p.Ctx).Scan(&current, &auth); err != nil || current != want || fixture.Normalize(auth) != system {
					t.Fatal(current, auth, err)
				}
				s := fixture.Select(query, []string{"effective", "auth"}, []string{"NAME", "TEXT"}, [][]any{{want, system}})
				s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, backend.Recorder, s)
			}
			assert("identity_reader")
			var retained driver.Rows
			if !backend.PG {
				if err := conn.Raw(func(raw any) error {
					var e error
					retained, e = raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT current_user,current_catalog,system_user", nil)
					return e
				}); err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "SET ROLE") || strings.Contains(dump.String(), database) {
					t.Fatal("serialized session state", dump.String())
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			if err := conn.QueryRowContext(p.Ctx, "SELECT who,session_name FROM identity_default").Scan(&current, &session); err != nil || current != "identity_reader" || session != bootstrap {
				t.Fatal(current, session, err)
			}
			assert("identity_reader")
			run(fixture.Exec("RESET ROLE", 0))
			assert(bootstrap)
			if retained != nil {
				values := make([]driver.Value, 3)
				if err := retained.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{"identity_reader", database, nil}) {
					t.Fatal(values, err)
				}
				if err := retained.Next(values); err != io.EOF {
					t.Fatal(err)
				}
				if err := retained.Close(); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("SET ROLE identity_reader", 0))
			run(fixture.Exec("INSERT INTO identity_default(id) VALUES(2)", 1))
			s := fixture.Select("SELECT who,session_name FROM identity_default ORDER BY id", []string{"who", "session_name"}, []string{"TEXT", "TEXT"}, [][]any{{"identity_reader", bootstrap}, {"identity_reader", bootstrap}})
			s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
			s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var who, session string
				if err := rows.Scan(&who, &session); err != nil || who != "identity_reader" || session != bootstrap {
					t.Fatal(who, session, err)
				}
			}
			run(s)
		})
	}
}

type identityCancelReader struct {
	reader   *strings.Reader
	cancel   context.CancelFunc
	observed bool
}

func (r *identityCancelReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.observed = true
	r.cancel()
	return n, err
}

func TestReferenceSessionIdentityFailedAndCanceledControlsPreserveState(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"identity_control_owner"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap, database := "postgres", "reference/"+strings.TrimSuffix(t.Name(), "/"+backend.Name)
			database = database[:min(len(database), 63)]
			var system any
			if backend.PG {
				bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(fixture.Exec("CREATE ROLE identity_control_owner", 0))
			if backend.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO identity_control_owner", 0))
			}
			for _, q := range []string{"SET SESSION AUTHORIZATION identity_control_owner", "CREATE TABLE identity_sentinel(id INT,who TEXT DEFAULT current_user)", "INSERT INTO identity_sentinel(id) VALUES(77)", "RESET SESSION AUTHORIZATION", "SET ROLE identity_control_owner"} {
				run(fixture.Exec(q, map[bool]int64{true: 1, false: 0}[strings.HasPrefix(q, "INSERT")]))
			}
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			query := "SELECT current_user,current_catalog,session_user,system_user,id,who FROM identity_sentinel"
			var retained driver.Rows
			if !backend.PG {
				if err := conn.Raw(func(raw any) error {
					var e error
					retained, e = raw.(driver.QueryerContext).QueryContext(p.Ctx, query, nil)
					return e
				}); err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			}
			assert := func() {
				var current, name, session, who string
				var auth any
				var id int
				if err := conn.QueryRowContext(p.Ctx, query).Scan(&current, &name, &session, &auth, &id, &who); err != nil || current != "identity_control_owner" || name != database || session != bootstrap || fixture.Normalize(auth) != system || id != 77 || who != "identity_control_owner" {
					t.Fatal(current, name, session, auth, id, who, err)
				}
				s := fixture.Select(query, []string{"current_user", "current_catalog", "session_user", "system_user", "id", "who"}, []string{"NAME", "NAME", "NAME", "TEXT", "INT4", "TEXT"}, [][]any{{"identity_control_owner", database, bootstrap, system, int64(77), "identity_control_owner"}})
				s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				run(s)
			}
			script := "CREATE TABLE identity_replacement(id INT,who TEXT DEFAULT current_user); INSERT INTO identity_replacement(id) VALUES(1); INSERT INTO identity_replacement(id) VALUES(1/0);"
			if !backend.PG {
				fixture.State(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), "22012")
				assert()
				canceled, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				reader := &identityCancelReader{reader: strings.NewReader("CREATE TABLE identity_replacement(id INT);"), cancel: cancel}
				if err := stuntgres.Restore(canceled, p.Native, reader); !reader.observed || !errors.Is(err, context.Canceled) {
					t.Fatal(reader.observed, err)
				}
			} else {
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				_, err = tx.ExecContext(p.Ctx, script)
				fixture.State(t, err, "22012")
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			assert()
			if retained != nil {
				metadata := retained.(driver.RowsColumnTypeDatabaseTypeName)
				for i, want := range []string{"NAME", "NAME", "NAME", "TEXT", "INT4", "TEXT"} {
					if got := metadata.ColumnTypeDatabaseTypeName(i); got != want {
						t.Fatal(i, got, want)
					}
				}
				values := make([]driver.Value, 6)
				if err := retained.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{"identity_control_owner", database, "postgres", nil, int64(77), "identity_control_owner"}) {
					t.Fatal(values, err)
				}
				if err := retained.Next(values); err != io.EOF {
					t.Fatal(err)
				}
			}
		})
	}
}
