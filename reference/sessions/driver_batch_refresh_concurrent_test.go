package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"regexp"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Reuse the existing retained-message fixtures/result-set assertions. Discovery
// changes backend availability only; the SQL/API body is shared.
func concurrentBatchScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, fixture.ControlsReferenceFixture)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, b.PG)
			b.DB, b.Recorder = f.DB, f.Recorder
			body(t, p, b, f)
		})
	}
}

func concurrentBatchSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, peer bool) {
	t.Helper()
	batchExec(t, p, b, f.A, "CREATE TABLE batch_concurrent_base(i INT,s TEXT)", 0)
	batchExec(t, p, b, f.A, "INSERT INTO batch_concurrent_base VALUES(1,'old'),(2,NULL)", 2)
	batchExec(t, p, b, f.A, "CREATE MATERIALIZED VIEW batch_concurrent_mv AS SELECT i,s FROM batch_concurrent_base", 2)
	batchExec(t, p, b, f.A, "CREATE UNIQUE INDEX batch_concurrent_u ON batch_concurrent_mv(i) INCLUDE(s)", 0)
	if peer {
		batchExec(t, p, b, f.A, "CREATE MATERIALIZED VIEW batch_concurrent_peer AS SELECT i,s FROM batch_concurrent_base", 2)
		batchExec(t, p, b, f.A, "CREATE UNIQUE INDEX batch_concurrent_peer_u ON batch_concurrent_peer(i)", 0)
	}
}

func concurrentBatchOld() [][]any { return [][]any{{int64(1), "old"}, {int64(2), nil}} }
func concurrentBatchNew() [][]any { return [][]any{{int64(1), "new"}, {int64(3), "three"}} }

const concurrentBatchChange = "UPDATE batch_concurrent_base SET s='new' WHERE i=1; DELETE FROM batch_concurrent_base WHERE i=2; INSERT INTO batch_concurrent_base VALUES(3,'three'); "
const concurrentBatchRefresh = "REFRESH MATERIALIZED VIEW CONCURRENTLY batch_concurrent_mv"

func concurrentBatchDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &buffer); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(buffer.Bytes())
}

