package routine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func StageEventTrigger(h ddl.Host, auth ddl.Authority, statement parser.Statement) error {
	v := h.AccessView(auth)
	if s, ok := statement.(*parser.CreateEventTrigger); ok {
		actor, err := v.Roles.Descriptor(v.State.CurrentUser)
		if err != nil {
			return err
		}
		if !actor.Superuser {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to create event trigger "%s"`, s.Name.Text), Hint: "Must be superuser to create an event trigger."}
		}
		switch s.Event.Text {
		case "ddl_command_start", "ddl_command_end", "sql_drop", "table_rewrite":
		case "login":
			return sqlError("0A000", "login event triggers require an authenticated server login lifecycle")
		default:
			return sqlError("42601", fmt.Sprintf(`unrecognized event name "%s"`, s.Event.Text))
		}
		var tags []string
		for _, filter := range s.Filters {
			if filter.Name.Text != "tag" {
				return sqlError("42601", fmt.Sprintf(`unrecognized filter variable "%s"`, filter.Name.Text))
			}
			if tags != nil {
				return sqlError("42601", `filter variable "tag" specified more than once`)
			}
			tags = make([]string, len(filter.Values))
			for i, value := range filter.Values {
				tag := strings.Map(func(c rune) rune {
					if c >= 'a' && c <= 'z' {
						return c - ('a' - 'A')
					}
					return c
				}, value)
				recognized, eligible, rewrite := catalog.EventCommandTag(tag)
				if s.Event.Text == "table_rewrite" && !rewrite {
					return sqlError("0A000", "event triggers are not supported for "+value)
				}
				if !recognized {
					return sqlError("42601", fmt.Sprintf(`filter value "%s" not recognized for filter variable "tag"`, value))
				}
				if !eligible {
					return sqlError("0A000", "event triggers are not supported for "+value)
				}
				tags[i] = tag
			}
		}
		key := ids.NameKey{ClassID: ids.EventTriggerClassID, Name: s.Name.Text}
		if h.StageRead().NameAddress(key) != (ids.ObjectAddress{}) {
			return sqlError("42710", fmt.Sprintf(`event trigger "%s" already exists`, s.Name.Text))
		}
		function, err := h.LookupFunction(auth, s.Function, "")
		if err != nil {
			return err
		}
		if function == nil {
			return sqlError("42883", "function "+catalog.ObjectNameText(s.Function)+"() does not exist")
		}
		if function.RoutineKind == catalog.ProcedureRoutine {
			return sqlError("42809", catalog.ObjectNameText(s.Function)+"() is a procedure")
		}
		if function.SQL == nil || function.SQL.Invocation != proc.EventTriggerInvocation {
			return sqlError("42P17", "function "+catalog.ObjectNameText(s.Function)+" must return type event_trigger")
		}
		oid, err := h.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := h.Revision()
		if err != nil {
			return err
		}
		d := &catalog.EventTrigger{ObjectAddress: ids.ObjectAddress{ClassID: ids.EventTriggerClassID, ObjectID: oid}, ObjectRevision: revision, ObjectName: s.Name.Text, Owner: v.State.CurrentUser, Function: v.Ref(function.Address()), Event: s.Event.Text, Enabled: true, Tags: tags}
		return h.Stage().Create(d)
	}
	name, absent := "", false
	switch s := statement.(type) {
	case *parser.AlterEventTrigger:
		name = s.Name.Text
	case *parser.DropEventTrigger:
		name, absent = s.Name.Text, s.IfExists
	default:
		return fmt.Errorf("stuntgres: invalid event trigger DDL")
	}
	d, _ := h.StageRead().Get(h.StageRead().NameAddress(ids.NameKey{ClassID: ids.EventTriggerClassID, Name: name})).(*catalog.EventTrigger)
	if d == nil {
		if !absent {
			return sqlError("42704", fmt.Sprintf(`event trigger "%s" does not exist`, name))
		}
		h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`event trigger "%s" does not exist, skipping`, name)})
		return nil
	}
	if err := ddl.Check(h, auth, access.Requirement{Object: v.Ref(d.Address()), OwnerOnly: true}); err != nil {
		return err
	}
	if s, ok := statement.(*parser.AlterEventTrigger); ok {
		next := *d
		next.Enabled = s.Enabled
		var err error
		next.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		return h.Stage().Replace(&next)
	}
	h.DropTarget(d.Address())
	return h.RecordDependencyPath(auth)
}
