package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

var builtinCollationVersion = "1"

func collationActualVersion(d *catalog.Collation) (*string, error) {
	if d.Provider == "d" {
		return databaseCollationActualVersion()
	}
	if _, err := catalog.CollationSemantics(d); err != nil {
		return nil, err
	}
	if d.Provider == "b" {
		return &builtinCollationVersion, nil
	}
	return nil, nil
}

func collationVersionWarning(op *Operation, d *catalog.Collation, view catalog.Read, actual *string) error {
	if d.Version == nil {
		return nil
	}
	if actual == nil {
		return sqlError("XX000", fmt.Sprintf(`collation "%s" has no actual version, but a version was recorded`, d.Name()))
	}
	if *d.Version != *actual {
		ns := view.Get(d.Namespace()).Name()
		op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: fmt.Sprintf(`collation "%s" has version mismatch`, d.Name()), Detail: fmt.Sprintf("The collation in the database was created using version %s, but the operating system provides version %s.", *d.Version, *actual), Hint: "Rebuild all objects affected by this collation and run ALTER COLLATION " + dependencyIdentifier(ns) + "." + dependencyIdentifier(d.Name()) + " REFRESH VERSION, or build PostgreSQL with the right library version."})
	}
	return nil
}

func (e *Engine) stageCreateCollationLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateCollation) error {
	ns, err := e.collationCreationNamespaceLocked(&auth, stage, s.Name)
	if err != nil {
		return err
	}
	if op != nil && op.restoreTableDestination {
		bootstrap, guardErr := auth.roles.Descriptor(auth.state.Authenticated)
		if guardErr != nil || !bootstrap.Superuser || e.importTargets == nil {
			return fmt.Errorf("stuntgres: invalid trusted restore collation authority")
		}
	}
	if err = auth.CheckCreate(ns); err != nil {
		denied, ok := err.(*sqlstate.Error)
		if op == nil || !op.restoreTableDestination || !ok || denied.Code != "42501" {
			return err
		}
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	d := catalog.Collation{ObjectName: name, ObjectNamespace: ns.Address, Owner: auth.state.CurrentUser, Provider: "c", Deterministic: true, Encoding: 6}
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
		source, err := e.findUtilityCollation(auth, *from)
		if err != nil {
			return err
		}
		if source.Provider == "d" {
			return sqlError("42P17", `collation "default" cannot be copied`)
		}
		d.Version, err = collationActualVersion(source)
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
	if old := stage.LookupName(key); old != (ids.ObjectAddress{}) {
		message := fmt.Sprintf(`collation "%s" already exists`, name)
		if d.Encoding != -1 {
			message = fmt.Sprintf(`collation "%s" for encoding "UTF8" already exists`, name)
		}
		if s.IfNotExists {
			schemaNotice(op, "42710", message+", skipping")
			return nil
		}
		return sqlError("42710", message)
	}
	d.Semantics, err = catalog.CollationSemantics(&d)
	if err != nil {
		return err
	}
	actual, err := collationActualVersion(&d)
	if err != nil {
		return err
	}
	if d.Version == nil {
		d.Version = actual
	}
	if err = collationVersionWarning(op, &d, auth.catalog, actual); err != nil {
		return err
	}
	d.OID, err = e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	d.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	return stage.Create(&d)
}

func (e *Engine) stageAlterCollationLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.AlterCollation) error {
	d, err := e.findUtilityCollation(auth, s.Name)
	if err != nil {
		return err
	}
	var ownerTarget authorization.PrincipalRef
	if s.Action == "owner" {
		ownerTarget, err = schemaOwner(auth, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if ownerTarget == d.Owner {
			return nil
		}
	}
	ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address())
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return err
	}
	if d.Provider == "d" && s.Action == "refresh" {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "cannot refresh version of default collation", Hint: "Use ALTER DATABASE ... REFRESH COLLATION VERSION instead."}
	}
	next := *d
	switch s.Action {
	case "refresh":
		actual, err := collationActualVersion(d)
		if err != nil {
			return err
		}
		if (actual == nil) != (d.Version == nil) {
			return sqlError("XX000", "invalid collation version change")
		}
		if actual == nil || *actual == *d.Version {
			schemaNotice(op, "00000", "version has not changed")
			return nil
		}
		schemaNotice(op, "00000", fmt.Sprintf("changing version from %s to %s", *d.Version, *actual))
		next.Version = actual
	case "rename":
		if err = auth.CheckCreate(e.catalog.Domain.Ref(ref.Epoch, d.Namespace())); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "schema":
		dest, err := e.collationCreationNamespaceLocked(&auth, stage, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = auth.CheckCreate(dest); err != nil {
			return err
		}
		sourceNamespace := auth.catalog.MustNamespace(d.Namespace())
		targetNamespace := auth.catalog.MustNamespace(dest.Address)
		if d.Namespace() == dest.Address {
			return nil
		}
		if sourceNamespace.Temporary != (ids.SessionToken{}) || targetNamespace.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
		next.ObjectNamespace = dest.Address
	case "owner":
		if err = ownershipEligibility(auth, ownerTarget, d.Namespace()); err != nil {
			return err
		}
		next.Owner = ownerTarget
	}
	key := catalog.DescriptorNameKey(&next)
	if a := stage.LookupName(key); a != (ids.ObjectAddress{}) && a != d.Address() {
		return sqlError("42710", fmt.Sprintf(`collation "%s" already exists in schema "%s"`, next.Name(), auth.catalog.Get(next.Namespace()).Name()))
	}
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	return stage.Replace(&next)
}

func (e *Engine) stageDropCollationLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.DropCollation) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		d, err := e.findUtilityCollation(auth, name)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); s.IfExists && ok && (diagnostic.Code == "42704" || diagnostic.Code == "3F000") {
				schemaNotice(op, "00000", fmt.Sprintf(`collation "%s" does not exist, skipping`, objectNameText(name)))
				continue
			}
			return err
		}
		ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address())
		if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return err
		}
		if d.Seeded() {
			path, pathErr := e.referencePath(auth)
			if pathErr != nil {
				return pathErr
			}
			return sqlError("2BP01", "cannot drop "+dependencyObject(auth.catalog, d.Address(), path)+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			stage.domainTargets = append(stage.domainTargets, d.Address())
		}
	}
	stage.dependencyPath, _ = e.referencePath(auth)
	return nil
}

func (e *Engine) findUtilityCollation(auth AuthorizationView, name parser.ObjectName) (*catalog.Collation, error) {
	d, err := e.findCollation(auth, name)
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" {
		copy := *diagnostic
		copy.Position = ""
		err = &copy
	}
	return d, err
}
