package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceControlTransactionsHostAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"owner", "reader"})
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			admin, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			defer admin.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, s) }
			for _, q := range []string{"CREATE USER owner", "CREATE USER reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE secret(id INT)", "INSERT INTO secret VALUES(7)"} {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
				}
				if q == "CREATE TABLE secret(id INT)" {
					fixture.WithPublicSchemaOwner(t, p, backend, "owner", func() { run(admin, fixture.Exec(q, n)) })
				} else {
					run(admin, fixture.Exec(q, n))
				}
			}
			var pool *sql.DB
			var nativeReader *sql.DB
			if !backend.PG {
				d := p.NativeConnector.Driver().(*stuntgres.Driver)
				connector, err := d.OpenConnectorAs(dsn, "reader")
				if err != nil {
					t.Fatal(err)
				}
				nativeReader = sql.OpenDB(connector)
				defer nativeReader.Close()
				wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{connector}, fixture.Interceptor{})
				configured, err := wrapped.(driver.DriverContext).OpenConnector("")
				if err != nil {
					t.Fatal(err)
				}
				pool = sql.OpenDB(configured)
				defer pool.Close()
			} else {
				pool = backend.DB
			}
			pool.SetMaxOpenConns(2)
			a, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if backend.PG {
				run(a, fixture.Exec("SET SESSION AUTHORIZATION reader", 0))
				defer a.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			}
			run(a, fixture.Exec("SET application_name TO retained", 0))
			for _, q := range []string{"SELECT id FROM secret", "INSERT INTO secret VALUES(8)"} {
				stmt, err := a.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("valid Prepare must succeed", err)
				}
				_, err = stmt.ExecContext(p.Ctx)
				stmt.Close()
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table secret")
				fixture.AssertDiagnostic(t, err, &want)
			}
			if !backend.PG {
				if err = stuntgres.Reset(p.Ctx, nativeReader); err != nil {
					t.Fatal("host reset depends on SQL role", err)
				}
				if err = stuntgres.Restore(p.Ctx, nativeReader, strings.NewReader("SET SESSION AUTHORIZATION owner; CREATE TABLE secret(id INT); INSERT INTO secret VALUES(99);")); err != nil {
					t.Fatal("host restore depends on SQL role", err)
				}
			} else {
				// PostgreSQL has no host catalog replacement API: the actual owner updates
				// its table, without conferring any SQL authority on the retained reader.
				run(admin, fixture.Exec("DELETE FROM secret", 1))
				run(admin, fixture.Exec("INSERT INTO secret VALUES(99)", 1))
			}
			identity := fixture.Select("SELECT session_user,current_user,session_user", []string{"session_user", "current_user", "session_user"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"reader", "reader", "reader"}})
			identity.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				scan := reflect.TypeFor[string]()
				if backend.PG {
					scan = reflect.TypeFor[any]()
				}
				for _, typ := range types {
					if typ.ScanType() != scan {
						t.Fatalf("NAME ScanType %v want %v", typ.ScanType(), scan)
					}
				}
			}
			identity.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var first, current, last string
				if err := rows.Scan(&first, &current, &last); err != nil || first != "reader" || current != "reader" || last != "reader" {
					t.Fatal(first, current, last, err)
				}
			}
			run(a, identity)
			var setting string
			if err = a.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "retained" {
				t.Fatal(setting, err)
			}
			run(a, fixture.ErrorDiagnostic("INSERT INTO secret VALUES(8)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table secret")))
			run(admin, fixture.Select("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
		})
	}
}
