package routine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Admission resolves actual identity once; spelling is never execution authority.
func admitFunctionLanguage(h ddl.Host, auth ddl.Authority, s *parser.CreateFunction) (*catalog.Language, catalog.LanguageExecution, error) {
	view := h.AccessView(auth)
	d, err := AdmitLanguage(view, auth, s.Language.Text)
	if err != nil {
		return nil, catalog.ExternalLanguage, err
	}
	kind, err := catalog.ResolveLanguageExecution(view.Catalog, d)
	if err != nil {
		return nil, kind, err
	}
	if err = checkRoutineBodyForm(s, kind, d); err != nil {
		return nil, kind, err
	}
	if kind == catalog.ExternalLanguage {
		return nil, kind, sqlError("0A000", "LANGUAGE "+d.Name()+" functions are unsupported in embedded mode")
	}
	return d, kind, nil
}

func AdmitLanguage(view access.View, env access.Environment, name string) (*catalog.Language, error) {
	address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: name})
	if address == (ids.ObjectAddress{}) {
		d := sqlError("42704", fmt.Sprintf("language %q does not exist", name)).(*sqlstate.Error)
		if name == "plpgsql" {
			d.Hint = "Use CREATE EXTENSION to load the language into the database."
		}
		return nil, d
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

func checkRoutineBodyForm(s *parser.CreateFunction, kind catalog.LanguageExecution, language *catalog.Language) error {
	if s.Standard != nil && kind != catalog.SQLLanguage {
		return sqlError("42P13", "inline SQL function body only valid for language SQL")
	}
	if s.SymbolSpecified && (kind != catalog.ExternalLanguage || language.Address().ObjectID != 13) {
		return sqlError("42P13", "only one AS item needed for language \""+language.Name()+"\"")
	}
	return nil
}

func StageCreateLanguage(h DDLHost, auth ddl.Authority, s *parser.CreateLanguage) error {
	view := h.AccessView(auth)
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "must be superuser to create custom procedural language")
	}
	d := &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID}, ObjectName: s.Name.Text, Owner: view.State.CurrentUser, Procedural: true, Trusted: s.Trusted}
	// All SQL lookup/type checks precede duplicate detection and the native ABI
	// boundary. Matching SQL signatures do not manufacture handler capabilities.
	var handlers [3]*catalog.Procedure
	for i, raw := range []parser.ObjectName{s.Handler, s.Inline, s.Validator} {
		if len(raw.Parts) == 0 {
			continue
		}
		kind := catalog.PLHandler(i + 1)
		_, signature, _ := kind.Declaration()
		namespaces, err := h.SearchNamespaces(auth, raw, ids.ProcedureClassID)
		if err != nil {
			return err
		}
		p, count := selectUtilityRoutine(h.AccessView(auth).Catalog, namespaces, raw.Parts[len(raw.Parts)-1].Text, "function", &signature)
		if count == 0 {
			args := ""
			if i == 1 {
				args = "internal"
			}
			if i == 2 {
				args = "oid"
			}
			return sqlError("42883", "function "+catalog.ObjectNameText(raw)+"("+args+") does not exist")
		}
		if i == 0 && p.Result != 2280 {
			return sqlError("42809", "function "+catalog.ObjectNameText(raw)+" must return type language_handler")
		}
		handlers[i] = p
	}
	address := h.Stage().LookupName(catalog.DescriptorNameKey(d))
	if address != (ids.ObjectAddress{}) {
		if !s.Replace {
			return sqlError("42710", fmt.Sprintf("language %q already exists", d.Name()))
		}
		old, ok := h.AccessView(auth).Catalog.Get(address).(*catalog.Language)
		if !ok {
			return fmt.Errorf("stuntgres: missing incumbent language authority")
		}
		d.ObjectAddress, d.Owner, d.ACL = old.Address(), old.Owner, old.ACL
		d.Extension, d.Origin = old.Extension, old.Origin
	}
	for i, p := range handlers {
		if p == nil {
			continue
		}
		kind := catalog.PLHandler(i + 1)
		if p.PLHandler != kind || catalog.ValidatePLHandler(h.AccessView(auth).Catalog, p) != nil {
			role := []string{"call handler", "inline handler", "validator"}[i]
			return sqlError("0A000", "language "+role+" has no implemented native capability")
		}
	}
	d.Call = handlers[0].Address()
	if handlers[1] != nil {
		d.Inline = handlers[1].Address()
	}
	if handlers[2] != nil {
		d.Validator = handlers[2].Address()
	}
	if address == (ids.ObjectAddress{}) {
		oid, err := h.AllocateOID()
		if err != nil {
			return err
		}
		d.ObjectAddress.ObjectID = oid
	}
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if address == (ids.ObjectAddress{}) {
		err = h.Stage().Create(d)
	} else {
		err = h.Stage().Replace(d)
	}
	if err != nil {
		return err
	}
	return h.RecordDependencyPath(auth)
}

func languageOwner(h DDLHost, auth ddl.Authority, d *catalog.Language) error {
	if err := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
			return sqlError("42501", "must be owner of language "+d.Name())
		}
		return err
	}
	return nil
}

func languageTarget(h DDLHost, auth ddl.Authority, name string) (*catalog.Language, error) {
	v := h.AccessView(auth).Catalog
	d, _ := v.Get(v.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: name})).(*catalog.Language)
	if d == nil {
		return nil, sqlError("42704", fmt.Sprintf("language %q does not exist", name))
	}
	if err := (catalog.LanguageProvider{}).Validate(v, d); err != nil {
		return nil, err
	}
	return d, nil
}

func StageAlterLanguage(h DDLHost, auth ddl.Authority, s *parser.AlterLanguage) error {
	d, err := languageTarget(h, auth, s.Name.Text)
	if err != nil {
		return err
	}
	next := *d
	view := h.AccessView(auth)
	if s.Action == "owner" {
		next.Owner, err = access.SchemaOwner(view, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
	}
	if err = languageOwner(h, auth, d); err != nil {
		return err
	}
	if s.Action == "owner" {
		if next.Owner == d.Owner {
			return nil
		}
		if err = access.OwnershipEligibility(view, auth, next.Owner, ids.ObjectAddress{}); err != nil {
			return err
		}
		next.ACL = authorization.SchemaACLOwner(d.ACL, d.Owner, next.Owner)
	} else {
		next.ObjectName = s.Target.Text
		if h.Stage().LookupName(catalog.DescriptorNameKey(&next)) != (ids.ObjectAddress{}) {
			return sqlError("42710", fmt.Sprintf("language %q already exists", next.Name()))
		}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Stage().Replace(&next); err != nil {
		return err
	}
	return h.RecordDependencyPath(auth)
}

func StageDropLanguage(h DDLHost, auth ddl.Authority, s *parser.DropLanguage) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		d, err := languageTarget(h, auth, name.Text)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" && s.IfExists {
				h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: diagnostic.Message + ", skipping"})
				continue
			}
			return err
		}
		if err = languageOwner(h, auth, d); err != nil {
			return err
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	return h.RecordDependencyPath(auth)
}
