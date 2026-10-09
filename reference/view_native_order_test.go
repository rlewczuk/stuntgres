package reference_test

import (
	"context"
	"reflect"
	"testing"
)

// Inspect exact adapter descriptors and raw cells on each actual consumer route.
func referenceViewNestedRead(t *testing.T, p *referencePair, b referenceBackend, executor referenceExecutor, want [][]any) {
	t.Helper()
	query := "SELECT id FROM w ORDER BY id"
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
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
	if b.pg {
		scanType = reflect.TypeFor[int32]()
	}
	if c.Name() != "id" || c.DatabaseTypeName() != "INT4" || c.ScanType() != scanType || nullable != !b.pg || known != !b.pg || lengthKnown || decimalKnown {
		t.Fatalf("unexpected nested descriptor pg=%v: %+v nullable=(%v,%v) length=%v decimal=%v scan=%v", b.pg, c, nullable, known, lengthKnown, decimalKnown, c.ScanType())
	}
	var values [][]any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		referenceAssertRawValue(t, b.pg, "INT4", v)
		values = append(values, []any{v})
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(values, want, false); !ok {
		t.Fatal("nested rows", values, "want", want)
	}
	referenceViewRecorded(t, b.recorder, query, []string{"id"}, []string{"INT4"}, want, false)
}

func TestReferenceViewNativeNestedSecondDumpAfterTemporaryRemap(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(7)", 1), referenceExec("CREATE VIEW v AS SELECT id FROM t", 0), referenceExec("CREATE VIEW w AS SELECT id FROM v", 0)} {
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
			referenceViewNestedRead(t, p, b, b.db, [][]any{{int64(7)}})
			if b.pg {
				held := referenceViewHold(t, p, b, "SELECT id FROM w ORDER BY id")
				referenceViewNestedRead(t, p, b, held, [][]any{{int64(7)}})
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO t VALUES(8)", 1))
				referenceViewNestedRead(t, p, b, b.db, [][]any{{int64(7)}, {int64(8)}})
				referenceViewNestedRead(t, p, b, held, [][]any{{int64(7)}, {int64(8)}})
			}
		})
	}
	first := referenceViewDump(t, p)
	t.Run("native_destination", func(t *testing.T) {
		destination := newReferenceFixture(t, false, true)
		b := destination.backends()[0]
		retained, err := b.db.Conn(destination.ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := retained.Close(); err != nil {
				t.Errorf("retained connection close: %v", err)
			}
		})
		for _, name := range []string{"retained_one", "retained_two"} {
			referenceRunStep(t, destination.ctx, retained, b.recorder, referenceExec("CREATE TEMPORARY SEQUENCE "+name, 0))
			referenceRunStep(t, destination.ctx, retained, b.recorder, referenceSelect("SELECT nextval('"+name+"')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		referenceViewRestore(t, destination, first)
		held := referenceViewHold(t, destination, b, "SELECT id FROM w ORDER BY id")
		referenceViewNestedRead(t, destination, b, b.db, [][]any{{int64(7)}})
		referenceViewNestedRead(t, destination, b, held, [][]any{{int64(7)}})
		second := referenceViewDump(t, destination)
		referenceViewNestedRead(t, destination, b, b.db, [][]any{{int64(7)}})
		referenceViewNestedRead(t, destination, b, held, [][]any{{int64(7)}})
		for _, name := range []string{"retained_one", "retained_two"} {
			referenceRunStep(t, destination.ctx, retained, b.recorder, referenceSelect("SELECT currval('"+name+"')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			referenceRunStep(t, destination.ctx, retained, b.recorder, referenceSelect("SELECT nextval('"+name+"')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		}
		t.Run("native_third", func(t *testing.T) {
			third := newReferenceFixture(t, false, true)
			referenceViewRestore(t, third, second)
			b := third.backends()[0]
			held := referenceViewHold(t, third, b, "SELECT id FROM w ORDER BY id")
			referenceViewNestedRead(t, third, b, b.db, [][]any{{int64(7)}})
			referenceViewNestedRead(t, third, b, held, [][]any{{int64(7)}})
			referenceRunStep(t, third.ctx, b.db, b.recorder, referenceExec("INSERT INTO t VALUES(8)", 1))
			referenceViewNestedRead(t, third, b, b.db, [][]any{{int64(7)}, {int64(8)}})
			referenceViewNestedRead(t, third, b, held, [][]any{{int64(7)}, {int64(8)}})
		})
	})
}
