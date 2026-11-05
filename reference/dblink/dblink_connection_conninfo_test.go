package reference_test

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionConninfo(t *testing.T) {
	for _, tc := range []struct{ name, input, detail string }{
		{"missing-equals", "dbname", `missing "=" after "dbname" in connection info string`},
		{"unclosed-quote", "dbname='x", `unterminated quoted string in connection info string`},
		{"unknown-key", "dbc_unknown=x", `invalid connection option "dbc_unknown"`},
		{"uri-port", "postgresql://localhost:invalid/postgres", `invalid integer value "invalid" for connection option "port"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connectionCase(t, false, func(s *connectionScenario) { s.run(parserFailure(tc.input, tc.detail)); s.list() })
		})
	}
	t.Run("real-keyword-uri", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			const role = "dbc_remote_'role"
			const password = `p'a\ss:@/ ?#%`
			if _, err := fixture.Admin.ExecContext(s.p.Ctx, "CREATE ROLE "+pq.QuoteIdentifier(role)+" LOGIN PASSWORD "+pq.QuoteLiteral(password)); err != nil {
				s.t.Fatal(err)
			}
			defer func() {
				if _, err := fixture.Admin.ExecContext(s.p.Ctx, "DROP ROLE "+pq.QuoteIdentifier(role)); err != nil {
					s.t.Error(err)
				}
			}()
			base, err := pgconn.ParseConfig(s.conninfo("base", true))
			if err != nil {
				s.t.Fatal(err)
			}
			override, _ := url.Parse(connectionURI(base.Host, base.Port, "ignored", "wrong", base.Database, "uri_override"))
			// libpq URI queries percent-decode; unlike form encoding, '+' is
			// a literal plus, so the password's space must remain %20.
			parameters := override.Query()
			parameters.Set("user", role)
			parameters.Set("password", password)
			override.RawQuery = strings.ReplaceAll(parameters.Encode(), "+", "%20")
			for i, conninfo := range []string{
				fmt.Sprintf(" host = %s port = %d dbname = %s user = ignored user = %s password = %s sslmode = disable", connectionQuote(base.Host), base.Port, connectionQuote(base.Database), connectionQuote(role), connectionQuote(password)),
				connectionURI(base.Host, base.Port, role, password, base.Database, "uri_reserved"),
				override.String(),
			} {
				app := fmt.Sprintf("dbc_reserved_%d", connectionSequence.Add(1))
				if i == 0 {
					conninfo += " application_name=" + app
				} else {
					u, _ := url.Parse(conninfo)
					q := u.Query()
					q.Set("application_name", app)
					u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
					conninfo = u.String()
				}
				s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "OK", "reserved", conninfo))
				var pid int32
				var user, db string
				if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pid,usename,datname FROM pg_stat_activity WHERE application_name=$1", app).Scan(&pid, &user, &db); err != nil || user != role || db != base.Database || pid <= 0 {
					s.t.Fatal("reserved credential identity", pid, user, db, err)
				}
				if !s.b.PG && s.wire(pid).Info().CredentialMechanism != "scram-sha-256" {
					s.t.Fatal("supplied secret is not actual auth proof", s.wire(pid).Info())
				}
				s.disconnect(new("reserved"), pid)
			}
			bad := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=wrong sslmode=disable", connectionQuote(base.Host), base.Port, connectionQuote(base.Database), connectionQuote(role))
			s.externalFailure("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "retry", bad)
			s.list()
			good := connectionURI(base.Host, base.Port, role, password, base.Database, "dbc_retry_"+strconv.FormatUint(connectionSequence.Add(1), 10))
			s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "OK", "retry", good))
			s.run(connectionText("SELECT dbc_ext.dblink_disconnect($1::text) AS value", "OK", "retry"))
			s.list()
		})
	})
	t.Run("unreachable-and-empty", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			s.externalFailure("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "unreachable", "host=/dbc_nonexistent_socket port=5432 dbname=stuntgres_reference connect_timeout=1")
			s.externalFailure("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "ipv6", "postgresql://stuntgres:stuntgres@[::1]:1/stuntgres_reference?sslmode=disable&connect_timeout=1")
			// PG's server-account default role postgres is fixture-owned NOLOGIN.
			// Native host defaults are explicitly an unavailable external socket.
			// Neither route may reinterpret empty text as the fixture DSN.
			if !s.b.PG {
				s.t.Setenv("PGHOST", "/dbc_nonexistent_socket")
				s.t.Setenv("PGHOSTADDR", "")
				s.t.Setenv("PGSERVICE", "")
			}
			s.externalFailure("SELECT dbc_ext.dblink_connect($1::text) AS value", "")
			s.list()
		})
	})
	t.Run("alias-authentic-mapping", func(t *testing.T) {
		connectionCase(t, true, connectionAliasPhysical, registerConnectionAlias)
	})
	t.Run("alias-mapping-precedence", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			s.exec("CREATE ROLE dbc_reader")
			s.exec("GRANT USAGE ON SCHEMA dbc_ext TO dbc_reader")
			s.exec("CREATE FOREIGN DATA WRAPPER dbc_metadata NO HANDLER NO VALIDATOR")
			defer s.exec("DROP FOREIGN DATA WRAPPER dbc_metadata CASCADE")
			s.exec("CREATE SERVER dbc_alias FOREIGN DATA WRAPPER dbc_metadata OPTIONS (dbname 'not_opened')")
			s.exec("REVOKE USAGE ON FOREIGN SERVER dbc_alias FROM PUBLIC")
			app, pid := s.open(new("alias"))
			before := s.activity()
			s.exec("SET SESSION AUTHORIZATION dbc_reader")
			defer s.exec("RESET SESSION AUTHORIZATION")
			s.run(connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "42704", `user mapping not found for user "dbc_reader", server "dbc_alias"`, "alias", "dbc_alias"))
			s.list("alias")
			s.unchangedActivity(before)
			if s.pid(app) != pid {
				s.t.Fatal("alias resolution error replaced existing named connection")
			}
			s.exec("RESET SESSION AUTHORIZATION")
			s.disconnect(new("alias"), pid)
		})
	})
}

// Only remote address/port/libpq FATAL-envelope Detail is environment-dependent.
// The complete remaining SQL diagnostic is independently fixed; empty Detail is
// itself a failure. Parser Detail never uses this comparison limit.
func (s *connectionScenario) externalFailure(q string, args ...any) {
	s.t.Helper()
	var err error
	if s.prepared {
		st, e := s.c.PrepareContext(s.p.Ctx, q)
		if e != nil {
			s.t.Fatal(e)
		}
		defer st.Close()
		r, e := st.QueryContext(s.p.Ctx, args...)
		err = e
		if r != nil {
			r.Close()
		}
	} else {
		r, e := s.c.QueryContext(s.p.Ctx, q, args...)
		err = e
		if r != nil {
			r.Close()
		}
	}
	if err == nil {
		s.t.Fatal("external failure published success")
	}
	got := fixture.ExtractDiagnostic(s.t, err)
	if got.Detail == "" {
		s.t.Fatal("missing genuine remote failure detail", got)
	}
	got.Detail = ""
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08001", Message: "could not establish connection"}
	if got != want {
		s.t.Fatalf("external failure diagnostic %#v want %#v", got, want)
	}
}
