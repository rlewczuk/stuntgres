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

func intarraySortControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) []int64 {
	t.Helper()
	intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
	intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT id,d,u FROM ia_sort_rows ORDER BY id", []string{"id", "d", "u"}, []string{"INT4", "_INT4", "_INT4"}, []bool{false, true, true}, [][]any{{int64(1), "{3,2,1,1}", "{1,2,3}"}, {int64(2), "{3,2,1,1}", "{1,2,3}"}, {int64(3), "{3,2,1,1}", "{1,2,3}"}}))
	intarraySortDefaults(t, p, b, c)
	return intarraySortCatalog(t, p, b, c, owner)
}

func intarraySortReplay(t *testing.T, p *fixture.Pair, dump []byte) {
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
	// Unconditional cleanup is registered before destructive replay. Native
	// source ownership is postgres, independently of the PostgreSQL caller.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS ia_collision CASCADE", "DROP SCHEMA IF EXISTS ia_provider CASCADE", "DROP SCHEMA IF EXISTS public CASCADE", "CREATE SCHEMA public"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("sort replay cleanup %q: %v", q, err)
			}
		}
	}()
	for _, q := range []string{"DROP SCHEMA IF EXISTS ia_collision CASCADE", "DROP SCHEMA IF EXISTS ia_provider CASCADE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", string(dump)} {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal("actual emitted sort SQL replay", err)
		}
	}
	intarraySortControlVerify(t, p, b, c, "postgres")
}

func intarraySortNativeRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	sourceIDs := intarraySortControlVerify(t, p, b, c, "postgres")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	defer c.ExecContext(p.Ctx, "ROLLBACK")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_sort_rows(id,a) VALUES (99,'{987654321}')", 1))
	dump := intarrayDump(t, p)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
	if bytes.Contains(dump, []byte("987654321")) {
		t.Fatal("Dump leaked private sort inputs")
	}
	if again := intarrayDump(t, p); !bytes.Equal(dump, again) {
		t.Fatal("committed sort dump is not deterministic")
	}
	old := intarraySortRetain(t, p, b, c)
	defer old.rows.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	intarrayBoth(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	intarraySortControlVerify(t, p, b, c, "postgres")
	intarraySortConsume(t, b, old)

	target := fixture.NewFixture(t, false, true)
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	tc, err := tb.DB.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	fixture.RunStep(t, target.Ctx, tc, tb.Recorder, fixture.Exec("CREATE TEMP TABLE ia_sort_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	destinationIDs := intarraySortControlVerify(t, target, tb, tc, "postgres")
	if reflect.DeepEqual(sourceIDs, destinationIDs) {
		t.Fatal("allocator spacer did not displace sort identities")
	}
	intarraySortReplay(t, p, dump)
}

// Restored INSERT values retain the public Restore scalar whitelist. Invoke
// the installed operations through stored CHECK expressions, not arbitrary
// function calls in the INSERT payload.
var intarraySortRestoreFailures = []struct {
	query      string
	diagnostic fixture.Diagnostic
}{
	{"INSERT INTO ia_sort_rows(id,a) VALUES (10,'{2,1}'),(11,'{1,NULL}')", intarraySetNullError()},
	{"INSERT INTO ia_sort_uniq_guard(id,a) VALUES (10,'{2,1}'),(11,'{1,NULL}')", intarraySetNullError()},
	{"INSERT INTO ia_sort_rows(id,a,direction) VALUES (10,'{2,1}','ASC'),(11,'{2,1}','invalid')", intarraySortDirectionError()},
}

func intarraySortFailureRows(b fixture.Backend) fixture.Step {
	return intarraySelect(b, "SELECT id,a FROM ia_sort_uniq_guard ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{int64(1), "{1,1,2}"}})
}

func intarraySortNativeFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	ids := intarraySortControlVerify(t, p, b, c, "postgres")
	dump := intarrayDump(t, p)
	old := intarraySortRetain(t, p, b, c)
	defer old.rows.Close()
	for _, tc := range intarraySortRestoreFailures {
		bad := append(append([]byte(nil), dump...), []byte("\n"+tc.query+";\n")...)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)), &tc.diagnostic)
		if after := intarrayDump(t, p); !bytes.Equal(dump, after) {
			t.Fatal("failed SQL replacement changed sort state")
		}
		if afterIDs := intarraySortControlVerify(t, p, b, c, "postgres"); !reflect.DeepEqual(ids, afterIDs) {
			t.Fatalf("failed SQL replacement changed sort identities %v -> %v", ids, afterIDs)
		}
		intarrayBoth(t, p, b, c, intarraySortFailureRows(b))
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	ioFailure := errors.New("sort control I/O sentinel")
	for _, item := range []struct{ got, want error }{
		{stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled},
		{stuntgres.Dump(ctx, p.Native, new(bytes.Buffer)), context.Canceled},
		{stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: ioFailure}), ioFailure},
		{stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{Err: ioFailure}), ioFailure},
	} {
		var state interface{ SQLState() string }
		if !errors.Is(item.got, item.want) || errors.As(item.got, &state) {
			t.Fatalf("ordinary sort control error %v want %v", item.got, item.want)
		}
		if after := intarrayDump(t, p); !bytes.Equal(dump, after) {
			t.Fatal("failed control mutated sort dump")
		}
	}
	if afterIDs := intarraySortControlVerify(t, p, b, c, "postgres"); !reflect.DeepEqual(ids, afterIDs) {
		t.Fatalf("failed controls changed sort identities %v -> %v", ids, afterIDs)
	}
	intarrayBoth(t, p, b, c, intarraySortFailureRows(b))
	intarraySortConsume(t, b, old)
	if after := intarrayDump(t, p); !bytes.Equal(dump, after) {
		t.Fatal("failed replacement changed sort logical state")
	}
}

