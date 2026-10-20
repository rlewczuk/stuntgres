package engine

import "stuntgres/internal/engine/plan"

// ruleCTECommands maps a rule action's CTE definitions to their commands.
type ruleCTECommands map[*plan.CTEDefinition]*ruleCTECommand

var (
	_ ruleCTEsSlot  = ruleCTECommands(nil)
	_ viewWriteSlot = (*viewWrite)(nil)
)

func (ruleCTECommands) RuleCTEsSlot() {}
func (*viewWrite) ViewWriteSlot()     {}

// ruleCTEsOf returns the rule CTE commands in s, or nil.
func ruleCTEsOf(s ruleCTEsSlot) ruleCTECommands {
	m, _ := s.(ruleCTECommands)
	return m
}

// ruleCTEsSlotOf stores m; a nil map stays a nil slot.
func ruleCTEsSlotOf(m ruleCTECommands) ruleCTEsSlot {
	if m == nil {
		return nil
	}
	return m
}

// viewWriteOf returns the view-write lowering in s, or nil.
func viewWriteOf(s viewWriteSlot) *viewWrite {
	w, _ := s.(*viewWrite)
	return w
}

// viewWriteSlotOf stores w; a nil lowering stays a nil slot.
func viewWriteSlotOf(w *viewWrite) viewWriteSlot {
	if w == nil {
		return nil
	}
	return w
}
