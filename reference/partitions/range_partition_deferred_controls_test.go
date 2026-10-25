package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"github.com/ngrok/sqlmw"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRangePartitionDeferredControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rp_defer_owner"})
	referenceRangeScenario(t, referenceRangeDeferredControlBody)
}

func referenceRangeDeferredControlBody(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
		}
	}()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	run(fixture.Exec("CREATE USER rp_defer_owner", 0))
	run(fixture.Exec("SET SESSION AUTHORIZATION rp_defer_owner", 0))
	fixture.WithPublicSchemaOwner(t, p, b, "rp_defer_owner", func() {
		for _, query := range []string{
			"CREATE TABLE rp_defer_target(k REGCLASS)",
			"CREATE TABLE rp_defer_other(k INT)",
			"CREATE TABLE rp_defer_root(k INT,payload REGCLASS DEFAULT 'rp_defer_target'::REGCLASS) PARTITION BY RANGE(k)",
			"CREATE TABLE rp_defer_middle PARTITION OF rp_defer_root DEFAULT PARTITION BY RANGE(k)",
			"CREATE TABLE rp_defer_leaf PARTITION OF rp_defer_middle(payload DEFAULT 'rp_defer_other'::REGCLASS) DEFAULT",
			"ALTER TABLE rp_defer_target ALTER COLUMN k SET DEFAULT 'rp_defer_root'::REGCLASS",
		} {
			run(fixture.Exec(query, 0))
		}
	})
	run(fixture.Prepared(fixture.Exec("INSERT INTO rp_defer_root(k) VALUES($1)", 1, 1)))
	run(fixture.Prepared(fixture.Exec("INSERT INTO rp_defer_leaf(k) VALUES($1)", 1, 2)))
	expected := fixture.Select("SELECT k,payload='rp_defer_target'::REGCLASS AS parent_default,payload='rp_defer_other'::REGCLASS AS leaf_default FROM rp_defer_root ORDER BY k", []string{"k", "parent_default", "leaf_default"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}})
	check := func(db fixture.Executor, record *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, db, record, expected)
		fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT c.relname,t.tableowner,c.relkind AS kind FROM pg_class c JOIN pg_tables t ON t.tablename=c.relname WHERE t.schemaname='public' AND (c.relname='rp_defer_root' OR c.relname='rp_defer_middle' OR c.relname='rp_defer_leaf') ORDER BY c.relname", []string{"relname", "tableowner", "kind"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"rp_defer_leaf", "rp_defer_owner", "r"}, {"rp_defer_middle", "rp_defer_owner", "p"}, {"rp_defer_root", "rp_defer_owner", "p"}}))
	}
	check(conn, b.Recorder)
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(conn, b.Recorder)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("deferred hierarchy redump changed", err)
		}
		// Post-execution private validation rejects a non-deferrable replay even
		// though SET DEFAULT NULL itself bound; the live catalog must not swap.
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"ALTER TABLE rp_defer_root ALTER payload SET DEFAULT NULL;"))
		want := fixture.ControlsReferenceDiagnostic("0A000", "restore accepts only deferred captured-reference or regclass defaults")
		fixture.AssertDiagnostic(t, err, &want)
		check(conn, b.Recorder)
		driver := stuntgres.NewDriver()
		raw, err := driver.OpenConnector("range-deferred-destination")
		if err != nil {
			t.Fatal(err)
		}
		native := sql.OpenDB(raw)
		defer native.Close()
		wrapped := sqlmw.Driver(driver, fixture.Interceptor{})
		configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("range-deferred-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(configured)
		defer destination.Close()
		record := &fixture.Recorder{}
		fixture.RunStep(t, p.Ctx, destination, record, fixture.Exec("CREATE USER rp_defer_owner", 0))
		if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(destination, record)
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			// Provision the real importing owner before consuming the unmodified
			// emitted stream. Cleanup restores the complete fixture schema first.
			cleanup := func() {
				cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", "DROP ROLE IF EXISTS rp_defer_owner"} {
					if _, err := oracle.ExecContext(cleanupContext, query); err != nil {
						t.Error(err)
					}
				}
			}
			defer cleanup()
			for _, query := range []string{"CREATE USER rp_defer_owner", "GRANT USAGE,CREATE ON SCHEMA public TO rp_defer_owner"} {
				if _, err := oracle.ExecContext(p.Ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL rejected actual deferred hierarchy dump", err)
			}
			check(oracle, p.PostgresRecord)
		}
	}
	live := "ALTER TABLE rp_defer_root ALTER payload SET DEFAULT 'rp_defer_other'::REGCLASS"
	if b.PG {
		run(fixture.Exec(live, 0))
	} else {
		run(referenceRangeError(live, "0A000", "altering partition hierarchies is unsupported", fixture.Diagnostic{}))
	}
	run(fixture.Prepared(fixture.Exec("INSERT INTO rp_defer_root(k) VALUES($1)", 1, 3)))
	expected.Rows = append(expected.Rows, []any{int64(3), !b.PG, b.PG})
	run(expected)
}
