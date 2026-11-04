package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func intarraySetControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) []int64 {
	t.Helper()
	intarrayBoth(t, p, b, c, intarraySetStoredCheck(b))
	intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT id,d FROM ia_set_rows ORDER BY id", []string{"id", "d"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{int64(1), "{1,2,3}"}, {int64(2), "{1,2,3}"}, {int64(3), "{1,2,3}"}}))
	// Exercise stored DEFAULT and CHECK code after replacement, not only rows
	// that were serialized with already computed values.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_set_rows(id,a,b) VALUES (4,'{6,6}','{}')", 1))
	intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT d FROM ia_set_rows WHERE id=4", []string{"d"}, []string{"_INT4"}, []bool{true}, [][]any{{"{1,2,3}"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ia_set_rows WHERE id=4", 1))
	return intarraySetCatalog(t, p, b, c, owner)
}

func intarraySetReplay(t *testing.T, p *fixture.Pair, dump []byte) {
	t.Helper()
	if p.Postgres == nil {
		return
	}
	b := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Registered before the first replay statement, including authorization
	// restoration on fatal assertions. Replay's source owner is postgres.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS ia_collision CASCADE", "DROP SCHEMA IF EXISTS ia_provider CASCADE", "DROP SCHEMA IF EXISTS public CASCADE", "CREATE SCHEMA public"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("intarray set replay cleanup %q: %v", q, err)
			}
		}
	}()
	for _, q := range []string{"DROP SCHEMA IF EXISTS ia_collision CASCADE", "DROP SCHEMA IF EXISTS ia_provider CASCADE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", string(dump)} {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
	}
	intarraySetControlVerify(t, p, b, c, "postgres")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_set_rows(id,a,b) VALUES (4,'{6,6}','{}')", 1))
	intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT d FROM ia_set_rows WHERE id=4", []string{"d"}, []string{"_INT4"}, []bool{true}, [][]any{{"{1,2,3}"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ia_set_rows WHERE id=4", 1))
}

func intarraySetNativeRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const sourceOwner = "postgres"
	sourceIDs := intarraySetControlVerify(t, p, b, c, sourceOwner)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_set_rows(id,a,b) VALUES (99,'{987654321}','{}')", 1))
	dump := intarrayDump(t, p)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
	if bytes.Contains(dump, []byte("987654321")) {
		t.Fatal("Dump leaked transaction-private selected inputs")
	}
	old := intarraySetRetain(t, p, b, c)
	defer old.rows.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	intarraySetControlVerify(t, p, b, c, sourceOwner)
	intarraySetConsume(t, b, old)

	target := fixture.NewFixture(t, false, true)
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	tc, err := tb.DB.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	fixture.RunStep(t, target.Ctx, tc, tb.Recorder, fixture.Exec("CREATE TEMP TABLE ia_set_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	destinationIDs := intarraySetControlVerify(t, target, tb, tc, sourceOwner)
	if reflect.DeepEqual(sourceIDs, destinationIDs) {
		t.Fatal("live TEMP allocator spacer did not displace selected identities")
	}
	intarraySetReplay(t, p, dump)
}

func intarraySetNativeFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	ids := intarraySetControlVerify(t, p, b, c, "postgres")
	dump := intarrayDump(t, p)
	old := intarraySetRetain(t, p, b, c)
	defer old.rows.Close()
	// A genuine emitted stream reaches the stored row-dependent containment
	// CHECK. Even its empty right operand must not bypass NULL validation.
	bad := append(append([]byte(nil), dump...), []byte("\nINSERT INTO ia_set_rows(id,a,b) VALUES (11,'{NULL}','{}');\n")...)
	want := intarraySetNullError()
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)), &want)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	ioFailure := errors.New("intarray selected control I/O sentinel")
	for _, item := range []struct{ got, want error }{
		{stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled},
		{stuntgres.Dump(ctx, p.Native, new(bytes.Buffer)), context.Canceled},
		{stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: ioFailure}), ioFailure},
		{stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: ioFailure}), ioFailure},
	} {
		var state interface{ SQLState() string }
		if !errors.Is(item.got, item.want) || errors.As(item.got, &state) {
			t.Fatalf("ordinary control error %v want %v", item.got, item.want)
		}
	}
	afterIDs := intarraySetControlVerify(t, p, b, c, "postgres")
	if !reflect.DeepEqual(ids, afterIDs) {
		t.Fatalf("failed replacement changed selected identities %v -> %v", ids, afterIDs)
	}
	intarraySetConsume(t, b, old)
	if after := intarrayDump(t, p); !bytes.Equal(dump, after) {
		t.Fatal("failed replacement changed deterministic dump", string(after))
	}
}

func TestReferenceIntarraySetControls(t *testing.T) {
	for _, phase := range []string{"round_trip", "failed_replacement"} {
		t.Run(phase, func(t *testing.T) {
			intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				intarraySetSeed(t, p, b, c)
				intarrayBoth(t, p, b, c, intarraySetStoredCheck(b))
				if !b.PG {
					if phase == "round_trip" {
						intarraySetNativeRoundtrip(t, p, b, c)
					} else {
						intarraySetNativeFailures(t, p, b, c)
					}
				} else {
					// PostgreSQL has no native control format: independently exercise
					// the identical selected failure as ordinary atomic SQL instead.
					q := "INSERT INTO ia_set_rows(id,a,b) VALUES (11,'{NULL}','{}')"
					intarrayError(t, p, b, c, q, false, "execute", intarraySetNullError())
					intarrayBoth(t, p, b, c, intarraySetStoredCheck(b))
				}
			})
		})
	}
}
