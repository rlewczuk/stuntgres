package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func validOperatorName(name string) bool {
	if len(name) == 0 || len(name) > 63 || name == "!=" || name == "=>" || strings.Contains(name, "/*") || strings.Contains(name, "--") {
		return false
	}
	for _, c := range name {
		if !strings.ContainsRune("~!@#^&|`?+-*/%<>=", c) {
			return false
		}
	}
	return len(name) == 1 || !strings.ContainsAny(name[len(name)-1:], "+-") || strings.ContainsAny(name, "~!@#^&|`?%")
}
func operatorOptionName(o parser.OperatorOption) (parser.ObjectName, error) {
	if !o.HasValue {
		return parser.ObjectName{}, sqlError("42601", o.Name+" requires a parameter")
	}
	switch o.Kind {
	case parser.OperatorOptionNameArgument:
		return o.Reference, nil
	case parser.OperatorOptionTypeArgument:
		if len(o.Type.Parts) != 0 {
			return parser.ObjectName{Parts: o.Type.Parts}, nil
		}
		parts := make([]parser.Identifier, 0, 2)
		if o.Type.Schema != nil {
			parts = append(parts, *o.Type.Schema)
		}
		parts = append(parts, parser.Identifier{Text: o.Type.Name, Pos: o.Type.Pos})
		return parser.ObjectName{Parts: parts}, nil
	case parser.OperatorOptionStringArgument:
		return parser.ObjectName{Parts: []parser.Identifier{{Text: o.Value, Pos: o.ValuePos}}}, nil
	case parser.OperatorOptionNoneArgument:
		return parser.ObjectName{Parts: []parser.Identifier{{Text: "none", Pos: o.ValuePos}}}, nil
	}
	return parser.ObjectName{}, sqlError("42601", "argument of "+o.Name+" must be a name")
}

func operatorOptionType(o parser.OperatorOption) (parser.TypeName, error) {
	if !o.HasValue {
		return parser.TypeName{}, sqlError("42601", o.Name+" requires a parameter")
	}
	switch o.Kind {
	case parser.OperatorOptionTypeArgument:
		return o.Type, nil
	case parser.OperatorOptionStringArgument, parser.OperatorOptionNoneArgument:
		name := o.Value
		if o.Kind == parser.OperatorOptionNoneArgument {
			name = "none"
		}
		return parser.TypeName{Name: name, Quoted: true, Parts: []parser.Identifier{{Text: name, Pos: o.ValuePos}}, Pos: o.ValuePos}, nil
	}
	return parser.TypeName{}, sqlError("42601", "argument of "+o.Name+" must be a type name")
}

// ALTER's unquoted NONE is a NULL argument; quoted/literal none remains a name.
func operatorAlterOption(o parser.OperatorOption) parser.OperatorOption {
	if o.Kind == parser.OperatorOptionNoneArgument {
		o.HasValue, o.Kind = false, parser.OperatorOptionNoArgument
	}
	return o
}

func (e *Engine) resolveOperatorArgument(op *Operation, auth AuthorizationView, raw parser.TypeName) (ids.OID, error) {
	input, err := e.captureRegtypeInput(auth, op)
	if err != nil {
		return 0, err
	}
	id, err := input.resolveParsedTypeName(op.Context(), raw, op)
	return ids.OID(id), err
}

func (e *Engine) checkOperatorArgumentUsage(auth AuthorizationView, id ids.OID) error {
	if id == 0 {
		return nil
	}
	return auth.CanUse(e.catalog.Domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}))
}

