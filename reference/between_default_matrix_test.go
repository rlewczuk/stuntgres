package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceBetweenDefaultCompositionMatrix(t *testing.T) {
	for _, family := range []struct {
		name, a, l, u string
		null          bool
	}{
		{"comparison", "TRUE", "(1<2)", "(2<3)", false},
		{"tested", "(1<2)", "FALSE", "TRUE", false},
		{"nested", "TRUE", "(1 BETWEEN 0 AND 2)", "(2 BETWEEN 1 AND 3)", false},
		{"null", "NULL", "(1<2)", "(2<3)", true},
		{"reversed-inner", "FALSE", "(2 BETWEEN 3 AND 1)", "(1 BETWEEN 0 AND 2)", false},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", family.name, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				run := func(step referenceStep) { step.prepared = prepared; scalarStep(t, p, step) }
				definitions := []string{}
				for i, form := range betweenReferenceForms {
					definitions = append(definitions, fmt.Sprintf("b%d BOOL DEFAULT (%s %s %s AND %s)", i, family.a, form, family.l, family.u))
				}
				run(referenceExec("CREATE TABLE d("+strings.Join(definitions, ",")+")", 0))
				run(referenceExec("INSERT INTO d DEFAULT VALUES", 1))
				run(referenceExec("INSERT INTO d DEFAULT VALUES", 1))
				row := []any{true, false, true, false}
				if family.null {
					row = []any{nil, nil, nil, nil}
				}
				step := referenceSelect("SELECT * FROM d", []string{"b0", "b1", "b2", "b3"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{row, row})
				step.bag = true
				run(step)
			})
		}
	}
}
