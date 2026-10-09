package reference_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReferenceCharacterExplicitCastsAndConversions(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE seed(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
	for _, tc := range []struct {
		expression, name, typ string
		length                int64
		value                 any
	}{
		{"CAST('too long' AS varchar(5))", "varchar", "VARCHAR", 5, "too l"},
		{"'too long'::varchar(5)", "varchar", "VARCHAR", 5, "too l"},
		{"CAST('too long' AS char(5))", "bpchar", "BPCHAR", 5, "too l"},
		{"'ok'::char(5)", "bpchar", "BPCHAR", 5, "ok   "},
		{"CAST('ab' AS char)", "bpchar", "BPCHAR", 1, "a"},
		{"'ab  '::bpchar", "bpchar", "BPCHAR", -1, "ab  "},
		{"'é🙂x'::varchar(2)", "varchar", "VARCHAR", 2, "é🙂"},
		{"CAST(CAST('a ' AS char(2)) AS text)", "text", "TEXT", -1, "a"},
		{"CAST(CAST('a   ' AS bpchar) AS varchar(1))", "varchar", "VARCHAR", 1, "a"},
		{"CAST('a '::varchar(2) AS text)", "text", "TEXT", -1, "a "},
		{"CAST('a '::char(2) AS char(4))", "bpchar", "BPCHAR", 4, "a   "},
		{"CAST(NULL AS char(3))", "bpchar", "BPCHAR", 3, nil},
		{"CAST(NULL AS varchar(2))", "varchar", "VARCHAR", 2, nil},
		{"CAST(NULL AS text)", "text", "TEXT", -1, nil},
	} {
		q := "SELECT " + tc.expression + " FROM seed"
		for _, prepared := range []bool{false, true} {
			s := referenceSelect(q, []string{tc.name}, []string{tc.typ}, [][]any{{tc.value}})
			s.prepared = prepared
			p.step(t, s)
			for _, backend := range p.backends() {
				referenceCharacterMetadata(t, p, backend.db, q, prepared, !backend.pg, []string{tc.name}, []string{tc.typ}, []int64{tc.length}, []referenceCharacterNullability{{}})
			}
		}
	}
	p.step(t, referenceSelect("SELECT CAST(CAST(12 AS text) AS varchar(1)),CAST(TRUE AS text)::char(6) FROM seed", []string{"varchar", "bpchar"}, []string{"VARCHAR", "BPCHAR"}, [][]any{{"1", "true  "}}))
	p.step(t, referenceSelect("SELECT CAST(CAST('12 ' AS bpchar) AS text)::int4 FROM seed", []string{"int4"}, []string{"INT4"}, [][]any{{int64(12)}}))
	p.step(t, referenceSelect("SELECT CAST(NULL AS char(3)) IS NULL,CAST('a' AS char(3)) IS NOT DISTINCT FROM CAST('a ' AS text),CAST('a' AS char(3)) IS DISTINCT FROM CAST('a' AS text) FROM seed", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}}))
	p.step(t, referenceSelect("SELECT num_nulls(CAST(NULL AS char(3)),CAST('a' AS varchar(2)))", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.step(t, referenceExec("CREATE TABLE c(v CHAR(2))", 0))
	failure := referenceImplicitError("INSERT INTO c VALUES(CAST('abcd' AS varchar(3)))", referenceDiagnostic{Code: "22001", Message: "value too long for type character(2)"})
	failure.errorStage = "execute"
	p.step(t, failure)
	p.step(t, referencePrepared(failure))
	p.step(t, referenceExec("INSERT INTO c VALUES('a')", 1))
	p.step(t, referenceSelect("SELECT CAST(v AS char(4)),CAST(v AS text),CAST(v AS varchar(3)) FROM c", []string{"v", "v", "v"}, []string{"BPCHAR", "TEXT", "VARCHAR"}, [][]any{{"a   ", "a", "a"}}))
	p.step(t, referenceSelect("SELECT v::char(4)::text FROM c", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
	p.step(t, referenceSelect("SELECT CAST('1' AS integer) FROM seed", []string{"int4"}, []string{"INT4"}, [][]any{{int64(1)}}))
	for _, q := range []string{`SELECT CAST('a' AS "VARCHAR") FROM seed`, `SELECT 'a'::"character" FROM seed`} {
		name := `"VARCHAR"`
		if strings.Contains(q, `"character"`) {
			name = `"character"`
		}
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42704", Message: "type " + name + " does not exist", Position: "20"})
		if name == `"character"` {
			s.diagnostic.Position = "13"
		}
		s.selectRows, s.errorStage = true, "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	stmt, err := p.memory.PrepareContext(p.ctx, "SELECT CAST(1 AS varchar(2)) FROM seed")
	if stmt != nil {
		stmt.Close()
		t.Fatal("unsupported native cast prepared")
	}
	referenceState(t, err, "0A000")
}

func TestReferenceCharacterTimestampCastConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE seed(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
	for _, tc := range []struct {
		q, name, typ string
		value        any
	}{
		{"SELECT CAST(TIMESTAMP 'epoch' AS text) FROM seed", "text", "TEXT", "1970-01-01 00:00:00"},
		{"SELECT TIMESTAMP 'epoch'::timestamp FROM seed", "timestamp", "TIMESTAMP", time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		for _, backend := range p.backends() {
			for _, prepared := range []bool{false, true} {
				func() {
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
					backend.recorder.mu.Lock()
					recordStart := len(backend.recorder.operations)
					backend.recorder.mu.Unlock()
					rows, closeQuery, err := transactionQuery(ctx, backend.db, tc.q, prepared)
					if err != nil {
						t.Fatal(err)
					}
					defer closeQuery()
					names, err := rows.Columns()
					if err != nil || !reflect.DeepEqual(names, []string{tc.name}) {
						t.Fatal(names, err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != 1 {
						t.Fatal(columns, err)
					}
					if columns[0].Name() != tc.name || columns[0].DatabaseTypeName() != tc.typ {
						t.Fatal(columns[0])
					}
					if tc.typ == "TIMESTAMP" && columns[0].ScanType() != reflect.TypeFor[time.Time]() {
						t.Fatal("timestamp ScanType", columns[0].ScanType())
					}
					checkRaw := func(raw any) {
						referenceAssertRawValue(t, backend.pg, tc.typ, raw)
						if tc.typ == "TIMESTAMP" {
							value, ok := raw.(time.Time)
							if !ok {
								t.Fatal("raw timestamp", raw)
							}
							if _, offset := value.Zone(); offset != 0 {
								t.Fatal("nonzero raw timestamp offset", value)
							}
						}
						if !reflect.DeepEqual(referenceNormalize(raw), tc.value) {
							t.Fatalf("raw %v want %v", raw, tc.value)
						}
					}
					if !rows.Next() {
						t.Fatal("missing epoch", rows.Err())
					}
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					checkRaw(raw)
					if rows.Next() || rows.Err() != nil {
						t.Fatal("epoch cardinality/iteration", rows.Err())
					}
					if err := closeQuery(); err != nil {
						t.Fatal(err)
					}
					backend.recorder.mu.Lock()
					var captured *referenceOperation
					for _, operation := range backend.recorder.operations[recordStart:] {
						if operation.SQL == tc.q && operation.Columns != nil {
							captured = operation
						}
					}
					backend.recorder.mu.Unlock()
					if captured == nil || !reflect.DeepEqual(captured.Columns, names) || !captured.Closed || captured.CloseErr != nil || captured.Err != nil || len(captured.Rows) != 1 || len(captured.Rows[0]) != 1 {
						t.Fatal("missing consumed epoch capture", captured)
					}
					checkRaw(captured.Rows[0][0])
				}()
			}
		}
	}
}