func (e *Engine) operatorArgument(op *Operation, auth AuthorizationView, raw parser.TypeName) (ids.OID, error) {
	if raw.Name == "" || strings.EqualFold(raw.Name, "none") && raw.Schema == nil && !raw.Quoted {
		return 0, nil
	}
	if raw.Setof {
		return 0, sqlError("42P13", "SETOF type not allowed for operator argument")
	}
	id, err := e.resolveOperatorArgument(op, auth, raw)
	if err != nil {
		return 0, err
	}
	return id, e.checkOperatorArgumentUsage(auth, id)
}
func operatorInputName(v catalog.Read, left, right ids.OID, name parser.ObjectName) string {
	n := objectNameText(name)
	if left != 0 {
		t, _ := v.LookupType(sqltype.ID(left))
		n = t.DiagnosticName() + " " + n
	}
	if right != 0 {
		t, _ := v.LookupType(sqltype.ID(right))
		n += " " + t.DiagnosticName()
	}
	return n
}
func (e *Engine) findUtilityOperator(op *Operation, auth AuthorizationView, s parser.OperatorSignature) (*catalog.Operator, error) {
	left, err := e.operatorArgument(op, auth, s.Left)
	if err != nil {
		return nil, err
	}
	right, err := e.operatorArgument(op, auth, s.Right)
	if err != nil {
		return nil, err
	}
	path, err := e.lookupNamespaces(auth, s.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	key := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, Left: left, Right: right, ObjectName: s.Name.Parts[len(s.Name.Parts)-1].Text}
	for i := range path.count() {
		key.ObjectNamespace = path.at(i)
		a := auth.catalog.NameAddress(catalog.DescriptorNameKey(&key))
		if d, ok := auth.catalog.Operator(a); ok {
			return d, nil
		}
	}
	return nil, sqlError("42883", "operator does not exist: "+operatorInputName(auth.catalog, left, right, s.Name))
}
func (e *Engine) operatorRoutine(auth AuthorizationView, name parser.ObjectName, inputs []ids.OID) (*catalog.Procedure, error) {
	path, err := e.lookupNamespaces(auth, name, functionLookup)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(inputs))
	types := make([]string, len(inputs))
	for i, id := range inputs {
		ids[i] = strconv.FormatUint(uint64(id), 10)
		if t, ok := auth.catalog.LookupType(sqltype.ID(id)); ok {
			types[i] = t.DiagnosticName()
		}
	}
	d := procedureInNamespaces(auth.catalog, path, name.Parts[len(name.Parts)-1].Text, catalog.ProcedureSignature(strings.Join(ids, ",")))
	if d == nil {
		return nil, sqlError("42883", "function "+objectNameText(name)+"("+strings.Join(types, ", ")+") does not exist")
	}
	if d.RoutineKind != 'f' {
		return nil, sqlError("42809", objectNameText(name)+" is not a function")
	}
	if err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
		return nil, err
	}
	return d, nil
}

// Both join signatures are legal, but neither wins over the other when both
// exist. Inputs are lookup identities, not callable provider declarations.
func operatorEstimatorVariant(name parser.ObjectName, five catalog.RoutineIdentity, fiveCount int, four catalog.RoutineIdentity, fourCount int) (catalog.RoutineIdentity, int, error) {
	if fiveCount != 0 && fourCount != 0 {
		return catalog.RoutineIdentity{}, 0, sqlError("42725", "join estimator function "+objectNameText(name)+" has multiple matches")
	}
	if fiveCount != 0 {
		return five, fiveCount, nil
	}
	return four, fourCount, nil
}

