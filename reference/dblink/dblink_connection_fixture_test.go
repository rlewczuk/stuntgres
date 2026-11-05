package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var connectionSequence atomic.Uint64

type connectionScenario struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	c        *sql.Conn
	db       *sql.DB
	closed   bool
	notices  *[]fixture.Diagnostic
	provider *fixture.ExternalSQLProvider
	prepared bool
}

func connectionCase(t *testing.T, physical bool, body func(*connectionScenario), configure ...func(*stuntgres.Driver) error) {
	t.Helper()
	if physical && !*fixture.Mode {
		t.Skip("genuine external PostgreSQL connections require REFERENCE=1")
	}
	fixture.SchemaReferenceCleanup(t, "dbc_ext", "dbc_moved")
	fixture.RoleReferenceCleanup(t, []string{"dbc_reader", "dbc_owner"})
	provider := &fixture.ExternalSQLProvider{}
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "dblink_fixture", Version: fixture.ExternalSQLFixtureVersion, Provider: provider}); err != nil {
			return err
		}
		if err := d.BindDblinkExternalSQLProvider("dblink_fixture"); err != nil {
			return err
		}
		for _, extra := range configure {
			if err := extra(d); err != nil {
				return err
			}
		}
		return nil
	})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
					var notices []fixture.Diagnostic
					db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					backend := b
					backend.Recorder = recorder
					s := &connectionScenario{t: t, p: p, b: backend, c: c, db: db, provider: provider, prepared: prepared, notices: &notices}
					defer db.Close()
					defer c.Close()
					cleanup := func() {
						if !s.closed {
							for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP TABLE IF EXISTS dbc_null_input"} {
								if _, err := c.ExecContext(p.Ctx, q); err != nil {
									t.Error(q, err)
								}
							}
						}
						for _, q := range []string{"DROP EXTENSION IF EXISTS dblink CASCADE", "DROP SCHEMA IF EXISTS dbc_ext CASCADE", "DROP SCHEMA IF EXISTS dbc_moved CASCADE", "DROP TABLE IF EXISTS dbc_marker CASCADE"} {
							if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
						for _, role := range []string{"dbc_reader", "dbc_owner"} {
							var exists bool
							if err := b.DB.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
								t.Error(err)
								continue
							}
							if exists {
								if _, err := b.DB.ExecContext(p.Ctx, "DROP ROLE "+role); err != nil {
									t.Error(err)
								}
							}
						}
					}
					cleanup()
					defer cleanup()
					s.exec("CREATE SCHEMA dbc_ext")
					s.exec("CREATE EXTENSION dblink SCHEMA dbc_ext")
					body(s)
				})
			}
		})
	}
}

func (s *connectionScenario) run(step fixture.Step) {
	s.t.Helper()
	step.Prepared = s.prepared
	fixture.RunStep(s.t, s.p.Ctx, s.c, s.b.Recorder, step)
}
func (s *connectionScenario) exec(q string) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.c, s.b.Recorder, fixture.Exec(q, 0))
}
func connectionText(q string, value any, args ...any) fixture.Step {
	step := fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{value}}, args...)
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { connectionTextMetadata(t, rows, false) }
	return step
}

