package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func operatorOptionDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var actual *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &actual) || !reflect.DeepEqual(actual, want) {
		t.Fatalf("diagnostic = %#v (%v), want %#v", actual, err, want)
	}
}

func operatorParsedOptions(t *testing.T, text string) []parser.OperatorOption {
	t.Helper()
	statements, err := parser.Parse("CREATE OPERATOR public.#%# (" + text + ")")
	if err != nil {
		t.Fatal(text, err)
	}
	return statements[0].(*parser.CreateOperator).Options
}

func TestUserOperatorOptionExtraction(t *testing.T) {
	for _, literal := range []string{"", "int4", "integer", "pg_catalog.int4", `A."B"`, "int4[]", "SETOF int4", "numeric(8,2)", " Int4 "} {
		pos := parser.Position{Offset: 27, Character: 28}
		o := parser.OperatorOption{Name: "function", HasValue: true, Kind: parser.OperatorOptionStringArgument, Value: literal, ValuePos: pos}
		name, err := operatorOptionName(o)
		if err != nil || !reflect.DeepEqual(name.Parts, []parser.Identifier{{Text: literal, Pos: pos}}) {
			t.Fatal("literal name reinterpreted", literal, name, err)
		}
		o.Name = "leftarg"
		typ, err := operatorOptionType(o)
		want := parser.TypeName{Name: literal, Quoted: true, Parts: []parser.Identifier{{Text: literal, Pos: pos}}, Pos: pos}
		if err != nil || !reflect.DeepEqual(typ, want) {
			t.Fatal("literal type reinterpreted", literal, typ, err)
		}
	}
	for _, text := range []string{"function=int4", "function=int4[]", "function=int4(8)", "function=SETOF int4"} {
		o := operatorParsedOptions(t, text)[0]
		name, err := operatorOptionName(o)
		if err != nil || len(name.Parts) != 1 || name.Parts[0].Text != "int4" {
			t.Fatal("generic name canonicalized as a type", text, name, err)
		}
	}
	for _, tc := range []struct {
		text  string
		parts []string
	}{
		{"INTEGER", []string{"pg_catalog", "int4"}},
		{"BOOLEAN", []string{"pg_catalog", "bool"}},
		{"NUMERIC", []string{"pg_catalog", "numeric"}},
		{`"integer"`, []string{"integer"}},
		{"pg_catalog.int4", []string{"pg_catalog", "int4"}},
	} {
		o := operatorParsedOptions(t, "function="+tc.text)[0]
		name, err := operatorOptionName(o)
		if err != nil || len(name.Parts) != len(tc.parts) {
			t.Fatal(tc.text, name, err)
		}
		for i, part := range tc.parts {
			if name.Parts[i].Text != part {
				t.Fatal("SQL alias/generic name provenance", tc.text, name)
			}
		}
	}
	for _, tc := range []struct{ text, nameError, typeError string }{
		{"function", "function requires a parameter", "function requires a parameter"},
		{"function=7", "argument of function must be a name", "argument of function must be a type name"},
		{"function=1.0", "argument of function must be a name", "argument of function must be a type name"},
		{"function=OPERATOR(public.+)", "", "argument of function must be a type name"},
	} {
		o := operatorParsedOptions(t, tc.text)[0]
		_, err := operatorOptionName(o)
		if tc.nameError != "" {
			operatorOptionDiagnostic(t, err, "42601", tc.nameError, "")
		} else if err != nil {
			t.Fatal(err)
		}
		_, err = operatorOptionType(o)
		operatorOptionDiagnostic(t, err, "42601", tc.typeError, "")
	}
	for _, text := range []string{"function=pg_catalog.int4[]", "function=pg_catalog.numeric(8,2)", "function=SETOF pg_catalog.int4"} {
		o := operatorParsedOptions(t, text)[0]
		name, err := operatorOptionName(o)
		if err != nil || len(name.Parts) != 2 || name.Parts[0].Text != "pg_catalog" || name.Parts[1].Text != o.Type.Name {
			t.Fatal("name extraction must ignore type decorations only", text, name, err)
		}
		typ, err := operatorOptionType(o)
		if err != nil || !reflect.DeepEqual(typ, o.Type) {
			t.Fatal("type extraction lost decorations", text, typ, err)
		}
	}
	for _, tc := range []struct {
		rhs   string
		want  bool
		valid bool
	}{
		{"", true, true}, {"true", true, true}, {"'TrUe'", true, true}, {"ON", true, true}, {"'ON'", true, true}, {"false", false, true}, {"off", false, true}, {"'OFF'", false, true},
		{"0", false, true}, {"1", true, true}, {"+1", true, true}, {"-0", false, true}, {"+0", false, true}, {"0x1", true, true}, {"0o0", false, true}, {"0b1", true, true}, {"0_1", true, true},
		{"-1", false, false}, {"2", false, false}, {"1.0", false, false}, {"1e0", false, false}, {"'1'", false, false}, {"'0'", false, false}, {"'t'", false, false}, {"'tru'", false, false}, {"'of'", false, false}, {"'o'", false, false}, {"'yes'", false, false}, {"'no'", false, false}, {"''", false, false}, {"' true'", false, false}, {"pg_catalog.off", false, false}, {"off[]", false, false}, {"off(1)", false, false}, {"SETOF off", false, false}, {"NONE", false, false},
	} {
		text := "hashes"
		if tc.rhs != "" {
			text += "=" + tc.rhs
		}
		o := operatorParsedOptions(t, text)[0]
		got, err := operatorBoolean(o)
		if tc.valid {
			if err != nil || got != tc.want {
				t.Fatal(text, got, err)
			}
		} else {
			operatorOptionDiagnostic(t, err, "42601", "hashes requires a Boolean value", "")
		}
	}
	for _, text := range []string{"restrict", "restrict=NONE", "restrict='none'", `restrict="none"`} {
		o := operatorParsedOptions(t, text)[0]
		create := o
		o = operatorAlterOption(o)
		nullable := text == "restrict" || text == "restrict=NONE"
		if o.HasValue == nullable {
			t.Fatal("ALTER NULL classification", text, o)
		}
		if !nullable {
			name, err := operatorOptionName(o)
			if err != nil || len(name.Parts) != 1 || name.Parts[0].Text != "none" {
				t.Fatal(text, name, err)
			}
		}
		if text == "restrict=NONE" {
			name, err := operatorOptionName(create)
			if err != nil || name.Parts[0].Text != "none" {
				t.Fatal("CREATE NONE lost real name", name, err)
			}
		}
	}
	for _, tc := range []struct {
		text    string
		alter   bool
		message string
	}{
		{"function=7,function=int4pl", false, "argument of function must be a name"},
		{"leftarg=7,leftarg=int4", false, "argument of leftarg must be a type name"},
		{"rightarg=SETOF int4,rightarg=int4", false, "SETOF type not allowed for operator argument"},
		{"hashes='t',hashes=true", false, "hashes requires a Boolean value"},
		{"function=7", true, "argument of function must be a name"},
		{"userop_unknown=7", true, "argument of userop_unknown must be a name"},
		{"sort1=7", true, "argument of sort1 must be a name"},
		{"hashes=1", true, "argument of hashes must be a name"},
		{"commutator=NONE", true, "commutator requires a parameter"},
		{"negator", true, "negator requires a parameter"},
		{"function=NONE", true, `operator attribute "function" cannot be changed`},
	} {
		code := "42601"
		if tc.message == "SETOF type not allowed for operator argument" {
			code = "42P13"
		}
		operatorOptionDiagnostic(t, operatorOptionsSyntax(nil, operatorParsedOptions(t, tc.text), tc.alter), code, tc.message, "")
	}
	for _, text := range []string{"commutator='abc',commutator='#=#'", "leftarg=missing_type,leftarg=int4", "function=missing_function,function=int4pl", "sort1=7,merges=false", "sort2", "hashes=true,hashes=false"} {
		if err := operatorOptionsSyntax(nil, operatorParsedOptions(t, text), false); err != nil {
			t.Fatal("premature semantic lookup/obsolete extraction", text, err)
		}
	}
	for _, text := range []string{"restrict,join=NONE,hashes=NONE,merges", "restrict='none'", `join="none"`} {
		if err := operatorOptionsSyntax(nil, operatorParsedOptions(t, text), true); err != nil {
			t.Fatal("ALTER nullable/name extraction", text, err)
		}
	}
}

