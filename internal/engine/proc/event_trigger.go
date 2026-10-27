package proc

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var eventTriggerNames = [...]string{"tg_event", "tg_tag"}

func EventTriggerVariables(resolve PLTypeResolver) ([]PLVariable, error) {
	pg := parser.Identifier{Text: "pg_catalog"}
	u, record, err := resolve(parser.PLType{Type: parser.TypeName{Name: "text", Schema: &pg}})
	if err != nil {
		return nil, err
	}
	if record || u.Type == nil || u.Type.ID() != sqltype.Text {
		return nil, fmt.Errorf("stuntgres: invalid event trigger TEXT authority")
	}
	return []PLVariable{{Name: eventTriggerNames[0], Usage: u}, {Name: eventTriggerNames[1], Usage: u}}, nil
}

func NewPLEventTriggerFrame(d *Definition, event, tag string) (*PLFrame, error) {
	if d == nil || d.Invocation != EventTriggerInvocation || d.PL == nil || d.PL.Invocation != EventTriggerInvocation {
		return nil, fmt.Errorf("stuntgres: missing checked event trigger program")
	}
	f := NewPLFrame(d.PL, d, &Frame{}, sqltype.CollationRef{})
	for i, text := range [2]string{event, tag} {
		f.Values[i] = sqltype.String(d.PL.Variables[i].Usage.Type, text)
	}
	return f, nil
}

// EventTrigger executes only an installed event function. It has a void-like
// exit, never a scalar pseudotype value or a DML composite-row contract.
func (r *PLRun) EventTrigger() error {
	r.Host.EnterRoutine()
	defer r.Host.LeaveRoutine()
	_, flow, err := r.Block(r.Definition.PL.Root)
	if err != nil {
		return err
	}
	if flow.Kind != PLNormal && flow.Kind != PLReturn && !(flow.Kind == PLExit && flow.Target == r.Definition.PL.Root.Control) {
		return fmt.Errorf("stuntgres: unconsumed event trigger control transfer")
	}
	return nil
}
