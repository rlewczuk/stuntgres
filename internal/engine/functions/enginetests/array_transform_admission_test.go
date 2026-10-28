package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

type arrayAdmissionOperand struct {
	expr.Info
	value     sqltype.Value
	evaluated *bool
	err       error
}

func (e arrayAdmissionOperand) Eval(expr.Context) (sqltype.Value, error) {
	*e.evaluated = true
	return e.value, e.err
}

func TestArrayTransformAdmissionOperandAndResultOwnership(t *testing.T) {
	catalog := engine.New().CatalogRead()
	array, _ := catalog.LookupType(1007)
	text, _ := catalog.LookupType(sqltype.Text)
	input, err := array.Input(context.Background(), "{1,NULL,3}", -1)
	if err != nil {
		t.Fatal(err)
	}
	var arrayDone, separatorDone bool
	first := arrayAdmissionOperand{Info: expr.Info{Usage: array.Usage()}, value: input, evaluated: &arrayDone}
	second := arrayAdmissionOperand{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, ","), evaluated: &separatorDone}
	call := functions.ArrayTransform{Info: expr.Info{Usage: text.Usage()}, Kind: functions.ArrayToString, Args: []expr.Expr{first, second}, Slots: [3]uint8{0, 1}}
	refusal := errors.New("array workload credit refused")
	ctx := workadmission.Activate(workadmission.With(context.Background(), func(ctx context.Context, class workadmission.Class) error {
		if class != workadmission.WorkArrayTransform {
			t.Fatalf("unexpected class: %v", class)
		}
		if !arrayDone || !separatorDone {
			t.Fatal("parent admitted before operand evaluation")
		}
		return refusal
	}))
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	value, err := call.Eval(evaluation)
	if err != refusal || value.Type() != nil {
		t.Fatal("refused transformation exposed a result", value, err)
	}
	// Operand errors and strict NULL do not enter parent element work.
	first.err = errors.New("operand failure")
	call.Args[0] = first
	if _, err := call.Eval(evaluation); err != first.err {
		t.Fatal("admission displaced operand error", err)
	}
	first.err, first.value = nil, sqltype.Null(array)
	call.Args[0] = first
	value, err = call.Eval(evaluation)
	if err != nil || !value.IsNull() || value.Type() != text {
		t.Fatal("NULL operand entered element work", value, err)
	}
	// The same immutable recipe remains usable with a fresh ordinary context.
	first.value = input
	call.Args[0] = first
	evaluation = &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	value, err = call.Eval(evaluation)
	got, outputErr := value.Text()
	if err != nil || outputErr != nil || got != "1,3" {
		t.Fatal(got, err, outputErr)
	}
}