func validateOperatorEstimatorMetadata(name parser.ObjectName, join bool, result ids.OID, builtin, superuser bool) error {
	if !builtin && !superuser {
		return sqlError("42501", "must be superuser to specify a non-built-in selectivity estimation function")
	}
	if result == 0 {
		return sqlError("0A000", "operator estimator return metadata is unavailable")
	}
	if result != 701 {
		kind := "restriction"
		if join {
			kind = "join"
		}
		return sqlError("42P17", kind+" estimator function "+objectNameText(name)+" must return type float8")
	}
	return nil
}
func (e *Engine) operatorEstimator(auth AuthorizationView, name parser.ObjectName, join bool) (ids.OID, error) {
	if len(name.Parts) == 0 {
		return 0, nil
	}
	path, err := e.lookupNamespaces(auth, name, functionLookup)
	if err != nil {
		return 0, err
	}
	n := name.Parts[len(name.Parts)-1].Text
	signature := catalog.ProcedureSignature("2281,26,2281,23")
	builtin, superuser := true, false
	if join {
		signature = "2281,26,2281,21,2281"
	}
	identity, count := selectRoutine(auth.catalog, path, n, &signature)
	if join {
		four := catalog.ProcedureSignature("2281,26,2281,21")
		alternate, other := selectRoutine(auth.catalog, path, n, &four)
		identity, count, err = operatorEstimatorVariant(name, identity, count, alternate, other)
		if err != nil {
			return 0, err
		}
	}
	if count == 0 {
		inputs := []ids.OID{2281, 26, 2281, 23}
		if join {
			inputs = []ids.OID{2281, 26, 2281, 21, 2281}
		}
		_, err := e.operatorRoutine(auth, name, inputs)
		return 0, err
	}
	result := releasedOperatorEstimatorResult(identity.ID)
	if d, ok := auth.catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID}); ok {
		if d.RoutineKind != catalog.FunctionRoutine {
			return 0, sqlError("42809", objectNameText(name)+" is not a function")
		}
		result = d.Result
		if err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
			return 0, err
		}
		builtin = d.Seeded()
		if !builtin {
			actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
			if err != nil {
				return 0, err
			}
			superuser = actor.Superuser
		}
	}
	if err := validateOperatorEstimatorMetadata(name, join, result, builtin, superuser); err != nil {
		return 0, err
	}
	return identity.ID, nil
}
func operatorOptionError(name string) error {
	return sqlError("42P13", fmt.Sprintf(`operator attribute "%s" cannot be changed if it has already been set`, name))
}
func validateOperatorOptions(d *catalog.Operator, comm, neg parser.ObjectName) error {
	// OperatorValidateParams completes binary eligibility before checking
	// Boolean results, even when an earlier Boolean-only option is also set.
	if d.Left == 0 {
		if len(comm.Parts) > 0 {
			return sqlError("42P13", "only binary operators can have commutators")
		}
		if d.Join != 0 {
			return sqlError("42P13", "only binary operators can have join selectivity")
		}
		if d.Merge {
			return sqlError("42P13", "only binary operators can merge join")
		}
		if d.Hash {
			return sqlError("42P13", "only binary operators can hash")
		}
	}
	if d.Result != ids.OID(sqltype.Bool) {
		if len(neg.Parts) > 0 {
			return sqlError("42P13", "only boolean operators can have negators")
		}
		if d.Restriction != 0 {
			return sqlError("42P13", "only boolean operators can have restriction selectivity")
		}
		if d.Join != 0 {
			return sqlError("42P13", "only boolean operators can have join selectivity")
		}
		if d.Merge {
			return sqlError("42P13", "only boolean operators can merge join")
		}
		if d.Hash {
			return sqlError("42P13", "only boolean operators can hash")
		}
	}
	return nil
}
func operatorBoolean(o parser.OperatorOption) (bool, error) {
	if !o.HasValue {
		return true, nil
	}
	value := o.Value
	switch o.Kind {
	case parser.OperatorOptionIntegerArgument:
		// Integer nodes use their value, including signed zero and radix input.
		digits := strings.ReplaceAll(value, "_", "")
		sign := int64(1)
		if len(digits) > 0 && (digits[0] == '+' || digits[0] == '-') {
			if digits[0] == '-' {
				sign = -1
			}
			digits = digits[1:]
		}
		base := 10
		if len(digits) > 2 && digits[0] == '0' {
			switch digits[1] {
			case 'x', 'X':
				base, digits = 16, digits[2:]
			case 'o', 'O':
				base, digits = 8, digits[2:]
			case 'b', 'B':
				base, digits = 2, digits[2:]
			}
		}
		n, err := strconv.ParseInt(digits, base, 32)
		if err == nil && (n*sign == 0 || n*sign == 1) {
			return n*sign == 1, nil
		}
	case parser.OperatorOptionTypeArgument:
		// A complete decorated or qualified TypeName cannot equal a Boolean
		// word; never accept just its last component.
		if o.Type.Schema == nil && len(o.Type.Parts) <= 1 && !o.Type.Setof && len(o.Type.Modifiers) == 0 && len(o.Type.ArrayBounds) == 0 {
			value = o.Type.Name
		}
		fallthrough
	case parser.OperatorOptionStringArgument, parser.OperatorOptionFloatArgument:
		switch strings.ToLower(value) {
		case "true", "on":
			return true, nil
		case "false", "off":
			return false, nil
		}
	}
	return false, sqlError("42601", o.Name+" requires a Boolean value")
}

