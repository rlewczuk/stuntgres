package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceNumericArithmeticSpecials(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		numericArithmeticUnit(t, p, b)
		// Independent REL_18_0 numeric.out 49-pair arithmetic matrices, row-major.
		operands := []string{"0", "1", "-1", "4.2", "Infinity", "-Infinity", "NaN"}
		matrices := []struct {
			op   string
			rows [][]string
		}{
			{"+", [][]string{
				{"0", "1", "-1", "4.2", "Infinity", "-Infinity", "NaN"},
				{"1", "2", "0", "5.2", "Infinity", "-Infinity", "NaN"},
				{"-1", "0", "-2", "3.2", "Infinity", "-Infinity", "NaN"},
				{"4.2", "5.2", "3.2", "8.4", "Infinity", "-Infinity", "NaN"},
				{"Infinity", "Infinity", "Infinity", "Infinity", "Infinity", "NaN", "NaN"},
				{"-Infinity", "-Infinity", "-Infinity", "-Infinity", "NaN", "-Infinity", "NaN"},
				{"NaN", "NaN", "NaN", "NaN", "NaN", "NaN", "NaN"},
			}},
			{"-", [][]string{
				{"0", "-1", "1", "-4.2", "-Infinity", "Infinity", "NaN"},
				{"1", "0", "2", "-3.2", "-Infinity", "Infinity", "NaN"},
				{"-1", "-2", "0", "-5.2", "-Infinity", "Infinity", "NaN"},
				{"4.2", "3.2", "5.2", "0.0", "-Infinity", "Infinity", "NaN"},
				{"Infinity", "Infinity", "Infinity", "Infinity", "NaN", "Infinity", "NaN"},
				{"-Infinity", "-Infinity", "-Infinity", "-Infinity", "-Infinity", "NaN", "NaN"},
				{"NaN", "NaN", "NaN", "NaN", "NaN", "NaN", "NaN"},
			}},
			{"*", [][]string{
				{"0", "0", "0", "0.0", "NaN", "NaN", "NaN"},
				{"0", "1", "-1", "4.2", "Infinity", "-Infinity", "NaN"},
				{"0", "-1", "1", "-4.2", "-Infinity", "Infinity", "NaN"},
				{"0.0", "4.2", "-4.2", "17.64", "Infinity", "-Infinity", "NaN"},
				{"NaN", "Infinity", "-Infinity", "Infinity", "Infinity", "-Infinity", "NaN"},
				{"NaN", "-Infinity", "Infinity", "-Infinity", "-Infinity", "Infinity", "NaN"},
				{"NaN", "NaN", "NaN", "NaN", "NaN", "NaN", "NaN"},
			}},
		}
		for _, matrix := range matrices {
			t.Run(matrix.op, func(t *testing.T) {
				vectors := []numericArithmeticVector{}
				for i, a := range operands {
					for j, c := range operands {
						vectors = append(vectors, numericArithmeticVector{"'" + a + "'::numeric " + matrix.op + " '" + c + "'::numeric", matrix.rows[i][j]})
					}
				}
				numericArithmeticVectors(t, p, b, vectors)
			})
		}
		// Published 42-pair division and modulo outputs, denominator order excludes zero.
		denominators := operands[1:]
		quot := [][]string{
			{"0.00000000000000000000", "0.00000000000000000000", "0.00000000000000000000", "0", "0", "NaN"},
			{"1.00000000000000000000", "-1.00000000000000000000", "0.23809523809523809524", "0", "0", "NaN"},
			{"-1.00000000000000000000", "1.00000000000000000000", "-0.23809523809523809524", "0", "0", "NaN"},
			{"4.2000000000000000", "-4.2000000000000000", "1.00000000000000000000", "0", "0", "NaN"},
			{"Infinity", "-Infinity", "Infinity", "NaN", "NaN", "NaN"},
			{"-Infinity", "Infinity", "-Infinity", "NaN", "NaN", "NaN"},
			{"NaN", "NaN", "NaN", "NaN", "NaN", "NaN"},
		}
		mod := [][]string{
			{"0", "0", "0.0", "0", "0", "NaN"}, {"0", "0", "1.0", "1", "1", "NaN"}, {"0", "0", "-1.0", "-1", "-1", "NaN"},
			{"0.2", "0.2", "0.0", "4.2", "4.2", "NaN"}, {"NaN", "NaN", "NaN", "NaN", "NaN", "NaN"}, {"NaN", "NaN", "NaN", "NaN", "NaN", "NaN"}, {"NaN", "NaN", "NaN", "NaN", "NaN", "NaN"},
		}
		for _, matrix := range []struct {
			op   string
			rows [][]string
		}{{"/", quot}, {"%", mod}} {
			t.Run(matrix.op, func(t *testing.T) {
				vectors := []numericArithmeticVector{}
				for i, a := range operands {
					for j, c := range denominators {
						vectors = append(vectors, numericArithmeticVector{"'" + a + "'::numeric " + matrix.op + " '" + c + "'::numeric", matrix.rows[i][j]})
					}
				}
				numericArithmeticVectors(t, p, b, vectors)
			})
		}
		powerOperands := []string{"0", "1", "2", "4.2", "Infinity", "NaN"}
		power := [][]string{
			{"1.0000000000000000", "0.0000000000000000", "0.0000000000000000", "0.0000000000000000", "0", "NaN"},
			{"1.0000000000000000", "1.0000000000000000", "1.0000000000000000", "1.0000000000000000", "1", "1"},
			{"1.0000000000000000", "2.0000000000000000", "4.0000000000000000", "18.379173679952560", "Infinity", "NaN"},
			{"1.0000000000000000", "4.2000000000000000", "17.640000000000000", "414.61691860129675", "Infinity", "NaN"},
			{"1", "Infinity", "Infinity", "Infinity", "Infinity", "NaN"},
			{"1", "NaN", "NaN", "NaN", "NaN", "NaN"},
		}
		t.Run("power", func(t *testing.T) {
			vectors := []numericArithmeticVector{}
			for i, a := range powerOperands {
				for j, c := range powerOperands {
					vectors = append(vectors, numericArithmeticVector{"'" + a + "'::numeric ^ '" + c + "'::numeric", power[i][j]})
				}
			}
			numericArithmeticVectors(t, p, b, vectors)
		})
		numericArithmeticVectors(t, p, b, []numericArithmeticVector{
			{"'NaN'::numeric/0", "NaN"}, {"'NaN'::numeric%0", "NaN"}, {"'NaN'::numeric^0", "1"},
			{"'-Infinity'::numeric^3", "-Infinity"}, {"'-Infinity'::numeric^2", "Infinity"}, {"'-Infinity'::numeric ^ -3", "0"},
			{"(-1)::numeric ^ 'Infinity'::numeric", "1"}, {"(-1)::numeric ^ '-Infinity'::numeric", "1"},
			{"0.5::numeric ^ 'Infinity'::numeric", "0"}, {"0.5::numeric ^ '-Infinity'::numeric", "Infinity"}, {"2::numeric ^ '-Infinity'::numeric", "0"},
			{"+'Infinity'::numeric", "Infinity"}, {"-'Infinity'::numeric", "-Infinity"}, {"-'-Infinity'::numeric", "Infinity"}, {"-'NaN'::numeric", "NaN"},
		})
		for _, op := range []string{"+", "-", "*", "/", "%", "^"} {
			for _, expr := range []string{"NULL::numeric " + op + " 0::numeric", "0::numeric " + op + " NULL::numeric", "NULL::numeric " + op + " (-1)::numeric"} {
				t.Run(fmt.Sprintf("null/%s", expr), func(t *testing.T) { numericArithmeticVectors(t, p, b, []numericArithmeticVector{{expr, nil}}) })
			}
		}
	})
}
