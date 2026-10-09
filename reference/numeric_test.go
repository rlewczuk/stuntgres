package reference_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestReferenceNumericExactValuesInferenceAndTypmods(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			run := func(s referenceStep) {
				s.prepared = prepared
				p.step(t, s)
				if s.selectRows {
					numericReferenceScanTypes(t, p, s)
				}
			}
			run(referenceExec(`CREATE TABLE n(id INTEGER,v NUMERIC,a DECIMAL,b DEC,c "numeric")`, 0))
			run(referenceSelect("SELECT * FROM n", []string{"id", "v", "a", "b", "c"}, []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, nil))
			exact := "12345678901234567890.1234567890123456789000"
			run(referenceExec("INSERT INTO n(id,v) VALUES(1,'"+exact+"'),(2,'-"+exact+"'),(3,001.2300),(4,-0.000),(5,.1),(6,1.230e2),(7,1.230e-2),(8,5e2)", 8))
			run(referenceSelect("SELECT v FROM n ORDER BY id", []string{"v"}, []string{"NUMERIC"}, [][]any{{exact}, {"-" + exact}, {"1.2300"}, {"0.000"}, {"0.1"}, {"123.0"}, {"0.01230"}, {"500"}}))
			run(referenceExec("CREATE TABLE exact_binds(id INT,v NUMERIC)", 0))
			for i, arg := range []any{exact, "-" + exact, []byte(exact), []byte("-" + exact)} {
				run(referenceExec("INSERT INTO exact_binds VALUES($1,$2)", 1, int64(i+1), arg))
			}
			run(referenceSelect("SELECT v FROM exact_binds ORDER BY id", []string{"v"}, []string{"NUMERIC"}, [][]any{{exact}, {"-" + exact}, {exact}, {"-" + exact}}))
			q := "SELECT 2147483647,2147483648,-2147483648,9223372036854775807,-9223372036854775808,9223372036854775808,-9223372036854775809,1.,1e0,.001,1.618_034,1e1_0,010 FROM n WHERE id=1"
			names := make([]string, 13)
			for i := range names {
				names[i] = "?column?"
			}
			run(referenceSelect(q, names, []string{"INT4", "INT8", "INT4", "INT8", "INT8", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "INT4"}, [][]any{{int64(2147483647), int64(2147483648), int64(-2147483648), int64(9223372036854775807), int64(-9223372036854775808), "9223372036854775808", "-9223372036854775809", "1", "1", "0.001", "1.618034", "10000000000", int64(10)}}))
			run(referenceExec("CREATE TABLE radix(v NUMERIC)", 0))
			run(referenceExec("INSERT INTO radix VALUES(0x8000000000000000),(0o1000000000000000000000),(0b1"+strings.Repeat("0", 63)+")", 3))
			run(referenceSelect("SELECT v FROM radix", []string{"v"}, []string{"NUMERIC"}, [][]any{{"9223372036854775808"}, {"9223372036854775808"}, {"9223372036854775808"}}))
			run(referenceExec("CREATE TABLE sized(a NUMERIC(3,1),b NUMERIC(2,-3),c NUMERIC(3,5),d NUMERIC(2))", 0))
			run(referenceExec("INSERT INTO sized VALUES(1.25,1499,.009994,2.5),(-1.25,-1500,.000004,-2.5),(99.94,99499,-.000005,0)", 3))
			run(referenceSelect("SELECT * FROM sized ORDER BY a", []string{"a", "b", "c", "d"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"-1.3", "-2000", "0.00000", "-3"}, {"1.3", "1000", "0.00999", "3"}, {"99.9", "99000", "-0.00001", "0"}}))
			run(referenceExec("CREATE TABLE ints(v BIGINT)", 0))
			run(referenceExec("INSERT INTO ints VALUES(9007199254740993),(1.5),(-1.5)", 3))
			run(referenceSelect("SELECT v FROM ints WHERE v=9007199254740993.0", []string{"v"}, []string{"INT8"}, [][]any{{int64(9007199254740993)}}))
			for _, literal := range []string{"9007199254740992.0", "9223372036854775808"} {
				run(referenceSelect("SELECT v FROM ints WHERE v="+literal, []string{"v"}, []string{"INT8"}, nil))
			}
			run(referenceExec("CREATE TABLE converted(i INT,t TEXT,f DOUBLE PRECISION)", 0))
			run(referenceExec("INSERT INTO converted VALUES(1.5,1.2300,.5),(-1.5,1e2,1e0)", 2))
			run(referenceSelect("SELECT * FROM converted ORDER BY i", []string{"i", "t", "f"}, []string{"INT4", "TEXT", "FLOAT8"}, [][]any{{int64(-2), "100", float64(1)}, {int64(2), "1.2300", float64(.5)}}))
			run(referenceExec("CREATE TABLE real_values(id INT,v REAL)", 0))
			run(referenceExec("INSERT INTO real_values VALUES(1,'16777217')", 1))
			run(referenceSelect("SELECT id FROM real_values WHERE v=16777217.0", []string{"id"}, []string{"INT4"}, nil))
			run(referenceSelect("SELECT id FROM real_values WHERE v='16777217'", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceSelect("SELECT id FROM real_values WHERE v=1e100", []string{"id"}, []string{"INT4"}, nil))
		})
	}
}
func TestReferenceNumericSpecialsAndHeldParameters(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE n(id INT,v NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO n VALUES(1,'-Infinity'),(2,-1),(3,0),(4,1),(5,'Infinity'),(6,'NaN'),(7,'NaN'),(8,NULL)", 8))
	p.step(t, referenceSelect("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"-Infinity"}, {"-1"}, {"0"}, {"1"}, {"Infinity"}, {"NaN"}, {"NaN"}, {nil}}))
	p.step(t, referencePrepared(referenceSelect("SELECT v FROM n ORDER BY v DESC", []string{"v"}, []string{"NUMERIC"}, [][]any{{nil}, {"NaN"}, {"NaN"}, {"Infinity"}, {"1"}, {"0"}, {"-1"}, {"-Infinity"}})))
	p.step(t, referenceSelect("SELECT v FROM n ORDER BY v DESC", []string{"v"}, []string{"NUMERIC"}, [][]any{{nil}, {"NaN"}, {"NaN"}, {"Infinity"}, {"1"}, {"0"}, {"-1"}, {"-Infinity"}}))
	p.step(t, referenceSelect("SELECT id FROM n WHERE v='NaN' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}, {int64(7)}}))
	p.step(t, referenceSelect("SELECT id FROM n WHERE v=NULL", []string{"id"}, []string{"INT4"}, nil))
	p.step(t, referenceExec("CREATE TABLE binds(v NUMERIC(3,1))", 0))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO binds VALUES($1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, arg := range []any{"1.25", []byte("-1.25"), nil, int64(2), float64(1.5), "nan"} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, arg))
			}
			failure := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}, "99.95")
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, backend.recorder, failure)
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, "1.2"))
			q = "SELECT id FROM n WHERE v=$1 ORDER BY id"
			filter, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer filter.Close()
			reused := referenceHeldStatement{referenceExecutor: backend.db, statement: filter, query: q}
			for _, frame := range []struct {
				arg  any
				rows [][]any
			}{{"NaN", [][]any{{int64(6)}, {int64(7)}}}, {nil, nil}, {"-1", [][]any{{int64(2)}}}} {
				rows, err := reused.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), q, frame.arg)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				numericReferenceQueryRows(t, backend, rows, columns, referenceSelect(q, []string{"id"}, []string{"INT4"}, frame.rows, frame.arg))
			}
		}()
	}
	p.step(t, referenceSelect("SELECT v FROM binds ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"-1.3"}, {"1.2"}, {"1.3"}, {"1.5"}, {"2.0"}, {"NaN"}, {nil}}))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM n WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}, {int64(7)}}, "NaN")))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM n WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)))
	conflict := referenceImplicitError("SELECT $1 FROM n WHERE v=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "numeric versus text", Position: "8"}, "1.2500")
	conflict.selectRows = true
	conflict.errorStage = "prepare"
	p.step(t, referencePrepared(conflict))
	conflict.errorStage = "execute"
	p.step(t, conflict)
	// Zero-argument pq Query uses simple protocol. Assert both complete records
	// separately, without normalizing their different SQLSTATE or primary.
	conflict.args = nil
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, conflict)
	simple := referenceImplicitError(conflict.query, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "8"})
	simple.selectRows = true
	simple.errorStage = "execute"
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, simple)
	}
	p.step(t, referenceSelect("SELECT $1 FROM n WHERE id=1", []string{"?column?"}, []string{"TEXT"}, [][]any{{"1.2500"}}, "1.2500"))
	p.step(t, referenceExec("CREATE TABLE repeats(a NUMERIC(3,1),b NUMERIC(3,2))", 0))
	p.step(t, referencePrepared(referenceExec("INSERT INTO repeats VALUES($1,$1)", 1, "1.255")))
	p.step(t, referenceSelect("SELECT * FROM repeats", []string{"a", "b"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.3", "1.26"}}))
	p.step(t, referenceExec("DROP TABLE repeats", 0))
	p.step(t, referenceExec("CREATE TABLE repeats(a NUMERIC(3,1),b NUMERIC(3,2))", 0))
	p.step(t, referenceExec("INSERT INTO repeats VALUES($1,$1)", 1, "1.255"))
	p.step(t, referenceSelect("SELECT * FROM repeats", []string{"a", "b"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.3", "1.26"}}))
	// pq's negative-scale DecimalSize decodes unsigned scale incorrectly. Native
	// logical metadata is independently asserted rather than copied from pq.
	rows, err := p.memory.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), "SELECT * FROM repeats")
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if types[0].ScanType() != reflect.TypeFor[string]() {
		t.Fatal(types[0])
	}
}
