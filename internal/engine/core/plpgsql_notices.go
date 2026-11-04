package core

import (
	"stuntgres/internal/engine/ids"
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

// streamPLNotices publishes a top-level DO block's notices as its RAISE
// completes, as PostgreSQL sends each NoticeResponse at once. Only the DO's own
// frame outside PL subtransactions streams: no outer executor or notice-context
// mark is open there, so the detached records are final. Nested routines keep
// theirs until the statement ends.
func (s *StatementScope) streamPLNotices() error {
	publish := s.protocolOrZero().notices
	if publish == nil || !s.plNoticeStream || s.activeSQLRoutineDepth != 1 || len(s.Op.Notices) == 0 || s.TxRef == (ids.TxRef{}) || s.plSubtransactionActive() {
		return nil
	}
	return s.protocolSimpleIO(func() error { return publish(s.Op.DetachNotices()) })
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
		if complete, ok := s.autoExplainNoticeContexts[i]; ok && complete == *diagnostic {
			continue
		}
		if diagnostic.Context != "" {
			diagnostic.Context += "\n"
		}
		diagnostic.Context += context
	}
}
