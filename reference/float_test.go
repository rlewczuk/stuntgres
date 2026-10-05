//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceFloatDeclarationsDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
			run(referenceExec(`CREATE TABLE aliases(a REAL NOT NULL,b FLOAT4,c DOUBLE PRECISION,d FLOAT8,e FLOAT,f FLOAT(1),g FLOAT(24),h FLOAT(25),i FLOAT(53),j "float4",k "float8")`, 0))
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT * FROM aliases", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}, []string{"FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8", "FLOAT4", "FLOAT8"}, [][]any{})
				s.prepared = prepared
				run(s)
			}
			for _, tc := range []struct{ q, code, message, position string }{{"CREATE TABLE f(x FLOAT(0))", "22023", "precision for type float must be at least 1 bit", "24"}, {"CREATE TABLE f(x FLOAT(54))", "22023", "precision for type float must be less than 54 bits", "24"}, {"CREATE TABLE f(x FLOAT(-1))", "42601", `syntax error at or near "-"`, "24"}, {"CREATE TABLE f(x FLOAT(1.5))", "42601", `syntax error at or near "1.5"`, "24"}, {"CREATE TABLE f(x FLOAT(2147483648))", "42601", `syntax error at or near "2147483648"`, "24"}} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.prepared = prepared
					run(s)
				}
			}
			run(referenceImplicitError("CREATE TABLE IF NOT EXISTS aliases(x FLOAT(0))", referenceDiagnostic{Code: "22023", Message: "precision for type float must be at least 1 bit", Position: "44"}))
			run(referenceExec("CREATE TABLE f(r REAL,d DOUBLE PRECISION)", 0))
			for _, tc := range []struct{ q, code, message, position string }{
				{"INSERT INTO f VALUES('oops','1')", "22P02", `invalid input syntax for type real: "oops"`, "22"},
				{"INSERT INTO f VALUES('1','oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "26"},
				{"INSERT INTO f VALUES('1e999','1')", "22003", `"1e999" is out of range for type real`, "22"},
				{"INSERT INTO f VALUES('1','1e-999')", "22003", `"1e-999" is out of range for type double precision`, "26"},
				{"SELECT r FROM f WHERE r='oops'", "22P02", `invalid input syntax for type real: "oops"`, "25"},
				{"/*é*/ SELECT r FROM f WHERE r='oops'", "22P02", `invalid input syntax for type real: "oops"`, "31"},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.prepared = prepared
					run(s)
				}
			}
			for _, arg := range []string{"oops", "1e999", "1e-46"} {
				code, message := "22P02", `invalid input syntax for type real: "oops"`
				if arg != "oops" {
					code, message = "22003", `"`+arg+`" is out of range for type real`
				}
				want := referenceDiagnostic{Code: code, Message: message}
				if backend.name == "postgres" {
					want.Context = "unnamed portal parameter $1 = '...'"
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO f VALUES($1,'1')", want, arg)
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
			}
			run(referenceExec("INSERT INTO f VALUES('99','99')", 1))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("INSERT INTO f VALUES('1','1'),('oops','2')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type real: "oops"`, Position: "32"})
				s.prepared = prepared
				run(s)
				run(referenceSelect("SELECT * FROM f", []string{"r", "d"}, []string{"FLOAT4", "FLOAT8"}, [][]any{{float64(99), float64(99)}}))
			}
		})
	}
}

