package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const postgresFDWProviderName = "postgres-fdw-real-fixture"

func postgresFDWConfigure(d *stuntgres.Driver, provider *fixture.ExternalSQLProvider, version string, bind bool) error {
	if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: postgresFDWProviderName, Version: version, Provider: provider}); err != nil {
		return err
	}
	if !bind {
		return nil
	}
	return d.BindPostgresFDWExternalSQLProvider(postgresFDWProviderName)
}

func postgresFDWEach(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend, *sql.Conn, bool, func(fixture.Step))) {
	t.Helper()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					cleanup := func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET search_path", "DROP EXTENSION IF EXISTS postgres_fdw CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS postgres_fdw CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS pf_alias CASCADE", "DROP SERVER IF EXISTS pf_server CASCADE", "DROP SERVER IF EXISTS pf_rejected CASCADE", "DROP SCHEMA IF EXISTS pf_helpers CASCADE", "DROP SCHEMA IF EXISTS pf_moved CASCADE", "DROP TABLE IF EXISTS pf_local CASCADE", "DROP FUNCTION IF EXISTS public.postgres_fdw_handler()", "DROP FUNCTION IF EXISTS public.postgres_fdw_validator(text[],oid)"} {
							if _, err := c.ExecContext(ctx, q); err != nil {
								t.Error("postgres_fdw cleanup:", q, err)
							}
						}
					}
					cleanup()
					defer cleanup()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					body(t, b, c, prepared, run)
				})
			}
		})
	}
}

func postgresFDWScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		body(t, p, b, c, prepared, run)
	})
}

func postgresFDWInstall(run func(fixture.Step)) {
	run(fixture.Exec("CREATE EXTENSION postgres_fdw", 0))
	run(fixture.Exec("CREATE SERVER pf_server FOREIGN DATA WRAPPER postgres_fdw", 0))
}

func postgresFDWCall(entries []string, class int) string {
	values := make([]string, len(entries))
	for i, entry := range entries {
		values[i] = fileFDWLiteral(entry)
	}
	return "SELECT postgres_fdw_validator(ARRAY[" + strings.Join(values, ",") + "]::text[]," + strconv.Itoa(class) + "::oid) AS validated"
}

func postgresFDWError(q, code, message string) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fdwDiagnostic(code, message))
	s.ErrorStage = "execute"
	return s
}

func postgresFDWInvalid(q, name, hint string) fixture.Step {
	s := postgresFDWError(q, "HV00D", `invalid option "`+name+`"`)
	s.Diagnostic.Hint = hint
	return s
}

func postgresFDWStored(run func(fixture.Step), class int, entries ...string) {
	query, name := "", ""
	switch class {
	case 1417:
		query, name = "SELECT srvoptions FROM pg_foreign_server WHERE srvname='pf_server'", "srvoptions"
	case 1418:
		query, name = "SELECT umoptions FROM pg_user_mapping WHERE umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='pf_server')", "umoptions"
	case 3118:
		query, name = "SELECT ftoptions FROM pg_foreign_table WHERE ftrelid='pf_table'::regclass", "ftoptions"
	case 1249:
		query, name = "SELECT attfdwoptions FROM pg_attribute WHERE attrelid='pf_table'::regclass AND attname='id'", "attfdwoptions"
	default:
		panic("unsupported option context")
	}
	var value any
	if len(entries) != 0 {
		value = relationOptionsArray(entries)
	}
	run(fixture.Select(query, []string{name}, []string{"_TEXT"}, [][]any{{value}}))
}

