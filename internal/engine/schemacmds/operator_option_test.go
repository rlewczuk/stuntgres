package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
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

func TestUserOperatorArgumentValidationOrder(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	withPublicSchemaUnitOwner(t, e, "pg_monitor", func() {
		e.Mu.Lock()
		refreshed, err := e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		auth = refreshed
		auth.State.CurrentUser = auth.Roles.LookupRef("pg_monitor")
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
			before := e.CatalogRead().State()
			stage := e.BeginCatalogStage(auth.Catalog)
			e.Mu.Lock()
			err = routine.StageCreateOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, s)
			e.Mu.Unlock()
			operatorOptionDiagnostic(t, err, tc.code, tc.message, tc.detail)
			if e.CatalogRead().State() != before || stage.DescriptorCount() != 0 {
				t.Fatal("failed validation published/staged operator", tc.options)
			}
			e.DiscardCatalogStage(stage)
		}
	})
}

func TestUserOperatorAlterSemanticCheckpoints(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	for _, definition := range []*parser.CreateOperator{
		operatorUnitDefinition("#=#", "int4eq"),
		operatorUnitDefinition("#@#", "int4ne"),
	} {
		stage := operatorUnitStage(t, e, op, auth, definition)
		collationUnitPublish(t, e, stage)
		auth.Recapture(e.CatalogRead())
	}
	signature := parser.OperatorSignature{Name: operatorUnitName("public", "#=#"), Left: parser.TypeName{Name: "int4"}, Right: parser.TypeName{Name: "int4"}}
	source := operatorUnitLookup(t, auth.Catalog, "#=#")
	peer := operatorUnitLookup(t, auth.Catalog, "#@#")
	// Both references are captured from the original stage, but their updates
	// must accumulate on the same actual private peer rather than stale copies.
	stage := e.BeginCatalogStage(auth.Catalog)
	e.Mu.Lock()
	err := routine.StageAlterOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, "commutator=OPERATOR(public.#@#),negator=OPERATOR(public.#@#),hashes,merges")})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	staged := e.StageRead(stage)
	next := staged.MustOperator(source.Address())
	reciprocal := staged.MustOperator(peer.Address())
	if next.Commutator != peer.Address().ObjectID || next.Negator != peer.Address().ObjectID || !next.Hash || !next.Merge ||
		reciprocal.Commutator != source.Address().ObjectID || reciprocal.Negator != source.Address().ObjectID {
		t.Fatal("staged roots lost one link kind", next, reciprocal)
	}
	if source.Commutator != 0 || source.Negator != 0 || peer.Commutator != 0 || peer.Negator != 0 || e.CatalogRead().State() != auth.Catalog.State() {
		t.Fatal("private reciprocal updates changed published roots")
	}
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	// Shell lookup must also precede protected fields; CREATE retains its own
	// forward-shell path and supplies a real shell for this competitor.
	shellGenerator := operatorUnitDefinition("#~#", "int4eq")
	shellGenerator.Options = append(shellGenerator.Options, parser.OperatorOption{Name: "commutator", HasValue: true, Kind: parser.OperatorOptionNameArgument, Reference: operatorUnitName("public", "#!#")})
	stage = operatorUnitStage(t, e, op, auth, shellGenerator)
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
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
			before := e.CatalogRead().State()
			stage := e.BeginCatalogStage(auth.Catalog)
			e.Mu.Lock()
			err := routine.StageAlterOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, tc.options)})
			e.Mu.Unlock()
			operatorOptionDiagnostic(t, err, tc.code, tc.message, "")
			if e.CatalogRead().State() != before || stage.DescriptorCount() != 0 {
				t.Fatal("semantic validation changed actual catalog roots", tc.options)
			}
			e.DiscardCatalogStage(stage)
		})
	}
	stage = operatorUnitStage(t, e, op, auth, operatorUnitDefinition("#%#", "int4pl"))
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	signature.Name = operatorUnitName("public", "#%#")
	for _, tc := range []struct{ options, code, message string }{
		{"negator=OPERATOR(public.#?#),hashes", "42883", "operator does not exist: integer public.#?# integer"},
		{"negator=OPERATOR(public.#%#),hashes", "42P13", "operator cannot be its own negator"},
		{"negator=OPERATOR(public.#@#),hashes", "42P13", "only boolean operators can have negators"},
		{"commutator=OPERATOR(public.#@#),merges", "42P13", "only boolean operators can merge join"},
	} {
		t.Run(tc.options, func(t *testing.T) {
			stage := e.BeginCatalogStage(auth.Catalog)
			e.Mu.Lock()
			err := routine.StageAlterOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, tc.options)})
			e.Mu.Unlock()
			operatorOptionDiagnostic(t, err, tc.code, tc.message, "")
			if e.CatalogRead().State() != auth.Catalog.State() || stage.DescriptorCount() != 0 {
				t.Fatal("parameter validation followed reciprocal mutation", tc.options)
			}
			e.DiscardCatalogStage(stage)
		})
	}
}