func TestUserOperatorArgumentValidationOrder(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: 23}}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	withPublicSchemaUnitOwner(t, e, "pg_monitor", func() {
		e.mu.Lock()
		refreshed, err := e.authorizationLocked(op)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		auth = refreshed
		auth.state.CurrentUser = auth.roles.LookupRef("pg_monitor")
		for _, tc := range []struct{ options, code, message, detail string }{
			{"leftarg=userop_missing_type", "42P13", "operator function must be specified", ""},
			{"leftarg=userop_missing_type,function=int4um", "42704", `type "userop_missing_type" does not exist`, ""},
			{"leftarg=int4,function=int4um", "42P13", "operator right argument type must be specified", "Postfix operators are not supported."},
			{"leftarg=int4,rightarg=userop_missing_type,function=int4pl", "42704", `type "userop_missing_type" does not exist`, ""},
			{"leftarg=userop_missing_left,rightarg=userop_missing_right,function=int4pl", "42704", `type "userop_missing_left" does not exist`, ""},
			{"leftarg=int4,rightarg=int4,function=userop_missing_function", "42501", "permission denied for type integer", ""},
			{"leftarg=int4(8),rightarg=int4,function=int4pl", "42601", `type modifier is not allowed for type "pg_catalog.int4"`, ""},
			{"leftarg=SETOF int4,rightarg=int4", "42P13", "SETOF type not allowed for operator argument", ""},
			{"leftarg='',function=int4um", "42704", `type "" does not exist`, ""},
			{"leftarg=NONE,function=int4um", "42704", `type "none" does not exist`, ""},
		} {
			s := &parser.CreateOperator{Name: operatorUnitName("public", "#%#"), Options: operatorParsedOptions(t, tc.options)}
			before := e.CatalogRead().state
			stage := e.BeginCatalogStage(auth.catalog)
			e.mu.Lock()
			err = e.stageCreateOperatorLocked(op, auth, stage, s)
			e.mu.Unlock()
			operatorOptionDiagnostic(t, err, tc.code, tc.message, tc.detail)
			if e.CatalogRead().state != before || len(stage.descriptors) != 0 {
				t.Fatal("failed validation published/staged operator", tc.options)
			}
			e.DiscardCatalogStage(stage)
		}
	})
}

