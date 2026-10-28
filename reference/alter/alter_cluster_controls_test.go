package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func clusterControlRoundtrip(t *testing.T, p *fixture.Pair, check func(func(fixture.Step))) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector("cluster-restore")
	if err != nil {
		t.Fatal(err)
	}
	target := sql.OpenDB(connector)
	defer target.Close()
	c, err := target.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE ROLE cluster_dump_owner", "CREATE TEMP TABLE retained(id int)", "INSERT INTO retained VALUES(99)", "CREATE TABLE sentinel(id int)", "INSERT INTO sentinel VALUES(42)"} {
		if _, err = c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	retained, err := c.QueryContext(p.Ctx, "SELECT id FROM retained")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	for range 2 {
		if err = stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(func(step fixture.Step) { persistenceControlRun(t, p, c, step) })
		persistenceControlRun(t, p, c, fixture.Select("SELECT id FROM retained", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
		var again bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, target, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("noncanonical cluster roundtrip", err, dump.String(), again.String())
		}
	}
	fixture.StringCoreRetainedRows(t, retained, nil, []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}, false)
	if _, err = c.ExecContext(p.Ctx, "CREATE TABLE sentinel(id int)"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecContext(p.Ctx, "INSERT INTO sentinel VALUES(42)"); err != nil {
		t.Fatal(err)
	}
	held, err := c.QueryContext(p.Ctx, "SELECT id,note FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var before bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, target, &before); err != nil {
		t.Fatal(err)
	}
	unchanged := func(t *testing.T) {
		t.Helper()
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, target, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed restore changed preimage", err)
		}
		check(func(step fixture.Step) { persistenceControlRun(t, p, c, step) })
		persistenceControlRun(t, p, c, fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(42)}}))
		persistenceControlRun(t, p, c, fixture.Select("SELECT id FROM retained", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
	}
	failure := errors.New("cluster restore reader failure")
	if err = stuntgres.Restore(p.Ctx, target, addConstraintFailedReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	unchanged(t)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err = stuntgres.Restore(ctx, target, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged(t)
	err = stuntgres.Restore(p.Ctx, target, strings.NewReader(dump.String()+"ALTER TABLE public.t CLUSTER ON missing_idx;"))
	want := persistenceDiagnostic("42704", `index "missing_idx" for table "t" does not exist`)
	fixture.AssertDiagnostic(t, err, &want)
	unchanged(t)
	for _, tc := range []struct {
		query, message string
	}{
		{"ALTER TABLE t CLUSTER ON a", "restore accepts only unconditional qualified clustering preferences"},
		{"ALTER TABLE IF EXISTS public.t CLUSTER ON a", "restore accepts only unconditional qualified clustering preferences"},
		{"ALTER TABLE ONLY public.t CLUSTER ON a", "restore accepts only unconditional qualified clustering preferences"},
		{"ALTER TABLE public.t CLUSTER ON a, CLUSTER ON b", "restore accepts only unconditional qualified clustering preferences"},
		{"ALTER TABLE public.t CLUSTER ON a, ADD COLUMN extra int", "restore accepts only unconditional qualified clustering preferences"},
		{"ALTER TABLE public.t SET WITHOUT CLUSTER", "restore accepts only existing-index or NOT VALID constraint attachments"},
		{"ALTER TABLE public.t SET WITHOUT OIDS", "restore accepts only existing-index or NOT VALID constraint attachments"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			old, err := c.QueryContext(p.Ctx, "SELECT id,note FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			temp, err := c.QueryContext(p.Ctx, "SELECT id FROM retained")
			if err != nil {
				t.Fatal(err)
			}
			defer temp.Close()
			err = stuntgres.Restore(p.Ctx, target, strings.NewReader(dump.String()+tc.query+";"))
			want := persistenceDiagnostic("0A000", tc.message)
			fixture.AssertDiagnostic(t, err, &want)
			unchanged(t)
			fixture.StringCoreRetainedRows(t, old, nil, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "two"}}, false)
			fixture.StringCoreRetainedRows(t, temp, nil, []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}, false)
		})
	}
	check(func(step fixture.Step) { persistenceControlRun(t, p, p.Native, step) })
	persistenceControlRun(t, p, c, fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(42)}}))
	fixture.StringCoreRetainedRows(t, held, nil, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "two"}}, false)
	var source bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &source); err != nil || !bytes.Equal(dump.Bytes(), source.Bytes()) {
		t.Fatal("restore mutated source", err)
	}
}
func TestReferenceAlterClusterControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cluster_dump_owner"})
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		for _, q := range []string{"ALTER TABLE t CLUSTER ON b", "CREATE ROLE cluster_dump_owner", "CREATE TABLE pk(id int PRIMARY KEY,note text)", "ALTER INDEX pk_pkey RENAME TO preferred_pk", "ALTER TABLE pk CLUSTER ON preferred_pk", "ALTER TABLE pk OWNER TO cluster_dump_owner", "CREATE TABLE uq(id int UNIQUE)", "ALTER TABLE uq CLUSTER ON uq_id_key", `CREATE TABLE "Quoted table"(id int)`, `CREATE INDEX "Quoted index" ON "Quoted table"(id)`, `ALTER TABLE "Quoted table" CLUSTER ON "Quoted index"`} {
			persistenceExec(t, p, b, c, q)
		}
		persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO pk VALUES(7,'seven')", 1))
		persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO uq VALUES(9)", 1))
		persistenceRun(t, p, b, c, fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT id FROM t", 2))
		for _, q := range []string{"CREATE INDEX mi ON mv(id)", "ALTER MATERIALIZED VIEW mv CLUSTER ON mi"} {
			persistenceExec(t, p, b, c, q)
		}
		check := func(run func(fixture.Step)) {
			run(clusterBits(false, true))
			run(clusterRows())
			for _, v := range []struct{ table, index string }{{"pk", "preferred_pk"}, {"uq", "uq_id_key"}, {`"Quoted table"`, "Quoted index"}, {"mv", "mi"}} {
				run(clusterState(v.table, [][]any{{v.index, true}}))
			}
			run(fixture.Select("SELECT id,note FROM pk", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seven"}}))
			run(fixture.Select("SELECT id FROM uq", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.Select("SELECT id FROM mv ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			fixture.MaterializedViewPopulation(run, "mv", true)
			run(fixture.Select("SELECT c.conname,i.indisclustered FROM pg_constraint c JOIN pg_index i ON c.conindid=i.indexrelid WHERE c.conrelid='pk'::regclass", []string{"conname", "indisclustered"}, []string{"NAME", "BOOL"}, [][]any{{"preferred_pk", true}}))
		}
		check(func(step fixture.Step) { persistenceRun(t, p, b, c, step) })
		if !b.PG {
			clusterControlRoundtrip(t, p, check)
			if p.Postgres != nil {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					for _, q := range []string{"DROP MATERIALIZED VIEW IF EXISTS mv", "DROP TABLE IF EXISTS t,pk,uq,\"Quoted table\" CASCADE", "DROP ROLE IF EXISTS cluster_dump_owner"} {
						if _, err := p.Postgres.ExecContext(ctx, q); err != nil {
							t.Errorf("replay cleanup: %v", err)
						}
					}
				}()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, "CREATE ROLE cluster_dump_owner"); err != nil {
					t.Fatal(err)
				}
				// Fixture public has no default schema privileges for restored owners.
				if _, err := p.Postgres.ExecContext(p.Ctx, "GRANT USAGE,CREATE ON SCHEMA public TO cluster_dump_owner"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if _, err := p.Postgres.ExecContext(ctx, "REVOKE USAGE,CREATE ON SCHEMA public FROM cluster_dump_owner"); err != nil {
						t.Errorf("replay schema cleanup: %v", err)
					}
				}()
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PostgreSQL logical replay", err)
				}
				for _, oracle := range p.Backends() {
					if oracle.PG {
						run := func(step fixture.Step) { persistenceRun(t, p, oracle, p.Postgres, step) }
						fixture.MaterializedViewPopulation(run, "mv", false)
						run(fixture.MaterializedViewUnpopulated("mv"))
						run(fixture.Exec("REFRESH MATERIALIZED VIEW mv", 0))
						check(run)
					}
				}
			}
		}
	})
}
