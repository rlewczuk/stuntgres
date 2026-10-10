package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"
)

func TestReferenceRangePartitionDefaultWriterSerialization(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_serial(k INT,payload INT) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_serial_def PARTITION OF rp_serial DEFAULT PARTITION BY RANGE(payload)", 0))
		run(referenceExec("CREATE TABLE rp_serial_leaf PARTITION OF rp_serial_def DEFAULT", 0))
		writer, err := b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Rollback()
		referenceRunStep(t, p.ctx, writer, b.recorder, referenceExec("INSERT INTO rp_serial_leaf VALUES(5,10)", 1))
		run(referenceSelect("SELECT * FROM rp_serial", []string{"k", "payload"}, []string{"INT4", "INT4"}, nil))
		creator, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer creator.Close()
		q := "CREATE TABLE rp_serial_new PARTITION OF rp_serial FOR VALUES FROM(5) TO(7)"
		type outcome struct {
			result sql.Result
			err    error
		}
		var got outcome
		if b.pg {
			var pid int
			if err := creator.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			defer cancel()
			result := make(chan outcome, 1)
			joined := false
			defer func() {
				cancel()
				writer.Rollback()
				if !joined {
					select {
					case <-result:
					case <-time.After(5 * time.Second):
						t.Error("default creator did not join")
					}
				}
			}()
			go func() { r, e := creator.ExecContext(ctx, q); result <- outcome{r, e} }()
			for {
				var waiting bool
				if err := referenceAdmin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='rp_serial'::regclass AND mode='AccessExclusiveLock' AND NOT granted)", pid).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case got = <-result:
					joined = true
					t.Fatalf("CREATE failed to wait on default writer: %v", got.err)
				default:
				}
				runtime.Gosched()
			}
			if err := writer.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case got = <-result:
				joined = true
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		} else {
			// No public native queue-admission observer exists. Internal tests
			// prove its concurrent admission; public SQL proves the same serial
			// default frontier and rejection after the actual writer commit.
			if err := writer.Commit(); err != nil {
				t.Fatal(err)
			}
			got.result, got.err = creator.ExecContext(p.ctx, q)
		}
		if got.result != nil {
			t.Fatal("failed CREATE exposed result")
		}
		referenceAssertDiagnostic(t, got.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `updated partition constraint for default partition "rp_serial_def" would be violated by some row`, Schema: "public", Table: "rp_serial_def"})
		run(referenceSelect("SELECT * FROM rp_serial", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(10)}}))
		run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_serial_new'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
