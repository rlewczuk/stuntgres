package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/ngrok/sqlmw"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func typedConstraintUtility(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared bool, run func(fixture.Step), query string, before fixture.Step) {
	t.Helper()
	if !prepared {
		run(fixture.Exec(query, 0))
		return
	}
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("typed constraint Prepare", err)
	}
	defer stmt.Close()
	// Successful Prepare is quiet: CREATE has no table, attachment is still zero.
	run(before)
	result, err := stmt.ExecContext(p.Ctx)
	if err != nil {
		t.Fatal("typed constraint Execute", err)
	}
	fixture.LockAffected(t, result, 0)
}

// Attachment must preserve identities; imported identities belong to their own
// destination and are checked by relationships, never by cross-engine OID equality.
func typedConstraintIdentities(t *testing.T, p *fixture.Pair, c *sql.Conn, constraint string) [4]int64 {
	t.Helper()
	var ids [4]int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 't'::regclass::oid,'t'::regtype::oid,'pair'::regtype::oid").Scan(&ids[0], &ids[1], &ids[2]); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='"+constraint+"'").Scan(&ids[3]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id <= 0 {
			t.Fatal("invalid relation/type/constraint identity", ids)
		}
	}
	if ids[1] == ids[2] {
		t.Fatal("own row type aliases standalone source", ids)
	}
	return ids
}

func typedConstraintAttachmentState(run func(fixture.Step), attached bool) {
	run(fixture.TypedTableAttachment("t", "pair", attached))
	run(fixture.Select("SELECT 't'::regtype::oid<>'pair'::regtype::oid AS distinct_row", []string{"distinct_row"}, []string{"BOOL"}, [][]any{{true}}))
	var edges [][]any
	if attached {
		edges = [][]any{{"n"}}
	}
	run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='t'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjid='pair'::regtype::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, edges))
	run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1247::oid AND objid='t'::regtype::oid AND objsubid=0 AND refclassid='pg_class'::regclass::oid AND refobjid='t'::regclass::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}))
	run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='pair'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjid='pair'::regtype::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}))
}

func typedConstraintControls(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, setup []fixture.Step, state func(fixture.Executor, *fixture.Recorder, bool), invalidInsert string, want fixture.Diagnostic) {
	t.Helper()
	if b.PG {
		// This export uses already admitted ordinary CREATE plus attachment, so
		// PostgreSQL-only oracle selection can replay actual native bytes before
		// the missing typed grammar is corrected. No native Restore is hidden here.
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for _, s := range setup {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	source := strings.Index(dump.String(), `CREATE TYPE "public"."pair" AS (`)
	table := strings.Index(dump.String(), `CREATE TABLE "public"."t" OF "public"."pair" (`)
	if source < 0 || table <= source {
		t.Fatalf("dump must retain source-first genuine OF definition: %s", dump.String())
	}
	if b.PG {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted typed constraint SQL replay", err)
		}
		state(p.Postgres, p.PostgresRecord, true)
		return
	}

	freshDriver := stuntgres.NewDriver()
	fresh := fixture.DriverPool(t, freshDriver, "typed-constraint-fresh")
	connector, err := freshDriver.OpenConnector("typed-constraint-fresh")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
	recordedConnector, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	observeFresh := sql.OpenDB(recordedConnector)
	defer observeFresh.Close()
	// A new destination must Restore directly, not rely on a preceding Reset.
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("fresh typed constraint Restore", err)
	}
	state(observeFresh, p.MemoryRecord, false)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("Reset/Restore typed constraint", err)
	}
	state(b.DB, b.Recorder, false)
	// A late constraint violation during isolated replay must preserve the full
	// installed state, including the typed attachment and future enforcement.
	for _, target := range []struct{ control, observe *sql.DB }{{fresh, observeFresh}, {p.Native, b.DB}} {
		err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(dump.String()+"\n"+invalidInsert+";"))
		fixture.AssertDiagnostic(t, err, &want)
		state(target.observe, p.MemoryRecord, false)
	}
}