func operatorOptionsSyntax(op *Operation, options []parser.OperatorOption, alter bool) error {
	for _, raw := range options {
		o := raw
		if alter {
			o = operatorAlterOption(o)
			if o.HasValue {
				if _, err := operatorOptionName(o); err != nil {
					return err
				}
			}
		}
		switch o.Name {
		case "leftarg", "rightarg":
			if alter {
				return sqlError("42601", fmt.Sprintf(`operator attribute "%s" cannot be changed`, o.Name))
			}
			typ, err := operatorOptionType(o)
			if err != nil {
				return err
			}
			if typ.Setof {
				return sqlError("42P13", "SETOF type not allowed for operator argument")
			}
		case "function", "procedure":
			if alter {
				return sqlError("42601", fmt.Sprintf(`operator attribute "%s" cannot be changed`, o.Name))
			}
			if _, err := operatorOptionName(o); err != nil {
				return err
			}
		case "restrict", "join", "commutator", "negator":
			if alter && !o.HasValue && (o.Name == "restrict" || o.Name == "join") {
				continue
			}
			if _, err := operatorOptionName(o); err != nil {
				return err
			}
		case "hashes", "merges":
			if _, err := operatorBoolean(o); err != nil {
				return err
			}
		case "sort1", "sort2", "ltcmp", "gtcmp":
			if alter {
				return sqlError("42601", fmt.Sprintf(`operator attribute "%s" not recognized`, o.Name))
			}
		default:
			message := fmt.Sprintf(`operator attribute "%s" not recognized`, o.Name)
			if alter {
				return sqlError("42601", message)
			}
			op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42601", Message: message})
		}
	}
	return nil
}

// collectOperatorOptions resolves surviving estimators and collects link names.
// It changes only the caller's private descriptor, never the catalog stage.
func (e *Engine) collectOperatorOptions(auth AuthorizationView, d *catalog.Operator, options []parser.OperatorOption, alter bool) (parser.ObjectName, parser.ObjectName, error) {
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
		id, err := e.operatorEstimator(auth, name, i == 1)
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
func stageOperatorDescriptor(stage *CatalogStage, d *catalog.Operator) error {
	if _, exists := stage.Base.Find(d.Address()); exists {
		return stage.Replace(d)
	}
	if old := stage.Descriptors[d.Address()]; old != nil {
		if catalog.DescriptorNameKey(old) != catalog.DescriptorNameKey(d) {
			return catalog.Conflict{}
		}
		stage.Descriptors[d.Address()] = d
		return nil
	}
	return stage.Create(d)
}

// resolveOperatorLink captures an exact peer once, including shell/owner checks.
// Only CREATE may create a shell here; ALTER resolution never changes the stage.
func (e *Engine) resolveOperatorLink(auth AuthorizationView, stage *CatalogStage, d *catalog.Operator, name parser.ObjectName, negator, allowShell bool) (*catalog.Operator, error) {
	if len(name.Parts) == 0 {
		return nil, nil
	}
	symbol := name.Parts[len(name.Parts)-1].Text
	if !validOperatorName(symbol) {
		return nil, sqlError("42602", fmt.Sprintf(`"%s" is not a valid operator name`, symbol))
	}
	left, right := d.Right, d.Left
	if negator {
		left, right = d.Left, d.Right
	}
	path, err := e.lookupNamespaces(auth, name, operatorLookup)
	if err != nil {
		// CREATE's catalog peer lookup has no expression error location.
		// Keep ALTER's existing reference diagnostic context unchanged.
		if diagnostic, ok := err.(*sqlstate.Error); ok && allowShell && diagnostic.Position != "" {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return nil, err
	}
	view := e.stageRead(stage)
	var peer *catalog.Operator
	key := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectName: symbol, Left: left, Right: right}
	for i := range path.count() {
		key.ObjectNamespace = path.at(i)
		if found, ok := view.Operator(view.NameAddress(catalog.DescriptorNameKey(&key))); ok {
			peer = found
			break
		}
	}
	if peer != nil && !allowShell && peer.Function == 0 {
		return nil, sqlError("42883", "operator is only a shell: "+operatorInputName(auth.catalog, left, right, name))
	}
	if peer == nil {
		if !allowShell {
			return nil, sqlError("42883", "operator does not exist: "+operatorInputName(auth.catalog, left, right, name))
		}
		ns, err := e.collationCreationNamespaceLocked(&auth, stage, name)
		if err != nil {
			return nil, err
		}
		// CREATE resolves peers before staging the source. A reference to the
		// source itself needs its reserved identity, not a second shell.
		if ns.Address == d.Namespace() && symbol == d.Name() && left == d.Left && right == d.Right {
			return d, nil
		}
		if err = auth.CheckCreate(ns); err != nil {
			return nil, err
		}
		id, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		rev, err := e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		peer = &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}, ObjectNamespace: ns.Address, ObjectRevision: rev, ObjectName: symbol, Left: left, Right: right, Owner: auth.state.CurrentUser}
		if err = stage.Create(peer); err != nil {
			return nil, err
		}
	} else if peer.Address() != d.Address() {
		peerAuth := auth
		peerAuth.Recapture(view)
		if err = peerAuth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(auth.catalog.Epoch(), peer.Address()), OwnerOnly: true}); err != nil {
			return nil, err
		}
	}
	return peer, nil
}

