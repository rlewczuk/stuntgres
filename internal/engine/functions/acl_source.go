package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e ACLValue) SourceAdmission() (bool, bool) {
	return e.Kind != ACLContains, e.Kind == ACLExplode
}

func (e ACLValue) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(ACLValue)
	return ok && e.Kind == ACLExplode && o.Kind == ACLExplode &&
		sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

func (e ACLValue) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	if e.Kind == ACLContains {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	if e.Kind != ACLExplode {
		value, err := e.Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	cursor, err := e.OpenExplode(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, State: &cursor,
		Advance: nextACLSource, Release: closeACLSource,
	}), nil
}

func nextACLSource(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	return s.State.(*ACLExplodeCursor).Next(ctx, s.Type)
}

func closeACLSource(state any) {
	state.(*ACLExplodeCursor).Close()
}
