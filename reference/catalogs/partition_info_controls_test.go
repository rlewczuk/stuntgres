package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func partitionInfoSmoke(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	partitionInfoRun(t, p, b, c, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_r')", partitionInfoTreeRows(false)))
	partitionInfoRun(t, p, b, c, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_r_key')", partitionInfoTreeRows(true)))
	partitionInfoRun(t, p, b, c, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_m')", [][]any{{"pinfo_m", "pinfo_r", false, int64(0)}, {"pinfo_l", "pinfo_m", true, int64(1)}}))
	partitionInfoAncestors(t, p, b, c, "'pinfo_l'", "pinfo_l", "pinfo_m", "pinfo_r")
	partitionInfoRoot(t, p, b, c, "'pinfo_l'", "pinfo_r")
	partitionInfoExec(t, p, b, c, "ALTER TABLE pinfo_r DETACH PARTITION pinfo_m")
	partitionInfoDetached(t, p, b, c, false)
	partitionInfoExec(t, p, b, c, "ALTER TABLE pinfo_m DETACH PARTITION pinfo_l")
	partitionInfoDetached(t, p, b, c, true)
}

func partitionInfoDetached(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, leafDetached bool) {
	t.Helper()
	partitionInfoRun(t, p, b, x, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_r')", [][]any{{"pinfo_r", nil, false, int64(0)}, {"pinfo_s", "pinfo_r", true, int64(1)}, {"pinfo_e", "pinfo_r", false, int64(1)}}))
	partitionInfoRun(t, p, b, x, partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_r_key')", [][]any{{"pinfo_r_key", nil, false, int64(0)}, {"pinfo_s_k_payload_key", "pinfo_r_key", true, int64(1)}, {"pinfo_e_k_payload_key", "pinfo_r_key", false, int64(1)}}))
	for _, names := range [][3]string{{"pinfo_m", "pinfo_l", "pinfo_r"}, {"pinfo_m_k_payload_key", "pinfo_l_k_payload_key", "pinfo_r_key"}} {
		m, l := names[0], names[1]
		tree := [][]any{{m, nil, false, int64(0)}}
		if !leafDetached {
			tree = append(tree, []any{l, m, true, int64(1)})
		}
		partitionInfoRun(t, p, b, x, partitionInfoTree("SELECT * FROM pg_partition_tree('"+m+"')", tree))
		if leafDetached {
			partitionInfoEmpty(t, p, b, x, "'"+l+"'")
		} else {
			partitionInfoAncestors(t, p, b, x, "'"+l+"'", l, m)
			partitionInfoRoot(t, p, b, x, "'"+l+"'", m)
		}
	}
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT k,payload FROM pinfo_l UNION ALL SELECT k,payload FROM pinfo_s ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(11), "eleven"}}))
}

func partitionInfoControlPhase(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []byte {
	t.Helper()
	// Native APIs have no PostgreSQL equivalent. The SQL checks are shared with
	// the portable phase; actual bytes, never a SQL facsimile, feed PG replay.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	emitted := append([]byte(nil), dump.Bytes()...)
	old, err := c.QueryContext(p.Ctx, "SELECT * FROM pg_partition_ancestors('pinfo_m')")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	partitionInfoMetadata(false, false)(t, old)
	var oldOID int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'pinfo_m'::regclass::oid").Scan(&oldOID); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	partitionInfoEmpty(t, p, b, c, "0::regclass")
	partitionInfoRun(t, p, b, c, partitionInfoTree("SELECT * FROM pg_partition_tree($1::oid::regclass)", nil, oldOID))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
		t.Fatal(err)
	}
	partitionInfoDetached(t, p, b, c, true)
	if !old.Next() {
		t.Fatal("lost retained snapshot", old.Err())
	}
	var name string
	if err := old.Scan(&name); err != nil || name != "pinfo_m" {
		t.Fatal("retained identity", name, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained cursor", old.Err())
	}
	old.Close()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-restored")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
		t.Fatal(err)
	}
	// Unwrapped fresh controls deliberately use public QueryRow APIs, not a
	// fabricated middleware recorder belonging to another pool.
	var root string
	if err := fresh.QueryRowContext(p.Ctx, "SELECT pg_partition_root('pinfo_m')::text").Scan(&root); err != nil || root != "pinfo_m" {
		t.Fatal(root, err)
	}
	var count int64
	if err := fresh.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_partition_tree('pinfo_l')").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err := fresh.ExecContext(p.Ctx, "CREATE TABLE pinfo_sentinel(k INT); INSERT INTO pinfo_sentinel VALUES(73)"); err != nil {
		t.Fatal(err)
	}
	bad := append(append([]byte(nil), emitted...), []byte(";SELECT pg_partition_root('pinfo_missing'::regclass);")...)
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(bad)); err == nil {
		t.Fatal("failed restore accepted missing relation")
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, fresh, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var sentinel int64
	if err := fresh.QueryRowContext(p.Ctx, "SELECT k FROM pinfo_sentinel").Scan(&sentinel); err != nil || sentinel != 73 {
		t.Fatal("restore atomicity", sentinel, err)
	}
	return emitted
}

func partitionInfoReplayScenario(t *testing.T, stored bool) {
	t.Helper()
	var emitted []byte
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		if stored {
			partitionInfoExec(t, p, b, c, "CREATE VIEW pinfo_stored AS SELECT pg_partition_root('pinfo_l'::regclass) AS root", "CREATE VIEW pinfo_stored_tree AS SELECT * FROM pg_partition_tree('pinfo_r'::regclass)")
		}
		partitionInfoSmoke(t, p, b, c)
		checkStored := func() {
			if !stored {
				return
			}
			root := fixture.Select("SELECT root FROM pinfo_stored", []string{"root"}, []string{"REGCLASS"}, [][]any{{nil}})
			root.CheckMetadata = partitionInfoMetadata(b.PG, true)
			partitionInfoRun(t, p, b, c, root)
			tree := partitionInfoTree("SELECT * FROM pinfo_stored_tree", [][]any{{"pinfo_r", nil, false, int64(0)}, {"pinfo_s", "pinfo_r", true, int64(1)}, {"pinfo_e", "pinfo_r", false, int64(1)}})
			tree.CheckMetadata = partitionInfoMetadata(b.PG, true)
			partitionInfoRun(t, p, b, c, tree)
		}
		checkStored()
		if !b.PG {
			emitted = partitionInfoControlPhase(t, p, b, c)
			checkStored()
			return
		}
		// Portable discovery above never needs native output. In completed dual
		// mode the preceding native backend supplies the genuine emitted stream.
		if len(emitted) > 0 {
			partitionInfoExec(t, p, b, c, "DROP SCHEMA public CASCADE", "CREATE SCHEMA public")
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted PostgreSQL replay", err)
			}
			partitionInfoDetached(t, p, b, c, true)
			checkStored()
		}
	})
}

func TestReferencePartitionInfoControls(t *testing.T)     { partitionInfoReplayScenario(t, true) }
func TestReferencePartitionInfoFeatureSmoke(t *testing.T) { partitionInfoReplayScenario(t, false) }