func TestUserOperatorAlterSemanticCheckpoints(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	for _, definition := range []*parser.CreateOperator{
		operatorUnitDefinition("#=#", "int4eq"),
		operatorUnitDefinition("#@#", "int4ne"),
	} {
		stage := operatorUnitStage(t, e, op, auth, definition)
		collationUnitPublish(t, e, stage)
		auth.recaptureCatalog(e.CatalogRead())
	}
	signature := parser.OperatorSignature{Name: operatorUnitName("public", "#=#"), Left: parser.TypeName{Name: "int4"}, Right: parser.TypeName{Name: "int4"}}
	source, err := e.findUtilityOperator(op, auth, signature)
	if err != nil {
		t.Fatal(err)
	}
	peerSignature := signature
	peerSignature.Name = operatorUnitName("public", "#@#")
	peer, err := e.findUtilityOperator(op, auth, peerSignature)
	if err != nil {
		t.Fatal(err)
	}
	// Both references are captured from the original stage, but their updates
	// must accumulate on the same actual private peer rather than stale copies.
	stage := e.BeginCatalogStage(auth.catalog)
	e.mu.Lock()
	err = e.stageAlterOperatorLocked(op, auth, stage, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, "commutator=OPERATOR(public.#@#),negator=OPERATOR(public.#@#),hashes,merges")})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	staged := e.stageRead(stage)
	next := staged.state.descriptors[source.address].(*operatorDescriptor)
	reciprocal := staged.state.descriptors[peer.address].(*operatorDescriptor)
	if next.commutator != peer.address.ObjectID || next.negator != peer.address.ObjectID || !next.hash || !next.merge ||
		reciprocal.commutator != source.address.ObjectID || reciprocal.negator != source.address.ObjectID {
		t.Fatal("staged roots lost one link kind", next, reciprocal)
	}
	if source.commutator != 0 || source.negator != 0 || peer.commutator != 0 || peer.negator != 0 || e.CatalogRead().state != auth.catalog.state {
		t.Fatal("private reciprocal updates changed published roots")
	}
	collationUnitPublish(t, e, stage)
	auth.recaptureCatalog(e.CatalogRead())
	// Shell lookup must also precede protected fields; CREATE retains its own
	// forward-shell path and supplies a real shell for this competitor.
	shellGenerator := operatorUnitDefinition("#~#", "int4eq")
	shellGenerator.Options = append(shellGenerator.Options, parser.OperatorOption{Name: "commutator", HasValue: true, Kind: parser.OperatorOptionNameArgument, Reference: operatorUnitName("public", "#!#")})
	stage = operatorUnitStage(t, e, op, auth, shellGenerator)
	collationUnitPublish(t, e, stage)
	auth.recaptureCatalog(e.CatalogRead())
	for _, tc := range []struct{ options, code, message string }{
		{"hashes=false,commutator=OPERATOR(public.#?#)", "42883", "operator does not exist: integer public.#?# integer"},
		{"hashes=false,merges=false", "42P13", `operator attribute "merges" cannot be changed if it has already been set`},
		{"commutator=OPERATOR(public.#=#),negator=OPERATOR(public.#?#)", "42883", "operator does not exist: integer public.#?# integer"},
		{"commutator=OPERATOR(public.#?#),negator=OPERATOR(public.#=#)", "42883", "operator does not exist: integer public.#?# integer"},
		{"commutator=OPERATOR(public.#=#),negator=OPERATOR(public.#=#)", "42P13", "operator cannot be its own negator"},
		{"commutator=OPERATOR(public.#=#),merges=false", "42P13", `operator attribute "commutator" cannot be changed if it has already been set`},
		{"negator=OPERATOR(public.#~#),merges=false", "42P13", `operator attribute "negator" cannot be changed if it has already been set`},
		{"restrict=userop_missing_estimator,join=userop_missing_join,commutator=OPERATOR(public.#?#),hashes=false", "42883", "function userop_missing_estimator(internal, oid, internal, integer) does not exist"},
		{"join=userop_missing_join,commutator=OPERATOR(public.#?#),hashes=false", "42883", "function userop_missing_join(internal, oid, internal, smallint, internal) does not exist"},
		{"commutator=OPERATOR(public.#!#),negator=OPERATOR(public.#=#),merges=false", "42883", "operator is only a shell: integer public.#!# integer"},
		{"commutator=OPERATOR(public.#=#),negator=OPERATOR(public.#!#),merges=false", "42883", "operator is only a shell: integer public.#!# integer"},
	} {
		t.Run(tc.options, func(t *testing.T) {
			before := e.CatalogRead().state
			stage := e.BeginCatalogStage(auth.catalog)
			e.mu.Lock()
			err := e.stageAlterOperatorLocked(op, auth, stage, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, tc.options)})
			e.mu.Unlock()
			operatorOptionDiagnostic(t, err, tc.code, tc.message, "")
			if e.CatalogRead().state != before || len(stage.descriptors) != 0 {
				t.Fatal("semantic validation changed actual catalog roots", tc.options)
			}
			e.DiscardCatalogStage(stage)
		})
	}
	stage = operatorUnitStage(t, e, op, auth, operatorUnitDefinition("#%#", "int4pl"))
	collationUnitPublish(t, e, stage)
	auth.recaptureCatalog(e.CatalogRead())
	signature.Name = operatorUnitName("public", "#%#")
	for _, tc := range []struct{ options, code, message string }{
		{"negator=OPERATOR(public.#?#),hashes", "42883", "operator does not exist: integer public.#?# integer"},
		{"negator=OPERATOR(public.#%#),hashes", "42P13", "operator cannot be its own negator"},
		{"negator=OPERATOR(public.#@#),hashes", "42P13", "only boolean operators can have negators"},
		{"commutator=OPERATOR(public.#@#),merges", "42P13", "only boolean operators can merge join"},
	} {
		t.Run(tc.options, func(t *testing.T) {
			stage := e.BeginCatalogStage(auth.catalog)
			e.mu.Lock()
			err := e.stageAlterOperatorLocked(op, auth, stage, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, tc.options)})
			e.mu.Unlock()
			operatorOptionDiagnostic(t, err, tc.code, tc.message, "")
			if e.CatalogRead().state != auth.catalog.state || len(stage.descriptors) != 0 {
				t.Fatal("parameter validation followed reciprocal mutation", tc.options)
			}
			e.DiscardCatalogStage(stage)
		})
	}
}