// Float4 exports intentionally differ: native widens its binary32 payload,
// whereas pq parses PostgreSQL's decimal output directly as binary64. Each
// backend must satisfy its own expected value before bit32 semantic parity.
func TestReferenceFloatValuesBindsOrdering(t *testing.T) {
	p := newReferencePair(t)
	var returned [2][]float64
	for backend, db := range []*sql.DB{p.memory, p.postgres} {
		record := p.memoryRecord
		if backend == 1 {
			record = p.postgresRecord
		}
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, record, s) }
		run(referenceExec("CREATE TABLE f(id INTEGER,r REAL,d DOUBLE PRECISION)", 0))
		run(referenceExec("INSERT INTO f VALUES(1,'0.1','0.1'),(2,'7.038531e-26','5e-324'),(3,'16777217','1.2345678901234567'),(4,'33554448','-0')", 4))
		rows, err := db.QueryContext(p.ctx, "SELECT r,d FROM f ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		expectedR := []float64{float64(float32(0.1)), float64(math.Float32frombits(0x15ae43fd)), 16777216, 33554448}
		if backend == 1 {
			expectedR = []float64{0.1, 7.038531e-26, 16777216, 33554448}
		}
		expectedD := []float64{0.1, math.SmallestNonzeroFloat64, 1.2345678901234567, math.Copysign(0, -1)}
		index := 0
		for rows.Next() {
			var r, d float64
			if err = rows.Scan(&r, &d); err != nil {
				t.Fatal(err)
			}
			if index >= 4 || math.Float64bits(r) != math.Float64bits(expectedR[index]) || math.Float64bits(d) != math.Float64bits(expectedD[index]) {
				t.Fatalf("backend%d row%d: %.17g %.17g", backend, index, r, d)
			}
			returned[backend] = append(returned[backend], r)
			index++
		}
		if err = rows.Err(); err != nil || index != 4 {
			t.Fatal(index, err)
		}
		rows.Close()
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT id FROM f WHERE r=16777217", []string{"id"}, []string{"INT4"}, [][]any{})
			s.prepared = prepared
			run(s)
			s = referenceSelect("SELECT id FROM f WHERE r='16777217'", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}})
			s.prepared = prepared
			run(s)
		}
		held, err := db.PrepareContext(p.ctx, "SELECT id FROM f WHERE r=$1")
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range []any{"16777217", int64(16777217), float64(16777217)} {
			rows, err := held.QueryContext(p.ctx, arg)
			if err != nil {
				t.Fatal(err)
			}
			var id int64
			if !rows.Next() || rows.Scan(&id) != nil || id != 3 || rows.Next() {
				t.Fatal("contextual float4 mismatch", arg, id)
			}
			rows.Close()
		}
		held.Close()
		run(referenceExec("CREATE TABLE ordered(id INTEGER,r REAL)", 0))
		insert, err := db.PrepareContext(p.ctx, "INSERT INTO ordered VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range []any{math.Inf(-1), float64(-1), math.Copysign(0, -1), float32(0), float64(1), math.Inf(1), math.NaN(), "nan", nil} {
			result, err := insert.ExecContext(p.ctx, i+1, v)
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
		}
		insert.Close()
		for _, descending := range []bool{false, true} {
			q := "SELECT id,r FROM ordered ORDER BY r"
			if descending {
				q += " DESC"
			}
			rows, err := db.QueryContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			groups := []int{0, 1, 2, 2, 3, 4, 5, 5, 6}
			if descending {
				groups = []int{6, 5, 5, 4, 3, 2, 2, 1, 0}
			}
			seen := map[int64]bool{}
			index := 0
			for rows.Next() {
				var id int64
				var r any
				if err = rows.Scan(&id, &r); err != nil {
					t.Fatal(err)
				}
				group := map[int64]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 6: 4, 7: 5, 8: 5, 9: 6}[id]
				if seen[id] || index >= 9 || group != groups[index] {
					t.Fatal(backend, index, id, r)
				}
				seen[id] = true
				if id == 7 || id == 8 {
					if !math.IsNaN(r.(float64)) {
						t.Fatal(r)
					}
				}
				if id == 9 && r != nil {
					t.Fatal(r)
				}
				index++
			}
			if rows.Err() != nil || index != 9 {
				t.Fatal(index, rows.Err())
			}
			rows.Close()
		}
		run(referenceSelect("SELECT id FROM ordered WHERE r='NaN' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
		run(referencePrepared(referenceSelect("SELECT r FROM ordered WHERE r=$1", []string{"r"}, []string{"FLOAT4"}, [][]any{}, nil)))
	}
	// pq's float64 parse followed by float32 narrowing is not a valid
	// semantic decoder for every REAL output: the direct32 counterexample
	// double-rounds. Classify that adapter artifact explicitly, retaining
	// the independently asserted backend float64 values above.
	for i, wantBits := range []uint32{0x3dcccccd, 0x15ae43fd, 0x4b800000, 0x4c000004} {
		if math.Float32bits(float32(returned[0][i])) != wantBits {
			t.Fatal("native REAL payload", i, returned[0][i], wantBits)
		}
	}
	for i, spelling := range []string{"0.1", "7.038531e-26", "16777216", "3.3554448e+07"} {
		decoded, err := strconv.ParseFloat(spelling, 32)
		if err != nil || math.Float32bits(float32(decoded)) != math.Float32bits(float32(returned[0][i])) {
			t.Fatal("direct32 PostgreSQL output semantic decoder", spelling, decoded, err)
		}
	}
	if math.Float32bits(float32(returned[1][1])) != 0x15ae43fe {
		t.Fatal("pq binary64-to-binary32 double-rounding witness", returned[1][1])
	}
	if reflect.DeepEqual(returned[0], returned[1]) {
		t.Fatal("fixture failed to distinguish binary32 adapter export")
	}
}