func checkOperatorLinkReplacement(d, peer *catalog.Operator, negator bool) error {
	if peer == nil {
		return nil
	}
	prior := d.Commutator
	if negator {
		prior = d.Negator
	}
	if prior != 0 && prior != peer.Address().ObjectID {
		attribute := "commutator"
		if negator {
			attribute = "negator"
		}
		return operatorOptionError(attribute)
	}
	return nil
}

// Stage by captured identity, reading the latest private peer so two link kinds
// targeting the same operator retain each other's reciprocal updates.
func (e *Engine) stageResolvedOperatorLink(stage *CatalogStage, d, peer *catalog.Operator, negator bool) error {
	if peer == nil {
		return nil
	}
	view := e.stageRead(stage)
	peer = view.MustOperator(peer.Address())
	if peer.Address() == d.Address() {
		if negator {
			return sqlError("42P13", "operator cannot be its own negator")
		}
		d.Commutator = d.Address().ObjectID
		return nil
	}
	next := *peer
	link := peer.Commutator
	attribute := "commutator"
	if negator {
		link = peer.Negator
		attribute = "negator"
	}
	if link != 0 && link != d.Address().ObjectID {
		third, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: link})
		if !ok {
			return fmt.Errorf("stuntgres: missing reciprocal operator")
		}
		return sqlError("42P13", attribute+" operator "+peer.Name()+" is already the "+attribute+" of operator "+third.Name())
	}
	if negator {
		d.Negator = peer.Address().ObjectID
		next.Negator = d.Address().ObjectID
	} else {
		d.Commutator = peer.Address().ObjectID
		next.Commutator = d.Address().ObjectID
	}
	if next == *peer {
		return nil
	}
	var err error
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	return stageOperatorDescriptor(stage, &next)
}

