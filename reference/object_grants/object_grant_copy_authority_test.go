package reference_test

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantCopyRoutineAuthority(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		t.Run(map[bool]string{false: "check", true: "default"}[defaults], func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.json_array_length(json)"})
				objectGrantConsumerACL(run)
				table := "rg_copy"
				declaration := "CREATE TABLE rg_copy(id integer,j json,CONSTRAINT rg_copy_nonempty CHECK(pg_catalog.json_array_length(j)>0))"
				seed := "INSERT INTO rg_copy VALUES(99,'[9]'::json)"
				query := "COPY rg_copy FROM STDIN"
				payload := "1\t[1,2]\n2\t[3]\n"
				deniedContext := "COPY rg_copy, line 1: \"1\t[1,2]\""
				columns, types := []string{"id", "j"}, []string{"INT4", "JSON"}
				sentinel := []any{int64(99), "[9]"}
				accepted := [][]any{{int64(1), "[1,2]"}, {int64(2), "[3]"}, sentinel}
				if defaults {
					table = "rg_copy_default"
					declaration = "CREATE TABLE rg_copy_default(id integer,n integer DEFAULT pg_catalog.json_array_length('[1,2]'::json))"
					seed = "INSERT INTO rg_copy_default VALUES(99,9)"
					query, payload = "COPY rg_copy_default(id) FROM STDIN", "1\n2\n"
					deniedContext = ""
					columns, types = []string{"id", "n"}, []string{"INT4", "INT4"}
					sentinel = []any{int64(99), int64(9)}
					accepted = [][]any{{int64(1), int64(2)}, {int64(2), int64(2)}, sentinel}
				}
				run(fixture.Exec(declaration, 0))
				run(fixture.Exec(seed, 1))
				objectGrantConsumerOwner(run, table)
				run(fixture.Exec("GRANT INSERT ON "+table+" TO og_object_reader", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_other", 0))
				// The stream borrower is unwrapped native or real pgx, never pq.
				// c remains the independently owned admin/observer connection.
				pool := p.Native
				if b.PG {
					pool = p.CopyPostgres
				}
				stream, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				defer stream.ExecContext(p.Ctx, "RESET ROLE")
				identity := func() {
					t.Helper()
					var actor string
					if err := stream.QueryRowContext(p.Ctx, "SELECT current_user::text").Scan(&actor); err != nil {
						t.Fatal(err)
					}
					if actor != "og_object_reader" {
						t.Fatalf("COPY caller identity got %q", actor)
					}
				}
				if _, err := stream.ExecContext(p.Ctx, "SET ROLE og_object_reader"); err != nil {
					t.Fatal(err)
				}
				read := func(rows [][]any) {
					run(fixture.Select("SELECT "+strings.Join(columns, ",")+" FROM "+table+" ORDER BY id", columns, types, rows))
				}
				copyRows := func(input io.Reader) objectGrantCopyOutcome {
					result, err := fixture.CopyFrom(p.Ctx, stream, b.PG, query, input)
					return objectGrantCopyOutcome{result, err}
				}
				denied := func() {
					t.Helper()
					out := copyRows(strings.NewReader(payload))
					objectGrantCopyFailure(t, out, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length", Context: deniedContext})
					identity()
					read([][]any{sentinel})
				}
				denied()
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
				// A subsequent real Read is parked after row1's bytes, before row2
				// or EOF. This proves reader lifecycle and publication isolation,
				// not that any particular CHECK has already evaluated.
				first, second := "1\t[1,2]\n", "2\t[3]\n"
				if defaults {
					first, second = "1\n", "2\n"
				}
				objectGrantCopyGated(t, p, b, stream, query, first, second, func() { read([][]any{sentinel}) })
				identity()
				read(accepted)
				run(fixture.Exec("DELETE FROM "+table+" WHERE id=1", 1))
				run(fixture.Exec("DELETE FROM "+table+" WHERE id=2", 1))
				if !defaults {
					out := copyRows(strings.NewReader("3\t[1]\n4\t[]\n"))
					objectGrantCopyFailure(t, out, fixture.Diagnostic{
						Code: "23514", Message: `new row for relation "rg_copy" violates check constraint "rg_copy_nonempty"`,
						Detail: "Failing row contains (id, j) = (4, []).", Context: "COPY rg_copy, line 2: \"4\t[]\"",
						Schema: "public", Table: "rg_copy", Constraint: "rg_copy_nonempty",
					})
					identity()
					read([][]any{sentinel})
				}
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_reader", 0))
				denied()
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
				// Fresh IDs distinguish regrant from the first successful stream.
				fresh := "5\t[1,2]\n6\t[3]\n"
				if defaults {
					fresh = "5\n6\n"
				}
				objectGrantCopySuccess(t, copyRows(strings.NewReader(fresh)))
				identity()
				accepted[0][0], accepted[1][0] = int64(5), int64(6)
				read(accepted)
			})
		})
	}
}

type objectGrantCopyOutcome struct {
	result stuntgres.CopyResult
	err    error
}

func objectGrantCopyFailure(t *testing.T, out objectGrantCopyOutcome, want fixture.Diagnostic) {
	t.Helper()
	if out.result != (stuntgres.CopyResult{}) {
		t.Fatalf("failed stream exposed result: %+v", out.result)
	}
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	fixture.AssertDiagnostic(t, out.err, &want)
}

func objectGrantCopySuccess(t *testing.T, out objectGrantCopyOutcome) {
	t.Helper()
	if out.err != nil || out.result != (stuntgres.CopyResult{Count: 2, CommandTag: "COPY 2"}) {
		t.Fatalf("COPY result=%+v err=%v; want count2/tag COPY 2", out.result, out.err)
	}
}

// Only the COPY goroutine touches readers/calls. The observer owns channels and
// releaseOnce; it inspects calls only after joining. No callback reenters stream.
type objectGrantCopyReader struct {
	ctx         context.Context
	first       *strings.Reader
	second      *strings.Reader
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	enteredOnce sync.Once
	calls       int
}

func (r *objectGrantCopyReader) unblock() { r.releaseOnce.Do(func() { close(r.release) }) }

func (r *objectGrantCopyReader) Read(buf []byte) (int, error) {
	r.calls++
	if r.first.Len() > 0 {
		return r.first.Read(buf)
	}
	r.enteredOnce.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return r.second.Read(buf)
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

func objectGrantCopyGated(t *testing.T, p *fixture.Pair, b fixture.Backend, stream *sql.Conn, query, first, second string, observe func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	reader := &objectGrantCopyReader{ctx: ctx, first: strings.NewReader(first), second: strings.NewReader(second), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan objectGrantCopyOutcome, 1)
	joined := false
	defer func() {
		cancel()
		reader.unblock()
		if !joined {
			<-done
		}
	}()
	go func() {
		result, err := fixture.CopyFrom(ctx, stream, b.PG, query, reader)
		done <- objectGrantCopyOutcome{result, err}
	}()
	select {
	case <-reader.entered:
	case out := <-done:
		joined = true
		t.Fatalf("COPY returned before gated subsequent reader entry: %+v, %v", out.result, out.err)
	case <-ctx.Done():
		t.Fatal("COPY did not reach subsequent reader callback:", ctx.Err())
	}
	observe()
	reader.unblock()
	out := <-done
	joined = true
	objectGrantCopySuccess(t, out)
	if reader.calls < 2 {
		t.Fatal("gated COPY did not use subsequent real Read")
	}
}
