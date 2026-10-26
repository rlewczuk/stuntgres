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

func ltxtqueryControlChecks(b fixture.Backend) []fixture.Step {
	checks := []fixture.Step{
		ltxtquerySelect(b, "SELECT id,v FROM lt_controls ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "Europe & Russia@* & !Transportation"}, {int64(2), "foo%@*"}, {int64(3), "AbC"}, {int64(4), nil}, {int64(5), nil}}),
		ltxtquerySelect(b, "SELECT v FROM lt_control_vacant", []string{"v"}, [][]any{}),
	}
	for i := range checks {
		names, nulls := checks[i].Columns, []bool{false, true}
		if i == 1 { nulls = []bool{false} }
		checks[i].CheckMetadata = func(t *testing.T, rows *sql.Rows) { ltxtqueryMetadata(t, b.PG, rows, names, nulls) }
	}
	return checks
}

func ltxtqueryConsumeRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var id int64
		var value any
		if err := rows.Scan(&id, &value); err != nil { t.Fatal(err) }
		if value != nil { if _, ok := value.(string); !ok { t.Fatalf("retained raw value %T", value) } }
		got = append(got, []any{id, value})
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(got, want) { t.Fatalf("retained rows %#v want %#v", got, want) }
}

func ltxtqueryBadRestore() (string, fixture.Diagnostic) {
	q := "CREATE EXTENSION ltree; CREATE TABLE lt_staged(v ltxtquery); INSERT INTO lt_staged VALUES ('a'); INSERT INTO lt_staged VALUES ('foo@bar');"
	want := fixture.ControlsReferenceDiagnostic("42601", "modifiers syntax error")
	want.Position = ltreeSQLPosition(q, "'foo@bar'")
	return q, want
}

func ltxtqueryVerifyControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, s := range ltxtqueryControlChecks(b) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
	ltxtqueryIdentityChecks(t, p, b, c)
	lqueryIdentityChecks(t, p, b, c)
	tree:=ltreeSelect(b,"SELECT v FROM lt_tree_consumer",[]string{"v"},[][]any{{"a.b"},{nil}});tree.Bag=true
	fixture.RunStep(t,p.Ctx,c,b.Recorder,tree)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT attnotnull,atttypid='ltxtquery'::regtype::oid AS same_type,atttypmod,attcollation::text AS attcollation FROM pg_attribute WHERE attrelid='lt_controls'::regclass::oid AND attname='v'",[]string{"attnotnull","same_type","atttypmod","attcollation"},[]string{"BOOL","BOOL","INT4","TEXT"},[][]any{{false,true,int64(-1),"0"}}))
	ltreeIdentityChecks(t, p, b, c)
	s := lquerySelect(b,"SELECT v FROM lt_query_consumer",[]string{"v"},[][]any{{"a%@*"},{nil}}); s.Bag=true
	fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
}

func ltxtqueryNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO lt_controls VALUES (99,'PrivateSentinel')", 1))
	var dump bytes.Buffer
	err := stuntgres.Dump(p.Ctx, p.Native, &dump)
	_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
	if err != nil || rollbackErr != nil { t.Fatalf("Dump %v rollback %v", err, rollbackErr) }
	if strings.Contains(dump.String(), "PrivateSentinel") { t.Fatal("Dump leaked private row") }
	ext, table := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
	if ext < 0 || table < ext || !strings.Contains(dump.String(), "ltxtquery") { t.Fatal("extension must precede consumers", dump.String()) }
	if strings.Contains(dump.String(), "CREATE DOMAIN") || strings.Contains(dump.String(), "CREATE TYPE") { t.Fatal("extension-owned scalar emitted standalone", dump.String()) }
	check := ltxtqueryControlChecks(b)[0]
	retained, err := b.DB.QueryContext(p.Ctx, check.Query)
	if err != nil { t.Fatal(err) }
	defer retained.Close()
	metadata := ltxtqueryMetadata(t, false, retained, check.Columns, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ltxtquery','lquery','ltree')", []string{"typname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	ltxtqueryVerifyControls(t, p, b, c)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO lt_controls VALUES (98,$1)",1,"after_restore"))
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DELETE FROM lt_controls WHERE id=98",1))
	ltxtqueryConsumeRetained(t, retained, check.Rows)
	ltxtqueryColumnMetadata(t, false, metadata, check.Columns, []bool{false, true})

	var sourceOID int64
	if err:=c.QueryRowContext(p.Ctx,"SELECT 'ltxtquery'::regtype::oid").Scan(&sourceOID);err!=nil {t.Fatal(err)}
	target := fixture.NewFixture(t, false, true)
	// Prove the destination allocator has advanced past the source scalar.
	// Restore still may reuse its numeric OID: independent engines do not share
	// generations, and a fresh descriptor need not have a different number.
	for {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE lt_allocator_spacer(v integer)", 0))
		var allocated int64
		if err := target.Memory.QueryRowContext(target.Ctx, "SELECT 'lt_allocator_spacer'::regclass::oid").Scan(&allocated); err != nil { t.Fatal(err) }
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("DROP TABLE lt_allocator_spacer", 0))
		if allocated > sourceOID { break }
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	destination := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	var destinationOID int64
	if err:=target.Memory.QueryRowContext(target.Ctx,"SELECT 'ltxtquery'::regtype::oid").Scan(&destinationOID);err!=nil {t.Fatal(err)}
	if sourceOID<=0 || destinationOID<=0 {t.Fatal("invalid independent scalar identity",sourceOID,destinationOID)}
	ltxtqueryVerifyControls(t, target, destination, target.Memory)
	fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Exec("INSERT INTO lt_controls VALUES (98,$1)",1,"destination_bound"))
	fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Exec("DELETE FROM lt_controls WHERE id=98",1))
	ltxtqueryVerifyControls(t,p,b,c)
	old, err := target.Memory.QueryContext(target.Ctx, check.Query)
	if err != nil { t.Fatal(err) }
	defer old.Close()
	oldMetadata := ltxtqueryMetadata(t, false, old, check.Columns, []bool{false, true})
	var beforeOID, afterOID int64
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('ltxtquery'::regtype)::oid").Scan(&beforeOID); err != nil { t.Fatal(err) }
	bad, want := ltxtqueryBadRestore()
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	canceled, cancel := context.WithCancel(target.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) { t.Fatalf("canceled Restore %v", err) } else {
		var state interface{ SQLState() string }
		if errors.As(err, &state) { t.Fatalf("context error acquired SQLSTATE %q", state.SQLState()) }
	}
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('ltxtquery'::regtype)::oid").Scan(&afterOID); err != nil { t.Fatal(err) }
	if beforeOID != afterOID { t.Fatalf("failed Restore replaced identity %d/%d", beforeOID, afterOID) }
	ltxtqueryVerifyControls(t, target, destination, target.Memory)
	ltxtqueryConsumeRetained(t, old, check.Rows)
	ltxtqueryColumnMetadata(t, false, oldMetadata, check.Columns, []bool{false, true})
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil { t.Fatal(err) }
	if !bytes.Equal(after.Bytes(), dump.Bytes()) { t.Fatal("failed Restore changed deterministic Dump", after.String()) }
	ltxtqueryVerifyControls(t,p,b,c)
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual emitted SQL replay", err) }
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		ltxtqueryVerifyControls(t, p, pg, pg.DB)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
	}
}

func ltxtqueryRecreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	if !b.PG {
		check:=ltxtqueryControlChecks(b)[0]
		old,err:=b.DB.QueryContext(p.Ctx,check.Query);if err!=nil {t.Fatal(err)}
		metadata:=ltxtqueryMetadata(t,false,old,check.Columns,[]bool{false,true})
		defer func(){ltxtqueryConsumeRetained(t,old,check.Rows);ltxtqueryColumnMetadata(t,false,metadata,check.Columns,[]bool{false,true})}()
	}
	literalSQL := "INSERT INTO lt_controls VALUES (6,'a | b & c')"
	boundSQL := "INSERT INTO lt_controls VALUES (7,$1::text::ltxtquery)"
	literal, err := c.PrepareContext(p.Ctx, literalSQL)
	if err != nil { t.Fatal(err) }
	defer literal.Close()
	bound, err := c.PrepareContext(p.Ctx, boundSQL)
	if err != nil { t.Fatal(err) }
	defer bound.Close()
	var before, after int64
	if err := c.QueryRowContext(p.Ctx, "SELECT ('ltxtquery'::regtype)::oid").Scan(&before); err != nil { t.Fatal(err) }
	for _, q := range []string{"DROP TABLE lt_tree_consumer", "DROP TABLE lt_query_consumer", "DROP TABLE lt_controls", "DROP TABLE lt_control_vacant", "DROP EXTENSION ltree", "CREATE EXTENSION ltree", "CREATE TABLE lt_controls(id integer NOT NULL,v ltxtquery)", "CREATE TABLE lt_control_vacant(v ltxtquery NOT NULL)"} { run(fixture.Exec(q, 0)) }
	run(fixture.Exec("CREATE TABLE lt_query_consumer(v lquery)",0))
	run(fixture.Exec("CREATE TABLE lt_tree_consumer(v ltree)",0))
	run(fixture.Exec("INSERT INTO lt_tree_consumer VALUES ('a.b'),(NULL)",2))
			run(fixture.Exec("INSERT INTO lt_query_consumer VALUES ('a*@%'),(NULL)",2))
			run(fixture.Exec("INSERT INTO lt_controls VALUES (1,'Europe & Russia@* & !Transportation'),(2,'foo%@*'),(3,'AbC'),(4,NULL),(5,NULL)",5))
	ltxtqueryVerifyControls(t, p, b, c)
	if err := c.QueryRowContext(p.Ctx, "SELECT ('ltxtquery'::regtype)::oid").Scan(&after); err != nil { t.Fatal(err) }
	if before <= 0 || after <= 0 || before == after { t.Fatalf("recreation identity %d/%d", before, after) }
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalSQL}, b.Recorder, fixture.Exec(literalSQL, 1))
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: bound, SQL: boundSQL}, b.Recorder, fixture.Exec(boundSQL, 1, "foo%@*"))
	run(ltxtquerySelect(b, "SELECT id,v FROM lt_controls WHERE id>5 ORDER BY id", []string{"id", "v"}, [][]any{{int64(6), "a | b & c"}, {int64(7), "foo%@*"}}))
	fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:bound,SQL:boundSQL},b.Recorder,fixture.Exec(boundSQL,1,nil))
	checkNull:=ltxtquerySelect(b,"SELECT v FROM lt_controls WHERE id=7",[]string{"v"},[][]any{{"foo%@*"},{nil}});checkNull.Bag=true;run(checkNull)
}

func TestReferenceLtxtqueryControls(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			ltreeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE lt_controls(id integer NOT NULL,v ltxtquery)", 0))
			run(fixture.Exec("CREATE TABLE lt_control_vacant(v ltxtquery NOT NULL)", 0))
			run(fixture.Exec("CREATE TABLE lt_query_consumer(v lquery)",0))
	run(fixture.Exec("CREATE TABLE lt_tree_consumer(v ltree)",0))
	run(fixture.Exec("INSERT INTO lt_tree_consumer VALUES ('a.b'),(NULL)",2))
			run(fixture.Exec("INSERT INTO lt_query_consumer VALUES ('a*@%'),(NULL)",2))
			run(fixture.Exec("INSERT INTO lt_controls VALUES (1,'Europe & Russia@* & !Transportation'),(2,'foo%@*'),(3,'AbC'),(4,NULL),(5,NULL)",5))
			ltxtqueryVerifyControls(t, p, b, c)
			if !b.PG { ltxtqueryNativeControls(t, p, b, c) }
		})
	})
	t.Run("staged_error_location", func(t *testing.T) {
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q, want := ltxtqueryBadRestore()
			if b.PG { ltreeError(t, p, b, c, q, false, false, false, want) } else { fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(q)), &want) }
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='lt_staged'",[]string{"relname"},[]string{"NAME"},[][]any{}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		})
	})
}
func TestReferenceLtxtqueryRecreation(t *testing.T) {
	ltxtqueryRenameMove(t)
	ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		ltreeInstall(t,p,b,c)
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE TABLE lt_controls(id integer NOT NULL,v ltxtquery)",0))
		run(fixture.Exec("CREATE TABLE lt_control_vacant(v ltxtquery NOT NULL)",0))
		run(fixture.Exec("CREATE TABLE lt_query_consumer(v lquery)",0))
	run(fixture.Exec("CREATE TABLE lt_tree_consumer(v ltree)",0))
	run(fixture.Exec("INSERT INTO lt_tree_consumer VALUES ('a.b'),(NULL)",2))
		run(fixture.Exec("INSERT INTO lt_query_consumer VALUES ('a*@%'),(NULL)",2))
		run(fixture.Exec("INSERT INTO lt_controls VALUES (1,'Europe & Russia@* & !Transportation'),(2,'foo%@*'),(3,'AbC'),(4,NULL),(5,NULL)",5))
		ltxtqueryRecreate(t,p,b,c)
	})
}
