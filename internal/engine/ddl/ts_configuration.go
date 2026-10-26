package ddl

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type ConfigurationHost interface {
	Host
	// SearchConfigurationNamespaces resolves every existing configuration DDL
	// operand with current caller authority and semantic utility diagnostics.
	SearchConfigurationNamespaces(Authority, parser.ObjectName, ids.OID) ([]ids.ObjectAddress, error)
	AddDomainBase(catalog.Descriptor)
}

func configurationError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func configurationLookup(h ConfigurationHost, auth Authority, name parser.ObjectName, class ids.OID) (catalog.Descriptor, error) {
	path, err := h.SearchConfigurationNamespaces(auth, name, class)
	if err != nil {
		return nil, err
	}
	view := h.AccessView(auth).Catalog
	for _, ns := range path {
		if d := view.Get(view.NameAddress(ids.NameKey{ClassID: class, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text})); d != nil {
			return d, nil
		}
	}
	what := "configuration"
	if class == ids.TSDictionaryClassID {
		what = "dictionary"
	}
	return nil, configurationError("42704", fmt.Sprintf(`text search %s "%s" does not exist`, what, catalog.ObjectNameText(name)))
}
func configurationOwner(h ConfigurationHost, auth Authority, d *catalog.TSConfiguration, name parser.ObjectName) error {
	if err := Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		if e, ok := err.(*sqlstate.Error); ok && e.Code == "42501" {
			return configurationError("42501", "must be owner of text search configuration "+catalog.ObjectNameText(name))
		}
		return err
	}
	return nil
}
func StageCreateConfiguration(h ConfigurationHost, auth Authority, s *parser.CreateConfiguration) error {
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	d := &catalog.TSConfiguration{ObjectNamespace: ns.Address, ObjectName: s.Name.Parts[len(s.Name.Parts)-1].Text, Owner: h.AccessView(auth).State.CurrentUser}
	var source *catalog.TSConfiguration
	var parsed bool
	for _, o := range s.Options {
		switch o.Name.Text {
		case "parser":
			if !o.Supplied {
				return configurationError("42601", "parser requires a parameter")
			}
			if o.Numeric {
				return configurationError("42601", "argument of parser must be a name")
			}
			path, err := h.SearchConfigurationNamespaces(auth, o.Value, 3601)
			if err != nil {
				return err
			}
			p := catalog.DefaultTextParser()
			found := false
			for _, ns := range path {
				if ns.ObjectID == p.Namespace && o.Value.Parts[len(o.Value.Parts)-1].Text == p.Name {
					found = true
					break
				}
			}
			if !found {
				return configurationError("42704", fmt.Sprintf(`text search parser "%s" does not exist`, catalog.ObjectNameText(o.Value)))
			}
			d.Parser = ids.ObjectAddress{ClassID: 3601, ObjectID: p.OID}
			parsed = true
		case "copy":
			if !o.Supplied {
				return configurationError("42601", "copy requires a parameter")
			}
			if o.Numeric {
				return configurationError("42601", "argument of copy must be a name")
			}
			raw, err := configurationLookup(h, auth, o.Value, ids.TSConfigurationClassID)
			if err != nil {
				return err
			}
			source = raw.(*catalog.TSConfiguration)
			h.AddDomainBase(source)
		default:
			return configurationError("42601", fmt.Sprintf(`text search configuration parameter "%s" not recognized`, o.Name.Text))
		}
	}
	if source != nil && parsed {
		return configurationError("42601", "cannot specify both PARSER and COPY options")
	}
	if source != nil {
		d.Parser = source.Parser
		d.Mappings = catalog.CloneTSMappings(source.Mappings)
	} else if !parsed {
		return configurationError("42P17", "text search parser is required")
	}
	d.ObjectAddress = ids.ObjectAddress{ClassID: ids.TSConfigurationClassID}
	if h.Stage().LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_ts_config_cfgname_index"`, Detail: fmt.Sprintf("Key (cfgname, cfgnamespace)=(%s, %d) already exists.", d.Name(), ns.Address.ObjectID), Schema: "pg_catalog", Table: "pg_ts_config", Constraint: "pg_ts_config_cfgname_index"}
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
	return h.Stage().Create(d)
}
func StageAlterConfiguration(h ConfigurationHost, auth Authority, s *parser.AlterConfiguration) error {
	raw, err := configurationLookup(h, auth, s.Name, ids.TSConfigurationClassID)
	if err != nil {
		return err
	}
	d := raw.(*catalog.TSConfiguration)
	if s.Action == "schema" {
		dest, err := h.ExistingCreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = h.CheckCreateIn(auth, dest); err != nil {
			return err
		}
	}
	name := s.Name
	if s.Action != "mapping" {
		name = parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}
	}
	if err = configurationOwner(h, auth, d, name); err != nil {
		return err
	}
	h.AddDomainBase(d)
	next := *d
	view := h.AccessView(auth)
	for _, a := range d.Dependencies() {
		if a.ClassID == ids.TSDictionaryClassID {
			if dependency := view.Catalog.Get(a); dependency != nil {
				h.AddDomainBase(dependency)
			}
		}
	}
	switch s.Action {
	case "mapping":
		if err = configurationMappings(h, auth, &next, s); err != nil {
			return err
		}
	case "rename":
		if err = h.CheckCreateIn(auth, ObjectRef(h, auth, d.Namespace())); err != nil {
			return err
		}
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
		view = h.AccessView(auth)
	case "owner":
		var owner authorization.PrincipalRef
		owner, err = access.SchemaOwner(view, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if err = access.OwnershipEligibility(view, auth, owner, d.Namespace()); err != nil {
			return err
		}
		next.Owner = owner
	default:
		return fmt.Errorf("stuntgres: invalid configuration alteration")
	}
	if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) && address != d.Address() {
		return configurationError("42710", fmt.Sprintf(`text search configuration "%s" already exists in schema "%s"`, next.Name(), view.Catalog.Get(next.Namespace()).Name()))
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Context().Err(); err != nil {
		return err
	}
	return h.Stage().Replace(&next)
}
func configurationMappings(h ConfigurationHost, auth Authority, d *catalog.TSConfiguration, s *parser.AlterConfiguration) error {
	var tokens []int32
	for _, t := range s.Tokens {
		id, ok := catalog.TextParserTokenID(t.Text)
		if !ok {
			return configurationError("22023", fmt.Sprintf(`token type "%s" does not exist`, t.Text))
		}
		if !slices.Contains(tokens, id) {
			tokens = append(tokens, id)
		}
	}
	var old ids.ObjectAddress
	if s.MappingAction == parser.ConfigurationReplaceMapping {
		raw, err := configurationLookup(h, auth, s.OldDictionary, ids.TSDictionaryClassID)
		if err != nil {
			return err
		}
		h.AddDomainBase(raw)
		old = raw.Address()
	}
	dictionaries := make([]ids.ObjectAddress, len(s.Dictionaries))
	for i, name := range s.Dictionaries {
		raw, err := configurationLookup(h, auth, name, ids.TSDictionaryClassID)
		if err != nil {
			return err
		}
		h.AddDomainBase(raw)
		dictionaries[i] = raw.Address()
	}
	d.Mappings = catalog.CloneTSMappings(d.Mappings)
	if s.MappingAction == parser.ConfigurationReplaceMapping {
		for i := range d.Mappings {
			m := &d.Mappings[i]
			if len(tokens) > 0 && !slices.Contains(tokens, m.TokenID) {
				continue
			}
			for j, a := range m.Dictionaries {
				if a == old {
					m.Dictionaries[j] = dictionaries[0]
				}
			}
		}
		return nil
	}
	descriptors := catalog.TextParserTokens()
	for _, token := range tokens {
		if err := h.Context().Err(); err != nil {
			return err
		}
		index, exists := slices.BinarySearchFunc(d.Mappings, token, func(m catalog.TSMapping, t int32) int {
			if m.TokenID < t {
				return -1
			}
			if m.TokenID > t {
				return 1
			}
			return 0
		})
		switch s.MappingAction {
		case parser.ConfigurationAddMapping:
			if exists {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_ts_config_map_index"`, Detail: fmt.Sprintf("Key (mapcfg, maptokentype, mapseqno)=(%d, %d, 1) already exists.", d.Address().ObjectID, token), Schema: "pg_catalog", Table: "pg_ts_config_map", Constraint: "pg_ts_config_map_index"}
			}
			fallthrough
		case parser.ConfigurationAlterMapping:
			m := catalog.TSMapping{TokenID: token, Dictionaries: slices.Clone(dictionaries)}
			if exists {
				d.Mappings[index] = m
			} else {
				d.Mappings = slices.Insert(d.Mappings, index, m)
			}
		case parser.ConfigurationDropMapping:
			if !exists {
				message := fmt.Sprintf(`mapping for token type "%s" does not exist`, descriptors[token-1].Alias)
				if !s.IfExists {
					return configurationError("42704", message)
				}
				Notice(h, "00000", message+", skipping")
			} else {
				d.Mappings = slices.Delete(d.Mappings, index, index+1)
			}
		}
	}
	return nil
}
func StageDropConfiguration(h ConfigurationHost, auth Authority, s *parser.DropConfiguration) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		raw, err := configurationLookup(h, auth, name, ids.TSConfigurationClassID)
		if err != nil {
			if e, ok := err.(*sqlstate.Error); s.IfExists && ok && (e.Code == "42704" || e.Code == "3F000") {
				Notice(h, "00000", fmt.Sprintf(`text search configuration "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
				continue
			}
			return err
		}
		d := raw.(*catalog.TSConfiguration)
		if err = configurationOwner(h, auth, d, name); err != nil {
			return err
		}
		if d.Seeded() {
			return configurationError("2BP01", "cannot drop text search configuration "+catalog.ObjectNameText(name)+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	_ = h.RecordDependencyPath(auth)
	return h.Context().Err()
}
