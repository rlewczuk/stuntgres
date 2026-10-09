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
	"testing"
)

func TestReferenceSessionIdentityDefaultsViewsAndControls(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap, database := "postgres", "reference/"+strings.TrimSuffix(t.Name(), "/"+backend.name)
			database = database[:min(len(database), 63)]
			var system any
			if backend.pg {
				bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
			run(referenceExec("CREATE ROLE identity_reader", 0))
			if backend.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO identity_reader", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION identity_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(referenceExec("CREATE TABLE identity_default(id INT,who TEXT DEFAULT current_user,session_name TEXT DEFAULT session_user)", 0))
			run(referenceExec("CREATE VIEW identity_view AS SELECT current_user AS effective,system_user AS auth FROM identity_default", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("SET ROLE identity_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			var current, session string
			if err := conn.QueryRowContext(p.ctx, "SELECT current_user,session_user").Scan(&current, &session); err != nil || current != "identity_reader" || session != bootstrap {
				t.Fatal(current, session, err)
			}
			run(referenceExec("INSERT INTO identity_default(id) VALUES(1)", 1))
			const query = "SELECT effective,auth FROM identity_view"
			held, err := conn.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			assert := func(want string) {
				var auth any
				if err := held.QueryRowContext(p.ctx).Scan(&current, &auth); err != nil || current != want || referenceNormalize(auth) != system {
					t.Fatal(current, auth, err)
				}
				s := referenceSelect(query, []string{"effective", "auth"}, []string{"NAME", "TEXT"}, [][]any{{want, system}})
				s.checkMetadata = sessionIdentityMetadata(backend.pg)
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}, backend.recorder, s)
			}
			assert("identity_reader")
			var retained driver.Rows
			if !backend.pg {
				if err := conn.Raw(func(raw any) error {
					var e error
					retained, e = raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT current_user,current_catalog,system_user", nil)
					return e
				}); err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "SET ROLE") || strings.Contains(dump.String(), database) {
					t.Fatal("serialized session state", dump.String())
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			if err := conn.QueryRowContext(p.ctx, "SELECT who,session_name FROM identity_default").Scan(&current, &session); err != nil || current != "identity_reader" || session != bootstrap {
				t.Fatal(current, session, err)
			}
			assert("identity_reader")
			run(referenceExec("RESET ROLE", 0))
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
			run(referenceExec("SET ROLE identity_reader", 0))
			run(referenceExec("INSERT INTO identity_default(id) VALUES(2)", 1))
			s := referenceSelect("SELECT who,session_name FROM identity_default ORDER BY id", []string{"who", "session_name"}, []string{"TEXT", "TEXT"}, [][]any{{"identity_reader", bootstrap}, {"identity_reader", bootstrap}})
			s.checkMetadata = sessionIdentityMetadata(backend.pg)
			s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
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
	roleReferenceCleanup(t, []string{"identity_control_owner"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap, database := "postgres", "reference/"+strings.TrimSuffix(t.Name(), "/"+backend.name)
			database = database[:min(len(database), 63)]
			var system any
			if backend.pg {
				bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
			run(referenceExec("CREATE ROLE identity_control_owner", 0))
			if backend.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO identity_control_owner", 0))
			}
			for _, q := range []string{"SET SESSION AUTHORIZATION identity_control_owner", "CREATE TABLE identity_sentinel(id INT,who TEXT DEFAULT current_user)", "INSERT INTO identity_sentinel(id) VALUES(77)", "RESET SESSION AUTHORIZATION", "SET ROLE identity_control_owner"} {
				run(referenceExec(q, map[bool]int64{true: 1, false: 0}[strings.HasPrefix(q, "INSERT")]))
			}
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			query := "SELECT current_user,current_catalog,session_user,system_user,id,who FROM identity_sentinel"
			var retained driver.Rows
			if !backend.pg {
				if err := conn.Raw(func(raw any) error {
					var e error
					retained, e = raw.(driver.QueryerContext).QueryContext(p.ctx, query, nil)
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
				if err := conn.QueryRowContext(p.ctx, query).Scan(&current, &name, &session, &auth, &id, &who); err != nil || current != "identity_control_owner" || name != database || session != bootstrap || referenceNormalize(auth) != system || id != 77 || who != "identity_control_owner" {
					t.Fatal(current, name, session, auth, id, who, err)
				}
				s := referenceSelect(query, []string{"current_user", "current_catalog", "session_user", "system_user", "id", "who"}, []string{"NAME", "NAME", "NAME", "TEXT", "INT4", "TEXT"}, [][]any{{"identity_control_owner", database, bootstrap, system, int64(77), "identity_control_owner"}})
				s.checkMetadata = sessionIdentityMetadata(backend.pg)
				run(s)
			}
			script := "CREATE TABLE identity_replacement(id INT,who TEXT DEFAULT current_user); INSERT INTO identity_replacement(id) VALUES(1); INSERT INTO identity_replacement(id) VALUES(1/0);"
			if !backend.pg {
				referenceState(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)), "22012")
				assert()
				canceled, cancel := context.WithCancel(p.ctx)
				defer cancel()
				reader := &identityCancelReader{reader: strings.NewReader("CREATE TABLE identity_replacement(id INT);"), cancel: cancel}
				if err := stuntgres.Restore(canceled, p.native, reader); !reader.observed || !errors.Is(err, context.Canceled) {
					t.Fatal(reader.observed, err)
				}
			} else {
				tx, err := conn.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				_, err = tx.ExecContext(p.ctx, script)
				referenceState(t, err, "22012")
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