func TestReferenceMaterializedRefreshConcurrentRetained(t *testing.T) {
	t.Run("multiple_objects_repeated_and_RR", func(t *testing.T) {
		concurrentBatchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			concurrentBatchSeed(t, p, b, f, true)
			tx, err := f.B.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			fixture.QueryLockRollbackCleanup(t, tx)
			batchRefreshRows(t, p, b, tx, "batch_concurrent_mv", concurrentBatchOld())
			q := concurrentBatchChange + concurrentBatchRefresh + "; REFRESH MATERIALIZED VIEW CONCURRENTLY batch_concurrent_peer; SELECT i,s FROM batch_concurrent_mv; UPDATE batch_concurrent_base SET s='latest' WHERE i=1; " + concurrentBatchRefresh + "; REFRESH MATERIALIZED VIEW CONCURRENTLY batch_concurrent_peer; SELECT i,s FROM batch_concurrent_mv"
			latest := [][]any{{int64(1), "latest"}, {int64(3), "three"}}
			batchSets(t, batchQuery(t, p, f.A, q), batchRefreshSet(concurrentBatchNew()...), batchRefreshSet(latest...))
			batchRefreshRows(t, p, b, tx, "batch_concurrent_mv", concurrentBatchOld())
			batchRefreshRows(t, p, b, tx, "batch_concurrent_peer", concurrentBatchOld())
			observer, err := f.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			for _, name := range []string{"batch_concurrent_mv", "batch_concurrent_peer", "batch_concurrent_base"} {
				batchRefreshRows(t, p, b, observer, name, latest)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", latest)
		})
	})
	t.Run("private_create_nullable_survivor", func(t *testing.T) {
		concurrentBatchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			batchExec(t, p, b, f.A, "CREATE TABLE batch_concurrent_base(i INT,s TEXT)", 0)
			batchExec(t, p, b, f.A, "INSERT INTO batch_concurrent_base VALUES(1,NULL)", 1)
			q := "CREATE MATERIALIZED VIEW batch_concurrent_mv AS SELECT i,s FROM batch_concurrent_base; CREATE UNIQUE INDEX batch_concurrent_u ON batch_concurrent_mv(i) INCLUDE(s); INSERT INTO batch_concurrent_base VALUES(1,NULL); " + concurrentBatchRefresh + "; SELECT i,s FROM batch_concurrent_mv; " + concurrentBatchRefresh + "; SELECT i,s FROM batch_concurrent_mv"
			one := [][]any{{int64(1), nil}}
			batchSets(t, batchQuery(t, p, f.A, q), batchRefreshSet(one...), batchRefreshSet(one...))
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", one)
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_base", [][]any{{int64(1), nil}, {int64(1), nil}})
			batchRefreshPopulation(t, p, b, f.B, "batch_concurrent_mv", true)
			batchExec(t, p, b, f.B, concurrentBatchRefresh, 0)
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", one)
		})
	})
	t.Run("late_failure_rolls_back_prefix", func(t *testing.T) {
		concurrentBatchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			concurrentBatchSeed(t, p, b, f, false)
			var before []byte
			if !b.PG {
				before = concurrentBatchDump(t, p)
			}
			q := "SELECT i,s FROM batch_concurrent_mv; " + concurrentBatchChange + concurrentBatchRefresh + "; SELECT i,s FROM batch_concurrent_mv; " + concurrentBatchRefresh + "; SELECT missing FROM batch_concurrent_base"
			rows := batchQuery(t, p, f.A, q)
			defer rows.Close()
			batchReadSet(t, rows, batchRefreshSet(concurrentBatchOld()...))
			if !rows.NextResultSet() {
				t.Fatal("lost private concurrent output", rows.Err())
			}
			batchReadSet(t, rows, batchRefreshSet(concurrentBatchNew()...))
			if rows.NextResultSet() {
				t.Fatal("failed suffix returned output")
			}
			d := batchDiag("42703", `column "missing" does not exist`)
			d.Position = batchPosition(q, "missing")
			fixture.AssertDiagnostic(t, rows.Err(), &d)
			rows.Close()
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", concurrentBatchOld())
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_base", concurrentBatchOld())
			if !b.PG && !bytes.Equal(before, concurrentBatchDump(t, p)) {
				t.Fatal("late suffix changed Dump")
			}
			batchExec(t, p, b, f.A, concurrentBatchChange+concurrentBatchRefresh, 0)
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", concurrentBatchNew())
		})
	})
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "promotion_rollback", true: "promotion_commit"}[commit], func(t *testing.T) {
			concurrentBatchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
				concurrentBatchSeed(t, p, b, f, false)
				q := "SELECT i,s FROM batch_concurrent_mv; " + concurrentBatchChange + concurrentBatchRefresh + "; BEGIN; SELECT i,s FROM batch_concurrent_mv"
				batchSets(t, batchQuery(t, p, f.A, q), batchRefreshSet(concurrentBatchOld()...), batchRefreshSet(concurrentBatchNew()...))
				batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", concurrentBatchOld())
				end := "ROLLBACK"
				want := concurrentBatchOld()
				if commit {
					end = "COMMIT"
					want = concurrentBatchNew()
				}
				batchExec(t, p, b, f.A, end, 0)
				batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", want)
				batchRefreshRows(t, p, b, f.B, "batch_concurrent_base", want)
			})
		})
	}
	t.Run("owner_runtime_diagnostic", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"batch_concurrent_caller"})
		concurrentBatchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			var bootstrap string
			if err := f.A.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&bootstrap); err != nil {
				t.Fatal(err)
			}
			schemaTransferred := false
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				if _, err := f.A.ExecContext(cleanup, "RESET ROLE"); err != nil {
					t.Error("retained role cleanup", err)
				}
				if schemaTransferred {
					if _, err := f.A.ExecContext(cleanup, "ALTER SCHEMA public OWNER TO "+pq.QuoteIdentifier(bootstrap)); err != nil {
						t.Error("retained schema owner cleanup", err)
					}
				}
			}()
			concurrentBatchSeed(t, p, b, f, false)
			batchExec(t, p, b, f.A, "CREATE ROLE batch_concurrent_caller", 0)
			batchExec(t, p, b, f.A, "GRANT MAINTAIN ON batch_concurrent_mv TO batch_concurrent_caller", 0)
			if b.PG {
				batchExec(t, p, b, f.A, "GRANT USAGE ON SCHEMA public TO batch_concurrent_caller", 0)
			} else {
				// Native schema GRANT is not implemented. Transfer only schema authority;
				// the bootstrap-owned base/MV/index and explicit table ACL stay unchanged.
				batchExec(t, p, b, f.A, "ALTER SCHEMA public OWNER TO batch_concurrent_caller", 0)
				schemaTransferred = true
			}
			fixture.RunStep(t, p.Ctx, f.A, b.Recorder, fixture.Select("SELECT matviewowner FROM pg_matviews WHERE matviewname='batch_concurrent_mv'", []string{"matviewowner"}, []string{"NAME"}, [][]any{{bootstrap}}))
			batchExec(t, p, b, f.A, "SET ROLE batch_concurrent_caller", 0)
			denied := fixture.ErrorDiagnostic("SELECT i,s FROM public.batch_concurrent_mv", batchDiag("42501", "permission denied for materialized view batch_concurrent_mv"))
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, f.A, b.Recorder, denied)
			batchExec(t, p, b, f.A, "RESET ROLE", 0)
			q := "INSERT INTO batch_concurrent_base VALUES(1,'different'); SET ROLE batch_concurrent_caller; REFRESH MATERIALIZED VIEW CONCURRENTLY public.batch_concurrent_mv; RESET ROLE"
			result, err := f.A.ExecContext(p.Ctx, q)
			if result != nil {
				t.Fatal("failed retained runtime exposed result")
			}
			raw := fixture.ExtractDiagnostic(t, err)
			if raw.Code != "23505" || raw.Message != `duplicate key value violates unique constraint "batch_concurrent_u"` {
				t.Fatalf("retained runtime failed before expected unique violation: %#v", raw)
			}
			if b.PG {
				var server *pq.Error
				if !errors.As(err, &server) {
					t.Fatalf("retained runtime transport: %#v; error %v", raw, err)
				}
				shape := regexp.MustCompile(`^SQL statement "INSERT INTO public\.batch_concurrent_mv SELECT \(diff\.newdata\)\.\* FROM pg_temp_[1-9][0-9]*\.pg_temp_[1-9][0-9]*_2 diff WHERE tid IS NULL"$`)
				if !shape.MatchString(raw.Context) {
					t.Fatalf("retained runtime physical context mismatch; complete diagnostic: %#v", raw)
				}
				copy := *server
				copy.Where = ""
				err = &copy
			}
			d := batchDiag("23505", `duplicate key value violates unique constraint "batch_concurrent_u"`)
			d.Detail = "Key (i)=(1) already exists."
			d.Schema = "public"
			d.Table = "batch_concurrent_mv"
			d.Constraint = "batch_concurrent_u"
			fixture.AssertDiagnostic(t, err, &d)
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_mv", concurrentBatchOld())
			batchRefreshRows(t, p, b, f.B, "batch_concurrent_base", concurrentBatchOld())
		})
	})
}
