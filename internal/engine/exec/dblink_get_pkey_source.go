package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) AdvanceDblinkGetPkey(n plan.InstalledDblinkGetPkeyExpr, ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || (cursor.State == nil && cursor.Position >= 0) {
		return cursor.Next(ctx)
	}
	body, err := c.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, expr.RoutineBodyInputs{Count: 1, First: n.Arg.ResultUsage().Type, Occurrence: n.Occurrence, Source: n.Source})
	if err != nil {
		cursor.Close()
		return sqltype.Value{}, false, err
	}
	defer body.Leave()
	if cursor.Position < 0 {
		name, err := cursor.Value.Text()
		if err != nil {
			cursor.Close()
			return sqltype.Value{}, false, err
		}
		host := c.LiveHost()
		if host == nil {
			cursor.Close()
			return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing dblink source authority")
		}
		names, err := host.Contrib().DblinkGetPkey(n, name)
		if err != nil {
			cursor.Close()
			return sqltype.Value{}, false, err
		}
		cursor.State = names
		cursor.Value = sqltype.Value{}
		cursor.Position = 0
	}
	return cursor.Next(ctx)
}
func nextDblinkPkeySource(ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if cursor.Position < 0 {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing dblink first-call authority")
	}
	names, ok := cursor.State.([]string)
	if !ok {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid dblink source state")
	}
	if cursor.Position == len(names) {
		return sqltype.Value{}, false, nil
	}
	d := cursor.Type
	position, err := sqltype.Integer(d.CompositeMember(0).Usage.Type, int64(cursor.Position+1))
	if err != nil {
		return sqltype.Value{}, false, err
	}
	value, err := sqltype.OwnedCompositeValue(d, []sqltype.Value{position, sqltype.String(d.CompositeMember(1).Usage.Type, names[cursor.Position])})
	cursor.Position++
	return value, err == nil, err
}
