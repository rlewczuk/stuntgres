package engine

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Replay uses the ordinary binder/publication path, then admits the same
// validated captured-reference and selected regclass definitions Dump defers.
func (e *Engine) validateRestoredDefault(op *session.Operation, s *parser.AlterTableDefault) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.resolveReference(auth, s.Name)
	if err != nil {
		return err
	}
	d := auth.Catalog.MustRelation(ref.Address)
	if InheritedDefaultReplay(s, d) {
		return nil
	}
	slot, ok := d.Index[s.Column.Text]
	if !ok || !DefaultReplayDeferrable(d.ColumnMeta.Defaults[d.Attributes[slot]]) {
		return sqlError("0A000", "restore accepts only deferred captured-reference or regclass defaults")
	}
	return nil
}
