package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// cursorXMLInput follows the cursor's existing detachable execution authority.
// It never retains the operation that declared or bound the producer.
type cursorXMLInput struct{ authority *exec.Authority }

func (r cursorXMLInput) XMLInputDiagnostic(d sqlstate.Error) sqlstate.Error { return d }
func (r cursorXMLInput) XMLInputWarning(d sqlstate.Error) {
	if r.authority == nil || r.authority.Host == nil {
		return
	}
	if s := ScopeOf(r.authority.Host); s != nil {
		s.Op.EmitNotice(session.LevelWarning, d)
	}
}
