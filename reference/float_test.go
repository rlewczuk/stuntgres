package reference_test

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceFloatDeclarationsDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec(`CREATE TABLE aliases(a REAL NOT NULL,b FLOAT4,c DOUBLE PRECISION,d FLOAT8,e FLOAT,f FLOAT(1),g FLOAT(24),h FLOAT(25),i FLOAT(53),j "float4",k "float8")`, 0))
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT * FROM aliases", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}, []string{"FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT4", "FLOAT4", "FLOAT8", "FLOAT8", "FLOAT4", "FLOAT8"}, [][]any{})
				s.prepared = prepared
				run(s)
				func() {
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
					var executor referenceExecutor = backend.db
					if prepared {
						stmt, err := backend.db.PrepareContext(ctx, s.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: s.query}
					}
					rows, err := executor.QueryContext(ctx, s.query)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for i, typ := range types {
						scan := reflect.TypeFor[float64]()
						if backend.pg && s.types[i] == "FLOAT4" {
							scan = reflect.TypeFor[float32]()
						}
						nullable, known := typ.Nullable()
						_, hasLength := typ.Length()
						_, _, hasPrecision := typ.DecimalSize()
						if typ.ScanType() != scan || known != !backend.pg || nullable != (!backend.pg && i != 0) || hasLength || hasPrecision {
							t.Fatal(typ, nullable, known)
						}
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("empty metadata query", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
				}()
			}
			for _, tc := range []struct{ q, code, message, position string }{{"CREATE TABLE f(x FLOAT(0))", "22023", "precision for type float must be at least 1 bit", "24"}, {"CREATE TABLE f(x FLOAT(54))", "22023", "precision for type float must be less than 54 bits", "24"}, {"CREATE TABLE f(x FLOAT(-1))", "42601", `syntax error at or near "-"`, "24"}, {"CREATE TABLE f(x FLOAT(1.5))", "42601", `syntax error at or near "1.5"`, "24"}, {"CREATE TABLE f(x FLOAT(2147483648))", "42601", `syntax error at or near "2147483648"`, "24"}} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
				}
			}
			run(referenceImplicitError("CREATE TABLE IF NOT EXISTS aliases(x FLOAT(0))", referenceDiagnostic{Code: "22023", Message: "precision for type float must be at least 1 bit", Position: "44"}))
			quoted := referenceImplicitError(`CREATE TABLE f(x "real")`, referenceDiagnostic{Code: "42704", Message: `type "real" does not exist`, Position: "18"})
			run(quoted)
			quoted.errorStage = "execute"
			run(referencePrepared(quoted))
		})
	}
}

