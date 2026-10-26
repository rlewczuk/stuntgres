package reference_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func brinIndexControls(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) string {
	t.Helper()
	if b.PG {
		return ""
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	create := `CREATE INDEX "bn" ON "public"."t" USING brin ("n"`
	if !strings.Contains(text, create) || !strings.Contains(text, `ALTER INDEX "public"."bn" SET (`) || !strings.Contains(text, `pages_per_range`) || !strings.Contains(text, `autosummarize`) {
		t.Fatal("missing BRIN CREATE plus option replay", text)
	}
	var sourceOID int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='bn'").Scan(&sourceOID); err != nil {
		t.Fatal(err)
	}
	retained, err := p.Native.QueryContext(p.Ctx, "SELECT id,n,s FROM t WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	foreign := fixture.NewFixture(t, false, true)
	temporary, err := foreign.Memory.Conn(foreign.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer temporary.Close()
	identities := heapRetainProcedureCollision(t, foreign, temporary, foreign.MemoryRecord, sourceOID)
	if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	frun := func(s fixture.Step) { fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, s) }
	var destinationOID int64
	if err := foreign.Memory.QueryRowContext(foreign.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='bn'").Scan(&destinationOID); err != nil || destinationOID == sourceOID {
		t.Fatal("actual TEMP collision did not remap BRIN identity", sourceOID, destinationOID, err)
	}
	if got := heapTemporaryIdentities(t, foreign, temporary); !reflect.DeepEqual(got, identities) {
		t.Fatal("retained TEMP identities changed", got, identities)
	}
	fixture.RunStep(t, foreign.Ctx, temporary, foreign.MemoryRecord, fixture.Select("SELECT v FROM pg_temp.heap_retained_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
	frun(brinIndexIDs("n>=4", 5, 6))
	frun(fixture.Exec("INSERT INTO t VALUES(8,12,'new')", 1))
	frun(brinIndexIDs("n>=4", 5, 6, 8))
	run(brinIndexIDs("n>=4", 5, 6))
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT relname FROM pg_class WHERE relname='bn'", []string{"relname"}, []string{"NAME"}, nil))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	run(brinIndexIDs("n>=4", 5, 6))
	types, err := retained.ColumnTypes()
	if err != nil || len(types) != 3 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "INT4" || types[2].DatabaseTypeName() != "TEXT" {
		t.Fatal("retained metadata", types, err)
	}
	if !retained.Next() {
		t.Fatal("retained source row missing", retained.Err())
	}
	var id, n int64
	var s string
	if err := retained.Scan(&id, &n, &s); err != nil || id != 1 || n != 1 || s != "a" {
		t.Fatal("retained row mutated", id, n, s, err)
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatal("retained row count", retained.Err())
	}
	return text
}

func TestReferenceBrinIndexControls(t *testing.T) {
	var emitted string
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		brinIndexSeed(run)
		run(fixture.Exec("CREATE INDEX bn ON t USING brin(n pg_catalog.int4_minmax_ops) WITH(pages_per_range=2,autosummarize=on)", 0))
		run(fixture.Exec("CREATE INDEX bx ON t USING brin(lower(s)) WITH(pages_per_range=2,autosummarize=on) WHERE id>1", 0))
		brinIndexMutate(run)
		if !b.PG {
			emitted = brinIndexControls(t, p, b, run)
			if !strings.Contains(emitted, `CREATE INDEX "bx" ON "public"."t" USING brin ((pg_catalog.lower("s")))`) || !strings.Contains(emitted, ` WHERE `) {
				t.Fatal("missing callable/partial BRIN replay", emitted)
			}
		} else {
			if emitted == "" {
				t.Fatal("missing unchanged native Dump stream")
			}
			run(fixture.Exec("DROP TABLE t CASCADE", 0))
			if _, err := b.DB.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("PostgreSQL rejected native CREATE plus ALTER replay", err)
			}
			run(brinIndexIDs("n>=4", 5, 6))
			run(fixture.Exec("INSERT INTO t VALUES(8,12,'new')", 1))
			run(brinIndexIDs("n>=4", 5, 6, 8))
		}
		run(brinIndexIDs("id>1 AND lower(s)='d'", 6))
		run(fixture.Exec("INSERT INTO t VALUES(9,13,'MiXeD')", 1))
		run(brinIndexIDs("id>1 AND lower(s)='mixed'", 9))
	})
}
