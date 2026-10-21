package typedef

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func collationVersionWarning(h StageHost, d *catalog.Collation, view catalog.Read, actual *string) error {
	if d.Version == nil {
		return nil
	}
	if actual == nil {
		return sqlError("XX000", fmt.Sprintf(`collation "%s" has no actual version, but a version was recorded`, d.Name()))
	}
	if *d.Version != *actual {
		ns := view.Get(d.Namespace()).Name()
		h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: fmt.Sprintf(`collation "%s" has version mismatch`, d.Name()), Detail: fmt.Sprintf("The collation in the database was created using version %s, but the operating system provides version %s.", *d.Version, *actual), Hint: "Rebuild all objects affected by this collation and run ALTER COLLATION " + catalog.DependencyIdentifier(ns) + "." + catalog.DependencyIdentifier(d.Name()) + " REFRESH VERSION, or build PostgreSQL with the right library version."})
	}
	return nil
}

func StageCreateCollationLocked(h StageHost, auth ddl.Authority, s *parser.CreateCollation) error {
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	trusted, err := h.TrustedRestoreDestination(auth, "collation")
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	if err = view.CheckCreate(auth, ns); err != nil {
		denied, ok := err.(*sqlstate.Error)
		if !trusted || !ok || denied.Code != "42501" {
			return err
		}
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	d := catalog.Collation{ObjectName: name, ObjectNamespace: ns.Address, Owner: view.State.CurrentUser, Provider: "c", Deterministic: true, Encoding: 6}
	from := s.From
	options := make(map[string]*parser.CollationOption, len(s.Options))
	for i := range s.Options {
		o := &s.Options[i]
		key := o.Name.Text
		switch key {
		case "from", "provider", "deterministic", "locale", "lc_collate", "lc_ctype", "rules", "version":
		default:
			return expr.ErrorAt("42601", fmt.Sprintf(`collation attribute "%s" not recognized`, key), o.Name.Pos)
		}
		if options[key] != nil {
			return expr.ErrorAt("42601", "conflicting or redundant options", o.Name.Pos)
		}
		options[key] = o
	}
	if o := options["from"]; o != nil {
		if len(s.Options) != 1 {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "conflicting or redundant options", Detail: "FROM cannot be specified together with any other options."}
		}
		if o.Numeric {
			return sqlError("42601", "argument of from must be a name")
		}
		from = o.Source
		if from == nil {
			from = &parser.ObjectName{Parts: []parser.Identifier{{Text: o.Value}}}
		}
	}
	if from != nil {
		source, err := findUtilityCollation(h, auth, *from)
		if err != nil {
			return err
		}
		if source.Provider == "d" {
			return sqlError("42P17", `collation "default" cannot be copied`)
		}
		d.Version, err = pgcatalog.CollationActualVersion(source)
		if err != nil {
			return err
		}
		d.Provider, d.Deterministic, d.Encoding = source.Provider, source.Deterministic, source.Encoding
		d.Collate, d.CType, d.Locale, d.Rules = source.Collate, source.CType, source.Locale, source.Rules
	} else {
		if o := options["provider"]; o != nil {
			switch strings.ToLower(o.Value) {
			case "libc":
				d.Provider = "c"
			case "builtin":
				d.Provider = "b"
			case "icu":
				d.Provider = "i"
			default:
				return sqlError("42P17", "unrecognized collation provider: "+o.Value)
			}
		}
		if o := options["deterministic"]; o != nil {
			if o.Numeric {
				number, numericErr := strconv.ParseInt(o.Value, 10, 32)
				if numericErr != nil || number != 0 && number != 1 {
					return sqlError("42601", "deterministic requires a Boolean value")
				}
				d.Deterministic = number == 1
			} else {
				switch strings.ToLower(o.Value) {
				case "true", "on":
					d.Deterministic = true
				case "false", "off":
					d.Deterministic = false
				default:
					return sqlError("42601", "deterministic requires a Boolean value")
				}
			}
		}
		textOption := func(name string) *string {
			if o := options[name]; o != nil {
				return catalog.CollationText(o.Value)
			}
			return nil
		}
		locale := textOption("locale")
		d.Collate, d.CType, d.Rules, d.Version = textOption("lc_collate"), textOption("lc_ctype"), textOption("rules"), textOption("version")
		if locale != nil && (d.Collate != nil || d.CType != nil) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "conflicting or redundant options", Detail: "LOCALE cannot be specified together with LC_COLLATE or LC_CTYPE."}
		}
		if d.Provider == "c" {
			if locale != nil {
				d.Collate, d.CType = locale, locale
			}
			if d.Collate == nil {
				return sqlError("42P17", `parameter "lc_collate" must be specified`)
			}
			if d.CType == nil {
				return sqlError("42P17", `parameter "lc_ctype" must be specified`)
			}
		} else {
			if locale == nil {
				return sqlError("42P17", `parameter "locale" must be specified`)
			}
			d.Locale = locale
			if d.Provider == "b" && *locale == "C.UTF8" {
				d.Locale = catalog.CollationText("C.UTF-8")
			}
			d.Encoding = -1
			if d.Provider == "b" {
				if *d.Locale != "C" {
					d.Encoding = 6
				}
				if _, err = catalog.CollationSemantics(&d); err != nil {
					return err
				}
			}
		}
	}
	if !d.Deterministic && d.Provider != "i" {
		return sqlError("0A000", "nondeterministic collations not supported with this provider")
	}
	if d.Rules != nil && d.Provider != "i" {
		return sqlError("42P17", "ICU rules cannot be specified unless locale provider is ICU")
	}
	key := ids.NameKey{ClassID: ids.CollationClassID, Namespace: ns.Address, Name: name}
	if old := h.Stage().LookupName(key); old != (ids.ObjectAddress{}) {
		message := fmt.Sprintf(`collation "%s" already exists`, name)
		if d.Encoding != -1 {
			message = fmt.Sprintf(`collation "%s" for encoding "UTF8" already exists`, name)
		}
		if s.IfNotExists {
			ddl.Notice(h, "42710", message+", skipping")
			return nil
		}
		return sqlError("42710", message)
	}
	d.Semantics, err = catalog.CollationSemantics(&d)
	if err != nil {
		return err
	}
	actual, err := pgcatalog.CollationActualVersion(&d)
	if err != nil {
		return err
	}
	if d.Version == nil {
		d.Version = actual
	}
	if err = collationVersionWarning(h, &d, view.Catalog, actual); err != nil {
		return err
	}
	d.OID, err = h.AllocateOID()
	if err != nil {
		return err
	}
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Create(&d)
}