func TestUserOperatorCreationCheckpoints(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	for _, definition := range []*parser.CreateOperator{
		operatorUnitDefinition("#%#", "int4pl"),
		{Name: operatorUnitName("public", "#=#"), Options: operatorParsedOptions(t, "leftarg=int4,rightarg=int4,function=int4eq,commutator=OPERATOR(public.#@#)")},
	} {
		stage := operatorUnitStage(t, e, op, auth, definition)
		collationUnitPublish(t, e, stage)
		auth.recaptureCatalog(e.CatalogRead())
	}
	// Both a defined duplicate and an existing shell must survive failures
	// before target admission, with no private source or forward peer staged.
	for _, symbol := range []string{"#%#", "#@#", "invalid"} {
		for _, tc := range []struct{ options, code, message string }{
			{"restrict=userop_missing_estimator,join=userop_missing_join", "42883", "function userop_missing_estimator(internal, oid, internal, integer) does not exist"},
			{"restrict=eqsel,join=userop_missing_join", "42883", "function userop_missing_join(internal, oid, internal, smallint, internal) does not exist"},
			{"hashes,commutator=OPERATOR(public.#?#)", "42P13", "only boolean operators can hash"},
		} {
			t.Run(symbol+"/"+tc.options, func(t *testing.T) {
				definition := operatorUnitDefinition(symbol, "int4pl")
				definition.Options = append(definition.Options, operatorParsedOptions(t, tc.options)...)
				stage := e.BeginCatalogStage(auth.catalog)
				e.mu.Lock()
				err := e.stageCreateOperatorLocked(op, auth, stage, definition)
				e.mu.Unlock()
				code, message := tc.code, tc.message
				if symbol == "invalid" && code == "42P13" {
					code, message = "42602", `"invalid" is not a valid operator name`
				}
				operatorOptionDiagnostic(t, err, code, message, "")
				if e.CatalogRead().state != auth.catalog.state || len(stage.descriptors) != 0 {
					t.Fatal("CREATE semantic failure changed published/private roots")
				}
				e.DiscardCatalogStage(stage)
			})
		}
	}
}

