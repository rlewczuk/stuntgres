package core

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

// EventCommand is lexical command scratch. The journal and row effects belong
// to StatementScope until Finish; nested SQL utilities borrow that same owner.
// Only actual non-internal deletion consumption sets Dropped.
type EventCommand struct {
	Tag          string
	Dropped      bool
	Records      []catalog.DroppedObject
	positions    map[ids.ObjectAddress]int
	Phase        string
	captureError error
}

// BeginEventCommand enters a lexical utility boundary, retaining its journal
// until Finish. Pair a successful entry with FinishEventCommand.
func (s *StatementScope) BeginEventCommand(tag string) (*EventCommand, error) {
	previous := s.EventCommand
	if previous == nil {
		e := s.Engine()
		e.Mu.Lock()
		matched := false
		for _, trigger := range e.PrivateCatalogLocked(s.PendingCatalog()).EventTriggers() {
			if trigger.Matches(trigger.Event, tag) {
				matched = true
				break
			}
		}
		e.Mu.Unlock()
		if !matched {
			// The descriptor index proves no phase can invoke a callback.
			// Keep ordinary utility completion and publication ownership intact.
			return nil, nil
		}
	}
	if s.Catalog == nil {
		s.Catalog = s.Owner.Catalog.Clone()
	}
	s.EventCommand = &EventCommand{Tag: tag}
	s.Completed = false
	if err := s.fireEventTrigger("ddl_command_start"); err != nil {
		s.EventCommand = previous
		return nil, err
	}
	return previous, nil
}

// FinishEventCommand completes eligible phases and restores the exact enclosing
// command. A primary failure suppresses later phases without publishing effects.
func (s *StatementScope) FinishEventCommand(previous *EventCommand, primary error) error {
	if s.EventCommand == nil {
		return primary
	}
	defer func() { s.EventCommand = previous; s.Completed = false }()
	if primary != nil {
		return primary
	}
	if s.EventCommand.captureError != nil {
		return s.EventCommand.captureError
	}
	if s.EventCommand.Dropped {
		if err := s.fireEventTrigger("sql_drop"); err != nil {
			return err
		}
	}
	return s.fireEventTrigger("ddl_command_end")
}

// RecordDroppedObjects admits successful deletion effects. Identity slices are
// immutable; repeated paths merge only the two independent provenance flags.
func (s *StatementScope) RecordDroppedObjects(records []catalog.DroppedObject) {
	command := s.EventCommand
	if command == nil {
		return
	}
	for _, record := range records {
		if !record.Found {
			continue
		}
		switch record.Address.ClassID {
		case ids.DatabaseClassID, ids.RoleClassID, ids.TablespaceClassID, ids.ParameterClassID, ids.EventTriggerClassID:
			continue
		}
		if command.positions == nil {
			command.positions = make(map[ids.ObjectAddress]int)
		}
		if slot, exists := command.positions[record.Address]; exists {
			command.Records[slot].Original = command.Records[slot].Original || record.Original
			command.Records[slot].Normal = command.Records[slot].Normal || record.Normal
			continue
		}
		command.positions[record.Address] = len(command.Records)
		command.Records = append(command.Records, record)
		command.Dropped = true
	}
}

// fireEventTrigger captures immutable function identities under Engine.mu and
// invokes outside all lifecycle/publication locks. Each phase recaptures state;
// prepared handles never retain a trigger list or an old function definition.
func (s *StatementScope) fireEventTrigger(event string) error {
	if s.EventCommand == nil {
		return nil
	}
	if err := s.Active(); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	var list []*catalog.EventTrigger
	for _, d := range auth.Catalog.EventTriggers() {
		if d.Matches(event, s.EventCommand.Tag) {
			list = append(list, d)
		}
	}
	e.Mu.Unlock()
	if len(list) == 0 {
		return nil
	}
	command := s.EventCommand
	previousPhase := command.Phase
	command.Phase = event
	defer func() { command.Phase = previousPhase }()
	slices.SortFunc(list, func(a, b *catalog.EventTrigger) int { return strings.Compare(a.Name(), b.Name()) })
	for _, trigger := range list {
		if err := s.Op.Context().Err(); err != nil {
			return err
		}
		e.Mu.Lock()
		current, failure := s.RoutineAuthorizationLocked()
		var d *catalog.Procedure
		if failure == nil {
			if current.Ref(trigger.Function.Address) != trigger.Function {
				failure = ids.ErrCatalogReplaced
			} else {
				d, _ = current.Catalog.Get(trigger.Function.Address).(*catalog.Procedure)
				if d == nil || d.SQL == nil || d.SQL.Invocation != proc.EventTriggerInvocation {
					failure = fmt.Errorf("stuntgres: invalid installed event function")
				} else {
					_, failure = exec.ValidateRoutineDescriptor(current.Catalog, current.Roles, d)
				}
			}
		}
		e.Mu.Unlock()
		if failure != nil {
			return failure
		}
		frame, failure := proc.NewPLEventTriggerFrame(d.SQL, event, s.EventCommand.Tag)
		if failure != nil {
			return failure
		}
		if failure = s.enterSQLRoutine(); failure != nil {
			frame.Close()
			return failure
		}
		failure = s.invokePLEventTrigger(d, frame, current.Catalog)
		s.leaveSQLRoutine()
		frame.Close()
		if failure != nil {
			return failure
		}
	}
	return s.Op.Context().Err()
}

func (s *StatementScope) invokePLEventTrigger(d *catalog.Procedure, frame *proc.PLFrame, read catalog.Read) (err error) {
	previousAtomic := s.plAtomic
	s.plAtomic = true
	defer func() { s.plAtomic = previousAtomic }()
	leaveEnvironment, err := s.enterRoutineEnvironment(d)
	if err != nil {
		return err
	}
	defer func() { err = leaveEnvironment(err) }()
	leavePolicy := s.activateRoutineCommandPolicy(d, false)
	defer leavePolicy()
	identity, err := s.plDiagnosticIdentityFor(d, read)
	if err != nil {
		return err
	}
	previous := s.sqlRoutine.plDiagnostic
	s.sqlRoutine.plDiagnostic = identity
	defer func() { s.sqlRoutine.plDiagnostic = previous }()
	return s.newPLRun(d, frame, read).EventTrigger()
}