// Float4 exports intentionally differ: native widens its binary32 payload,
// whereas pq parses PostgreSQL's decimal output directly as binary64. Each
// backend must satisfy its own expected value before bit32 semantic parity.
func TestReferenceFloatValuesBindsOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	var returned [2][]float64
	for _, selected := range p.backends() {
		backend := 0
		if selected.pg {
			backend = 1
		}
		db, record := selected.db, selected.recorder
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, record, s) }
		run(referenceExec("CREATE TABLE f(id INTEGER,r REAL,d DOUBLE PRECISION)", 0))
		run(referenceExec("INSERT INTO f VALUES(1,'0.1','0.1'),(2,'7.038531e-26','5e-324'),(3,'16777217','1.2345678901234567'),(4,'33554448','-0')", 4))
		run(referenceExec("CREATE TABLE root_f(id INTEGER,r REAL,d DOUBLE PRECISION)", 0))
		run(referenceExec("INSERT INTO root_f VALUES(1,'16777217','0.1'),(2,'7.038531e-26','5e-324'),(3,'0.1','1.2345678901234567')", 3))
		rootR := []float64{16777216, float64(math.Float32frombits(0x15ae43fd)), float64(float32(.1))}
		if selected.pg {
			rootR = []float64{16777216, 7.038531e-26, .1}
		}
		run(referenceSelect("SELECT r,d FROM root_f ORDER BY id", []string{"r", "d"}, []string{"FLOAT4", "FLOAT8"}, [][]any{{rootR[0], float64(.1)}, {rootR[1], math.SmallestNonzeroFloat64}, {rootR[2], float64(1.2345678901234567)}}))
		floatReferenceSingleRowConsumers(t, p, selected, rootR)
		run(referenceSelect("SELECT r FROM root_f WHERE r=16777217", []string{"r"}, []string{"FLOAT4"}, nil))
		run(referenceSelect("SELECT id FROM root_f WHERE r='16777217'", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		rows, err := db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, record), "SELECT r,d FROM f ORDER BY id")
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
		record.mu.Lock()
		var captured *referenceOperation
		for _, op := range record.operations {
			if op.SQL == "SELECT r,d FROM f ORDER BY id" && op.Columns != nil {
				captured = op
			}
		}
		record.mu.Unlock()
		if captured == nil || len(captured.Rows) != 4 || !captured.Closed || captured.CloseErr != nil {
			t.Fatal("missing consumed finite floats")
		}
		for i, row := range captured.Rows {
			for _, value := range row {
				referenceAssertRawValue(t, selected.pg, "FLOAT8", value)
			}
			if math.Float64bits(row[0].(float64)) != math.Float64bits(expectedR[i]) || math.Float64bits(row[1].(float64)) != math.Float64bits(expectedD[i]) {
				t.Fatal("recorded float bits", i, row)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT id FROM f WHERE r=16777217", []string{"id"}, []string{"INT4"}, [][]any{})
			s.prepared = prepared
			run(s)
			s = referenceSelect("SELECT id FROM f WHERE r='16777217'", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}})
			s.prepared = prepared
			run(s)
		}
		for _, tc := range []struct {
			table string
			id    int64
		}{{"f", 3}, {"root_f", 1}} {
			func() {
				query := "SELECT id FROM " + tc.table + " WHERE r=$1"
				held, err := db.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				reused := referenceHeldStatement{referenceExecutor: db, statement: held, query: query}
				for _, arg := range []any{"16777217", int64(16777217), float64(16777217)} {
					referenceRunStep(t, p.ctx, reused, record, referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{tc.id}}, arg))
				}
			}()
		}
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
	if p.postgres != nil {
		if math.Float32bits(float32(returned[1][1])) != 0x15ae43fe {
			t.Fatal("pq binary64-to-binary32 double-rounding witness", returned[1][1])
		}
		if reflect.DeepEqual(returned[0], returned[1]) {
			t.Fatal("fixture failed to distinguish binary32 adapter export")
		}
	}
}

func TestReferenceFloatConstraintDetails(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
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
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
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
	p := newReferenceScenario(t)
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
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT r::text,d::text FROM exact_output ORDER BY id", []string{"r", "d"}, []string{"TEXT", "TEXT"}, expected))
	}
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

// Keep the original non-Context QueryRow boundary alongside its recorded companion.
func floatReferenceSingleRowConsumers(t *testing.T, p *referencePair, backend referenceBackend, rootR []float64) {
	t.Helper()
	db, record := backend.db, backend.recorder
	for _, tc := range []struct {
		q    string
		r, d float64
	}{
		{"SELECT r,d FROM root_f WHERE id=2", rootR[1], math.SmallestNonzeroFloat64},
		{"SELECT r,d FROM root_f WHERE id=3", rootR[2], 1.2345678901234567},
	} {
		var r, d float64
		if err := db.QueryRow(tc.q).Scan(&r, &d); err != nil || math.Float64bits(r) != math.Float64bits(tc.r) || math.Float64bits(d) != math.Float64bits(tc.d) {
			t.Fatal("original single-row floating bits", tc.q, r, d, err)
		}
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
		record.mu.Lock()
		recordStart := len(record.operations)
		record.mu.Unlock()
		if err := db.QueryRowContext(ctx, tc.q).Scan(&r, &d); err != nil || math.Float64bits(r) != math.Float64bits(tc.r) || math.Float64bits(d) != math.Float64bits(tc.d) {
			t.Fatal("recorded single-row floating bits", tc.q, r, d, err)
		}
		record.mu.Lock()
		var captured *referenceOperation
		for _, op := range record.operations[recordStart:] {
			if op.SQL == tc.q && op.Columns != nil {
				captured = op
			}
		}
		record.mu.Unlock()
		if captured == nil || !reflect.DeepEqual(captured.Columns, []string{"r", "d"}) || len(captured.Rows) != 1 || len(captured.Rows[0]) != 2 || captured.Err != nil || !captured.Closed || captured.CloseErr != nil {
			t.Fatal("single-row floating recording", captured)
		}
		for i, value := range captured.Rows[0] {
			referenceAssertRawValue(t, backend.pg, []string{"FLOAT4", "FLOAT8"}[i], value)
			if math.Float64bits(value.(float64)) != math.Float64bits([]float64{tc.r, tc.d}[i]) {
				t.Fatal("recorded single-row floating bits", tc.q, i, value)
			}
		}
	}
}
