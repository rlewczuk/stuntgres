package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Install the handler on the connector before sql.DB opens its physical session:
// attaching it through Conn.Raw would miss diagnostics emitted by Connect.
func rsStartupPool(t *testing.T, p *fixture.Pair, b fixture.Backend, login, options string, notices *[]fixture.Diagnostic) *sql.DB {
	t.Helper()
	var connector driver.Connector
	if b.PG {
		u, err := url.Parse(fixture.DSN)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(login, "role-attributes-fixture")
		// Fixture DSN's empty application_name is ignored by pq but sent by
		// pgx as a client override. Omit it to observe catalog login defaults.
		query := u.Query()
		query.Del("application_name")
		u.RawQuery = query.Encode()
		if options != "" {
			q := u.Query()
			q.Set("options", options)
			u.RawQuery = q.Encode()
		}
		if options != "" {
			// pq's notice connector installs its handler after Connect. This
			// phase exercises pq options, not startup notice delivery.
			base, err := pq.NewConnector(u.String())
			if err != nil {
				t.Fatal(err)
			}
			connector = pq.ConnectorWithNoticeHandler(base, func(n *pq.Error) {
				*notices = append(*notices, fixture.ExtractDiagnostic(t, n))
			})
		} else {
			// pgx's public OnNotice is present during the startup exchange.
			config, err := pgx.ParseConfig(u.String())
			if err != nil {
				t.Fatal(err)
			}
			config.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
				*notices = append(*notices, fixture.ExtractDiagnostic(t, (*pgconn.PgError)(n)))
			}
			connector = stdlib.GetConnector(*config)
		}
	} else {
		if options != "" {
			t.Fatal("native public Connector has no startup-options API")
		}
		base, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(p.NativeDSN, login)
		if err != nil {
			t.Fatal(err)
		}
		connector = base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) {
			*notices = append(*notices, fixture.ExtractDiagnostic(t, &n))
		})
	}
	wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	t.Cleanup(func() { db.Close() })
	return db
}

// Startup notice capture uses pgx: unlike pq it exports NAME as a Go string.
// Native NAME has that same representation. Preserve metadata and full rows.
func rsStartupStep(s fixture.Step) fixture.Step {
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if s.Types[column] != "NAME" {
			return false
		}
		if _, ok := value.(string); !ok {
			t.Fatalf("startup NAME raw value %T %#v, want string", value, value)
		}
		return true
	}
	return s
}
