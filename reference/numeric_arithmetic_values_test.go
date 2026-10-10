package reference_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func numericArithmeticBackends(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, p, backend) })
	}
}

func numericArithmeticUnit(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE unit(id INT)", 0))
	referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO unit VALUES(1)", 1))
}

type numericArithmeticVector struct {
	expr  string
	value any
}

func numericArithmeticVectors(t *testing.T, p *referencePair, b referenceBackend, vectors []numericArithmeticVector) {
	t.Helper()
	for i, tc := range vectors {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("case%d/prepared%t", i, prepared), func(t *testing.T) {
				step := referenceSelect("SELECT "+tc.expr+" FROM unit", []string{"?column?"}, []string{"NUMERIC"}, [][]any{{tc.value}})
				step.prepared = prepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, step)
			})
		}
	}
}

func TestReferenceNumericArithmeticExactValues(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		numericArithmeticUnit(t, p, b)
		numericArithmeticVectors(t, p, b, []numericArithmeticVector{
			{"0.1+0.2", "0.3"}, {"1.20+2.300", "3.500"}, {"1.20-1.2", "0.00"}, {"1.20*2.300", "2.76000"},
			{"9007199254740993::numeric+1", "9007199254740994"}, {"9223372036854775808+1", "9223372036854775809"},
			{"12345678901234567890.12345678901234567890+0.00000000000000000001", "12345678901234567890.12345678901234567891"},
			{"+1.200::numeric", "1.200"}, {"-1.200::numeric", "-1.200"}, {"-(-1.200::numeric)", "1.200"}, {"-0.000::numeric", "0.000"},
			{"1::int+2.30::numeric", "3.30"}, {"2.30::numeric+1::int", "3.30"},
		})
		s := referenceSelect("SELECT 1.5::numeric+2::real FROM unit", []string{"?column?"}, []string{"FLOAT8"}, [][]any{{float64(3.5)}})
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
	})
}

func TestReferenceNumericArithmeticDivisionRemainderScale(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		numericArithmeticUnit(t, p, b)
		numericArithmeticVectors(t, p, b, []numericArithmeticVector{
			{"5.0/2", "2.5000000000000000"}, {"1.0/3", "0.33333333333333333333"}, {"2.0/3", "0.66666666666666666667"}, {"1.0/1.0", "1.00000000000000000000"}, {"-7.0/2", "-3.5000000000000000"},
			{"-7.00%2.0", "-1.00"}, {"7.00% -2.0", "1.00"}, {"-7.00% -2.0", "-1.00"}, {"5.5%2.00", "1.50"}, {"9007199254740993::numeric%2", "1"},
			{"5e-1001::numeric/1", "0." + strings.Repeat("0", 999) + "1"}, {"-5e-1001::numeric/1", "-0." + strings.Repeat("0", 999) + "1"}, {"4e-1001::numeric/1", "0." + strings.Repeat("0", 1000)},
			{"1e-16383::numeric*1e-16383::numeric", "0." + strings.Repeat("0", 16383)},
			{"5e-8192::numeric*1e-8192::numeric", "0." + strings.Repeat("0", 16382) + "1"}, {"-5e-8192::numeric*1e-8192::numeric", "-0." + strings.Repeat("0", 16382) + "1"}, {"4e-8192::numeric*1e-8192::numeric", "0." + strings.Repeat("0", 16383)},
			{"199999999999999999::numeric/20000000000000000", "10.0000000000000000"}, {"-199999999999999999::numeric/20000000000000000", "-10.0000000000000000"},
		})
	})
}

func TestReferenceNumericArithmeticPowerGrammar(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		numericArithmeticUnit(t, p, b)
		numericArithmeticVectors(t, p, b, []numericArithmeticVector{
			{"2::numeric^3::numeric^2::numeric", "64.0000000000000000"}, {"2::numeric^(3::numeric^2::numeric)", "512.0000000000000000"},
			{"2::numeric ^ -3", "0.1250000000000000"}, {"10::numeric^2", "100.00000000000000"}, {"-2::numeric^2", "4.0000000000000000"}, {"-(2::numeric^2)", "-4.0000000000000000"}, {"0::numeric^0", "1.0000000000000000"},
			{"2::numeric ^(-3)", "0.1250000000000000"}, {"7.00::numeric%(-2)", "1.00"},
			{"2::numeric OPERATOR(pg_catalog.^) 3*2", "64.000000000000000"},
		})
		for _, expr := range []string{"2^3^2", "2 OPERATOR(pg_catalog.^) 3*2"} {
			s := referenceSelect("SELECT "+expr+" FROM unit", []string{"?column?"}, []string{"FLOAT8"}, [][]any{{float64(64)}})
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
		}
		for _, tc := range []struct{ expr, signature string }{{"2::numeric^-3", "numeric ^- integer"}, {"7::numeric%-2", "numeric %- integer"}} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared%t", tc.expr, prepared), func(t *testing.T) {
					s := referenceImplicitError("SELECT "+tc.expr+" FROM unit", referenceDiagnostic{Code: "42883", Message: "operator does not exist: " + tc.signature, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "18"})
					s.selectRows = true
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				})
			}
		}
	})
}

