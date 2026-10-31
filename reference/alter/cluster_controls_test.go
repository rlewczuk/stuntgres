package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func clusterCommandNativeRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// This explicit native-control phase supplements the shared SQL scenario.
	old, err := c.QueryContext(p.Ctx, "SELECT id,k,payload FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	persistenceExec(t, p, b, c, "CLUSTER t")
	var dump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	preference := strings.Index(text, `ALTER TABLE "public"."t" CLUSTER ON "a";`)
	if preference < 0 || strings.Contains(text, "CLUSTER t") || strings.Contains(text, "CLUSTER USING") {
		t.Fatalf("dump must serialize preference, not CLUSTER execution: %s", text)
	}
	if index := strings.LastIndex(text, "CREATE INDEX"); index < 0 || index > preference {
		t.Fatal("preference precedes index definitions", text)
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	persistenceRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='t'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	persistenceRun(t, p, b, c, clusterCommandRows())
	persistenceRun(t, p, b, c, clusterCommandBits(true, false))
	persistenceExec(t, p, b, c, "CLUSTER t")
	fixture.StringCoreRetainedRows(t, old, nil, []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, clusterCommandRows().Rows, false)
	var again bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("CLUSTER changed logical dump after restore", dump.String(), again.String())
	}
}

func clusterCommandReorganizationScenario(t *testing.T) {
	t.Helper()
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterCommandSeed(t, p, b, c)
		persistenceExec(t, p, b, c, "CLUSTER t USING a")
		held := clusterCommandHeld(t, p, b, c, "CLUSTER t")
		clusterCommandExecuteHeld(t, p, b, c, held, "CLUSTER t")
		persistenceRun(t, p, b, c, clusterCommandBits(true, false))
		for _, q := range []string{"BEGIN", "CLUSTER t USING b"} {
			persistenceExec(t, p, b, c, q)
		}
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		persistenceRun(t, p, b, c, clusterCommandBits(false, true))
		persistenceExec(t, p, b, c, "ROLLBACK")
		persistenceRun(t, p, b, c, clusterCommandBits(true, false))
		persistenceRun(t, p, b, c, clusterCommandRows())
		if !b.PG {
			clusterCommandNativeRoundtrip(t, p, b, c)
		}
		want := persistenceDiagnostic("23505", `duplicate key value violates unique constraint "u"`)
		want.Detail, want.Schema, want.Table, want.Constraint = "Key (id)=(1) already exists.", "public", "t", "u"
		persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,7,'duplicate')", want))
		persistenceRun(t, p, b, c, fixture.Exec("UPDATE t SET k=8 WHERE id=3", 1))
		persistenceRun(t, p, b, c, fixture.Exec("DELETE FROM t WHERE id=5", 1))
		clusterCommandExecuteHeld(t, p, b, c, held, "CLUSTER t")
		persistenceRun(t, p, b, c, fixture.Select("SELECT id,k,payload FROM t ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "b"}, {int64(2), int64(1), "a"}, {int64(3), int64(8), "b"}, {int64(4), nil, nil}}))
	})
}

func TestReferenceClusterControls(t *testing.T) { clusterCommandReorganizationScenario(t) }