func TestUserOperatorParameterCheckpoints(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	for _, symbol := range []string{"##~", "##!"} {
		definition := &parser.CreateOperator{Name: operatorUnitName("public", symbol), Options: operatorParsedOptions(t, "rightarg=int4,function=int4um")}
		stage := operatorUnitStage(t, e, op, auth, definition)
		collationUnitPublish(t, e, stage)
		auth.recaptureCatalog(e.CatalogRead())
	}
	signature := parser.OperatorSignature{Name: operatorUnitName("public", "##~"), Right: parser.TypeName{Name: "int4"}}
	for _, tc := range []struct{ options, message string }{
		{"negator=OPERATOR(public.##!),hashes", "only binary operators can hash"},
		{"restrict=eqsel,hashes", "only binary operators can hash"},
		{"negator=OPERATOR(public.##!),merges,hashes", "only binary operators can merge join"},
		{"restrict=eqsel,merges,hashes", "only binary operators can merge join"},
		{"negator=OPERATOR(public.##!),join=eqjoinsel,merges,hashes", "only binary operators can have join selectivity"},
		{"restrict=eqsel,join=eqjoinsel,merges,hashes", "only binary operators can have join selectivity"},
		{"negator=OPERATOR(public.##!),restrict=eqsel", "only boolean operators can have negators"},
		{"restrict=eqsel", "only boolean operators can have restriction selectivity"},
	} {
		for _, alter := range []bool{false, true} {
			command := "CREATE"
			if alter {
				command = "ALTER"
			}
			t.Run(command+"/"+tc.options, func(t *testing.T) {
				stage := e.BeginCatalogStage(auth.catalog)
				e.mu.Lock()
				var err error
				if alter {
					err = e.stageAlterOperatorLocked(op, auth, stage, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, tc.options)})
				} else {
					definition := &parser.CreateOperator{Name: operatorUnitName("public", "##?"), Options: operatorParsedOptions(t, "rightarg=int4,function=int4um,"+tc.options)}
					err = e.stageCreateOperatorLocked(op, auth, stage, definition)
				}
				e.mu.Unlock()
				operatorOptionDiagnostic(t, err, "42P13", tc.message, "")
				if e.CatalogRead().state != auth.catalog.state || len(stage.descriptors) != 0 {
					t.Fatal("parameter failure changed source, peers or forward shells")
				}
				e.DiscardCatalogStage(stage)
			})
		}
	}
}

