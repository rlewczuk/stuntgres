package session

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// FTSInput emits the text-search input notices of operation Op: the NOTICE
// for a query without lexemes, positioned at a literal or naming the portal
// parameter it came from. A nil Op emits nothing.
type FTSInput struct {
	Op       *Operation
	position int
	portal   string
}

func (r FTSInput) EmptyQuery(ctx context.Context, text string) {
	if r.Op == nil || ctx.Err() != nil {
		return
	}
	n := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "text-search query doesn't contain lexemes: \"" + text + "\"", Context: r.portal}
	if r.position > 0 {
		n.Position = strconv.Itoa(r.position)
	}
	r.Op.EmitNotice(LevelNotice, n)
}
func (r FTSInput) FTSLiteral(position int) sqltype.FTSInputNotices {
	r.position = position
	r.portal = ""
	return r
}
func (r FTSInput) FTSParameter(slot int, empty bool) sqltype.FTSInputNotices {
	r.position = 0
	r.portal = fmt.Sprintf("unnamed portal parameter $%d = '...'", slot)
	if empty {
		r.portal = fmt.Sprintf("unnamed portal parameter $%d = ''", slot)
	}
	return r
}
