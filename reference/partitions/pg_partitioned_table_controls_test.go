package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pptControlSeed(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE ppt_control(a INT,b INT,v TEXT COLLATE \"C\") PARTITION BY RANGE(a,(b+1))",
		"CREATE TABLE ppt_control_default PARTITION OF ppt_control DEFAULT",
		"CREATE TABLE ppt_control_mid PARTITION OF ppt_control FOR VALUES FROM(0,0) TO(10,10) PARTITION BY LIST(v)",
		"CREATE TABLE ppt_control_mid_default PARTITION OF ppt_control_mid DEFAULT",
		"CREATE TABLE ppt_control_hash(a INT) PARTITION BY HASH(a)",
		"CREATE TABLE ppt_control_hash_leaf PARTITION OF ppt_control_hash FOR VALUES WITH(MODULUS 1,REMAINDER 0)",
	} {
		run(fixture.Exec(q, 0))
	}
}

func pptControlCheck(run func(fixture.Step)) {
	pptDefault(run, "ppt_control", "ppt_control_default")
	pptDefault(run, "ppt_control_mid", "ppt_control_mid_default")
	pptBoth(run, fixture.Select(`SELECT partstrat,partattrs::text,partclass::text,partcollation::text,pg_get_expr(partexprs,partrelid) AS expressions FROM pg_partitioned_table WHERE partrelid='ppt_control'::regclass`, []string{"partstrat", "partattrs", "partclass", "partcollation", "expressions"}, []string{"CHAR", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"r", "1 0", "1978 1978", "0 0", "(b + 1)"}}))
	pptBoth(run, fixture.Select(`SELECT p.partstrat,p.partattrs::text,p.partcollation::text,o.opcname FROM pg_partitioned_table p JOIN pg_opclass o ON o.oid=p.partclass[0] WHERE p.partrelid='ppt_control_mid'::regclass`, []string{"partstrat", "partattrs", "partcollation", "opcname"}, []string{"CHAR", "TEXT", "TEXT", "NAME"}, [][]any{{"l", "3", "950", "text_ops"}}))
	pptBoth(run, fixture.Select(`SELECT p.partstrat,o.opcmethod::bigint,o.opcintype::bigint,p.partattrs::text,p.partcollation::text FROM pg_partitioned_table p JOIN pg_opclass o ON o.oid=p.partclass[0] WHERE p.partrelid='ppt_control_hash'::regclass`, []string{"partstrat", "opcmethod", "opcintype", "partattrs", "partcollation"}, []string{"CHAR", "INT8", "INT8", "TEXT", "TEXT"}, [][]any{{"h", int64(405), int64(23), "1", "0"}}))
}

func pptRetainedDisplay(t *testing.T, rows *sql.Rows, want string) {
	t.Helper()
	if !rows.Next() {
		t.Fatal("retained display missing", rows.Err())
	}
	var got string
	if err := rows.Scan(&got); err != nil || got != want {
		t.Fatal("retained display", got, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained display tail", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferencePGPartitionedTableControls(t *testing.T) {
	pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := pptRun(t, p, b, c)
		pptControlSeed(run)
		pptControlCheck(run)
		if b.PG {
			return
		} // Public replacement controls have no PostgreSQL equivalent.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(dump.String()), "insert into pg_partitioned_table") || !strings.Contains(dump.String(), "PARTITION BY") {
			t.Fatal("Dump must serialize partition DDL", dump.String())
		}
		q := `SELECT pg_get_expr(partexprs,partrelid) AS expressions FROM pg_partitioned_table WHERE partrelid='ppt_control'::regclass`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		checkHeld := func(want string) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"expressions"}, []string{"TEXT"}, [][]any{{want}}))
		}
		checkHeld("(b + 1)")
		id, def := pptID(t, p, c, "ppt_control"), pptID(t, p, c, "ppt_control_default")
		raw, err := c.QueryContext(p.Ctx, "SELECT * FROM pg_partitioned_table WHERE partrelid=$1::oid", id)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		pptMetadata(false)(t, raw)
		old, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		run(fixture.Exec("ALTER TABLE ppt_control RENAME b TO renamed_b", 0))
		checkHeld("(renamed_b + 1)")
		renamed, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer renamed.Close()
		run(fixture.Exec("DROP TABLE ppt_control CASCADE", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"CREATE TABLE ppt_collision_a(a INT)", "CREATE TABLE ppt_collision_b(b TEXT)", "CREATE TABLE ppt_collision_c(c INT) PARTITION BY HASH(c)"} {
			fixture.RunStep(t, p.Ctx, p.Native, p.MemoryRecord, fixture.Exec(q, 0))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		pptControlCheck(run)
		// Literal regclass captures belong to the retired catalog lifetime;
		// immutable eager results survive, but the old handle cannot rebind it.
		staleRows, err := stmt.QueryContext(p.Ctx)
		if staleRows != nil {
			staleRows.Close()
			t.Fatal("retired literal handle returned rows")
		}
		fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held.Statement = stmt
		checkHeld("(b + 1)")
		if !raw.Next() {
			t.Fatal("retained raw row missing", raw.Err())
		}
		values := make([]any, 8)
		pointers := make([]any, 8)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := raw.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(values[:7], []any{id, "r", int64(2), def, "1 0", "1978 1978", "0 0"}) {
			t.Fatal("retained raw facts", values)
		}
		if tree, ok := values[7].(string); !ok || tree == "" {
			t.Fatalf("retained logical tree: %T %v", values[7], values[7])
		}
		if raw.Next() || raw.Err() != nil {
			t.Fatal("retained raw tail", raw.Err())
		}
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
		pptRetainedDisplay(t, old, "(b + 1)")
		pptRetainedDisplay(t, renamed, "(renamed_b + 1)")
		retained, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		for _, script := range []string{"CREATE TABLE broken(", "CREATE TABLE broken(k INT CHECK(k>0));INSERT INTO broken VALUES(0)"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			pptControlCheck(run)
			checkHeld("(b + 1)")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		pptControlCheck(run)
		checkHeld("(b + 1)")
		pptRetainedDisplay(t, retained, "(b + 1)")
	})
}

func TestReferencePGPartitionedTableCancellation(t *testing.T) {
	pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := pptRun(t, p, b, b.DB)
		run(fixture.Exec("CREATE TABLE ppt_cancel(a INT) PARTITION BY RANGE(a)", 0))
		pptBoth(run, pptFacts("ppt_cancel", "r", 1, "1", "1978", "0", nil))
		if b.PG {
			return
		} // pq pre-cancel output shape is not a portable contract.
		q := "SELECT * FROM pg_partitioned_table WHERE partrelid=$1::oid"
		id := pptID(t, p, b.DB, "ppt_cancel")
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, e := range []fixture.Executor{b.DB, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}} {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := e.QueryContext(ctx, q, id)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled read returned cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled read", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("fabricated cancellation SQLSTATE", state.SQLState())
			}
			pptBoth(run, pptFacts("ppt_cancel", "r", 1, "1", "1978", "0", nil))
			s := fixture.Select(q, pptColumns, pptTypes, [][]any{{id, "r", int64(1), int64(0), "1", "1978", "0", nil}}, id)
			s.CheckMetadata = pptMetadata(false)
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
		}
	})
}