func TestReferenceNumericArithmeticPowerPrecision(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		numericArithmeticUnit(t, p, b)
		// Published REL_18_0 numeric.out outputs; ^ uses numeric_power too.
		numericArithmeticVectors(t, p, b, []numericArithmeticVector{
			{"2::numeric^4.2", "18.379173679952560"}, {"4.2::numeric^4.2", "414.61691860129675"}, {"4.2::numeric^2", "17.640000000000000"}, {"(-2)::numeric^3", "-8.0000000000000000"}, {"(-2)::numeric ^ -1", "-0.5000000000000000"}, {"1e-1000::numeric^2", "0." + strings.Repeat("0", 1000)},
		})
		// Exact pinned PostgreSQL18.0 observations (tests-1), never binary approximations.
		numericArithmeticVectors(t, p, b, []numericArithmeticVector{
			{"2::numeric^0.5", "1.4142135623730950"},
			{"9::numeric^0.5", "3.0000000000000000"},
			{"0.01::numeric^0.5", "0.10000000000000000"},
			{"2.0000000000000000000000000000000000000000::numeric^0.5", "1.4142135623730950488016887242096980785697"},
			{"2::numeric ^ -0.5", "0.7071067811865475"},
			{"1e-1000::numeric^0.5", "0." + strings.Repeat("0", 499) + "1" + strings.Repeat("0", 500)},
			{"1e1000::numeric^0.5", "1" + strings.Repeat("0", 500) + ".0"},
			{"9.9999999999999999999999999999999999999999::numeric^0.5", "3.1622776601683793319988935444327185337195"},
			{"1.0000000000000000000000000000000000000001::numeric^10000000000000000000000000000000000000000::numeric", "2.7182818284590452353602874713526624977571"},
			{"0.9999999999999999999999999999999999999999::numeric^10000000000000000000000000000000000000000::numeric", "0.3678794411714423215955237701614608674458"},
			{"1.0000000000000000000000000000000000000001::numeric ^ -10000000000000000000000000000000000000000::numeric", "0.3678794411714423215955237701614608674458"},
			{"0.9999999999999999999999999999999999999999::numeric ^ -10000000000000000000000000000000000000000::numeric", "2.7182818284590452353602874713526624977574"},
		})
		for _, exp := range []struct{ text, negativeOne string }{
			{"2147483647", "-1.0000000000000000"}, {"2147483648", "1.0000000000000000"},
			{"-2147483648", "1.0000000000000000"}, {"-2147483649", "-1.0000000000000000"},
			{"9007199254740993", "-1.0000000000000000"}, {"9007199254740992", "1.0000000000000000"},
		} {
			numericArithmeticVectors(t, p, b, []numericArithmeticVector{
				{"1::numeric ^ (" + exp.text + "::numeric)", "1.0000000000000000"},
				{"(-1)::numeric ^ (" + exp.text + "::numeric)", exp.negativeOne},
			})
		}
		numericArithmeticGuardValue(t, p, b)
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("SELECT 2::numeric^8656.18 FROM unit", referenceDiagnostic{Code: "22003", Message: "value overflows numeric format"})
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
	})
}

// The complete pinned 2609-byte output is asserted by digest, with independent
// scale/weight and boundary digits; no truncation or numeric tolerance is used.
func numericArithmeticGuardValue(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	const q = "SELECT 2::numeric^8656.17 FROM unit"
	const digest = "732a8c7f4c511632706875ddccae0b30200590fef02fe2d3ad8ae578a62859b0"
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("guard/prepared%t", prepared), func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
			var exec referenceExecutor = b.db
			if prepared {
				stmt, err := b.db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				exec = referenceHeldStatement{b.db, stmt, q}
			}
			rows, err := exec.QueryContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			numericArithmeticMetadata(b.pg)(t, rows)
			if !rows.Next() {
				t.Fatal("missing guard value", rows.Err())
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			referenceAssertRawValue(t, b.pg, "NUMERIC", raw)
			value, ok := referenceNormalize(raw).(string)
			if !ok {
				t.Fatalf("guard value %T", raw)
			}
			if len(value) != 2609 || strings.IndexByte(value, '.') != 2606 ||
				!strings.HasPrefix(value, "5845444839947996300392209728822688629142") ||
				!strings.HasSuffix(value, "6088869445289232734381989413918233774.57") ||
				fmt.Sprintf("%x", sha256.Sum256([]byte(value))) != digest {
				t.Fatalf("guard value shape/digest mismatch length=%d digest=%x", len(value), sha256.Sum256([]byte(value)))
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("guard row count/cursor error", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			referenceViewRecorded(t, b.recorder, q, []string{"?column?"}, []string{"NUMERIC"}, [][]any{{value}}, false)
		})
	}
}
