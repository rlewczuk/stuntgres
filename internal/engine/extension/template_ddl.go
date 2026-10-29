package extension

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func templateSuperuser(h DictionaryHost, auth ddl.Authority, message string) error {
	view := h.AccessView(auth)
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return dictionaryError("42501", message)
	}
	return nil
}

func templateLookup(h DictionaryHost, auth ddl.Authority, name parser.ObjectName) (catalog.Descriptor, error) {
	var parts [3]parser.Identifier
	copy(parts[:], name.Parts)
	for i := range name.Parts {
		parts[i].Pos = parser.Position{}
	}
	return dictionaryLookup(h, auth, parser.ObjectName{Parts: parts[:len(name.Parts)]}, ids.TSTemplateClassID)
}

// Callback lookup is a utility identity lookup, not an expression call: no
// coercions, defaults, variadic expansion, EXECUTE check or expression position.
func templateCallback(h DictionaryHost, auth ddl.Authority, o parser.DictionaryOption) (*catalog.Procedure, error) {
	if !o.Supplied {
		return nil, dictionaryError("42601", o.Name.Text+" requires a parameter")
	}
	if o.Numeric {
		return nil, dictionaryError("42601", "argument of "+o.Name.Text+" must be a name")
	}
	name := o.Source
	if name == nil {
		name = &parser.ObjectName{Parts: []parser.Identifier{{Text: o.Value}}}
	}
	semantic := parser.ObjectName{Parts: make([]parser.Identifier, len(name.Parts))}
	for i, part := range name.Parts {
		semantic.Parts[i].Text = part.Text
	}
	signature, args := catalog.UnaccentInitSignature, "internal"
	if o.Name.Text == "lexize" {
		signature, args = catalog.UnaccentLexizeSignature, "internal, internal, internal, internal"
	}
	p, err := h.LookupFunction(auth, semantic, signature)
	if err != nil {
		return nil, err
	}
	description := catalog.ObjectNameText(*name) + "(" + args + ")"
	if p == nil || p.RoutineKind == catalog.ProcedureRoutine {
		return nil, dictionaryError("42883", "function "+description+" does not exist")
	}
	if p.Result != 2281 {
		return nil, dictionaryError("42P17", "function "+description+" should return type internal")
	}
	return p, nil
}

func StageCreateTemplate(h DictionaryHost, auth ddl.Authority, s *parser.CreateTemplate) error {
	if err := templateSuperuser(h, auth, "must be superuser to create text search templates"); err != nil {
		return err
	}
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	d := &catalog.TSTemplate{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSTemplateClassID}, ObjectNamespace: ns.Address, ObjectName: s.Name.Parts[len(s.Name.Parts)-1].Text}
	for _, o := range s.Options {
		if o.Name.Text != "init" && o.Name.Text != "lexize" {
			return dictionaryError("42601", fmt.Sprintf(`text search template parameter "%s" not recognized`, o.Name.Text))
		}
		p, err := templateCallback(h, auth, o)
		if err != nil {
			return err
		}
		h.AddDomainBase(p)
		if o.Name.Text == "init" {
			d.Init = p.Address()
		} else {
			d.Lexize = p.Address()
		}
	}
	if d.Lexize == (ids.ObjectAddress{}) {
		return dictionaryError("42P17", "text search template lexize method is required")
	}
	if h.Stage().LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_ts_template_tmplname_index"`, Detail: fmt.Sprintf("Key (tmplname, tmplnamespace)=(%s, %d) already exists.", d.Name(), ns.Address.ObjectID), Schema: "pg_catalog", Table: "pg_ts_template", Constraint: "pg_ts_template_tmplname_index"}
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	d.ObjectAddress.ObjectID = oid
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Context().Err(); err != nil {
		return err
	}
	return ddl.CreateCommand(h.Stage(), "CREATE TEXT SEARCH TEMPLATE", d)
}

func StageAlterTemplate(h DictionaryHost, auth ddl.Authority, s *parser.AlterTemplate) error {
	raw, err := templateLookup(h, auth, s.Name)
	if err != nil {
		return err
	}
	d := raw.(*catalog.TSTemplate)
	next := *d
	description, err := h.DependencyObject(auth, d.Address())
	if err != nil {
		return err
	}
	switch s.Action {
	case "rename":
		if err = templateSuperuser(h, auth, "must be superuser to rename "+description); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "schema":
		dest, err := h.ExistingCreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = h.CheckCreateIn(auth, dest); err != nil {
			return err
		}
		if dest.Address == d.Namespace() {
			ddl.AdmitNoopCommand(h.Stage(), "ALTER TEXT SEARCH TEMPLATE", d.Address())
			return nil
		}
		if err = templateSuperuser(h, auth, "must be superuser to set schema of "+description); err != nil {
			return err
		}
		next.ObjectNamespace = dest.Address
	default:
		return fmt.Errorf("stuntgres: invalid template alteration")
	}
	if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) {
		return dictionaryError("42710", fmt.Sprintf(`text search template "%s" already exists in schema "%s"`, next.Name(), h.StageRead().Get(next.Namespace()).Name()))
	}
	h.AddDomainBase(d)
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Context().Err(); err != nil {
		return err
	}
	return ddl.ReplaceCommand(h.Stage(), "ALTER TEXT SEARCH TEMPLATE", &next)
}

func StageDropTemplate(h DictionaryHost, auth ddl.Authority, s *parser.DropTemplate) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		raw, err := templateLookup(h, auth, name)
		if err != nil {
			if e, ok := err.(*sqlstate.Error); s.IfExists && ok && (e.Code == "42704" || e.Code == "3F000") {
				message := fmt.Sprintf(`text search template "%s" does not exist, skipping`, catalog.ObjectNameText(name))
				if e.Code == "3F000" {
					message = fmt.Sprintf(`schema "%s" does not exist, skipping`, name.Parts[0].Text)
				}
				ddl.Notice(h, "00000", message)
				continue
			}
			return err
		}
		d := raw.(*catalog.TSTemplate)
		// RemoveObjects permits the containing schema's owner to drop any
		// object in that schema, even this ownerless catalog class.
		if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Namespace()), OwnerOnly: true}); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "42501" {
				return err
			}
			if err = templateSuperuser(h, auth, "must be superuser"); err != nil {
				return err
			}
		}
		if d.Pinned() {
			object, err := h.DependencyObject(auth, d.Address())
			if err != nil {
				return err
			}
			return dictionaryError("2BP01", "cannot drop "+object+" because it is required by the database system")
		}
		if d.Extension != (ids.ObjectAddress{}) {
			object, err := h.DependencyObject(auth, d.Address())
			if err != nil {
				return err
			}
			owning, err := h.DependencyObject(auth, d.Extension)
			if err != nil {
				return err
			}
			diagnostic := dictionaryError("2BP01", "cannot drop "+object+" because "+owning+" requires it").(*sqlstate.Error)
			diagnostic.Hint = "You can drop " + owning + " instead."
			return diagnostic
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	_ = h.RecordDependencyPath(auth)
	return h.Context().Err()
}