func TestUserOperatorCreationCheckpoints(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	for _, definition := range []*parser.CreateOperator{
		operatorUnitDefinition("#%#", "int4pl"),
		{Name: operatorUnitName("public", "#=#"), Options: operatorParsedOptions(t, "leftarg=int4,rightarg=int4,function=int4eq,commutator=OPERATOR(public.#@#)")},
	} {
		stage := operatorUnitStage(t, e, op, auth, definition)
		collationUnitPublish(t, e, stage)
		auth.Recapture(e.CatalogRead())
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
				stage := e.BeginCatalogStage(auth.Catalog)
				e.Mu.Lock()
				err := routine.StageCreateOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, definition)
				e.Mu.Unlock()
				code, message := tc.code, tc.message
				if symbol == "invalid" && code == "42P13" {
					code, message = "42602", `"invalid" is not a valid operator name`
				}
				operatorOptionDiagnostic(t, err, code, message, "")
				if e.CatalogRead().State() != auth.Catalog.State() || stage.DescriptorCount() != 0 {
					t.Fatal("CREATE semantic failure changed published/private roots")
				}
				e.DiscardCatalogStage(stage)
			})
		}
	}
}

func TestUserOperatorParameterCheckpoints(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	for _, symbol := range []string{"##~", "##!"} {
		definition := &parser.CreateOperator{Name: operatorUnitName("public", symbol), Options: operatorParsedOptions(t, "rightarg=int4,function=int4um")}
		stage := operatorUnitStage(t, e, op, auth, definition)
		collationUnitPublish(t, e, stage)
		auth.Recapture(e.CatalogRead())
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
				stage := e.BeginCatalogStage(auth.Catalog)
				e.Mu.Lock()
				var err error
				if alter {
					err = routine.StageAlterOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, &parser.AlterOperator{Signature: signature, Action: "options", Options: operatorParsedOptions(t, tc.options)})
				} else {
					definition := &parser.CreateOperator{Name: operatorUnitName("public", "##?"), Options: operatorParsedOptions(t, "rightarg=int4,function=int4um,"+tc.options)}
					err = routine.StageCreateOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, definition)
				}
				e.Mu.Unlock()
				operatorOptionDiagnostic(t, err, "42P13", tc.message, "")
				if e.CatalogRead().State() != auth.Catalog.State() || stage.DescriptorCount() != 0 {
					t.Fatal("parameter failure changed source, peers or forward shells")
				}
				e.DiscardCatalogStage(stage)
			})
		}
	}
}

