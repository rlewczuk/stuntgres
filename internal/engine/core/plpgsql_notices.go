package core

import (
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
)

// plNoticeOrigin is borrowed execution provenance, not rendered context or a
// callback stack. Capture it before an expression replaces the lexical scratch.
type plNoticeOrigin struct {
	source, identity string
	position         parser.Position
	action           string
}

func (s *StatementScope) plNoticeOrigin(f *proc.PLFrame, position parser.Position, action string) plNoticeOrigin {
	return plNoticeOrigin{f.Program.Source, s.sqlRoutine.plDiagnostic, position, action}
}

// Finalize only notices emitted inside the caller's actual execution boundary.
// The operation still owns these filtered records; adapters deliver them later.
// Formatting does no work for the overwhelmingly common no-notice execution.
func (s *StatementScope) appendPLNoticeContext(start int, origin plNoticeOrigin) {
	if start == len(s.Op.Notices) || origin.action == "" {
		return
	}
	s.appendNoticeContext(start, proc.PLActionContext(origin.source, origin.identity, origin.position, origin.action))
}

func (s *StatementScope) appendNoticeContext(start int, context string) {
	for i := start; i < len(s.Op.Notices); i++ {
		diagnostic := &s.Op.Notices[i]
		if diagnostic.Context != "" {
			diagnostic.Context += "\n"
		}
		diagnostic.Context += context
	}
}
