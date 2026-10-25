package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var ean13ControlRows = [][]any{
	{int64(1), "978-0-12-345678-6"},
	{int64(2), "012-345678901-2"},
	{int64(3), "123-456789012-8!"},
	{int64(4), nil},
	{int64(5), "979-0-1234-5678-5"},
	{int64(6), "000-000000000-0"},
}

const ean13ControlDDL = "CREATE TABLE ean_control(id integer NOT NULL,v ean13)"
const ean13ControlInsert = "INSERT INTO ean_control VALUES(1,'9780123456786'),(2,'0123456789012'),(3,'1234567890120!'),(4,NULL),(5,'9790123456785'),(6,'0000000000000')"

func ean13ControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	s := fixture.Ean13Select(b, "SELECT id,v FROM ean_control ORDER BY id", []string{"id", "v"}, []string{"INT4", "EAN13"}, ean13ControlRows, []bool{false, true})
	fixture.RunStep(t, p.Ctx, db, recorder, s)
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Ean13Select(b, "SELECT id,v FROM ean_control WHERE id=99", []string{"id", "v"}, []string{"INT4", "EAN13"}, [][]any{}, []bool{false, true}))
	required := fixture.Ean13Select(b, "SELECT v FROM ean_required", []string{"v"}, []string{"EAN13"}, [][]any{{"123-456789012-8!"}}, []bool{false})
	fixture.RunStep(t, p.Ctx, db, recorder, required)
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(required))
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Ean13Select(b, "SELECT v FROM ean_required WHERE false", []string{"v"}, []string{"EAN13"}, [][]any{}, []bool{false}))
}

func ean13ConsumeRetained(t *testing.T, rows *sql.Rows) {
	t.Helper()
	got := [][]any{}
	for rows.Next() {
		var id, value any
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{fixture.Normalize(id), fixture.Normalize(value)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, ean13ControlRows) {
		t.Fatalf("retained rows = %#v, want %#v", got, ean13ControlRows)
	}
}

func TestReferenceEan13Controls(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Restore preserves positions in the entire source stream. Send the
		// identical script as one simple-query message to PostgreSQL before isn
		// exists, so its implicit transaction can roll back all staged objects.
		badPrefix := "CREATE EXTENSION isn; CREATE TABLE ean_staged(v ean13); INSERT INTO ean_staged VALUES('9780123456786'); INSERT INTO ean_staged VALUES("
		bad := badPrefix + "'1234567890120');"
		want := ean13Diagnostic("22P02", ean13Failures[0].message)
		want.Position = fixture.Position(utf8.RuneCountInString(badPrefix) + 1)
		if b.PG {
			step := fixture.ErrorDiagnostic(bad, want)
			step.ErrorStage = "execute"
			run(step)
		} else {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ean_staged'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		// A standalone statement has its own, statement-relative position.
		run(fixture.Exec("CREATE TABLE ean_staged(v ean13)", 0))
		run(fixture.Exec("INSERT INTO ean_staged VALUES('9780123456786')", 1))
		restoreWant := ean13Diagnostic("22P02", ean13Failures[0].message)
		restoreWant.Position = "31"
		run(fixture.ErrorDiagnostic("INSERT INTO ean_staged VALUES('1234567890120')", restoreWant))
		run(fixture.Ean13Select(b, "SELECT v FROM ean_staged", []string{"v"}, []string{"EAN13"}, [][]any{{"978-0-12-345678-6"}}, []bool{true}))
		run(fixture.Exec("DROP TABLE ean_staged", 0))
		run(fixture.Exec(ean13ControlDDL, 0))
		run(fixture.Exec(ean13ControlInsert, 6))
		run(fixture.Exec("CREATE TABLE ean_required(v ean13 NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO ean_required VALUES('1234567890120!')", 1))
		ean13ControlVerify(t, p, b, c, b.Recorder)
		if b.PG {
			run(fixture.Exec("DROP TABLE ean_control,ean_required", 0))
			run(fixture.Exec("DROP EXTENSION isn", 0))
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec(ean13ControlDDL, 0))
			run(fixture.Exec(ean13ControlInsert, 6))
			run(fixture.Exec("CREATE TABLE ean_required(v ean13 NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO ean_required VALUES('1234567890120!')", 1))
			ean13ControlVerify(t, p, b, c, b.Recorder)
			ean13Identity(t, p, b, c, "public")
			return
		}
		// Native-only controls do not precede or obstruct the PG discovery phase.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO ean_control VALUES(99,'1400000000007')", 1))
		var dump bytes.Buffer
		err := stuntgres.Dump(p.Ctx, p.Native, &dump)
		_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
		if err != nil || rollbackErr != nil {
			t.Fatalf("Dump = %v; rollback = %v", err, rollbackErr)
		}
		if strings.Contains(dump.String(), "140000000000") {
			t.Fatal("Dump leaked a private write")
		}
		ext, table := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
		if ext < 0 || table < ext || strings.Contains(dump.String(), "CREATE TYPE") || strings.Contains(dump.String(), "CREATE FUNCTION") {
			t.Fatal("Dump must install extension before consumers without standalone members", dump.String())
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,v FROM ean_control ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		ean13ControlVerify(t, p, b, c, b.Recorder)
		ean13Identity(t, p, b, c, "public")
		ean13ConsumeRetained(t, old)
		fixture.Ean13ColumnMetadata(t, false, metadata, []string{"id", "v"}, []string{"INT4", "EAN13"}, []bool{false, true})
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE ean_allocator_spacer(v integer)", 0))
		var sourceOID, displacedOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ean13'::regtype::oid").Scan(&sourceOID); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE EXTENSION isn", 0))
		if err := target.Memory.QueryRowContext(target.Ctx, "SELECT 'ean13'::regtype::oid").Scan(&displacedOID); err != nil {
			t.Fatal(err)
		}
		if sourceOID == displacedOID {
			t.Fatal("independent destination did not displace installed type allocation")
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		ean13ControlVerify(t, p, b, target.Memory, target.MemoryRecord)
		retained, err := target.Memory.QueryContext(target.Ctx, "SELECT id,v FROM ean_control ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		retainedMetadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
		ean13ControlVerify(t, p, b, target.Memory, target.MemoryRecord)
		ean13ConsumeRetained(t, retained)
		fixture.Ean13ColumnMetadata(t, false, retainedMetadata, []string{"id", "v"}, []string{"INT4", "EAN13"}, []bool{false, true})
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT relname FROM pg_class WHERE relname='ean_staged'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("Restore changed deterministic Dump", redump.String())
		}
		if p.Postgres != nil {
			ean13Replay(t, p, dump.String())
		}
	})
}

func ean13Replay(t *testing.T, p *fixture.Pair, dump string) {
	t.Helper()
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP EXTENSION IF EXISTS isn CASCADE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", "DISCARD ALL"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("EAN replay cleanup %s: %v", q, err)
			}
		}
	}()
	if _, err := c.ExecContext(p.Ctx, dump); err != nil {
		t.Fatal("actual native Dump SQL PostgreSQL replay", err)
	}
	for _, q := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE"} {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	b := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
	ean13ControlVerify(t, p, b, c, p.PostgresRecord)
	ean13Identity(t, p, b, c, "public")
}
