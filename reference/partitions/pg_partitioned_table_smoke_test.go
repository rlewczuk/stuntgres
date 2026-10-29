package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGPartitionedTableFeatureSmoke(t *testing.T) {
	pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := pptRun(t, p, b, c)
		for _, q := range []string{
			"CREATE TABLE ppt_smoke(a INT,b INT) PARTITION BY RANGE(a,(b+1))",
			"CREATE TABLE ppt_smoke_default PARTITION OF ppt_smoke DEFAULT",
			"CREATE TABLE ppt_smoke_hash(a INT) PARTITION BY HASH(a)",
		} {
			run(fixture.Exec(q, 0))
		}
		q := `SELECT partstrat,partnatts,partattrs::text,partclass::text,partcollation::text,partdefid='ppt_smoke_default'::regclass AS default_identity,pg_get_expr(partexprs,partrelid) AS expressions FROM pg_partitioned_table WHERE partrelid='ppt_smoke'::regclass`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func() {
			s := fixture.Select(q, []string{"partstrat", "partnatts", "partattrs", "partclass", "partcollation", "default_identity", "expressions"}, []string{"CHAR", "INT2", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT"}, [][]any{{"r", int64(2), "1 0", "1978 1978", "0 0", true, "(b + 1)"}})
			pptBoth(run, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			pptBoth(run, fixture.Select(`SELECT p.partstrat,o.opcmethod::bigint,o.opcname,o.opcintype::bigint FROM pg_partitioned_table p JOIN pg_opclass o ON o.oid=p.partclass[0] WHERE p.partrelid='ppt_smoke_hash'::regclass`, []string{"partstrat", "opcmethod", "opcname", "opcintype"}, []string{"CHAR", "INT8", "NAME", "INT8"}, [][]any{{"h", int64(405), "int4_ops", int64(23)}}))
			pptEmpty(run, b, "SELECT * FROM pg_partitioned_table WHERE partrelid=$1::oid", int64(4294967295))
		}
		check()
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("ALTER TABLE ppt_smoke DETACH PARTITION ppt_smoke_default", 0))
		pptDefault(run, "ppt_smoke", "")
		run(fixture.Exec("ROLLBACK", 0))
		check()
		s := fixture.ImplicitError("CREATE TABLE ppt_smoke_bad(a INT,b INT) PARTITION BY LIST(a,b)", fixture.Diagnostic{Code: "42P17", Message: `cannot use "list" partition strategy with more than one column`})
		s.ErrorStage = "execute"
		run(s)
		pptBoth(run, fixture.Select("SELECT relname FROM pg_class WHERE relname='ppt_smoke_bad'", []string{"relname"}, []string{"NAME"}, nil))
		check()
		if b.PG {
			return
		}
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
		check()
	})
}
