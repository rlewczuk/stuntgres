package sqltype

import (
	"context"
	"fmt"
	"math"
)

// SegIntervalOperation identifies the eight installed binary seg predicates.
type SegIntervalOperation uint8

const (
	SegLeft SegIntervalOperation = iota
	SegRight
	SegOverLeft
	SegOverRight
	SegSame
	SegOverlap
	SegContains
	SegContained
)

var segIntervalRoutines = [...]string{"seg_left", "seg_right", "seg_over_left", "seg_over_right", "seg_same", "seg_overlap", "seg_contains", "seg_contained"}

func (o SegIntervalOperation) Routine() string {
	if int(o) >= len(segIntervalRoutines) {
		return ""
	}
	return segIntervalRoutines[o]
}
func SegIntervalKind(name string) (SegIntervalOperation, bool) {
	for i, routine := range segIntervalRoutines {
		if name == routine {
			return SegIntervalOperation(i), true
		}
	}
	return 0, false
}
func (o SegIntervalOperation) ResultID() ID { return Bool }

type SegIntervalPlan struct {
	operation     SegIntervalOperation
	input, result *Descriptor
}

func BindSegInterval(op SegIntervalOperation, result, left, right Usage) (SegIntervalPlan, error) {
	if op.Routine() == "" || !left.Type.IsSeg() || left != left.Type.Usage() || right != left || result.Type == nil || result.Type.ID() != Bool || result != result.Type.Usage() {
		return SegIntervalPlan{}, fmt.Errorf("invalid seg interval signature")
	}
	return SegIntervalPlan{op, left.Type, result.Type}, nil
}
func (p SegIntervalPlan) Operation() SegIntervalOperation { return p.operation }

func (p SegIntervalPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !p.input.IsSeg() || p.result == nil || p.result.ID() != Bool || p.operation.Routine() == "" {
		return Value{}, fmt.Errorf("invalid seg interval plan")
	}
	for _, v := range values {
		if v.typ != p.input || !v.null && !v.seg.valid() {
			return Value{}, fmt.Errorf("invalid seg interval operand")
		}
	}
	if values[0].null || values[1].null {
		return Null(p.result), nil
	}
	x, y := values[0].seg, values[1].seg
	a, b := math.Float32frombits(x.LowerBits), math.Float32frombits(x.UpperBits)
	c, d := math.Float32frombits(y.LowerBits), math.Float32frombits(y.UpperBits)
	var result bool
	switch p.operation {
	case SegLeft:
		result = b < c
	case SegRight:
		result = a > d
	case SegOverLeft:
		result = b <= d
	case SegOverRight:
		result = a >= c
	case SegSame:
		result = a == c && b == d && x.LowerDigits == y.LowerDigits && x.UpperDigits == y.UpperDigits && x.LowerMarker == y.LowerMarker && x.UpperMarker == y.UpperMarker
	case SegOverlap:
		// Keep the released OR: uncertainty input can produce inverted endpoints.
		result = b >= d && a <= d || d >= b && c <= b
	case SegContains:
		result = a <= c && b >= d
	case SegContained:
		result = c <= a && d >= b
	}
	return Boolean(p.result, result), nil
}
