package functions

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (EncodingNameCall) SourceAdmission() (bool, bool) { return true, false }
func (n EncodingNameCall) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
func (Keywords) SourceAdmission() (bool, bool) { return true, true }
func (n Keywords) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(Keywords)
	return ok && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n Keywords) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil { return expr.SourceCursor{}, err }
	if err := n.Validate(); err != nil { return expr.SourceCursor{}, err }
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Advance: nextKeywordSource}), nil
}
func nextKeywordSource(_ context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	k, ok := parser.KeywordAt(s.Position)
	if !ok { return sqltype.Value{}, false, nil }
	s.Position++
	var code, category string
	switch k.Category {
	case 'U': code, category = "U", "unreserved"
	case 'C': code, category = "C", "unreserved (cannot be function or type name)"
	case 'T': code, category = "T", "reserved (can be function or type name)"
	case 'R': code, category = "R", "reserved"
	}
	bare := "requires AS"
	if k.BareLabel { bare = "can be bare label" }
	fields := make([]sqltype.Value, 5)
	fields[0] = sqltype.String(s.Type.CompositeMember(0).Usage.Type, k.Word)
	fields[1] = sqltype.String(s.Type.CompositeMember(1).Usage.Type, code)
	fields[2] = sqltype.Boolean(s.Type.CompositeMember(2).Usage.Type, k.BareLabel)
	fields[3] = sqltype.String(s.Type.CompositeMember(3).Usage.Type, category)
	fields[4] = sqltype.String(s.Type.CompositeMember(4).Usage.Type, bare)
	value, err := sqltype.OwnedCompositeValue(s.Type, fields)
	return value, err == nil, err
}
