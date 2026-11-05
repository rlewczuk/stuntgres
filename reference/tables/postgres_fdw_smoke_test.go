package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWFeatureSmoke(t *testing.T) {
	// Declaration/validation smoke is genuinely Docker-free in quick mode.
	postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWInstall(run)
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD fetch_size '2',ADD keep_connections 'true',ADD parallel_commit 'false',ADD parallel_abort 'false')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(user 'declared-only',password_required 'true')", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE pf_table(id integer OPTIONS(column_name 'Remote ID')) SERVER pf_server OPTIONS(schema_name 'not-contacted',table_name 'not-contacted',fetch_size '1',batch_size '2',async_capable 'false',analyze_sampling 'auto')", 0))
		run(fileFDWVoid(postgresFDWCall([]string{"fetch_size=010", "keep_connections=ON"}, 1417)))
		run(postgresFDWError("ALTER SERVER pf_server OPTIONS(SET fetch_size '0')", "22023", `"fetch_size" must be an integer value greater than zero`))
		postgresFDWStored(run, 1417, "fetch_size=2", "keep_connections=true", "parallel_commit=false", "parallel_abort=false")
	})
	if !*fixture.Mode {
		t.Log("physical source/connection/replay smoke is not exercised without REFERENCE=1")
		return
	}
	p, provider := postgresFDWPhysicalScenario(t)
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted"."data table" SET remote_label='x' WHERE "Remote ID"=1`, 1))
		postgresFDWPhysicalInstall(t, b, run)
		externalSQLProviderValues(t, p, b, c, "pf_table")
		pid := postgresFDWPID(t, p, c)
		if postgresFDWPID(t, p, c) != pid {
			t.Fatal("smoke lost retained real backend")
		}
		run(fixture.Select("SELECT path,zone,dates,intervals,digits FROM pf_identity", []string{"path", "zone", "dates", "intervals", "digits"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"pg_catalog", "GMT", "ISO, MDY", "postgres", "3"}}))
		run(postgresFDWError("ALTER FOREIGN TABLE pf_table OPTIONS(SET fetch_size '0')", "22023", `"fetch_size" must be an integer value greater than zero`))
		postgresFDWStored(run, 3118, "schema_name=pf source \"quoted", "table_name=data table", "fetch_size=1")
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD keep_connections 'false')", 0))
		postgresFDWObserveGone(t, p, pid)
		run(fixture.Exec("BEGIN", 0))
		closedPID := postgresFDWPID(t, p, c)
		run(fixture.Exec("COMMIT", 0))
		postgresFDWObserveGone(t, p, closedPID)
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(SET port '1')", 0))
		postgresFDWConnectionFailure(t, p, b, c, prepared, false)
		port := "5432"
		if !b.PG {
			config, err := pgconn.ParseConfig(fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
			port = strconv.Itoa(int(config.Port))
		}
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(SET port "+fileFDWLiteral(port)+",SET keep_connections 'true')", 0))
		run(externalSQLProviderRows(b.PG, "SELECT id,label,flag,bytes FROM pf_table WHERE id=1", [][]any{{int64(1), "x", true, "\x00\xff"}}))
		if b.PG {
			return
		}
		pid = postgresFDWPID(t, p, c)
		before := provider.OpenCalls()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted"."data table" SET remote_label='smoke-current' WHERE "Remote ID"=1`, 1))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		postgresFDWObserveGone(t, p, pid)
		if provider.OpenCalls() != before {
			t.Fatal("smoke replay opened/serialized physical resource")
		}
		run(externalSQLProviderRows(false, "SELECT id,label,flag,bytes FROM pf_table WHERE id=1", [][]any{{int64(1), "smoke-current", true, "\x00\xff"}}))
		if postgresFDWPID(t, p, c) == pid {
			t.Fatal("smoke replay reused destroyed physical connection")
		}
	})
}