// Settings providers retain native nullable metadata; dblink scalar kernels
// above expose unknown nullability. PostgreSQL reports unknown for both.
func connectionSettingText(q string, value any, pg bool) fixture.Step {
	step := fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{value}})
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { connectionTextMetadata(t, rows, !pg) }
	return step
}
func connectionTextMetadata(t *testing.T, rows *sql.Rows, nullableKnown bool) {
	t.Helper()
	m, err := rows.ColumnTypes()
	if err != nil || len(m) != 1 || m[0].Name() != "value" || m[0].DatabaseTypeName() != "TEXT" {
		t.Fatal("TEXT descriptor before Next", m, err)
	}
	fixture.ScalarAssertMetadata(t, false, m)
	if nullable, known := m[0].Nullable(); nullable != nullableKnown || known != nullableKnown {
		t.Fatal("TEXT nullable metadata", nullable, known, "want", nullableKnown, nullableKnown)
	}
	if length, known := m[0].Length(); length != int64(9223372036854775807) || !known {
		t.Fatal("TEXT length", length, known)
	}
	if precision, scale, known := m[0].DecimalSize(); precision != 0 || scale != 0 || known {
		t.Fatal("TEXT decimal metadata", precision, scale, known)
	}
}
func connectionError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}
func (s *connectionScenario) list(names ...string) {
	s.t.Helper()
	if len(names) == 0 {
		step := fixture.ArrayReferenceSelect("SELECT dbc_ext.dblink_get_connections() AS value", []string{"value"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{nil}}, s.b.PG)
		s.run(step)
		return
	}
	rows := make([][]any, len(names))
	for i, name := range names {
		rows[i] = []any{name}
	}
	step := fixture.Select("SELECT unnest(dbc_ext.dblink_get_connections()) AS value", []string{"value"}, []string{"TEXT"}, rows)
	step.Bag = true
	s.run(step)
}
func (s *connectionScenario) conninfo(app string, passwordRoute bool) string {
	s.t.Helper()
	if s.b.PG {
		host := "127.0.0.1"
		if passwordRoute {
			var err error
			host, err = fixture.Container.ContainerIP(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
		}
		return "host=" + host + " port=5432 dbname=stuntgres_reference user=stuntgres password=stuntgres sslmode=disable application_name=" + app
	}
	u, err := url.Parse(fixture.DSN)
	if err != nil {
		s.t.Fatal(err)
	}
	q := u.Query()
	q.Set("application_name", app)
	u.RawQuery = q.Encode()
	return u.String()
}
func (s *connectionScenario) open(name *string) (string, int32) {
	s.t.Helper()
	app := fmt.Sprintf("dbc_%d", connectionSequence.Add(1))
	conninfo := s.conninfo(app, false)
	q, args := "SELECT dbc_ext.dblink_connect($1::text) AS value", []any{conninfo}
	if name != nil {
		q, args = "SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", []any{*name, conninfo}
	}
	s.run(connectionText(q, "OK", args...))
	pid := s.pid(app)
	return app, pid
}
func (s *connectionScenario) pid(app string) int32 {
	s.t.Helper()
	var pid, observer int32
	var user, db string
	if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pg_backend_pid()").Scan(&observer); err != nil {
		s.t.Fatal(err)
	}
	if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT pid,usename,datname FROM pg_stat_activity WHERE application_name=$1", app).Scan(&pid, &user, &db); err != nil {
		s.t.Fatal("genuine backend identity", err)
	}
	if pid <= 0 || pid == observer || user != "stuntgres" || db != "stuntgres_reference" {
		s.t.Fatal("remote identity", pid, user, db, observer)
	}
	return pid
}
func connectionGone(t *testing.T, ctx context.Context, pid int32) {
	t.Helper()
	for {
		var n int
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("backend still live", pid, err)
		}
		runtime.Gosched()
	}
}
func (s *connectionScenario) wire(pid int32) *fixture.ExternalSQLWire {
	s.t.Helper()
	for _, c := range s.provider.Snapshot() {
		if c.Info().BackendPID == pid {
			return c
		}
	}
	s.t.Fatal("actual Provider.Open return missing", pid)
	return nil
}
func (s *connectionScenario) remote(name *string, pid int32, q string) {
	s.t.Helper()
	if s.b.PG {
		var tag string
		query, args := "SELECT dbc_ext.dblink_exec($1::text)", []any{q}
		if name != nil {
			query, args = "SELECT dbc_ext.dblink_exec($1::text,$2::text)", []any{*name, q}
		}
		if err := s.c.QueryRowContext(s.p.Ctx, query, args...).Scan(&tag); err != nil {
			s.t.Fatal("oracle-only remote observation", err)
		}
		return
	}
	if _, err := s.wire(pid).Execute(s.p.Ctx, q); err != nil {
		s.t.Fatal("retained genuine provider execution", err)
	}
}
func (s *connectionScenario) disconnect(name *string, pid int32) {
	s.t.Helper()
	q, args := "SELECT dbc_ext.dblink_disconnect() AS value", []any{}
	if name != nil {
		q, args = "SELECT dbc_ext.dblink_disconnect($1::text) AS value", []any{*name}
	}
	s.run(connectionText(q, "OK", args...))
	if !s.b.PG {
		if _, err := s.wire(pid).Execute(s.p.Ctx, "SELECT 1"); err == nil {
			s.t.Fatal("disconnected actual provider still executes")
		}
	}
	connectionGone(s.t, s.p.Ctx, pid)
}
func connectionQuote(value string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value) + "'"
}
func connectionURI(host string, port uint16, user, password, db, app string) string {
	u := &url.URL{Scheme: "postgresql", Host: net.JoinHostPort(host, strconv.Itoa(int(port))), User: url.UserPassword(user, password), Path: "/" + db}
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("application_name", app)
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	return u.String()
}

func (s *connectionScenario) activity() map[*fixture.ExternalSQLWire]fixture.ExternalSQLActivity {
	s.t.Helper()
	out := make(map[*fixture.ExternalSQLWire]fixture.ExternalSQLActivity)
	for _, wire := range s.provider.Snapshot() {
		out[wire] = wire.Activity()
	}
	return out
}
func (s *connectionScenario) unchangedActivity(before map[*fixture.ExternalSQLWire]fixture.ExternalSQLActivity) {
	s.t.Helper()
	wires := s.provider.Snapshot()
	if len(wires) != len(before) {
		s.t.Fatal("control opened a transport")
	}
	for _, wire := range wires {
		if got := wire.Activity(); got != before[wire] {
			s.t.Fatal("control performed transport I/O", got, before[wire])
		}
	}
}
