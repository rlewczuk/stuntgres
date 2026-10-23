package routine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Language admission uses the same real descriptor and ACL as pg_language.
func admitFunctionLanguage(h ddl.Host, auth ddl.Authority, s *parser.CreateFunction) error {
	if _, err := AdmitLanguage(h.AccessView(auth), auth, s.Language.Text); err != nil {
		return err
	}
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
		return checkRoutineBodyForm(s)
	default:
		return sqlError("42704", "language \""+language+"\" does not exist")
	}
}

func AdmitLanguage(view access.View, env access.Environment, name string) (*catalog.Language, error) {
	address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: name})
	if address == (ids.ObjectAddress{}) {
		return nil, sqlError("42704", "language \""+name+"\" does not exist")
	}
	d, err := catalog.LookupLanguage(view.Catalog, name)
	if err != nil {
		return nil, err
	}
	if !d.Trusted {
		actor, err := view.Roles.Descriptor(view.State.CurrentUser)
		if err != nil {
			return nil, err
		}
		if !actor.Superuser {
			return nil, sqlError("42501", "permission denied for language "+name)
		}
		return d, nil
	}
	if err := view.Check(env, access.Requirement{Object: view.Ref(address), Privileges: authorization.UsagePrivilege}); err != nil {
		return nil, err
	}
	return d, nil
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
