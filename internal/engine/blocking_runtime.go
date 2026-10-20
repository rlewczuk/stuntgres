package engine

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var _ functions.BlockingRuntime = (*EvalContext)(nil)

func (c *EvalContext) BlockingInquiry(kind functions.BlockingKind, pid int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: blocking inquiry lacks statement authority")
	}
	e := scope.op.session.Engine()
	// Validate under engine authority, then release it before manager reads.
	// Safe-snapshot acquisition is SSI metadata and stays under Engine.mu.
	e.mu.Lock()
	_, err := scope.writeAuthorizationLocked(e)
	var elements []sqltype.Value
	if err == nil && pid > 0 && kind == functions.SafeSnapshotBlockingInquiry {
		elements, err = e.safeSnapshotBlockingPidsLocked(ids.SessionID(pid), result.Element())
	}
	e.mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	if pid > 0 && kind == functions.OrdinaryBlockingInquiry {
		elements, err = e.blockingPidValues(ids.SessionID(pid), result.Element())
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if kind.Name() == "" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid blocking inquiry kind")
	}
	var dimensions []sqltype.ArrayDimension
	if len(elements) != 0 {
		dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
	}
	return sqltype.OwnedArrayValue(result, dimensions, elements)
}
