package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// ValidateRestoredDefault admits the replayed DEFAULT s on ref only when it
// is inherited replay, or the column's bound default is one Dump defers: a
// validated captured reference or a selected regclass.
func ValidateRestoredDefault(view catalog.Read, ref ids.ObjectRef, s *parser.AlterTableDefault) error {
	d := view.MustRelation(ref.Address)
	if table.InheritedDefaultReplay(s, d) {
		return nil
	}
	slot, ok := d.Index[s.Column.Text]
	if !ok || !table.DefaultReplayDeferrable(d.ColumnMeta.Defaults[d.Attributes[slot]]) {
		return sqlError("0A000", "restore accepts only deferred captured-reference or regclass defaults")
	}
	return nil
}