func StageAlterCollationLocked(h StageHost, auth ddl.Authority, s *parser.AlterCollation) error {
	d, err := findUtilityCollation(h, auth, s.Name)
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	var ownerTarget authorization.PrincipalRef
	if s.Action == "owner" {
		ownerTarget, err = access.SchemaOwner(view, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if ownerTarget == d.Owner {
			return nil
		}
	}
	ref := ddl.ObjectRef(h, auth, d.Address())
	if err = ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return err
	}
	if d.Provider == "d" && s.Action == "refresh" {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "cannot refresh version of default collation", Hint: "Use ALTER DATABASE ... REFRESH COLLATION VERSION instead."}
	}
	next := *d
	switch s.Action {
	case "refresh":
		actual, err := pgcatalog.CollationActualVersion(d)
		if err != nil {
			return err
		}
		if (actual == nil) != (d.Version == nil) {
			return sqlError("XX000", "invalid collation version change")
		}
		if actual == nil || *actual == *d.Version {
			ddl.Notice(h, "00000", "version has not changed")
			return nil
		}
		ddl.Notice(h, "00000", fmt.Sprintf("changing version from %s to %s", *d.Version, *actual))
		next.Version = actual
	case "rename":
		if err = view.CheckCreate(auth, view.Owner.Domain.Ref(ref.Epoch, d.Namespace())); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "schema":
		var dest ids.ObjectRef
		dest, auth, err = h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		view = h.AccessView(auth)
		if err = view.CheckCreate(auth, dest); err != nil {
			return err
		}
		sourceNamespace := view.Catalog.MustNamespace(d.Namespace())
		targetNamespace := view.Catalog.MustNamespace(dest.Address)
		if d.Namespace() == dest.Address {
			return nil
		}
		if sourceNamespace.Temporary != (ids.SessionToken{}) || targetNamespace.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
		next.ObjectNamespace = dest.Address
	case "owner":
		if err = access.OwnershipEligibility(view, auth, ownerTarget, d.Namespace()); err != nil {
			return err
		}
		next.Owner = ownerTarget
	}
	key := catalog.DescriptorNameKey(&next)
	if a := h.Stage().LookupName(key); a != (ids.ObjectAddress{}) && a != d.Address() {
		return sqlError("42710", fmt.Sprintf(`collation "%s" already exists in schema "%s"`, next.Name(), view.Catalog.Get(next.Namespace()).Name()))
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Replace(&next)
}

func StageDropCollationLocked(h StageHost, auth ddl.Authority, s *parser.DropCollation) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		d, err := findUtilityCollation(h, auth, name)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); s.IfExists && ok && (diagnostic.Code == "42704" || diagnostic.Code == "3F000") {
				ddl.Notice(h, "00000", fmt.Sprintf(`collation "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
				continue
			}
			return err
		}
		ref := ddl.ObjectRef(h, auth, d.Address())
		if err = ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if d.Seeded() {
			object, pathErr := h.DependencyObject(auth, d.Address())
			if pathErr != nil {
				return pathErr
			}
			return sqlError("2BP01", "cannot drop "+object+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	h.RecordDependencyPath(auth)
	return nil
}

func findUtilityCollation(h StageHost, auth ddl.Authority, name parser.ObjectName) (*catalog.Collation, error) {
	d, err := h.FindCollation(auth, name)
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" {
		copy := *diagnostic
		copy.Position = ""
		err = &copy
	}
	return d, err
}
