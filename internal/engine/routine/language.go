package routine

import (
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
)

// admitFunctionLanguage runs after namespace authority and before constructing
// any executable descriptor. This is syntax admission, not a language registry:
// pg_language's limited metadata inventory does not describe SQL executability.
func admitFunctionLanguage(h ddl.Host, auth ddl.Authority, s *parser.CreateFunction) error {
	switch language := s.Language.Text; language {
	case "sql":
		return nil
	case "c", "internal":
		view := h.AccessView(auth)
		actor, err := view.Roles.Descriptor(view.State.CurrentUser)
		if err != nil {
			return err
		}
		if !actor.Superuser {
			return sqlError("42501", "permission denied for language "+language)
		}
		if err := checkRoutineBodyForm(s); err != nil {
			return err
		}
		return sqlError("0A000", "LANGUAGE "+language+" functions are unsupported in embedded mode")
	case "plpgsql":
		if err := checkRoutineBodyForm(s); err != nil {
			return err
		}
		// Preserve the existing runtime boundary; installed PostgreSQL plpgsql
		// is not evidence of an embedded interpreter or native execution.
		return sqlError("0A000", "only LANGUAGE SQL functions are supported")
	default:
		return sqlError("42704", "language \""+language+"\" does not exist")
	}
}

func checkRoutineBodyForm(s *parser.CreateFunction) error {
	if s.Standard != nil && s.Language.Text != "sql" {
		return sqlError("42P13", "inline SQL function body only valid for language SQL")
	}
	if s.SymbolSpecified && s.Language.Text != "c" {
		return sqlError("42P13", "only one AS item needed for language \""+s.Language.Text+"\"")
	}
	return nil
}

// RejectCreateLanguage deliberately performs no handler lookup, catalog write,
// library access or interpreter provisioning. Authority precedes that boundary.
func RejectCreateLanguage(h ddl.Host, auth ddl.Authority) error {
	view := h.AccessView(auth)
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "must be superuser to create custom procedural language")
	}
	return sqlError("0A000", "custom procedural language handlers are unsupported in embedded mode")
}
