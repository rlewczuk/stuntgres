package core_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
)

func TestPGStatWALReplacementAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE TABLE wal_replace_marker(v integer)", "INSERT INTO wal_replace_marker VALUES(37)")
	resetStatsExec(t, s, "SELECT pg_stat_reset_shared('wal')")
	at := e.Cluster.WALResetAt
	var writer statistics.Writer
	key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "wal_replace_marker"), Category: statistics.WAL}
	seed := statistics.Fact{Operations: 23, TotalTime: 1250 * time.Microsecond}
	writer.Add(&e.Statistics, key, seed)
	writer.Flush(&e.Statistics, time.Now(), true)
	keyBefore := e.Statistics.Read(key)
	if at.IsZero() || keyBefore.Operations != 23 {
		t.Fatal("positive authority and real keyed WAL operation required")
	}
	before := e.Statistics.ReadShared(statistics.SharedWAL)
	ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_wal")
	old, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rows.Close()
	var dump bytes.Buffer
	if err := engine.Dump(t.Context(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "pg_stat_wal") || strings.Contains(dump.String(), "stats_reset") {
		t.Fatal("dump serialized monitoring history", dump.String())
	}
	catalog := e.CatalogRead().State()
	checkUnchanged := func() {
		t.Helper()
		if e.CatalogRead().State() != catalog || e.Statistics.Read(key) != keyBefore || e.Statistics.ReadShared(statistics.SharedWAL) != before || !e.Cluster.WALResetAt.Equal(at) {
			t.Fatal("failed replacement changed catalog/fact/authority")
		}
	}
	if err := engine.Restore(t.Context(), e, strings.NewReader(dump.String()+"\nCREATE TABLE failed(v wal_missing_type);")); err == nil {
		t.Fatal("late invalid restore accepted")
	}
	checkUnchanged()
	inputErr := errors.New("wal external input")
	if err := engine.Restore(t.Context(), e, io.MultiReader(bytes.NewReader(dump.Bytes()), walFailedInput{inputErr})); !errors.Is(err, inputErr) {
		t.Fatal("ordinary reader error", err)
	}
	checkUnchanged()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := engine.Restore(canceled, e, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal("ordinary pre-cancellation", err)
	}
	checkUnchanged()
	// Queue an old-generation delta before accepted replacement.
	writer.Add(&e.Statistics, key, seed)
	if err := engine.Restore(t.Context(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	writer.Flush(&e.Statistics, time.Now(), true)
	if got := e.Statistics.ReadShared(statistics.SharedWAL); got != (statistics.SharedFact{ResetAt: at}) {
		t.Fatal("replacement did not clear history/restore authority/fence old writer", got)
	}
	if !e.Cluster.WALResetAt.Equal(at) || e.CatalogRead().State() == catalog || e.Statistics.Read(key).Operations != 0 {
		t.Fatal("replacement authority boundary")
	}
	walOwnerRow(t, old, before)
	current, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	walOwnerRow(t, current, statistics.SharedFact{ResetAt: at})
}

type walFailedInput struct{ err error }

func (r walFailedInput) Read([]byte) (int, error) { return 0, r.err }
