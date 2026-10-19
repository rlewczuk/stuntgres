package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

// captureShape validates numeric bounds without retaining receiver storage.
func captureShape(count, limit int) (expr.CaptureOperands, bool, error) {
	if count < 0 || limit < 0 || count > limit {
		return expr.CaptureOperands{}, true, fmt.Errorf("invalid capture count")
	}
	return expr.CaptureOperands{Count: count, Indexed: true}, true, nil
}

// captureWritten validates transient storage; only numeric metadata is returned.
func captureWritten(args []expr.Expr, slots []uint8, count, limit int) (expr.CaptureOperands, bool, error) {
	if limit < 0 || limit > len(args) || count < 0 || count > limit || count > len(slots) {
		return expr.CaptureOperands{}, true, fmt.Errorf("invalid written capture operands")
	}
	for written := range count {
		if _, err := captureWrittenAt(args, slots, count, limit, written); err != nil {
			return expr.CaptureOperands{}, true, err
		}
	}
	return expr.CaptureOperands{Count: count, Indexed: true}, true, nil
}

// captureWrittenAt maps existing provenance directly, without operand copies.
func captureWrittenAt(args []expr.Expr, slots []uint8, count, limit, written int) (expr.Expr, error) {
	if limit < 0 || limit > len(args) || count < 0 || count > limit || count > len(slots) || written < 0 || written >= count {
		return nil, fmt.Errorf("invalid written capture operands")
	}
	return captureAt(args, limit, int(slots[written]))
}

// captureAt returns only the selected Expr, never the transient argument slice.
func captureAt(args []expr.Expr, count, written int) (expr.Expr, error) {
	if count < 0 || count > len(args) || written < 0 || written >= count {
		return nil, fmt.Errorf("invalid capture operand slot")
	}
	arg := args[written]
	if arg == nil {
		return nil, fmt.Errorf("nil written capture operand")
	}
	return arg, nil
}
