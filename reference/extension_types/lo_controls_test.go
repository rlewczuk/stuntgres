package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const loControlInsert = "INSERT INTO lo_controls VALUES (1,'0'),(2,'26'),(3,'2147483648'),(4,'4294967295'),(5,'4294967295'),(6,NULL)"

func loControlWant() [][]any {
	return [][]any{{int64(0)}, {int64(26)}, {int64(2147483648)}, {int64(4294967295)}, {int64(4294967295)}, {nil}}
}

func loControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT v FROM lo_controls ORDER BY id", []string{"v"}, []string{"OID"}, loControlWant())
		s.Prepared = prepared
		loMetadataRows(t, p, b, db, s, []bool{true})
		s = fixture.Select("SELECT v FROM lo_vacant", []string{"v"}, []string{"OID"}, [][]any{})
		s.Prepared = prepared
		loMetadataRows(t, p, b, db, s, []bool{false})
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT t.typname AS logical,t.typtype AS kind,t.typbasetype::bigint AS base FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid='lo_controls'::regclass AND a.attname='v'", []string{"logical", "kind", "base"}, []string{"NAME", "CHAR", "INT8"}, [][]any{{"lo", "d", int64(26)}})
		s.Prepared = prepared
		loRun(t, p, b, db, s)
		s = fixture.Select("SELECT v::text AS canonical FROM lo_controls WHERE id=4", []string{"canonical"}, []string{"TEXT"}, [][]any{{"4294967295"}})
		s.Prepared = prepared
		loRun(t, p, b, db, s)
		if b.PG {
			s = fixture.Select("SELECT pg_typeof(v)::text AS logical,v::text AS canonical FROM lo_controls WHERE id=4", []string{"logical", "canonical"}, []string{"TEXT", "TEXT"}, [][]any{{"lo", "4294967295"}})
			s.Prepared = prepared
			loRun(t, p, b, db, s)
		}
	}
	loRun(t, p, b, db, fixture.Select("SELECT atttypid='lo'::regtype AS logical FROM pg_attribute WHERE attrelid='lo_controls'::regclass AND attname='v'", []string{"logical"}, []string{"BOOL"}, [][]any{{true}}))
	loIdentity(t, p, b, db)
}

func loRetain(t *testing.T, p *fixture.Pair, db fixture.Executor) (*sql.Rows, []*sql.ColumnType) {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, "SELECT v FROM lo_controls ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rows.Close() })
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	loColumnMetadata(t, false, columns, []string{"v"}, []bool{true})
	return rows, columns
}

