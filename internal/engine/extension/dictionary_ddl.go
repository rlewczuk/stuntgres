package extension

import (
	"fmt"
	"slices"
	"strconv"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type DictionaryHost interface {
	ddl.ConfigurationHost
}

func dictionaryLookup(h DictionaryHost, auth ddl.Authority, name parser.ObjectName, class ids.OID) (catalog.Descriptor, error) {
	path, err := h.SearchNamespaces(auth, name, class)
	if err != nil {
		return nil, err
	}
	view := h.AccessView(auth).Catalog
	for _, ns := range path {
		address := view.NameAddress(ids.NameKey{ClassID: class, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text})
		if d := view.Get(address); d != nil {
			return d, nil
		}
	}
	what := "dictionary"
	if class == ids.TSTemplateClassID {
		what = "template"
	}
	return nil, dictionaryError("42704", fmt.Sprintf(`text search %s "%s" does not exist`, what, catalog.ObjectNameText(name)))
}
func dictionaryOptions(options []parser.DictionaryOption) []catalog.DictionaryOption {
	out := make([]catalog.DictionaryOption, len(options))
	for i, o := range options {
		out[i] = dictionaryOption(o)
	}
	return out
}
func dictionaryOption(o parser.DictionaryOption) catalog.DictionaryOption {
	value := o.Value
	if o.Numeric {
		if number, err := strconv.ParseInt(value, 10, 32); err == nil {
			value = strconv.FormatInt(number, 10)
		}
	}
	return catalog.DictionaryOption{Name: o.Name.Text, Value: value, Supplied: o.Supplied, Numeric: o.Numeric}
}
func dictionaryOwner(h DictionaryHost, auth ddl.Authority, d *catalog.TSDictionary, name parser.ObjectName) error {
	if err := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		if e, ok := err.(*sqlstate.Error); ok && e.Code == "42501" {
			return dictionaryError("42501", "must be owner of text search dictionary "+catalog.ObjectNameText(name))
		}
		return err
	}
	return nil
}

// Options and DROP report the supplied qualified name. Generic identity ALTERs
// report the catalog-local name. SET SCHEMA checks its destination's CREATE
// privilege before checking source ownership.
func dictionaryAlterAuthority(h DictionaryHost, auth ddl.Authority, d *catalog.TSDictionary, s *parser.AlterDictionary) error {
	if s.Action == "schema" {
		destination, err := h.ExistingCreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = h.CheckCreateIn(auth, destination); err != nil {
			return err
		}
	}
	name := s.Name
	if s.Action != "options" {
		name = parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}
	}
	return dictionaryOwner(h, auth, d, name)
}
func StageCreateDictionary(h DictionaryHost, auth ddl.Authority, s *parser.CreateDictionary) error {
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	tmpl, options, err := dictionaryCreateInputs(h, auth, s)
	if err != nil {
		return err
	}
	runtime, err := initializeDictionary(h, auth, tmpl, options)
	if err != nil {
		return err
	}
	for _, option := range options {
		if !option.Supplied {
			return dictionaryError("42601", option.Name+" requires a parameter")
		}
	}
	view := h.AccessView(auth)
	d := &catalog.TSDictionary{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSDictionaryClassID}, ObjectNamespace: ns.Address, ObjectName: s.Name.Parts[len(s.Name.Parts)-1].Text, Template: tmpl.Address(), Owner: view.State.CurrentUser, Options: options, Runtime: runtime}
	if prepared, ok := h.(preparedDictionaryHost); ok && prepared.preparation.Thesaurus != nil {
		d.NormalizerNamespace = prepared.preparation.Thesaurus.Normalizer.Namespace()
	}
	if h.Stage().LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_ts_dict_dictname_index"`, Detail: fmt.Sprintf("Key (dictname, dictnamespace)=(%s, %d) already exists.", d.Name(), ns.Address.ObjectID), Schema: "pg_catalog", Table: "pg_ts_dict", Constraint: "pg_ts_dict_dictname_index"}
	}
	h.AddDomainBase(tmpl)
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	d.ObjectAddress = ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: oid}
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Context().Err(); err != nil {
		return err
	}
	return ddl.CreateCommand(h.Stage(), "CREATE TEXT SEARCH DICTIONARY", d)
}
func StageAlterDictionary(h DictionaryHost, auth ddl.Authority, s *parser.AlterDictionary) error {
	raw, err := dictionaryLookup(h, auth, s.Name, ids.TSDictionaryClassID)
	if err != nil {
		return err
	}
	d := raw.(*catalog.TSDictionary)
	if err = dictionaryAlterAuthority(h, auth, d, s); err != nil {
		return err
	}
	h.AddDomainBase(d)
	next := *d
	view := h.AccessView(auth)
	switch s.Action {
	case "options":
		next.Options = MergeDictionaryOptions(d.Options, dictionaryOptions(s.Options))
		tmpl, ok := view.Catalog.TSTemplate(d.Template)
		if !ok {
			return InvalidProviderDescriptor()
		}
		next.Runtime, err = initializeDictionary(h, auth, tmpl, next.Options)
		if err != nil {
			return err
		}
		if prepared, ok := h.(preparedDictionaryHost); ok && prepared.preparation.Thesaurus != nil {
			next.NormalizerNamespace = prepared.preparation.Thesaurus.Normalizer.Namespace()
		}
		h.AddDomainBase(tmpl)
	case "rename":
		next.ObjectName = s.Target.Text
	case "schema":
		var dest ids.ObjectRef
		dest, auth, err = h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
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
		return fmt.Errorf("stuntgres: invalid dictionary alteration")
	}
	if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) && address != d.Address() {
		return dictionaryError("42710", fmt.Sprintf(`text search dictionary "%s" already exists in schema "%s"`, next.Name(), view.Catalog.Get(next.Namespace()).Name()))
	}
	next.Options = slices.Clip(next.Options)
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Context().Err(); err != nil {
		return err
	}
	return ddl.ReplaceCommand(h.Stage(), "ALTER TEXT SEARCH DICTIONARY", &next)
}
func StageDropDictionary(h DictionaryHost, auth ddl.Authority, s *parser.DropDictionary) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		raw, err := dictionaryLookup(h, auth, name, ids.TSDictionaryClassID)
		if err != nil {
			if e, ok := err.(*sqlstate.Error); s.IfExists && ok && (e.Code == "42704" || e.Code == "3F000") {
				ddl.Notice(h, "00000", fmt.Sprintf(`text search dictionary "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
				continue
			}
			return err
		}
		d := raw.(*catalog.TSDictionary)
		if err = dictionaryOwner(h, auth, d, name); err != nil {
			return err
		}
		if d.Pinned() {
			return dictionaryError("2BP01", "cannot drop text search dictionary "+catalog.ObjectNameText(name)+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	_ = h.RecordDependencyPath(auth)
	return h.Context().Err()
}
