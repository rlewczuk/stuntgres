package ddl

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type ParserHost interface {
	ConfigurationHost
	LookupParserRoutine(Authority, parser.ObjectName, catalog.ProcedureSignature) (catalog.RoutineIdentity, bool, error)
	// ParserSchemaNamespace resolves an existing ALTER destination, including
	// system namespaces. The caller still checks CREATE and superuser authority.
	ParserSchemaNamespace(Authority, parser.ObjectName) (ids.ObjectRef, error)
}

func parserSuperuser(h ConfigurationHost, auth Authority, action string, object catalog.Descriptor) error {
	view := h.AccessView(auth)
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		message := "must be superuser"
		if action != "" {
			message += " to " + action
		}
		if object != nil {
			description, err := h.DependencyObject(auth, object.Address())
			if err != nil {
				return err
			}
			message += " " + description
		}
		return configurationError("42501", message)
	}
	return nil
}

func parserSignatureText(name parser.ObjectName, signature catalog.ProcedureSignature) string {
	names := strings.Split(string(signature), ",")
	for i, name := range names {
		switch name {
		case "2281":
			names[i] = "internal"
		case "23":
			names[i] = "integer"
		case "3615":
			names[i] = "tsquery"
		}
	}
	return catalog.ObjectNameText(name) + "(" + strings.Join(names, ", ") + ")"
}

func StageCreateParser(h ParserHost, auth Authority, s *parser.CreateParser) error {
	if err := parserSuperuser(h, auth, "create text search parsers", nil); err != nil {
		return err
	}
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	d := &catalog.TSParser{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.TSParserClassID},
		ObjectNamespace: ns.Address, ObjectName: s.Name.Parts[len(s.Name.Parts)-1].Text,
	}
	for _, option := range s.Options {
		signature, result := catalog.ParserMethodSignature(option.Name.Text)
		if result == 0 {
			return configurationError("42601", fmt.Sprintf(`text search parser parameter "%s" not recognized`, option.Name.Text))
		}
		if !option.Supplied {
			return configurationError("42601", option.Name.Text+" requires a parameter")
		}
		if option.Numeric {
			return configurationError("42601", "argument of "+option.Name.Text+" must be a name")
		}
		procedure, ok, err := h.LookupParserRoutine(auth, option.Value, signature)
		if err != nil {
			return err
		}
		if !ok {
			return configurationError("42883", "function "+parserSignatureText(option.Value, signature)+" does not exist")
		}
		actual, ok := catalog.ParserRoutineResult(h.AccessView(auth).Catalog, procedure.ID)
		if !ok || actual != result {
			typeName := "internal"
			if result == 2278 {
				typeName = "void"
			}
			return configurationError("42P17", "function "+parserSignatureText(option.Value, signature)+" should return type "+typeName)
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: procedure.ID}
		if dependency := h.AccessView(auth).Catalog.Get(address); dependency != nil {
			h.AddDomainBase(dependency)
		}
		// Validate each occurrence before overwriting: invalid earlier duplicates
		// must not be hidden by a later valid callback.
		switch option.Name.Text {
		case "start":
			d.Start = procedure.ID
		case "gettoken":
			d.Token = procedure.ID
		case "end":
			d.End = procedure.ID
		case "headline":
			d.Headline = procedure.ID
		case "lextypes":
			d.LexType = procedure.ID
		}
	}
	for _, method := range [...]struct {
		name string
		oid  ids.OID
	}{{"start", d.Start}, {"gettoken", d.Token}, {"end", d.End}, {"lextypes", d.LexType}} {
		if method.oid == 0 {
			return configurationError("42P17", "text search parser "+method.name+" method is required")
		}
	}
	if h.Stage().LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
			Message: `duplicate key value violates unique constraint "pg_ts_parser_prsname_index"`,
			Detail:  fmt.Sprintf("Key (prsname, prsnamespace)=(%s, %d) already exists.", d.Name(), ns.Address.ObjectID),
			Schema:  "pg_catalog", Table: "pg_ts_parser", Constraint: "pg_ts_parser_prsname_index",
		}
	}
	d.ObjectAddress.ObjectID, err = h.AllocateOID()
	if err != nil {
		return err
	}
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Context().Err(); err != nil {
		return err
	}
	return CreateCommand(h.Stage(), "CREATE TEXT SEARCH PARSER", d)
}

func StageAlterParser(h ParserHost, auth Authority, s *parser.AlterParser) error {
	raw, err := configurationLookup(h, auth, s.Name, ids.TSParserClassID)
	if err != nil {
		return err
	}
	d := raw.(*catalog.TSParser)
	if s.Action == "schema" {
		dest, err := h.ParserSchemaNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = h.CheckCreateIn(auth, dest); err != nil {
			return err
		}
	}
	verb := "rename"
	if s.Action == "schema" {
		verb = "set schema of"
	}
	if err = parserSuperuser(h, auth, verb, d); err != nil {
		return err
	}
	h.AddDomainBase(d)
	next := *d
	switch s.Action {
	case "rename":
		next.ObjectName = s.Target.Text
	case "schema":
		dest, newAuth, err := h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		auth = newAuth
		if err = h.CheckCreateIn(auth, dest); err != nil {
			return err
		}
		next.ObjectNamespace = dest.Address
	default:
		return fmt.Errorf("stuntgres: invalid parser alteration")
	}
	if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) && address != d.Address() {
		return configurationError("42710", fmt.Sprintf(`text search parser "%s" already exists in schema "%s"`, next.Name(), h.AccessView(auth).Catalog.Get(next.Namespace()).Name()))
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Context().Err(); err != nil {
		return err
	}
	return ReplaceCommand(h.Stage(), "ALTER TEXT SEARCH PARSER", &next)
}

func StageDropParser(h ConfigurationHost, auth Authority, s *parser.DropParser) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		raw, err := configurationLookup(h, auth, name, ids.TSParserClassID)
		if err != nil {
			if e, ok := err.(*sqlstate.Error); s.IfExists && ok && (e.Code == "42704" || e.Code == "3F000") {
				Notice(h, "00000", fmt.Sprintf(`text search parser "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
				continue
			}
			return err
		}
		d := raw.(*catalog.TSParser)
		// DROP's containing-schema ownership bypass is distinct from CREATE
		// and ALTER: parsers themselves have neither ownership nor ACLs.
		if err = Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Namespace()), OwnerOnly: true}); err != nil {
			if err = parserSuperuser(h, auth, "", nil); err != nil {
				return err
			}
		}
		if d.Seeded() {
			return configurationError("2BP01", "cannot drop text search parser "+catalog.ObjectNameText(name)+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	_ = h.RecordDependencyPath(auth)
	return h.Context().Err()
}