func TestReferenceFloatConstraintDetails(t *testing.T) {
	p := newReferencePair(t)
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		record := &referenceRecorder{}
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, record, s) }
		run(referenceExec("CREATE TABLE nn(r REAL NOT NULL,d REAL)", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("INSERT INTO nn VALUES('1','1'),(NULL,'33554448')", referenceDiagnostic{Code: "23502", Message: `null value in column "r" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null, 3.3554448e+07).", Schema: "public", Table: "nn", Column: "r"})
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
			run(referenceSelect("SELECT * FROM nn", []string{"r", "d"}, []string{"FLOAT4", "FLOAT4"}, [][]any{}))
		}
		run(referenceExec("INSERT INTO nn VALUES(16777217,9007199254740993)", 1))
		run(referenceSelect("SELECT r FROM nn", []string{"r"}, []string{"FLOAT4"}, [][]any{{float64(16777216)}}))
	}
}

func TestReferenceFloatPrecisionScannerDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
			for _, spelling := range []string{"1.5junk", ".5junk", "1.5e+", ".5e-", "1.5e", "1.5e+3junk", "1.5_", "1.5e+3_"} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("CREATE TABLE f(x FLOAT("+spelling+"))", referenceDiagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "` + spelling + `"`, Position: "24"})
					s.prepared = prepared
					if prepared {
						s.errorStage = "prepare"
					} else {
						s.errorStage = "execute"
					}
					run(s)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("CREATE TABLE f(x FLOAT(1..5))", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ".."`, Position: "25"})
				s.prepared = prepared
				if prepared {
					s.errorStage = "prepare"
				} else {
					s.errorStage = "execute"
				}
				run(s)
			}
			run(referenceExec("CREATE TABLE f(r REAL)", 0))
			run(referenceSelect("SELECT * FROM f", []string{"r"}, []string{"FLOAT4"}, [][]any{}))
		})
	}
}

func TestReferenceFloatExactOutputText(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE exact_output(id INTEGER,r REAL,d DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO exact_output VALUES(1,'33554448','1.8014398509481992e+16'),(2,'-33554448','-1.8014398509481992e+16'),(3,'1e-45','5e-324'),(4,'0.00024414062','2.2250738585072014e-308'),(5,'3.4028235e+38','1.7976931348623157e+308'),(6,'-0','-0'),(7,'NaN','Infinity')", 7))
	p.step(t, referenceExec("INSERT INTO exact_output VALUES(8,'7.038531e-26','7.038531e-26'),(9,'1000000','1000000'),(10,'0.0001','0.0001'),(11,'0.00001','0.00001'),(12,'0','1e15'),(13,'-Infinity','-Infinity')", 6))
	// Oracle-only casts expose exact server output; native syntax remains bounded.
	expected := [][]any{{"3.3554448e+07", "1.8014398509481992e+16"}, {"-3.3554448e+07", "-1.8014398509481992e+16"}, {"1e-45", "5e-324"}, {"0.00024414062", "2.2250738585072014e-308"}, {"3.4028235e+38", "1.7976931348623157e+308"}, {"-0", "-0"}, {"NaN", "Infinity"}}
	expected = append(expected,
		[]any{"7.038531e-26", "7.038531e-26"},
		[]any{"1e+06", "1000000"},
		[]any{"0.0001", "0.0001"},
		[]any{"1e-05", "1e-05"},
		[]any{"0", "1e+15"},
		[]any{"-Infinity", "-Infinity"},
	)
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT r::text,d::text FROM exact_output ORDER BY id", []string{"r", "d"}, []string{"TEXT", "TEXT"}, expected))
	// The native counterpart exposes the sole codec through public Dump, not
	// unsupported casts or internal packages.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, row := range expected {
		pair := "'" + row[0].(string) + "', '" + row[1].(string) + "'"
		if !strings.Contains(dump.String(), pair) {
			t.Fatal("native exact output pair", pair, dump.String())
		}
	}
}
