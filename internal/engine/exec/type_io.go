package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// EnterTypeIO borrows this actual evaluator only for the immediate leaf kernel.
// Descriptor recursion passes the operation environment, never a cached host.
func (c *Context) EnterTypeIO(ctx context.Context, descriptor *sqltype.Descriptor, direction sqltype.TypeIODirection) (sqltype.RoutineBodyToken, error) {
	if c == nil || ctx == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing actual type I/O owner")
	}
	if err := ctx.Err(); err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	callback, err := catalog.CaptureTypeIO(c.Catalog, descriptor, direction)
	if err != nil {
		return sqltype.RoutineBodyToken{}, err
	}
	if c.ControlLiteralAuthority.present() {
		if _, _, err = c.controlLiteralAccess(); err != nil {
			return sqltype.RoutineBodyToken{}, err
		}
		// The real canonical private codec is host-internal, not a simulated client
		// subject. Its actual declared callback association above is still required.
		return sqltype.RoutineBodyToken{}, nil
	}
	host := c.LiveHost()
	if c.ClosedEvaluation != nil || host == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: active type I/O lacks borrowed invocation authority")
	}
	return host.EnterTypeIOCallback(callback)
}
