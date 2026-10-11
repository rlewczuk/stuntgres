package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Inspect exact adapter descriptors and raw cells on each actual consumer route.
func referenceViewNestedRead(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, want [][]any) {
	t.Helper()
	query := "SELECT id FROM w ORDER BY id"
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	rows, err := executor.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id"}) {
		t.Fatal(columns, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != 1 {
		t.Fatal(metadata, err)
	}
	c := metadata[0]
	nullable, known := c.Nullable()
	_, lengthKnown := c.Length()
	_, _, decimalKnown := c.DecimalSize()
	scanType := reflect.TypeFor[int64]()
	if b.PG {
		scanType = reflect.TypeFor[int32]()
	}
	if c.Name() != "id" || c.DatabaseTypeName() != "INT4" || c.ScanType() != scanType || nullable != !b.PG || known != !b.PG || lengthKnown || decimalKnown {
		t.Fatalf("unexpected nested descriptor pg=%v: %+v nullable=(%v,%v) length=%v decimal=%v scan=%v", b.PG, c, nullable, known, lengthKnown, decimalKnown, c.ScanType())
	}
	var values [][]any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, b.PG, "INT4", v)
		values = append(values, []any{v})
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(values, want, false); !ok {
		t.Fatal("nested rows", values, "want", want)
	}
	fixture.ViewRecorded(t, b.Recorder, query, []string{"id"}, []string{"INT4"}, want, false)
}

func TestReferenceViewNativeNestedSecondDumpAfterTemporaryRemap(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(7)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0), fixture.Exec("CREATE VIEW w AS SELECT id FROM v", 0)} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			referenceViewNestedRead(t, p, b, b.DB, [][]any{{int64(7)}})
			if b.PG {
				held := fixture.ViewHold(t, p, b, "SELECT id FROM w ORDER BY id")
				referenceViewNestedRead(t, p, b, held, [][]any{{int64(7)}})
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(8)", 1))
				referenceViewNestedRead(t, p, b, b.DB, [][]any{{int64(7)}, {int64(8)}})
				referenceViewNestedRead(t, p, b, held, [][]any{{int64(7)}, {int64(8)}})
			}
		})
	}
	first := fixture.ViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := fixture.NewFixture(t, false, true)
		b := destination.Backends()[0]
		retained, err := b.DB.Conn(destination.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := retained.Close(); err != nil {
				t.Errorf("retained connection close: %v", err)
			}
		})
		for _, name := range []string{"retained_one", "retained_two"} {
			fixture.RunStep(t, destination.Ctx, retained, b.Recorder, fixture.Exec("CREATE TEMPORARY SEQUENCE "+name, 0))
			fixture.RunStep(t, destination.Ctx, retained, b.Recorder, fixture.Select("SELECT nextval('"+name+"')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		fixture.ViewRestore(t, destination, first)
		held := fixture.ViewHold(t, destination, b, "SELECT id FROM w ORDER BY id")
		referenceViewNestedRead(t, destination, b, b.DB, [][]any{{int64(7)}})
		referenceViewNestedRead(t, destination, b, held, [][]any{{int64(7)}})
		second := fixture.ViewDump(t, destination)
		referenceViewNestedRead(t, destination, b, b.DB, [][]any{{int64(7)}})
		referenceViewNestedRead(t, destination, b, held, [][]any{{int64(7)}})
		for _, name := range []string{"retained_one", "retained_two"} {
			fixture.RunStep(t, destination.Ctx, retained, b.Recorder, fixture.Select("SELECT currval('"+name+"')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, destination.Ctx, retained, b.Recorder, fixture.Select("SELECT nextval('"+name+"')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		}
		t.Run("native_third", func(t *testing.T) {
			third := fixture.NewFixture(t, false, true)
			fixture.ViewRestore(t, third, second)
			b := third.Backends()[0]
			held := fixture.ViewHold(t, third, b, "SELECT id FROM w ORDER BY id")
			referenceViewNestedRead(t, third, b, b.DB, [][]any{{int64(7)}})
			referenceViewNestedRead(t, third, b, held, [][]any{{int64(7)}})
			fixture.RunStep(t, third.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(8)", 1))
			referenceViewNestedRead(t, third, b, b.DB, [][]any{{int64(7)}, {int64(8)}})
			referenceViewNestedRead(t, third, b, held, [][]any{{int64(7)}, {int64(8)}})
		})
	})
}
