package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const intarrayBadRestore = "CREATE SCHEMA ia_provider; CREATE EXTENSION intarray SCHEMA ia_provider; CREATE TABLE ia_staged(k ia_provider.intbig_gkey,a ia_provider.intbig_gkey[]); INSERT INTO ia_staged VALUES (NULL,'{}'); INSERT INTO ia_staged VALUES ('x',NULL);"

func intarraySeedControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	intarraySeedArrays(t, p, b, c)
	intarrayInstall(t, p, b, c)
	intarraySeedSupport(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ia_queries(id integer NOT NULL,v ia_provider.query_int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_queries VALUES (1,'1&(2|3)'),(2,'!!1'),(3,NULL)", 3))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW ia_support_io AS SELECT ia_provider._intbig_in(NULL) AS k", 0))
}

func intarrayControlChecks(b fixture.Backend) []fixture.Step {
	return []fixture.Step{
		intarrayArrayCheck(b),
		intarraySelect(b, "SELECT id,v FROM ia_queries ORDER BY id", []string{"id", "v"}, []string{"INT4", "QUERY_INT"}, []bool{false, true}, [][]any{{int64(1), "1 & ( 2 | 3 )"}, {int64(2), "!( !1 )"}, {int64(3), nil}}),
		intarraySupportCheck(b),
	}
}

func intarrayControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) [4]int64 {
	t.Helper()
	for _, check := range intarrayControlChecks(b) {
		intarrayBoth(t, p, b, c, check)
	}
	intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT k FROM ia_support_io", []string{"k"}, []string{"INTBIG_GKEY"}, []bool{true}, [][]any{{nil}}))
	return intarrayDeclarationChecks(t, p, b, c, owner)
}

type intarrayRetained struct {
	rows    *sql.Rows
	columns []*sql.ColumnType
	check   fixture.Step
}

func intarrayRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []intarrayRetained {
	t.Helper()
	var readers []intarrayRetained
	for _, check := range intarrayControlChecks(b) {
		rows, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rows.Close() })
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		check.CheckMetadata(t, rows)
		readers = append(readers, intarrayRetained{rows, columns, check})
	}
	return readers
}

func intarrayConsume(t *testing.T, readers []intarrayRetained) {
	t.Helper()
	for _, r := range readers {
		got := make([][]any, 0)
		for r.rows.Next() {
			values := make([]any, len(r.check.Columns))
			destinations := make([]any, len(values))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err := r.rows.Scan(destinations...); err != nil {
				t.Fatal(err)
			}
			for i, value := range values {
				if !r.check.CheckRaw(t, i, len(got), value) {
					fixture.AssertRawValue(t, false, r.check.RawTypes[i], value)
				}
				values[i] = fixture.Normalize(value)
			}
			got = append(got, values)
		}
		if err := r.rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := r.rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, r.check.Rows) {
			t.Fatalf("retained query=%q got=%#v want=%#v", r.check.Query, got, r.check.Rows)
		}
		// ColumnTypes were captured before replacement and remain valid after
		// cursor close; no new lookup may substitute the destination type.
		nullable := []bool{false, true}
		if len(r.check.Columns) == 3 {
			nullable = []bool{false, true, true}
		}
		intarrayMetadata(t, false, r.columns, r.check.Columns, r.check.RawTypes, nullable)
	}
}

func intarrayDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	ext, table := strings.Index(text, "CREATE EXTENSION"), strings.Index(text, "CREATE TABLE")
	if ext < 0 || table < ext || strings.Count(text, "CREATE EXTENSION") != 1 || !strings.Contains(text[ext:table], "intarray") || strings.Contains(text, "CREATE TYPE") || strings.Contains(text, "CREATE FUNCTION") {
		t.Fatal("symbolic extension must precede consumers without standalone owned declarations", text)
	}
	return dump.Bytes()
}

func intarrayNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const sourceOwner = "postgres"
	sourceIDs := intarrayControlVerify(t, p, b, c, sourceOwner)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_values VALUES (99,'{987654321}')", 1))
	private := append(intarrayArrayRows(), []any{int64(99), "{987654321}"})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, intarraySelect(b, "SELECT id,v FROM ia_values ORDER BY id", []string{"id", "v"}, []string{"INT4", "_INT4"}, []bool{false, true}, private))
	dump := intarrayDump(t, p)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
	if bytes.Contains(dump, []byte("987654321")) {
		t.Fatal("Dump leaked uncommitted integer array")
	}
	intarrayExternalIO(t, p, b, c, dump)
	old := intarrayRetain(t, p, b, c)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	intarrayControlVerify(t, p, b, c, sourceOwner)
	intarrayConsume(t, old)

	target := fixture.NewFixture(t, false, true)
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	tc, err := tb.DB.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	// A live destination TEMP object reserves allocator identities through
	// replacement, unlike a persistent spacer that Restore is free to discard.
	fixture.RunStep(t, target.Ctx, tc, tb.Recorder, fixture.Exec("CREATE TEMP TABLE ia_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	destinationIDs := intarrayControlVerify(t, target, tb, tc, sourceOwner)
	if sourceIDs == destinationIDs {
		t.Fatalf("allocator displacement did not exercise destination remap: %v", destinationIDs)
	}
	old = intarrayRetain(t, target, tb, tc)
	before := intarrayDump(t, target)
	want := intarrayReject()
	want.Position = strconv.Itoa(strings.LastIndex(intarrayBadRestore, "'x'") + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(intarrayBadRestore)), &want)
	ctx, cancel := context.WithCancel(target.Ctx)
	cancel()
	ioFailure := errors.New("intarray control I/O sentinel")
	for _, tc := range []struct{ err, want error }{
		{stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump)), context.Canceled},
		{stuntgres.Dump(ctx, target.Native, new(bytes.Buffer)), context.Canceled},
		{stuntgres.Restore(target.Ctx, target.Native, fixture.CatalogControlFailingReader{Err: ioFailure}), ioFailure},
		{stuntgres.Dump(target.Ctx, target.Native, fixture.CatalogControlFailingWriter{Err: ioFailure}), ioFailure},
	} {
		var state interface{ SQLState() string }
		if !errors.Is(tc.err, tc.want) || errors.As(tc.err, &state) {
			t.Fatalf("ordinary control error got=%v want=%v", tc.err, tc.want)
		}
	}
	if after := intarrayControlVerify(t, target, tb, tc, sourceOwner); after != destinationIDs {
		t.Fatalf("failed replacement changed incumbent identities %v -> %v", destinationIDs, after)
	}
	intarrayConsume(t, old)
	if after := intarrayDump(t, target); !bytes.Equal(before, after) {
		t.Fatal("failed replacement changed deterministic dump", string(after))
	}
	if p.Postgres != nil {
		// Genuine SQL replay of the actual emitted native dump; PostgreSQL has
		// no native Reset/Restore API and receives no synthetic equivalent.
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		pc, err := pg.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		// Register before executing any replay SQL. Fatal assertions still
		// restore the fixture principal and remove every replay-owned object
		// before the subsequent PostgreSQL backend starts its fresh scenario.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, query := range []string{
				"ROLLBACK",
				"RESET SESSION AUTHORIZATION",
				"DROP SCHEMA IF EXISTS ia_provider CASCADE",
				"DROP SCHEMA IF EXISTS public CASCADE",
				"CREATE SCHEMA public",
			} {
				if _, err := pc.ExecContext(ctx, query); err != nil {
					t.Errorf("intarray replay cleanup %q: %v", query, err)
				}
			}
		}()
		for _, q := range []string{"DROP SCHEMA IF EXISTS ia_provider CASCADE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", string(dump)} {
			if _, err := pc.ExecContext(p.Ctx, q); err != nil {
				t.Fatal("actual emitted SQL replay", err)
			}
		}
		intarrayControlVerify(t, p, pg, pc, sourceOwner)
	}
}

func TestReferenceIntarrayProviderControls(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarraySeedControls(t, p, b, c)
			intarrayControlVerify(t, p, b, c, intarrayFixtureOwner(b))
			if !b.PG {
				intarrayNativeControls(t, p, b, c)
			}
		})
	})
	t.Run("staged_error_location", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			want := intarrayReject()
			want.Position = strconv.Itoa(strings.LastIndex(intarrayBadRestore, "'x'") + 1)
			if b.PG {
				intarrayError(t, p, b, c, intarrayBadRestore, false, "execute", want)
			} else {
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(intarrayBadRestore)), &want)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		})
	})
}
