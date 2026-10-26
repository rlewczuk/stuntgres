package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype"
)

type FTSParserRuntime interface{ ResolveFTSParser(sqltype.Value) error }

// Args stay in written order; Order maps each operand to its formal slot.
// The analyzed node owns no invocation cursor or document tokens.
type FTSParser struct {
	expr.Info
	Args       [2]expr.Expr
	Order      [2]int
	Parse, OID bool
}

func (e FTSParser) arity() int {
	if e.Parse {
		return 2
	}
	return 1
}
func (e FTSParser) name() string {
	if e.Parse {
		return "ts_parse"
	}
	return "ts_token_type"
}
func (e FTSParser) Children() []expr.Expr { return e.Args[:e.arity()] }
func (e FTSParser) Validate() error {
	d := e.Usage.Type
	width := 3
	if e.Parse {
		width = 2
	}
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != width {
		return fmt.Errorf("stuntgres: invalid text parser result shape")
	}
	seen := [2]bool{}
	for i, arg := range e.Children() {
		slot := e.Order[i]
		if arg == nil || slot < 0 || slot >= e.arity() || seen[slot] {
			return fmt.Errorf("stuntgres: invalid text parser operands")
		}
		seen[slot] = true
		id := sqltype.Text
		if slot == 0 && e.OID {
			id = sqltype.Oid
		}
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid text parser formal")
		}
	}
	for i := range width {
		m := d.CompositeMember(i)
		name, id := "tokid", sqltype.Int4
		if i > 0 {
			id = sqltype.Text
			name = "token"
			if !e.Parse {
				name = "alias"
				if i == 2 {
					name = "description"
				}
			}
		}
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid text parser result member")
		}
	}
	return nil
}
func (e FTSParser) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: text parser set used as scalar")
}
func (e FTSParser) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	n := next.(FTSParser)
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}
func (e FTSParser) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSParser", children, e.arity()); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e FTSParser) SourceAdmission() (bool, bool) { return true, true }
func (e FTSParser) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(FTSParser)
	if !ok || e.Parse != o.Parse || e.OID != o.OID || e.Order != o.Order || !sqltype.SameRecordUsage(e.Usage, o.Usage) {
		return false
	}
	return true
}
func (e FTSParser) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := e.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Children() {
		v, err := arg.Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values[e.Order[i]] = v
	}
	for _, v := range values[:e.arity()] {
		if v.IsNull() {
			return expr.OwnedSource(e.Usage.Type, nil), nil
		}
	}
	runtime, ok := c.(FTSParserRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing text parser authority")
	}
	if err := runtime.ResolveFTSParser(values[0]); err != nil {
		return expr.SourceCursor{}, err
	}
	cursor := expr.SourceCursor{Type: e.Usage.Type, Advance: nextFTSDescriptor}
	if e.Parse {
		text, err := values[1].Text()
		if err != nil {
			return expr.SourceCursor{}, err
		}
		tokens, err := fts.ParseDocument(ctx, text)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		cursor.State, cursor.Advance = tokens, nextFTSToken
	}
	return expr.NewSourceCursor(cursor), nil
}
func nextFTSToken(_ context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	tokens := s.State.([]fts.DocumentToken)
	if s.Position == len(tokens) {
		return sqltype.Value{}, false, nil
	}
	token := tokens[s.Position]
	s.Position++
	id, err := sqltype.Integer(s.Type.CompositeMember(0).Usage.Type, int64(token.ID))
	if err != nil {
		return sqltype.Value{}, false, err
	}
	v, err := sqltype.OwnedCompositeValue(s.Type, []sqltype.Value{id, sqltype.String(s.Type.CompositeMember(1).Usage.Type, token.Text)})
	return v, err == nil, err
}
func nextFTSDescriptor(_ context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	if s.Position == len(ftsParserDescriptors) {
		return sqltype.Value{}, false, nil
	}
	d := ftsParserDescriptors[s.Position]
	s.Position++
	id, err := sqltype.Integer(s.Type.CompositeMember(0).Usage.Type, int64(s.Position))
	if err != nil {
		return sqltype.Value{}, false, err
	}
	v, err := sqltype.OwnedCompositeValue(s.Type, []sqltype.Value{id, sqltype.String(s.Type.CompositeMember(1).Usage.Type, d.Alias), sqltype.String(s.Type.CompositeMember(2).Usage.Type, d.Description)})
	return v, err == nil, err
}

var ftsParserDescriptors = fts.DocumentTypes()
