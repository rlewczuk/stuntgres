package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These are real SQL fixtures, deliberately independent of the large progress
// workload. Both indexes precede the committed deletion.
func vacuumPauseSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE "+table+"(id INT, payload TEXT)", 0),
		fixture.Exec("INSERT INTO "+table+" VALUES(0,'v'),(1,'v'),(2,'v'),(3,'v')", 4),
		fixture.Exec("CREATE INDEX "+table+"_id ON "+table+"(id)", 0),
		fixture.Exec("CREATE INDEX "+table+"_payload ON "+table+"(payload)", 0),
		fixture.Exec("DELETE FROM "+table+" WHERE id % 2 = 0", 2),
	} {
		vacuumRun(t, p, b, c, s)
	}
	if b.PG {
		copyProgressExec(t, p.Ctx, c, "ALTER TABLE "+table+" SET (autovacuum_enabled=false)")
	}
	vacuumPauseData(t, p, b, c, table)
	vacuumCount(t, p.Ctx, c, table, 0)
}

func vacuumPauseData(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	vacuumRun(t, p, b, c, fixture.Select("SELECT id,payload FROM "+table+" ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "v"}, {int64(3), "v"}}))
	vacuumRun(t, p, b, c, fixture.Select("SELECT indexrelid='"+table+"_id'::regclass AS id_index,indexrelid='"+table+"_payload'::regclass AS payload_index,indisvalid,indisready FROM pg_index WHERE indrelid='"+table+"'::regclass ORDER BY id_index DESC", []string{"id_index", "payload_index", "indisvalid", "indisready"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, true}, {false, true, true, true}}))
}

func vacuumPauseUnadmitted(t *testing.T, pause *stuntgres.ExecutionPause) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pause.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexecuted work admitted pause: %v", err)
	}
}

type vacuumPauseRequest struct {
	cancel   context.CancelFunc
	done     chan fixture.RelationOutcome
	finished chan struct{}
	joined   bool
}

func vacuumPauseLaunch(t *testing.T, source context.Context, executor fixture.Executor, query string, pauses ...*stuntgres.ExecutionPause) *vacuumPauseRequest {
	t.Helper()
	ctx, cancel := context.WithCancel(source)
	r := &vacuumPauseRequest{cancel: cancel, done: make(chan fixture.RelationOutcome, 1), finished: make(chan struct{})}
	// Registered before launch and after statement ownership: join before Close.
	t.Cleanup(func() {
		cancel()
		for _, pause := range pauses {
			pause.Resume()
		}
		if !r.joined {
			<-r.done
			r.joined = true
		}
	})
	go func() {
		result, err := executor.ExecContext(ctx, query)
		r.done <- fixture.RelationOutcome{Result: result, Err: err}
		close(r.finished)
	}()
	return r
}

func (r *vacuumPauseRequest) join() fixture.RelationOutcome {
	out := <-r.done
	r.joined = true
	return out
}

func (r *vacuumPauseRequest) admitted(t *testing.T, ctx context.Context, pause *stuntgres.ExecutionPause) {
	t.Helper()
	observe, cancel := context.WithCancel(ctx)
	admission := make(chan error, 1)
	go func() { admission <- pause.Wait(observe) }()
	select {
	case err := <-admission:
		cancel()
		if err != nil {
			t.Fatal("vacuum.inspect admission", err)
		}
	case <-r.finished:
		cancel()
		<-admission
		t.Fatal("VACUUM completed without vacuum.inspect lifetime admission", r.join().Err)
	}
	select {
	case <-r.finished:
		t.Fatal("admitted VACUUM did not remain held", r.join().Err)
	default:
	}
}

func vacuumPauseSuccess(t *testing.T, out fixture.RelationOutcome) {
	t.Helper()
	if out.Err != nil || out.Result == nil || out.Rows != nil {
		t.Fatal("VACUUM success", out)
	}
	n, err := out.Result.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal("VACUUM affected count", n, err)
	}
}

func vacuumPauseCanceled(t *testing.T, out fixture.RelationOutcome) {
	t.Helper()
	if out.Result != nil || out.Rows != nil {
		t.Fatal("canceled VACUUM published output", out)
	}
	fixture.LexicalOrdinary(t, out.Err, context.Canceled)
}

func vacuumPauseHealthy(t *testing.T, p *fixture.Pair, b fixture.Backend, c, observer *sql.Conn) {
	t.Helper()
	vacuumRun(t, p, b, c, fixture.Select("SELECT 42 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	vacuumProgressEmpty(t, p, b, observer)
}

func vacuumPauseObserve(t *testing.T, p *fixture.Pair, c fixture.Executor, id copyProgressIdentity) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, vacuumProgressQuery, id.pid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("held command missing progress", rows.Err())
	}
	s, _ := vacuumProgressScan(t, rows)
	vacuumProgressVisible(t, s, id, false)
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected progress bag", rows.Err())
	}
}
