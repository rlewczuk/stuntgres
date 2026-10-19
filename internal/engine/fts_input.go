package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type capturedFTSInput struct {
	op       *Operation
	position int
	portal   string
}

func (r capturedFTSInput) EmptyQuery(ctx context.Context, text string) {
	if r.op == nil || ctx.Err() != nil {
		return
	}
	n := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "text-search query doesn't contain lexemes: \"" + text + "\"", Context: r.portal}
	if r.position > 0 {
		n.Position = strconv.Itoa(r.position)
	}
	r.op.emitNotice(levelNotice, n)
}
func (r capturedFTSInput) FTSLiteral(position int) sqltype.FTSInputNotices {
	r.position = position
	r.portal = ""
	return r
}
func (r capturedFTSInput) FTSParameter(slot int, empty bool) sqltype.FTSInputNotices {
	r.position = 0
	r.portal = fmt.Sprintf("unnamed portal parameter $%d = '...'", slot)
	if empty {
		r.portal = fmt.Sprintf("unnamed portal parameter $%d = ''", slot)
	}
	return r
}
