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

func testCheckpointerReplacement(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE TABLE checkpointer_replace_marker(v integer)", "INSERT INTO checkpointer_replace_marker VALUES(37)")
	at := e.Cluster.CheckpointerResetAt
	var writer statistics.Writer
	seed := statistics.SharedFact{Counters: [8]int64{11, 13, 17, 19, 23, 29, 31, 37}, WriteTime: 1250 * time.Microsecond, SyncTime: 2750 * time.Microsecond}
	writer.Shared(&e.Statistics, statistics.Checkpointer, seed)
	writer.Flush(&e.Statistics, time.Now(), true)
	before := e.Statistics.ReadShared(statistics.Checkpointer)
	ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_checkpointer")
	old, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rows.Close()
	var dump bytes.Buffer
	if err := engine.Dump(t.Context(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "pg_stat_checkpointer") || strings.Contains(dump.String(), "stats_reset") {
		t.Fatal("dump serialized monitoring history", dump.String())
	}
	catalog := e.CatalogRead().State()
	checkUnchanged := func() {
		t.Helper()
		if e.CatalogRead().State() != catalog || e.Statistics.ReadShared(statistics.Checkpointer) != before || !e.Cluster.CheckpointerResetAt.Equal(at) {
			t.Fatal("failed replacement changed catalog/fact/authority")
		}
	}
	if err := engine.Restore(t.Context(), e, strings.NewReader(dump.String()+"\nCREATE TABLE failed(v checkpointer_missing_type);")); err == nil {
		t.Fatal("late invalid restore accepted")
	}
	checkUnchanged()
	inputErr := errors.New("checkpointer external input")
	if err := engine.Restore(t.Context(), e, io.MultiReader(bytes.NewReader(dump.Bytes()), checkpointerFailedInput{inputErr})); !errors.Is(err, inputErr) {
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
	writer.Shared(&e.Statistics, statistics.Checkpointer, seed)
	if err := engine.Restore(t.Context(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	writer.Flush(&e.Statistics, time.Now(), true)
	if got := e.Statistics.ReadShared(statistics.Checkpointer); got != (statistics.SharedFact{ResetAt: at}) {
		t.Fatal("replacement did not clear history/restore authority/fence old writer", got)
	}
	if !e.Cluster.CheckpointerResetAt.Equal(at) || e.CatalogRead().State() == catalog {
		t.Fatal("replacement authority boundary")
	}
	checkpointerOwnerRow(t, old, before)
	current, err := enginetest.PreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	checkpointerOwnerRow(t, current, statistics.SharedFact{ResetAt: at})
}

type checkpointerFailedInput struct{ err error }

func (r checkpointerFailedInput) Read([]byte) (int, error) { return 0, r.err }