func TestReferenceIntarraySortControls(t *testing.T) {
	for _, phase := range []string{"round_trip", "failed_replacement"} {
		t.Run(phase, func(t *testing.T) {
			intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				intarraySortSeed(t, p, b, c)
				if phase == "failed_replacement" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ia_sort_uniq_guard(id integer NOT NULL,a integer[],CHECK (ia_provider.uniq(a) IS NOT NULL))", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_sort_uniq_guard VALUES (1,'{1,1,2}')", 1))
					intarrayBoth(t, p, b, c, intarraySortFailureRows(b))
				}
				intarraySortControlVerify(t, p, b, c, intarrayFixtureOwner(b))
				if !b.PG {
					if phase == "round_trip" {
						intarraySortNativeRoundtrip(t, p, b, c)
					} else {
						intarraySortNativeFailures(t, p, b, c)
					}
				} else {
					// Same genuine SQL failures and unchanged stored consumers on PG;
					// native-only controls remain explicit phases of this scenario.
					for _, q := range []string{
						"INSERT INTO ia_sort_rows(id,a) VALUES (10,'{2,1}'),(11,ia_provider.sort('{NULL}'::integer[]))",
						"INSERT INTO ia_sort_rows(id,a) VALUES (10,'{2,1}'),(11,ia_provider.uniq('{NULL}'::integer[]))",
					} {
						for _, prepared := range []bool{false, true} {
							intarrayError(t, p, b, c, q, prepared, "execute", intarraySetNullError())
						}
					}
					if phase == "failed_replacement" {
						for _, tc := range intarraySortRestoreFailures {
							for _, prepared := range []bool{false, true} {
								intarrayError(t, p, b, c, tc.query, prepared, "execute", tc.diagnostic)
								intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
								intarrayBoth(t, p, b, c, intarraySortFailureRows(b))
							}
						}
					}
					intarraySortControlVerify(t, p, b, c, "stuntgres")
				}
			})
		})
	}
}
