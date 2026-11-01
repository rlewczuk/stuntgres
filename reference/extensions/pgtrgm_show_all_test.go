package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// SHOW ALL has backend-dependent unrelated rows. Assert its actual cursor,
// global sortedness and the complete independently specified pg_trgm subset;
// do not replace this consumer with a SELECT against pg_settings.
func pgTrgmShowAll(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want map[string]string) {
	t.Helper()
	rows, err := c.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), "SHOW ALL")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	b.Recorder.Mu.Lock()
	op := b.Recorder.Operations[len(b.Recorder.Operations)-1]
	b.Recorder.Mu.Unlock()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"name", "setting", "description"}) {
		t.Fatal("SHOW ALL columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 3 {
		t.Fatal("SHOW ALL metadata", meta, err)
	}
	for _, m := range meta {
		if m.DatabaseTypeName() != "TEXT" || m.ScanType() != reflect.TypeFor[string]() {
			t.Fatal("SHOW ALL type", m)
		}
	}
	previous, count := "", 0
	seen := map[string]bool{}
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil {
			t.Fatal(err)
		}
		folded := strings.ToLower(name)
		if folded <= previous {
			t.Fatal("SHOW ALL not sorted case-insensitively", previous, name)
		}
		previous = folded
		count++
		for _, setting := range pgTrgmSettings {
			if name != setting.name {
				continue
			}
			v, ok := want[name]
			if !ok || seen[name] || value != v || description != "Sets the threshold used by the "+setting.op+" operator." {
				t.Fatal("SHOW ALL pg_trgm row", name, value, description, want)
			}
			seen[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(want) {
		t.Fatal("SHOW ALL missing settings", seen, want)
	}
	b.Recorder.Mu.Lock()
	closed, closeErr, n := op.Closed, op.CloseErr, len(op.Rows)
	b.Recorder.Mu.Unlock()
	if !closed || closeErr != nil || n != count {
		t.Fatal("SHOW ALL middleware rows/closure", closed, closeErr, n, count)
	}
}
