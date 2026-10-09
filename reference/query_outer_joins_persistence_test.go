package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

// Held eager results retain their schema and owned values across native catalog
// replacement. PostgreSQL exercises the SQL/view portion of the same scenario.
func outerJoinsHeldRows(t *testing.T, rows *sql.Rows, want referenceStep) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, want.columns) {
		t.Fatalf("held columns %v: %v; want %v", names, err, want.columns)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(columns))
	for i, c := range columns {
		types[i] = c.DatabaseTypeName()
	}
	if !reflect.DeepEqual(types, want.types) {
		t.Fatalf("held types %v want %v", types, want.types)
	}
	var values [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		destinations := make([]any, len(row))
		for i := range row {
			destinations[i] = &row[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = referenceNormalize(row[i])
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(values, want.rows, true); !ok {
		t.Fatalf("held rows %#v want %#v", values, want.rows)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceQueryOuterJoinsPersistence(t *testing.T) {
	outerJoinsBackendScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, backend referenceBackend) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, form := range []string{"ON", "USING", "NATURAL"} {
				name := "persist_" + strings.ToLower(kind) + "_" + strings.ToLower(form)
				source := "a " + kind + " JOIN b USING(k)"
				projection := "*"
				if form == "ON" {
					source = "a " + kind + " JOIN b ON a.k=b.k"
					projection = "a.k AS ak,a.l,b.k AS bk,b.r"
				}
				if form == "NATURAL" {
					source = "a NATURAL " + kind + " JOIN b"
				}
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE VIEW "+name+" AS SELECT "+projection+" FROM "+source, 0))
			}
		}
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TABLE disjoint(z BOOLEAN)", 0))
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE VIEW persist_zero AS SELECT * FROM a NATURAL LEFT JOIN disjoint", 0))
		var dump bytes.Buffer
		if !backend.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			direct, err := conn.QueryContext(p.ctx, "SELECT * FROM persist_full_using")
			if err != nil {
				t.Fatal(err)
			}
			defer direct.Close()
			computed, err := conn.QueryContext(p.ctx, "SELECT k+1 AS next FROM persist_full_using")
			if err != nil {
				t.Fatal(err)
			}
			defer computed.Close()
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			outerJoinsHeldRows(t, direct, referenceSelect("", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows("FULL")))
			outerJoinsHeldRows(t, computed, referenceSelect("", []string{"next"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {nil}, {int64(4)}, {nil}}))
		}
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, form := range []string{"ON", "USING", "NATURAL"} {
				name := "persist_" + strings.ToLower(kind) + "_" + strings.ToLower(form)
				step := referenceSelect("SELECT * FROM "+name, []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows(kind))
				if form == "ON" {
					step = referenceSelect("SELECT * FROM "+name, []string{"ak", "l", "bk", "r"}, []string{"INT4", "TEXT", "INT8", "TEXT"}, outerJoinsOnRows(kind))
				}
				outerJoinsCases(t, p, conn, backend.recorder, step)
			}
		}
		outerJoinsCases(t, p, conn, backend.recorder, referenceSelect("SELECT * FROM persist_zero", []string{"k", "l", "z"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "a", nil}, {int64(1), "a", nil}, {int64(2), "b", nil}, {nil, "n", nil}}))
		var definition string
		if err := conn.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='persist_zero'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		outerJoinsAssertViewDefinition(t, "no_keys", definition)
		if !backend.pg {
			var again bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("outer join replay changed canonical dump", err)
			}
		}
	})
}
