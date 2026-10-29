package typedef

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ExactTimestampReplayNeeded is deliberately top-level and destination-owned.
// It does not authorize arbitrary typed values or temporal domains.
func ExactTimestampReplayNeeded(ctx context.Context, view catalog.Read, usage sqltype.Usage, value sqltype.Value) (bool, error) {
	if !exactTimestampUsage(view, usage) || value.Type() != usage.Type || value.IsNull() { return false, nil }
	n, err := value.Timestamp()
	if err != nil { return false, err }
	if n == sqltype.TimestampNegativeInfinity || n == sqltype.TimestampPositiveInfinity { return false, nil }
	coercion, err := sqltype.PlanCoercion(usage.Type.Usage(), usage, sqltype.Assignment)
	if err != nil { return false, err }
	rounded, err := coercion.Apply(ctx, value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil { return false, err }
	r, err := rounded.Timestamp()
	return n != r, err
}

func exactTimestampUsage(view catalog.Read, u sqltype.Usage) bool {
	if !u.Valid() || u.Typmod < 0 || u.Typmod > 5 || u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Timestamptz { return false }
	current, ok := view.LookupType(u.Type.ID())
	return ok && current == u.Type
}

func restoreExactTimestamp(env *DomainReplayEnv, u sqltype.Usage, text string) (sqltype.Value, DomainReplayProvenance, error) {
	bad := func() (sqltype.Value, DomainReplayProvenance, error) { return sqltype.Value{}, 0, fmt.Errorf("stuntgres: invalid exact timestamp replay") }
	if !exactTimestampUsage(env.Catalog, u) || env.Scalar == nil { return bad() }
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
	value, err := env.Scalar(raw, u.Type.Usage())
	if err != nil { return sqltype.Value{}, 0, err }
	needed, err := ExactTimestampReplayNeeded(env.Context, env.Catalog, u, value)
	if err != nil { return sqltype.Value{}, 0, err }
	if !needed { return bad() }
	canonical, err := u.Type.Output(env.Context, value)
	if err != nil { return sqltype.Value{}, 0, err }
	if canonical != text { return bad() }
	return value, DomainReplayTimestampExact, nil
}
