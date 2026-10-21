package routine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// collectOperatorOptions resolves surviving estimators and collects link names.
// It changes only the caller's private descriptor, never the catalog stage.
func collectOperatorOptions(h DDLHost, auth ddl.Authority, d *catalog.Operator, options []parser.OperatorOption, alter bool) (parser.ObjectName, parser.ObjectName, error) {
	var comm, neg parser.ObjectName
	var restriction, join *parser.OperatorOption
	for i := range options {
		value := options[i]
		if alter {
			value = operatorAlterOption(value)
		}
		o := &value
		var err error
		switch o.Name {
		case "commutator":
			comm, err = operatorOptionName(*o)
		case "negator":
			neg, err = operatorOptionName(*o)
		case "restrict":
			restriction = &options[i]
		case "join":
			join = &options[i]
		case "hashes", "merges":
			enabled, err := operatorBoolean(*o)
			if err != nil {
				return comm, neg, err
			}
			if o.Name == "hashes" {
				d.Hash = enabled
			} else {
				d.Merge = enabled
			}
		case "sort1", "sort2", "ltcmp", "gtcmp":
			d.Merge = true
		}
		if err != nil {
			return comm, neg, err
		}
	}
	for i, o := range []*parser.OperatorOption{restriction, join} {
		if o == nil {
			continue
		}
		value := *o
		if alter {
			value = operatorAlterOption(value)
		}
		var name parser.ObjectName
		if !alter || value.HasValue {
			var err error
			name, err = operatorOptionName(value)
			if err != nil {
				return comm, neg, err
			}
		}
		id, err := operatorEstimator(h, auth, name, i == 1)
		if err != nil {
			return comm, neg, err
		}
		if i == 0 {
			d.Restriction = id
		} else {
			d.Join = id
		}
	}
	return comm, neg, nil
}

// StageCreateOperator stages CREATE OPERATOR, with forward shells for its
// commutator and negator.
func StageCreateOperator(h DDLHost, auth ddl.Authority, s *parser.CreateOperator) error {
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	if err = operatorOptionsSyntax(h, s.Options, false); err != nil {
		return err
	}
	var left, right parser.TypeName
	var hasLeft, hasRight bool
	var function parser.ObjectName
	for _, o := range s.Options {
		switch o.Name {
		case "leftarg":
			left, err = operatorOptionType(o)
			hasLeft = true
		case "rightarg":
			right, err = operatorOptionType(o)
			hasRight = true
		case "function", "procedure":
			function, err = operatorOptionName(o)
		}
		if err != nil {
			return err
		}
	}
	if len(function.Parts) == 0 {
		return sqlError("42P13", "operator function must be specified")
	}
	var l, r ids.OID
	if hasLeft {
		l, err = h.ResolveRegtype(auth, left)
		if err != nil {
			return err
		}
	}
	if hasRight {
		r, err = h.ResolveRegtype(auth, right)
		if err != nil {
			return err
		}
	}
	if l == 0 && r == 0 {
		return sqlError("42P13", "operator argument types must be specified")
	}
	if r == 0 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "operator right argument type must be specified", Detail: "Postfix operators are not supported."}
	}
	if err = checkOperatorArgumentUsage(h, auth, l); err != nil {
		return err
	}
	if err = checkOperatorArgumentUsage(h, auth, r); err != nil {
		return err
	}
	inputs := []ids.OID{r}
	if l != 0 {
		inputs = []ids.OID{l, r}
	}
	implementation, err := operatorRoutine(h, auth, function, inputs)
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	if err = view.CanUse(auth, ddl.ObjectRef(h, auth, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: implementation.Result})); err != nil {
		return err
	}
	symbol := s.Name.Parts[len(s.Name.Parts)-1].Text
	next := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectNamespace: ns.Address, ObjectName: symbol, Left: l, Right: r, Owner: view.State.CurrentUser, Result: implementation.Result, Function: implementation.Address().ObjectID}
	// DefineOperator resolves surviving estimators before OperatorCreate checks
	// the name, parameter combinations, and only then duplicate/shell ownership.
	comm, neg, err := collectOperatorOptions(h, auth, &next, s.Options, false)
	if err != nil {
		return err
	}
	if !ValidOperatorName(symbol) {
		return sqlError("42602", fmt.Sprintf(`"%s" is not a valid operator name`, symbol))
	}
	if err = validateOperatorOptions(&next, comm, neg); err != nil {
		return err
	}
	old, _ := view.Catalog.Operator(view.Catalog.NameAddress(catalog.DescriptorNameKey(&next)))
	if old != nil {
		if old.Function != 0 {
			return sqlError("42723", "operator "+symbol+" already exists")
		}
		if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, old.Address()), OwnerOnly: true}); err != nil {
			return err
		}
		next.ObjectAddress = old.Address()
	} else {
		id, err := h.AllocateOID()
		if err != nil {
			return err
		}
		next.ObjectAddress = ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	// Resolve/create and authorize both peers before any reciprocal conflict
	// checks. CREATE permits forward shells; ALTER deliberately does not.
	commutator, err := resolveOperatorLink(h, auth, &next, comm, false, true)
	if err != nil {
		return err
	}
	negator, err := resolveOperatorLink(h, auth, &next, neg, true, true)
	if err != nil {
		return err
	}
	if negator != nil && negator.Address() == next.Address() {
		return sqlError("42P13", "operator cannot be its own negator")
	}
	if old == nil {
		err = h.Stage().Create(&next)
	} else {
		err = h.Stage().Replace(&next)
	}
	if err != nil {
		return err
	}
	if err = stageResolvedOperatorLink(h, &next, commutator, false); err != nil {
		return err
	}
	if err = stageResolvedOperatorLink(h, &next, negator, true); err != nil {
		return err
	}
	return stageOperatorDescriptor(h, &next)
}