func TestUserOperatorPeerSetupStagedRoots(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	public := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	key := func(symbol string) NameKey {
		return NameKey{ClassID: OperatorClassID, Namespace: public, Name: symbol, Signature: "23,23"}
	}
	definition := operatorUnitDefinition("#=#", "int4eq")
	definition.Options = append(definition.Options, operatorParsedOptions(t, "commutator=OPERATOR(public.#!#),negator=OPERATOR(public.#!#)")...)
	before := e.CatalogRead().state
	stage := operatorUnitStage(t, e, op, auth, definition)
	staged := e.stageRead(stage)
	source := staged.state.descriptors[staged.state.names[key("#=#")]].(*operatorDescriptor)
	shell := staged.state.descriptors[staged.state.names[key("#!#")]].(*operatorDescriptor)
	if source.commutator != shell.address.ObjectID || source.negator != shell.address.ObjectID ||
		shell.commutator != source.address.ObjectID || shell.negator != source.address.ObjectID ||
		shell.function != 0 || shell.result != 0 || shell.owner != auth.state.CurrentUser {
		t.Fatal("same forward shell lost identity, owner or one reciprocal kind", source, shell)
	}
	if e.CatalogRead().state != before || before.names[key("#=#")] != (ObjectAddress{}) || before.names[key("#!#")] != (ObjectAddress{}) {
		t.Fatal("forward peer setup mutated the published root")
	}
	collationUnitPublish(t, e, stage)
	auth.recaptureCatalog(e.CatalogRead())
	sourceBefore, shellBefore := *source, *shell
	completion := operatorUnitDefinition("#!#", "int4ne")
	completion.Options = append(completion.Options, operatorParsedOptions(t, "commutator=OPERATOR(public.#!#),negator=OPERATOR(public.#=#)")...)
	stage = operatorUnitStage(t, e, op, auth, completion)
	completed := e.stageRead(stage).state.descriptors[shell.address].(*operatorDescriptor)
	if completed.address != shell.address || completed.function != 144 || completed.result != 16 ||
		completed.owner != auth.state.CurrentUser || completed.commutator != completed.address.ObjectID ||
		completed.negator != source.address.ObjectID {
		t.Fatal("shell completion lost stable identity or self/peer links", completed)
	}
	if *source != sourceBefore || *shell != shellBefore || e.CatalogRead().state != auth.catalog.state {
		t.Fatal("completion changed an immutable predecessor")
	}
	collationUnitPublish(t, e, stage)
	auth.recaptureCatalog(e.CatalogRead())

	// Commutator setup creates and links a private forward shell before the
	// negator's reciprocal conflict. Ordinary stage discard must suffice.
	before = auth.catalog.state
	source = before.descriptors[source.address].(*operatorDescriptor)
	completed = before.descriptors[completed.address].(*operatorDescriptor)
	sourceBefore, shellBefore = *source, *completed
	failed := operatorUnitDefinition("#?#", "int4eq")
	failed.Options = append(failed.Options, operatorParsedOptions(t, "commutator=OPERATOR(public.#@#),negator=OPERATOR(public.#=#)")...)
	stage = e.BeginCatalogStage(auth.catalog)
	e.mu.Lock()
	err := e.stageCreateOperatorLocked(op, auth, stage, failed)
	e.mu.Unlock()
	operatorOptionDiagnostic(t, err, "42P13", "negator operator #=# is already the negator of operator #!#", "")
	staged = e.stageRead(stage)
	privateSource := staged.state.descriptors[staged.state.names[key("#?#")]].(*operatorDescriptor)
	privateShell := staged.state.descriptors[staged.state.names[key("#@#")]].(*operatorDescriptor)
	if privateSource.commutator != privateShell.address.ObjectID || privateShell.commutator != privateSource.address.ObjectID ||
		privateShell.function != 0 || privateShell.result != 0 {
		t.Fatal("failure did not retain the expected private reciprocal stage", privateSource, privateShell)
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().state != before || *source != sourceBefore || *completed != shellBefore ||
		before.names[key("#?#")] != (ObjectAddress{}) || before.names[key("#@#")] != (ObjectAddress{}) {
		t.Fatal("failed private peer setup changed published descriptors or names")
	}
}
