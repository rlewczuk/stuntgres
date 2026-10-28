package view

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

// RuleCTECommands maps a rule action's CTE definitions to their commands.
type RuleCTECommands map[*plan.CTEDefinition]*RuleCTECommand

var (
	_ analyze.RuleCTEsSlot  = RuleCTECommands(nil)
	_ analyze.ViewWriteSlot = (*Write)(nil)
)

func (RuleCTECommands) RuleCTEsSlot() {}
func (*Write) ViewWriteSlot()         {}

// Base is the physical or logical endpoint the view write lands on.
func (w *Write) Base() *catalog.Relation { return w.BaseRelation }
func (w *Write) LogicalTrigger() bool    { return w.Instead }

// RuleCTEsOf returns the rule CTE commands in s, or nil.
func RuleCTEsOf(s analyze.RuleCTEsSlot) RuleCTECommands {
	m, _ := s.(RuleCTECommands)
	return m
}

// RuleCTEsSlotOf stores m; a nil map stays a nil slot.
func RuleCTEsSlotOf(m RuleCTECommands) analyze.RuleCTEsSlot {
	if m == nil {
		return nil
	}
	return m
}

// ViewWriteOf returns the view-write lowering in s, or nil.
func ViewWriteOf(s analyze.ViewWriteSlot) *Write {
	w, _ := s.(*Write)
	return w
}

// ViewWriteSlotOf stores w; a nil lowering stays a nil slot.
func ViewWriteSlotOf(w *Write) analyze.ViewWriteSlot {
	if w == nil {
		return nil
	}
	return w
}
