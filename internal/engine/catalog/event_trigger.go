package catalog

import (
	"fmt"
	"iter"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

// EventTrigger is database-local. Tags is immutable; nil means no filter.
type EventTrigger struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	Function       ids.ObjectRef
	Event          string
	FiringMode     TriggerMode
	Tags           []string
}

func (d *EventTrigger) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *EventTrigger) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*EventTrigger) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *EventTrigger) Name() string                     { return d.ObjectName }
func (*EventTrigger) Kind() ids.ObjectKind               { return ids.EventTriggerObject }
func (d *EventTrigger) Matches(event, tag string, replica bool) bool {
	if !d.FiringMode.Valid() || !d.FiringMode.Fires(replica) || d.Event != event {
		return false
	}
	if d.Tags == nil {
		return true
	}
	for _, candidate := range d.Tags {
		if candidate == tag {
			return true
		}
	}
	return false
}

type EventTriggerProvider struct{}

func (EventTriggerProvider) ClassID() ids.OID { return ids.EventTriggerClassID }
func (EventTriggerProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*EventTrigger)
	invalid := func() error { return fmt.Errorf("stuntgres: invalid event trigger descriptor authority") }
	if !ok || d == nil || !d.FiringMode.Valid() || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.EventTriggerClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || d.Name() == "" || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if v.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) || d.Function != v.Owner().Domain.Ref(v.Epoch(), d.Function.Address) || d.Function.Address.ClassID != ids.ProcedureClassID || d.Function.Address.SubID != 0 {
		return invalid()
	}
	f, ok := v.Get(d.Function.Address).(*Procedure)
	if !ok || f.RoutineKind != FunctionRoutine || f.Result != 3838 || f.SQL == nil || f.SQL.Kind != proc.PLpgSQLBody || f.SQL.Invocation != proc.EventTriggerInvocation || f.SQL.Result.Type == nil || f.SQL.Result.Type.ID() != 3838 || len(f.SQL.Formals) != 0 || len(f.SQL.Inputs) != 0 || f.SQL.PL == nil || f.SQL.PL.Invocation != proc.EventTriggerInvocation {
		return invalid()
	}
	switch d.Event {
	case "ddl_command_start", "ddl_command_end", "sql_drop", "table_rewrite":
	default:
		return invalid()
	}
	if d.Tags != nil && len(d.Tags) == 0 {
		return invalid()
	}
	for _, tag := range d.Tags {
		recognized, eligible, rewrite := EventCommandTag(tag)
		if !recognized || !eligible || d.Event == "table_rewrite" && !rewrite {
			return invalid()
		}
	}
	return nil
}

// EventTriggers iterates the descriptor-derived class index, not a separate
// name or execution registry. A retained read retains the same immutable root.
func (r Read) EventTriggers() iter.Seq2[ids.ObjectAddress, *EventTrigger] {
	if r.state == nil || r.state.eventTriggers.Len() == 0 {
		return func(func(ids.ObjectAddress, *EventTrigger) bool) {}
	}
	return r.state.eventTriggers.All()
}

func (s *State) updateEventTriggers(old, next Descriptor) {
	a, was := old.(*EventTrigger)
	b, is := next.(*EventTrigger)
	if !was && !is {
		return
	}
	if was && (!is || a.Address() != b.Address()) {
		s.eventTriggers = s.eventTriggers.Delete(a.Address())
	}
	if is {
		if current, found := s.eventTriggers.Get(b.Address()); !found || current != b {
			s.eventTriggers = s.eventTriggers.Set(b.Address(), b)
		}
	}
}