func TestUserOperatorPeerSetupStagedRoots(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	key := func(symbol string) ids.NameKey {
		return ids.NameKey{ClassID: ids.OperatorClassID, Namespace: public, Name: symbol, Signature: "23,23"}
	}
	definition := operatorUnitDefinition("#=#", "int4eq")
	definition.Options = append(definition.Options, operatorParsedOptions(t, "commutator=OPERATOR(public.#!#),negator=OPERATOR(public.#!#)")...)
	before := e.CatalogRead().State()
	stage := operatorUnitStage(t, e, op, auth, definition)
	staged := e.StageRead(stage)
	source := staged.MustOperator(staged.NameAddress(key("#=#")))
	shell := staged.MustOperator(staged.NameAddress(key("#!#")))
	if source.Commutator != shell.Address().ObjectID || source.Negator != shell.Address().ObjectID ||
		shell.Commutator != source.Address().ObjectID || shell.Negator != source.Address().ObjectID ||
		shell.Function != 0 || shell.Result != 0 || shell.Owner != auth.State.CurrentUser {
		t.Fatal("same forward shell lost identity, owner or one reciprocal kind", source, shell)
	}
	if e.CatalogRead().State() != before || before.NameAddress(key("#=#")) != (ids.ObjectAddress{}) || before.NameAddress(key("#!#")) != (ids.ObjectAddress{}) {
		t.Fatal("forward peer setup mutated the published root")
	}
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())
	sourceBefore, shellBefore := *source, *shell
	completion := operatorUnitDefinition("#!#", "int4ne")
	completion.Options = append(completion.Options, operatorParsedOptions(t, "commutator=OPERATOR(public.#!#),negator=OPERATOR(public.#=#)")...)
	stage = operatorUnitStage(t, e, op, auth, completion)
	completed := e.StageRead(stage).MustOperator(shell.Address())
	if completed.Address() != shell.Address() || completed.Function != 144 || completed.Result != 16 ||
		completed.Owner != auth.State.CurrentUser || completed.Commutator != completed.Address().ObjectID ||
		completed.Negator != source.Address().ObjectID {
		t.Fatal("shell completion lost stable identity or self/peer links", completed)
	}
	if *source != sourceBefore || *shell != shellBefore || e.CatalogRead().State() != auth.Catalog.State() {
		t.Fatal("completion changed an immutable predecessor")
	}
	collationUnitPublish(t, e, stage)
	auth.Recapture(e.CatalogRead())

	// Commutator setup creates and links a private forward shell before the
	// negator's reciprocal conflict. Ordinary stage discard must suffice.
	before = auth.Catalog.State()
	source = before.MustOperator(source.Address())
	completed = before.MustOperator(completed.Address())
	sourceBefore, shellBefore = *source, *completed
	failed := operatorUnitDefinition("#?#", "int4eq")
	failed.Options = append(failed.Options, operatorParsedOptions(t, "commutator=OPERATOR(public.#@#),negator=OPERATOR(public.#=#)")...)
	stage = e.BeginCatalogStage(auth.Catalog)
	e.Mu.Lock()
	err := routine.StageCreateOperator(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, failed)
	e.Mu.Unlock()
	operatorOptionDiagnostic(t, err, "42P13", "negator operator #=# is already the negator of operator #!#", "")
	staged = e.StageRead(stage)
	privateSource := staged.MustOperator(staged.NameAddress(key("#?#")))
	privateShell := staged.MustOperator(staged.NameAddress(key("#@#")))
	if privateSource.Commutator != privateShell.Address().ObjectID || privateShell.Commutator != privateSource.Address().ObjectID ||
		privateShell.Function != 0 || privateShell.Result != 0 {
		t.Fatal("failure did not retain the expected private reciprocal stage", privateSource, privateShell)
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().State() != before || *source != sourceBefore || *completed != shellBefore ||
		before.NameAddress(key("#?#")) != (ids.ObjectAddress{}) || before.NameAddress(key("#@#")) != (ids.ObjectAddress{}) {
		t.Fatal("failed private peer setup changed published descriptors or names")
	}
}

// withPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func withPublicSchemaUnitOwner(t *testing.T, e *core.Engine, owner string, body func()) {
	t.Helper()
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if len(addresses) != 1 {
		t.Fatal("public schema fixture requires one public namespace")
	}
	descriptor, ok := view.Descriptor(addresses[0])
	if !ok {
		t.Fatal("public schema fixture requires its namespace descriptor")
	}
	namespace, ok := descriptor.(*catalog.Namespace)
	if !ok {
		t.Fatal("public schema fixture has a non-namespace descriptor")
	}
	roles := e.Roles()
	prior, err := roles.Descriptor(namespace.Owner)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := roles.Descriptor(e.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	login, err := e.ResolveLogin(bootstrap.Name)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}, login)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error("closing public schema fixture session:", err)
		}
	}()
	admin, err := sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Done()
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	// Restore while the borrowed operation and the prior owner are still live,
	// including when the body terminates through testing.T.Fatal.
	defer func() {
		restore, err := parser.Parse("ALTER SCHEMA public OWNER TO " + quote(prior.Name))
		if err != nil {
			t.Error("parsing public schema owner restoration:", err)
			return
		}
		if len(restore) != 1 {
			t.Errorf("expected single public schema owner restoration statement, got %d", len(restore))
			return
		}
		if _, err := e.Execute(admin, restore[0], 0, nil); err != nil {
			t.Error("restoring public schema owner:", err)
		}
	}()
	enginetest.RoleExecute(t, e, admin, "ALTER SCHEMA public OWNER TO "+quote(owner))
	body()
}

// testSessionID numbers the sessions this package's tests start directly. It
// starts above the IDs the enginetest helpers mint, so a test that uses both
// never registers a duplicate advisory session.
var testSessionID atomic.Uint64

func init() { testSessionID.Store(1 << 20) }
