package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceControlTransactionsHostAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"owner", "reader"})
	p := newReferenceScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			admin, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			defer admin.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
			if backend.pg {
				run(admin, referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			for _, q := range []string{"CREATE USER owner", "CREATE USER reader", "SET SESSION AUTHORIZATION owner", "CREATE TABLE secret(id INT)", "INSERT INTO secret VALUES(7)"} {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
				}
				run(admin, referenceExec(q, n))
			}
			var pool *sql.DB
			var nativeReader *sql.DB
			if !backend.pg {
				d := p.nativeConnector.Driver().(*stuntgres.Driver)
				connector, err := d.OpenConnectorAs(dsn, "reader")
				if err != nil {
					t.Fatal(err)
				}
				nativeReader = sql.OpenDB(connector)
				defer nativeReader.Close()
				wrapped := sqlmw.Driver(noticeReferenceDriver{connector}, referenceInterceptor{})
				configured, err := wrapped.(driver.DriverContext).OpenConnector("")
				if err != nil {
					t.Fatal(err)
				}
				pool = sql.OpenDB(configured)
				defer pool.Close()
			} else {
				pool = backend.db
			}
			pool.SetMaxOpenConns(2)
			a, err := pool.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if backend.pg {
				run(a, referenceExec("SET SESSION AUTHORIZATION reader", 0))
				defer a.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			}
			run(a, referenceExec("SET application_name TO retained", 0))
			for _, q := range []string{"SELECT id FROM secret", "INSERT INTO secret VALUES(8)"} {
				stmt, err := a.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal("valid Prepare must succeed", err)
				}
				_, err = stmt.ExecContext(p.ctx)
				stmt.Close()
				want := controlsReferenceDiagnostic("42501", "permission denied for table secret")
				referenceAssertDiagnostic(t, err, &want)
			}
			if !backend.pg {
				if err = stuntgres.Reset(p.ctx, nativeReader); err != nil {
					t.Fatal("host reset depends on SQL role", err)
				}
				if err = stuntgres.Restore(p.ctx, nativeReader, strings.NewReader("SET SESSION AUTHORIZATION owner; CREATE TABLE secret(id INT); INSERT INTO secret VALUES(99);")); err != nil {
					t.Fatal("host restore depends on SQL role", err)
				}
			} else {
				// PostgreSQL has no host catalog replacement API: the actual owner updates
				// its table, without conferring any SQL authority on the retained reader.
				run(admin, referenceExec("DELETE FROM secret", 1))
				run(admin, referenceExec("INSERT INTO secret VALUES(99)", 1))
			}
			identity := referenceSelect("SELECT session_user,current_user,session_user", []string{"session_user", "current_user", "session_user"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"reader", "reader", "reader"}})
			identity.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				scan := reflect.TypeFor[string]()
				if backend.pg {
					scan = reflect.TypeFor[any]()
				}
				for _, typ := range types {
					if typ.ScanType() != scan {
						t.Fatalf("NAME ScanType %v want %v", typ.ScanType(), scan)
					}
				}
			}
			identity.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var first, current, last string
				if err := rows.Scan(&first, &current, &last); err != nil || first != "reader" || current != "reader" || last != "reader" {
					t.Fatal(first, current, last, err)
				}
			}
			run(a, identity)
			var setting string
			if err = a.QueryRowContext(p.ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "retained" {
				t.Fatal(setting, err)
			}
			run(a, referenceErrorDiagnostic("INSERT INTO secret VALUES(8)", controlsReferenceDiagnostic("42501", "permission denied for table secret")))
			run(admin, referenceSelect("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
		})
	}
}