func loConsumeRetained(t *testing.T, rows *sql.Rows, columns []*sql.ColumnType) {
	t.Helper()
	want := loControlWant()
	i := 0
	for rows.Next() {
		var value any
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		value = fixture.OidReferenceValue(t, false, "OID", value)
		if i >= len(want) || value != want[i][0] {
			t.Fatal("retained lo value", i, value, want)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(want) {
		t.Fatal("retained cardinality", i)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	loColumnMetadata(t, false, columns, []string{"v"}, []bool{true})
}

type loFailIO struct{ err error }

func (f loFailIO) Read([]byte) (int, error)  { return 0, f.err }
func (f loFailIO) Write([]byte) (int, error) { return 0, f.err }

func loControlDump(t *testing.T, p *fixture.Pair) *bytes.Buffer {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	extensionAt, tableAt := strings.Index(text, "CREATE EXTENSION"), strings.Index(text, "CREATE TABLE")
	if extensionAt < 0 || tableAt <= extensionAt || !strings.Contains(text[extensionAt:tableAt], "lo") || strings.Contains(text, "CREATE DOMAIN") || strings.Contains(text, "CREATE FUNCTION") {
		t.Fatal("lo dump extension-before-consumers recipe", text)
	}
	return &dump
}

func loSameEngineControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dump := loControlDump(t, p)
	old, metadata := loRetain(t, p, c)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	loControlVerify(t, p, b, c)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	loAbsent(t, p, b, c)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	loControlVerify(t, p, b, c)
	loConsumeRetained(t, old, metadata)
}

func loFreshEngineControls(t *testing.T, p *fixture.Pair) {
	t.Helper()
	dump := loControlDump(t, p)
	text := dump.String()

	target := fixture.NewFixture(t, false, true)
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	loRun(t, target, tb, target.Memory, fixture.Exec("CREATE TABLE lo_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	loControlVerify(t, target, tb, target.Memory)
	loRun(t, target, tb, target.Memory, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='lo_allocator_spacer'", []string{"relname"}, []string{"NAME"}, [][]any{}))
	retained, captured := loRetain(t, target, target.Memory)
	var identity int64
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT oid::bigint FROM pg_type WHERE oid='lo'::regtype").Scan(&identity); err != nil {
		t.Fatal(err)
	}
	bad := "CREATE EXTENSION lo; CREATE TABLE lo_staged(v lo); INSERT INTO lo_staged VALUES ('26'); INSERT INTO lo_staged VALUES ('bad');"
	d := loInputDiagnostic("bad", "22P02")
	d.Position = strconv.Itoa(strings.LastIndex(bad, "'bad'") + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
	ctx, cancel := context.WithCancel(target.Ctx)
	cancel()
	for _, err := range []error{
		stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())),
		stuntgres.Dump(ctx, target.Native, new(bytes.Buffer)),
	} {
		var state interface{ SQLState() string }
		if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
			t.Fatal("ordinary control cancellation", err)
		}
	}
	ioErr := errors.New("lo control I/O sentinel")
	for _, err := range []error{
		stuntgres.Restore(target.Ctx, target.Native, loFailIO{ioErr}),
		stuntgres.Dump(target.Ctx, target.Native, loFailIO{ioErr}),
	} {
		var state interface{ SQLState() string }
		if !errors.Is(err, ioErr) || errors.As(err, &state) {
			t.Fatal("ordinary control I/O error", err)
		}
	}
	loRun(t, target, tb, target.Memory, fixture.Select("SELECT oid::bigint=$1 AS retained_identity FROM pg_type WHERE oid='lo'::regtype", []string{"retained_identity"}, []string{"BOOL"}, [][]any{{true}}, identity))
	loControlVerify(t, target, tb, target.Memory)
	loConsumeRetained(t, retained, captured)
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("lo deterministic replacement dump", after.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, text); err != nil {
			t.Fatal("actual lo dump SQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		loControlVerify(t, p, pg, pg.DB)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func loInvalidOwnerDump(t *testing.T, script string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v2 "
	if strings.Count(script, marker) != 1 {
		t.Fatal("expected exactly one actual lo owner record")
	}
	start := strings.Index(script, marker)
	if start < 0 {
		t.Fatal("missing actual lo owner metadata")
	}
	endOffset := strings.IndexByte(script[start:], '\n')
	if endOffset < 0 {
		t.Fatal("unterminated actual lo owner metadata")
	}
	end := start + endOffset
	data, err := base64.StdEncoding.DecodeString(script[start+len(marker) : end])
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(record["name"], &name); err != nil || name != "lo" {
		t.Fatal("actual owner record is not lo", name, err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil || len(members) != 2 {
		t.Fatal("actual lo owner member inventory", members, err)
	}
	changed := false
	for _, member := range members {
		var slot int
		if err := json.Unmarshal(member["slot"], &slot); err != nil {
			t.Fatal(err)
		}
		if slot != 0 {
			continue
		}
		var role string
		if err := json.Unmarshal(member["name"], &role); err != nil || role != "lo" || changed {
			t.Fatal("actual slot-zero domain role", role, err)
		}
		// Valid JSON and a real member slot, but the companion role is never
		// authority for the domain slot. All other emitted fields stay intact.
		member["name"] = json.RawMessage(`"lo[]"`)
		changed = true
	}
	if !changed {
		t.Fatal("missing slot-zero mutation target")
	}
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the genuine SQL unchanged and place the invalid ownership directive
	// after its extension, table and data statements.
	return script[:start] + script[end+1:] + marker + base64.StdEncoding.EncodeToString(data) + "\n"
}

func loOwnerMetadataControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dump := loControlDump(t, p)
	bad := loInvalidOwnerDump(t, dump.String())
	retained, metadata := loRetain(t, p, c)
	var identity int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE oid='lo'::regtype").Scan(&identity); err != nil {
		t.Fatal(err)
	}
	err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	var state interface{ SQLState() string }
	if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
		t.Fatal("invalid slot-zero lo ownership must reject as a control error", err)
	}
	loRun(t, p, b, c, fixture.Select("SELECT oid::bigint=$1 AS retained_identity FROM pg_type WHERE oid='lo'::regtype", []string{"retained_identity"}, []string{"BOOL"}, [][]any{{true}}, identity))
	loControlVerify(t, p, b, c)
	loConsumeRetained(t, retained, metadata)
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("invalid lo owner Restore changed deterministic dump", after.String())
	}
	if p.Postgres != nil {
		// PostgreSQL ignores native ownership comments: replaying this actual
		// emitted SQL proves it remains valid, not that PG validates the codec.
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, bad); err != nil {
			t.Fatal("actual emitted lo SQL with invalid native owner comment", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		loControlVerify(t, p, pg, pg.DB)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceLoControls(t *testing.T) {
	// Each independent lifecycle owns a fresh bounded fixture; no context is
	// refreshed or extended after a native replacement phase.
	for _, phase := range []string{"same_engine", "fresh_engine", "owner_metadata", "recreation"} {
		t.Run(phase, func(t *testing.T) {
			loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				loInstall(t, p, b, c)
				loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_controls(id integer NOT NULL,v lo)", 0))
				loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_vacant(v lo NOT NULL)", 0))
				loRun(t, p, b, c, fixture.Exec(loControlInsert, 6))
				loControlVerify(t, p, b, c)
				// Controls are native-only phases in the shared scenario. The
				// fresh-engine phase replays actual emitted SQL on PostgreSQL.
				switch phase {
				case "same_engine":
					if !b.PG {
						loSameEngineControls(t, p, b, c)
					}
					return
				case "fresh_engine":
					if !b.PG {
						loFreshEngineControls(t, p)
					}
					return
				case "owner_metadata":
					if !b.PG {
						loOwnerMetadataControls(t, p, b, c)
					}
					return
				}
				for _, q := range []string{"DROP TABLE lo_controls", "DROP TABLE lo_vacant", "DROP EXTENSION lo"} {
					loRun(t, p, b, c, fixture.Exec(q, 0))
				}
				loAbsent(t, p, b, c)
				loInstall(t, p, b, c)
				loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_controls(id integer NOT NULL,v lo)", 0))
				loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_vacant(v lo NOT NULL)", 0))
				q := "INSERT INTO lo_controls VALUES ($1,$2)"
				held := loHeld(t, p, c, q)
				for i, row := range loControlWant() {
					db := fixture.Executor(c)
					if i%2 == 1 {
						db = held
					}
					loRun(t, p, b, db, fixture.Exec(q, 1, i+1, row[0]))
				}
				loControlVerify(t, p, b, c)
			})
		})
	}
}