func postgresFDWPhysicalScenario(t *testing.T) (*fixture.Pair, *fixture.ExternalSQLProvider) {
	t.Helper()
	if !*fixture.Mode {
		t.Skip("genuine postgres_fdw physical phases require REFERENCE=1")
	}
	fixture.SchemaReferenceCleanup(t, `pf source "quoted`)
	provider := &fixture.ExternalSQLProvider{}
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		return postgresFDWConfigure(d, provider, fixture.ExternalSQLFixtureVersion, true)
	})
	for _, s := range []fixture.Step{
		fixture.Exec(`CREATE SCHEMA "pf source ""quoted"`, 0),
		fixture.Exec(`CREATE TABLE "pf source ""quoted"."data table"(remote_label text,unused integer,"Remote ID" integer NOT NULL,flag boolean,bytes bytea)`, 0),
		fixture.Exec(`INSERT INTO "pf source ""quoted"."data table" VALUES('x',91,1,true,decode('00ff','hex')),(NULL,92,2,false,NULL),(NULL,92,2,false,NULL),('',93,3,NULL,decode('','hex'))`, 4),
		fixture.Exec(`CREATE VIEW "pf source ""quoted".identity AS SELECT pg_backend_pid() AS pid,current_user::text AS remote_user,current_database()::text AS remote_database,current_setting('search_path') AS path,current_setting('TimeZone') AS zone,current_setting('DateStyle') AS dates,current_setting('IntervalStyle') AS intervals,current_setting('extra_float_digits') AS digits,current_setting('transaction_isolation') AS isolation`, 0),
	} {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
	}
	return p, provider
}

func postgresFDWPhysicalInstall(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	host, port := "127.0.0.1", "5432"
	config, err := pgconn.ParseConfig(fixture.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if !b.PG {
		host, port = config.Host, strconv.Itoa(int(config.Port))
	}
	run(fixture.Exec("CREATE EXTENSION postgres_fdw", 0))
	run(fixture.Exec("CREATE SERVER pf_server FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host "+fileFDWLiteral(host)+",port "+fileFDWLiteral(port)+",dbname 'stuntgres_reference',application_name 'pf_physical',fetch_size '2')", 0))
	run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(user "+fileFDWLiteral(config.User)+",password "+fileFDWLiteral(config.Password)+")", 0))
	run(fixture.Exec(`CREATE FOREIGN TABLE pf_table(id integer OPTIONS(column_name 'Remote ID') NOT NULL,label text OPTIONS(column_name 'remote_label'),flag boolean,bytes bytea) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'data table',fetch_size '1')`, 0))
	run(fixture.Exec(`CREATE FOREIGN TABLE pf_identity(pid integer,remote_user text,remote_database text,path text,zone text,dates text,intervals text,digits text,isolation text) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'identity')`, 0))
}

func postgresFDWPID(t *testing.T, p *fixture.Pair, c *sql.Conn) int32 {
	t.Helper()
	var pid int32
	var user, database string
	if err := c.QueryRowContext(p.Ctx, "SELECT pid,remote_user,remote_database FROM pf_identity").Scan(&pid, &user, &database); err != nil {
		t.Fatal(err)
	}
	if pid <= 0 || user != "stuntgres" || database != "stuntgres_reference" {
		t.Fatalf("actual remote identity: pid=%d user=%q database=%q", pid, user, database)
	}
	var count int64
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1 AND usename=$2 AND datname=$3 AND application_name='pf_physical'", pid, user, database).Scan(&count); err != nil || count != 1 {
		t.Fatalf("independent remote identity: count=%d err=%v", count, err)
	}
	return pid
}

func postgresFDWObserveGone(t *testing.T, p *fixture.Pair, pid int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
	defer cancel()
	externalSQLBackendGone(t, ctx, pid)
}

func postgresFDWQueryFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, prepared bool, d fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, d)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func postgresFDWClassName(class int) string { return fmt.Sprintf("context_%d", class) }

func postgresFDWPort(t *testing.T, b fixture.Backend) string {
	t.Helper()
	if b.PG {
		return "5432"
	}
	config, err := pgconn.ParseConfig(fixture.DSN)
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(int(config.Port))
}

// Release local/remote transaction locks before dropping source objects, even
// after a Fatal assertion. Each execution route must leave the next route clean.
func postgresFDWRemoteCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn, queries ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Error("remote source cleanup rollback:", err)
	}
	for _, q := range queries {
		if _, err := p.Postgres.ExecContext(ctx, q); err != nil {
			t.Error("remote source cleanup:", q, err)
		}
	}
}
