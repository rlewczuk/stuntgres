package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

// ruleCTECommands maps a rule action's CTE definitions to their commands.
type ruleCTECommands map[*plan.CTEDefinition]*ruleCTECommand

var (
	_ analyze.RuleCTEsSlot  = ruleCTECommands(nil)
	_ analyze.ViewWriteSlot = (*viewWrite)(nil)
)

func (ruleCTECommands) RuleCTEsSlot() {}
func (*viewWrite) ViewWriteSlot()     {}

// Base is the base relation the view write lands on.
func (w *viewWrite) Base() *catalog.Relation { return w.BaseRelation }

// ruleCTEsOf returns the rule CTE commands in s, or nil.
func RuleCTEsOf(s analyze.RuleCTEsSlot) ruleCTECommands {
	m, _ := s.(ruleCTECommands)
	return m
}

// ruleCTEsSlotOf stores m; a nil map stays a nil slot.
func RuleCTEsSlotOf(m ruleCTECommands) analyze.RuleCTEsSlot {
	if m == nil {
		return nil
	}
	return m
}

// viewWriteOf returns the view-write lowering in s, or nil.
func ViewWriteOf(s analyze.ViewWriteSlot) *viewWrite {
	w, _ := s.(*viewWrite)
	return w
}

// viewWriteSlotOf stores w; a nil lowering stays a nil slot.
func ViewWriteSlotOf(w *viewWrite) analyze.ViewWriteSlot {
	if w == nil {
		return nil
	}
	return w
}
