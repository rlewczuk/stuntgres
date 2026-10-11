package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConditionalDefaultBooleanDemand(t *testing.T) {
	ctx := context.Background()
	p := fixture.NewScenario(t)
	t.Run("native-controls", func(t *testing.T) {
		db := p.Native
		fixture.ScalarNativeExec(t, db, "CREATE TABLE d(a BOOL DEFAULT CASE WHEN TRUE THEN FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,b BOOL DEFAULT CASE WHEN TRUE THEN TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,c BOOL DEFAULT COALESCE(FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE),e BOOL DEFAULT COALESCE(TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE))")
		dump := func() []byte {
			t.Helper()
			var out bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &out); err != nil {
				t.Fatal(err)
			}
			return out.Bytes()
		}
		definitions := func(data []byte) []string {
			var lines []string
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "DEFAULT") {
					lines = append(lines, line)
				}
			}
			return lines
		}
		before := definitions(dump())
		if len(before) == 0 || !strings.Contains(strings.Join(before, "\n"), "oops") {
			t.Fatal("stored defaults missing")
		}
		fixture.ScalarNativeExec(t, db, "INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
		fixture.ScalarNativeTuples(t, db, "SELECT a,b,c,e FROM d", [][]any{{false, true, false, true}})
		snapshot := dump()
		if !reflect.DeepEqual(before, definitions(snapshot)) {
			t.Fatal("consumption rewrote stored defaults")
		}
		if err := stuntgres.Reset(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, db, bytes.NewReader(snapshot)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(snapshot, dump()) {
			t.Fatal("default roundtrip changed snapshot")
		}
		fixture.ScalarNativeExec(t, db, "INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
		fixture.ScalarNativeTuples(t, db, "SELECT a,b,c,e FROM d", [][]any{{false, true, false, true}, {false, true, false, true}})
		if !reflect.DeepEqual(before, definitions(dump())) {
			t.Fatal("restored consumption rewrote stored defaults")
		}
	})
	for _, backend := range p.Backends() {
		if !backend.PG {
			continue
		}
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { fixture.ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			run(fixture.Exec("CREATE TABLE d(a BOOL DEFAULT CASE WHEN TRUE THEN FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,b BOOL DEFAULT CASE WHEN TRUE THEN TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,c BOOL DEFAULT COALESCE(FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE),e BOOL DEFAULT COALESCE(TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE))", 0))
			for range 2 {
				run(fixture.Exec("INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1))
			}
			run(fixture.Select("SELECT a,b,c,e FROM d", []string{"a", "b", "c", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}, {false, true, false, true}}))
		})
	}
}