func (e *Engine) stageCreateOperatorLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateOperator) error {
	ns, err := e.collationCreationNamespaceLocked(&auth, stage, s.Name)
	if err != nil {
		return err
	}
	if err = e.conversionCheckCreate(op, auth, ns); err != nil {
		return err
	}
	if err = operatorOptionsSyntax(op, s.Options, false); err != nil {
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
		l, err = e.resolveOperatorArgument(op, auth, left)
		if err != nil {
			return err
		}
	}
	if hasRight {
		r, err = e.resolveOperatorArgument(op, auth, right)
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
	if err = e.checkOperatorArgumentUsage(auth, l); err != nil {
		return err
	}
	if err = e.checkOperatorArgumentUsage(auth, r); err != nil {
		return err
	}
	inputs := []ids.OID{r}
	if l != 0 {
		inputs = []ids.OID{l, r}
	}
	routine, err := e.operatorRoutine(auth, function, inputs)
	if err != nil {
		return err
	}
	if err = auth.CanUse(e.catalog.Domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: routine.Result})); err != nil {
		return err
	}
	symbol := s.Name.Parts[len(s.Name.Parts)-1].Text
	next := catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID}, ObjectNamespace: ns.Address, ObjectName: symbol, Left: l, Right: r, Owner: auth.state.CurrentUser, Result: routine.Result, Function: routine.Address().ObjectID}
	// DefineOperator resolves surviving estimators before OperatorCreate checks
	// the name, parameter combinations, and only then duplicate/shell ownership.
	comm, neg, err := e.collectOperatorOptions(auth, &next, s.Options, false)
	if err != nil {
		return err
	}
	if !validOperatorName(symbol) {
		return sqlError("42602", fmt.Sprintf(`"%s" is not a valid operator name`, symbol))
	}
	if err = validateOperatorOptions(&next, comm, neg); err != nil {
		return err
	}
	old, _ := auth.catalog.Operator(auth.catalog.NameAddress(catalog.DescriptorNameKey(&next)))
	if old != nil {
		if old.Function != 0 {
			return sqlError("42723", "operator "+symbol+" already exists")
		}
		if err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(auth.catalog.Epoch(), old.Address()), OwnerOnly: true}); err != nil {
			return err
		}
		next.ObjectAddress = old.Address()
	} else {
		id, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		next.ObjectAddress = ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}
	}
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	// Resolve/create and authorize both peers before any reciprocal conflict
	// checks. CREATE permits forward shells; ALTER deliberately does not.
	commutator, err := e.resolveOperatorLink(auth, stage, &next, comm, false, true)
	if err != nil {
		return err
	}
	negator, err := e.resolveOperatorLink(auth, stage, &next, neg, true, true)
	if err != nil {
		return err
	}
	if negator != nil && negator.Address() == next.Address() {
		return sqlError("42P13", "operator cannot be its own negator")
	}
	if old == nil {
		err = stage.Create(&next)
	} else {
		err = stage.Replace(&next)
	}
	if err != nil {
		return err
	}
	if err = e.stageResolvedOperatorLink(stage, &next, commutator, false); err != nil {
		return err
	}
	if err = e.stageResolvedOperatorLink(stage, &next, negator, true); err != nil {
		return err
	}
	return stageOperatorDescriptor(stage, &next)
}
func (e *Engine) stageAlterOperatorLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.AlterOperator) error {
	d, err := e.findUtilityOperator(op, auth, s.Signature)
	if err != nil {
		return err
	}
	next := *d
	if s.Action == "options" {
		if err = operatorOptionsSyntax(op, s.Options, true); err != nil {
			return err
		}
	}
	if s.Action == "schema" {
		ns, err := e.collationCreationNamespaceLocked(&auth, stage, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = e.conversionCheckCreate(op, auth, ns); err != nil {
			return err
		}
		source := auth.catalog.MustNamespace(d.Namespace())
		target := auth.catalog.MustNamespace(ns.Address)
		if source.Temporary != (ids.SessionToken{}) || target.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
		next.ObjectNamespace = ns.Address
	}
	if err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address()), OwnerOnly: true}); err != nil {
		return err
	}
	if operatorSeeded(d) {
		return sqlError("0A000", "altering built-in operators is unsupported")
	}
	switch s.Action {
	case "owner":
		target, err := schemaOwner(auth.accessView, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if target == d.Owner {
			return nil
		}
		if err = ownershipEligibility(auth.accessView, auth, target, d.Namespace()); err != nil {
			return err
		}
		next.Owner = target
	case "schema":
		if a := stage.LookupName(catalog.DescriptorNameKey(&next)); a != (ids.ObjectAddress{}) && a != d.Address() {
			return sqlError("42723", "operator "+d.Name()+" already exists in schema \""+s.Target.Text+"\"")
		}
	case "options":
		comm, neg, err := e.collectOperatorOptions(auth, &next, s.Options, true)
		if err != nil {
			return err
		}
		// ALTER resolves both peers and self-negation before any protected
		// fields or parameter combinations. No tuple/peer is staged yet.
		commutator, err := e.resolveOperatorLink(auth, stage, &next, comm, false, false)
		if err != nil {
			return err
		}
		negator, err := e.resolveOperatorLink(auth, stage, &next, neg, true, false)
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
		if err = e.stageResolvedOperatorLink(stage, &next, commutator, false); err != nil {
			return err
		}
		if err = e.stageResolvedOperatorLink(stage, &next, negator, true); err != nil {
			return err
		}
	}
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	return stage.Replace(&next)
}
func (e *Engine) stageDropOperatorLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.DropOperator) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Signatures))
	for _, sig := range s.Signatures {
		d, err := e.findUtilityOperator(op, auth, sig)
		if err != nil {
			if diag, ok := err.(*sqlstate.Error); ok && s.IfExists && (diag.Code == "42883" || diag.Code == "3F000") {
				message := diag.Message + ", skipping"
				if diag.Code == "42883" {
					message = "operator " + objectNameText(sig.Name) + " does not exist, skipping"
				}
				schemaNotice(op, "00000", message)
				continue
			}
			return err
		}
		namespaceOwner := auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Namespace()), OwnerOnly: true}) == nil
		if !namespaceOwner {
			if err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address()), OwnerOnly: true}); err != nil {
				if diag, ok := err.(*sqlstate.Error); ok && diag.Code == "42501" {
					return sqlError("42501", "must be owner of operator "+objectNameText(sig.Name))
				}
				return err
			}
		}
		if operatorSeeded(d) {
			path, _ := e.referencePath(auth)
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