// StageAlterOperator stages ALTER OPERATOR OWNER TO, SET SCHEMA and SET
// (options). Links to missing or shell operators are errors.
func StageAlterOperator(h DDLHost, auth ddl.Authority, s *parser.AlterOperator) error {
	d, err := findUtilityOperator(h, auth, s.Signature)
	if err != nil {
		return err
	}
	next := *d
	if s.Action == "options" {
		if err = operatorOptionsSyntax(h, s.Options, true); err != nil {
			return err
		}
	}
	if s.Action == "schema" {
		var ns ids.ObjectRef
		ns, auth, err = h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = h.CheckCreateIn(auth, ns); err != nil {
			return err
		}
		view := h.AccessView(auth).Catalog
		source := view.MustNamespace(d.Namespace())
		target := view.MustNamespace(ns.Address)
		if source.Temporary != (ids.SessionToken{}) || target.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
		next.ObjectNamespace = ns.Address
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		return err
	}
	if OperatorSeeded(d) {
		return sqlError("0A000", "altering built-in operators is unsupported")
	}
	switch s.Action {
	case "owner":
		view := h.AccessView(auth)
		target, err := access.SchemaOwner(view, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if target == d.Owner {
			return nil
		}
		if err = access.OwnershipEligibility(view, auth, target, d.Namespace()); err != nil {
			return err
		}
		next.Owner = target
	case "schema":
		if a := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); a != (ids.ObjectAddress{}) && a != d.Address() {
			return sqlError("42723", "operator "+d.Name()+" already exists in schema \""+s.Target.Text+"\"")
		}
	case "options":
		comm, neg, err := collectOperatorOptions(h, auth, &next, s.Options, true)
		if err != nil {
			return err
		}
		// ALTER resolves both peers and self-negation before any protected
		// fields or parameter combinations. No tuple/peer is staged yet.
		commutator, err := resolveOperatorLink(h, auth, &next, comm, false, false)
		if err != nil {
			return err
		}
		negator, err := resolveOperatorLink(h, auth, &next, neg, true, false)
		if err != nil {
			return err
		}
		if negator != nil && negator.Address() == next.Address() {
			return sqlError("42P13", "operator cannot be its own negator")
		}
		if err = checkOperatorLinkReplacement(d, commutator, false); err != nil {
			return err
		}
		if err = checkOperatorLinkReplacement(d, negator, true); err != nil {
			return err
		}
		if d.Merge && !next.Merge {
			return operatorOptionError("merges")
		}
		if d.Hash && !next.Hash {
			return operatorOptionError("hashes")
		}
		if err = validateOperatorOptions(&next, comm, neg); err != nil {
			return err
		}
		if err = stageResolvedOperatorLink(h, &next, commutator, false); err != nil {
			return err
		}
		if err = stageResolvedOperatorLink(h, &next, negator, true); err != nil {
			return err
		}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Replace(&next)
}

// StageDropOperator records the DROP OPERATOR targets and the dependency path
// for the engine's drop orchestration.
func StageDropOperator(h DDLHost, auth ddl.Authority, s *parser.DropOperator) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Signatures))
	for _, sig := range s.Signatures {
		d, err := findUtilityOperator(h, auth, sig)
		if err != nil {
			if diag, ok := err.(*sqlstate.Error); ok && s.IfExists && (diag.Code == "42883" || diag.Code == "3F000") {
				message := diag.Message + ", skipping"
				if diag.Code == "42883" {
					message = "operator " + catalog.ObjectNameText(sig.Name) + " does not exist, skipping"
				}
				h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
				continue
			}
			return err
		}
		namespaceOwner := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Namespace()), OwnerOnly: true}) == nil
		if !namespaceOwner {
			if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
				if diag, ok := err.(*sqlstate.Error); ok && diag.Code == "42501" {
					return sqlError("42501", "must be owner of operator "+catalog.ObjectNameText(sig.Name))
				}
				return err
			}
		}
		if OperatorSeeded(d) {
			object, _ := h.DependencyObject(auth, d.Address())
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
