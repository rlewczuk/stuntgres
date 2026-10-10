package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

func TestReferenceRangePartitionProviderRetirement(t *testing.T) {
	for _, physical := range []bool{false, true} {
		name := "OuterCommit"
		if physical {
			name = "PhysicalClose"
		}
		t.Run(name, func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				owner, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				peer, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, owner, b.recorder, s) }
				peerRun := func(s referenceStep) { referenceRunStep(t, p.ctx, peer, b.recorder, s) }
				peerRun(referenceExec("CREATE TEMP TABLE rp_retire_peer(v INT)", 0))
				peerRun(referenceExec("INSERT INTO rp_retire_peer VALUES(91)", 1))
				peerRun(referenceExec("CREATE TABLE rp_retire_sentinel(v INT)", 0))
				peerRun(referenceExec("INSERT INTO rp_retire_sentinel VALUES(71)", 1))
				if !physical {
					run(referenceExec("BEGIN", 0))
				}
				provider := "CREATE TEMP TABLE rp_retire_provider(v INT)"
				if !physical {
					provider += " ON COMMIT DROP"
				}
				run(referenceExec(provider, 0))
				run(referenceExec("CREATE SEQUENCE rp_retire_seq", 0))
				rootDDL := "CREATE TABLE rp_retire_root(k REGCLASS,v INT DEFAULT nextval('rp_retire_seq')) PARTITION BY RANGE(COALESCE(k,'rp_retire_provider'::REGCLASS))"
				// Native permanent CREATE in an explicit transaction remains
				// an existing capability boundary. PostgreSQL exercises the
				// full outer-COMMIT lifecycle below; private units exercise
				// native Commit against validated, already-owned descriptors.
				if !physical && !b.pg {
					run(referenceRangeError(rootDDL, "0A000", "statement is unsupported in an explicit transaction", referenceDiagnostic{}))
					run(referenceExec("ROLLBACK", 0))
					run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_retire_provider' OR relname='rp_retire_seq' OR relname='rp_retire_root' OR relname='rp_retire_mid' OR relname='rp_retire_leaf' OR relname='rp_retire_other' OR relname='rp_retire_index' OR relname='rp_retire_other_index' OR relname='rp_retire_view'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					peerRun(referenceSelect("SELECT v FROM rp_retire_peer", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
					peerRun(referenceSelect("SELECT v FROM rp_retire_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(71)}}))
					return
				}
				run(referenceExec(rootDDL, 0))
				run(referenceExec("ALTER SEQUENCE rp_retire_seq OWNED BY rp_retire_root.v", 0))
				run(referenceExec("CREATE TABLE rp_retire_mid PARTITION OF rp_retire_root DEFAULT PARTITION BY RANGE(v)", 0))
				run(referenceExec("CREATE TABLE rp_retire_leaf PARTITION OF rp_retire_mid(v UNIQUE) FOR VALUES FROM(MINVALUE) TO(2)", 0))
				run(referenceExec("CREATE TABLE rp_retire_other PARTITION OF rp_retire_mid(v UNIQUE) DEFAULT", 0))
				run(referenceExec("CREATE INDEX rp_retire_index ON rp_retire_leaf(v)", 0))
				run(referenceExec("CREATE INDEX rp_retire_other_index ON rp_retire_other(v)", 0))
				run(referenceExec("CREATE VIEW rp_retire_view AS SELECT v FROM rp_retire_root", 0))
				run(referenceExec("INSERT INTO rp_retire_root(k) VALUES(NULL)", 1))
				run(referencePrepared(referenceExec("INSERT INTO rp_retire_root(k,v) VALUES(NULL,$1)", 1, 2)))
				values := referenceSelect("SELECT v FROM rp_retire_root ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
				run(values)
				run(referenceSelect("SELECT v FROM rp_retire_leaf", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(referenceSelect("SELECT v FROM rp_retire_other", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
				// A real normal dependency must exist before either scopeless path.
				run(referenceSelect("SELECT COUNT(*) FROM pg_depend d JOIN pg_class r ON r.oid=d.objid JOIN pg_class p ON p.oid=d.refobjid WHERE d.classid=1259 AND d.refclassid=1259 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='n' AND r.relname='rp_retire_root' AND p.relname='rp_retire_provider'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				if physical {
					peerRun(values)
					if !b.pg {
						rows, err := owner.QueryContext(p.ctx, values.query)
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						if err := owner.Raw(func(raw any) error { return raw.(driver.Conn).Close() }); err != nil {
							t.Fatal(err)
						}
						referenceRunStep(t, p.ctx, sequenceFrozenRows{referenceExecutor: peer, rows: rows, query: values.query}, b.recorder, values)
					} else {
						var pid int
						if err := owner.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							t.Fatal(err)
						}
						if pid <= 0 {
							t.Fatal("invalid provider backend identity", pid)
						}
						if err := owner.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Fatal(err)
						}
						// Client socket close does not acknowledge server cleanup.
						// The bounded termination API waits for backend exit; an
						// already-exited backend can return false. Check absence
						// independently before inspecting the full object closure.
						ctx, cancel := context.WithTimeout(p.ctx, 8*time.Second)
						defer cancel()
						var terminated bool
						if err := peer.QueryRowContext(ctx, "SELECT pg_terminate_backend($1,5000)", pid).Scan(&terminated); err != nil {
							t.Fatal("waiting for provider backend exit", err)
						}
						var remains bool
						if err := peer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&remains); err != nil {
							t.Fatal(err)
						}
						if remains {
							t.Fatalf("provider backend %d remains after bounded termination (result=%t)", pid, terminated)
						}
					}
				} else {
					peerRun(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_retire_root' OR relname='rp_retire_leaf'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(referenceExec("COMMIT", 0))
				}
				peerRun(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_retire_provider' OR relname='rp_retire_root' OR relname='rp_retire_mid' OR relname='rp_retire_leaf' OR relname='rp_retire_other' OR relname='rp_retire_leaf_v_key' OR relname='rp_retire_other_v_key' OR relname='rp_retire_index' OR relname='rp_retire_other_index' OR relname='rp_retire_seq' OR relname='rp_retire_view'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				for _, relation := range []string{"rp_retire_root", "rp_retire_mid", "rp_retire_leaf", "rp_retire_other", "rp_retire_view"} {
					q := "SELECT v FROM " + relation
					step := referenceRangeError(q, "42P01", "relation \""+relation+"\" does not exist", referenceDiagnostic{Position: "15"})
					step.selectRows = true
					peerRun(step)
				}
				peerRun(referenceRangeError("SELECT nextval('rp_retire_seq')", "42P01", `relation "rp_retire_seq" does not exist`, referenceDiagnostic{Position: "16"}))
				peerRun(referenceSelect("SELECT v FROM rp_retire_peer", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
				peerRun(referenceSelect("SELECT v FROM rp_retire_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(71)}}))
				// Same names and unique values remain reusable after complete cleanup.
				peerRun(referenceExec("CREATE TABLE rp_retire_root(v INT UNIQUE)", 0))
				peerRun(referenceExec("INSERT INTO rp_retire_root VALUES(1),(2)", 2))
				peerRun(referenceSelect("SELECT v FROM rp_retire_root ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
	}
}
