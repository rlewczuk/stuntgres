package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGStatAllIndexesNativeControls(t *testing.T) {
	var emitted []byte
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing real emitted source SQL")
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("PostgreSQL replay of actual native definitions/data", err)
			}
			indexStatWant(t, indexStatGet(t, p, c, "stat_control_hash"), 0, 0, 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT payload FROM stat_control_rows ORDER BY payload NULLS LAST", []string{"payload"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"x"}, {"z"}, {nil}}))
			return
		}
		sourceDriver := stuntgres.NewDriver()
		connector, err := sourceDriver.OpenConnector("index-stat-source")
		if err != nil {
			t.Fatal(err)
		}
		source := sql.OpenDB(connector)
		defer source.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		sourceBackend := b
		sourceBackend.DB, sourceBackend.Recorder = recorded, recorder
		sourceConn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer sourceConn.Close()
		indexStatCreate(t, p, sourceBackend, sourceConn, "stat_control_rows", "stat_control_hash")
		fixture.StatisticsExec(t, p, sourceBackend, sourceConn, "CREATE TEMP TABLE stat_source_temp(n integer)", 0)
		fixture.StatisticsExec(t, p, sourceBackend, sourceConn, "CREATE INDEX stat_source_temp_idx ON stat_source_temp(n)", 0)
		sourceWriter, retireSource := fixture.StatisticsReportingConn(t, p, sourceBackend)
		indexStatPrefer(t, p, sourceBackend, sourceWriter)
		indexStatPayloads(t, p, sourceBackend, sourceWriter, "SELECT payload FROM stat_control_rows WHERE n=2", []string{"a", "b"})
		retireSource()
		indexStatAwait(t, p, sourceBackend, "stat_control_hash", 1, 2, 2)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, source, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if bytes.Contains(emitted, []byte("idx_scan")) || bytes.Contains(emitted, []byte("last_idx_scan")) {
			t.Fatal("runtime facts serialized")
		}
		indexStatCreate(t, p, b, c, "stat_control_rows", "stat_control_hash")
		writer, retire := fixture.StatisticsReportingConn(t, p, b)
		indexStatPrefer(t, p, b, writer)
		indexStatPayloads(t, p, b, writer, "SELECT payload FROM stat_control_rows WHERE n=2", []string{"a", "b"})
		retire()
		oldPersistent := indexStatAwait(t, p, b, "stat_control_hash", 1, 2, 2)
		keeper := fixture.IdentityConnection(t, p.Ctx, b.DB)
		for _, step := range []fixture.Step{fixture.Exec("CREATE TEMP TABLE stat_control_keep(n integer)", 0), fixture.Exec("INSERT INTO stat_control_keep VALUES(2),(2)", 2), fixture.Exec("CREATE INDEX stat_control_keep_idx ON stat_control_keep USING hash(n)", 0), fixture.Exec("SET enable_seqscan=off", 0), fixture.Select("SELECT n FROM stat_control_keep WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}})} {
			fixture.RunStep(t, p.Ctx, keeper, b.Recorder, step)
		}
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		// Retain the TEMP owner. Reached ordinary commands provide a publication
		// boundary once the normal throttle expires; no flush builtin/test hook.
		ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
		defer cancel()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		var temp indexStatRow
		for {
			var one int
			if err := keeper.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
				t.Fatal("TEMP publication boundary", err)
			}
			temp, err = indexStatRead(ctx, c, "stat_control_keep_idx")
			if err != nil {
				t.Fatal(err)
			}
			if temp.Scans == 1 {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("TEMP publication expired", temp, ctx.Err())
			case <-tick.C:
			}
		}
		indexStatWant(t, temp, 1, 2, 2)
		rows, err := c.QueryContext(p.Ctx, indexStatSelect, "stat_control_keep_idx")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		// Own the retained values before a replacement; the descriptor remains
		// usable after the result is closed and generation-owned catalog is replaced.
		var retained indexStatRow
		if !rows.Next() {
			t.Fatal("retained TEMP row missing")
		}
		if err := rows.Scan(&retained.Parent, &retained.Index, &retained.Schema, &retained.Table, &retained.Name, &retained.Scans, &retained.Last, &retained.Entries, &retained.Fetches); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		bad := append(bytes.Clone(emitted), []byte("\nCREATE TABLE late_failure(n no_such_type);")...)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
			t.Fatal("late failed Restore accepted")
		}
		canceled, stop := context.WithCancel(p.Ctx)
		stop()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		duringInput, cancelInput := context.WithCancel(p.Ctx)
		input := &indexStatCancelReader{Reader: bytes.NewReader(emitted), cancel: cancelInput}
		if err := stuntgres.Restore(duringInput, p.Native, input); !errors.Is(err, context.Canceled) || !input.entered {
			t.Fatal("canceled external Restore input", input.entered, err)
		}
		cancelInput()
		if got := indexStatGet(t, p, c, "stat_control_hash"); !got.equal(oldPersistent) {
			t.Fatal("failed Restore changed persistent history", oldPersistent, got)
		}
		if got := indexStatGet(t, p, c, "stat_control_keep_idx"); !got.equal(temp) {
			t.Fatal("failed Restore changed TEMP history", temp, got)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		fresh := indexStatGet(t, p, c, "stat_control_hash")
		indexStatWant(t, fresh, 0, 0, 0)
		if got := indexStatGet(t, p, c, "stat_control_keep_idx"); !got.equal(temp) {
			t.Fatal("destination TEMP identity/history not retained", temp, got)
		}
		if !retained.equal(temp) {
			t.Fatal("retained SQL values changed", retained, temp)
		}
		indexStatMetadata(t, columns, false)
		if _, err := indexStatRead(p.Ctx, c, "stat_source_temp_idx"); err != sql.ErrNoRows {
			t.Fatal("foreign source TEMP imported", err)
		}
		fixture.RunStep(t, p.Ctx, keeper, b.Recorder, fixture.Select("SELECT n FROM stat_control_keep ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if _, err := indexStatRead(p.Ctx, c, "stat_control_hash"); err != sql.ErrNoRows {
			t.Fatal("Reset retained persistent index", err)
		}
		if got := indexStatGet(t, p, c, "stat_control_keep_idx"); !got.equal(temp) {
			t.Fatal("Reset lost live destination TEMP history", temp, got)
		}
		// Public Dump text contains definitions/data only, not a hidden counter
		// channel. PostgreSQL sibling independently consumes these exact bytes.
		if strings.TrimSpace(string(emitted)) == "" {
			t.Fatal("empty emitted SQL")
		}
	})
}

// Cancel only after Restore actually enters external input, not before it is
// admitted. The target must keep its accepted identity/data/facts on this exit.
type indexStatCancelReader struct {
	io.Reader
	cancel  context.CancelFunc
	entered bool
}

func (r *indexStatCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 && !r.entered {
		r.entered = true
		r.cancel()
	}
	return n, err
}
