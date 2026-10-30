package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func tempTablespacesUnitError(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Detail != detail || d.Hint != "" {
		t.Fatalf("got %v want %s %q detail=%q", err, code, message, detail)
	}
}

func TestTempTablespacesPreparation(t *testing.T) {
	e := engine.New()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	read := e.Catalog.Current()
	auth := enginetest.CatalogAuthorization(e, read)
	providerDefault := read.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: 1663})
	providerGlobal := read.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: 1664})
	for _, tc := range []struct { value string; want []ids.OID }{
		{"", nil}, {`""`, []ids.OID{0}}, {"pg_default, pg_global, pg_default", []ids.OID{0, 1664, 0}},
		{` PG_DEFAULT , "", "pg_global"`, []ids.OID{0, 0, 1664}},
	} {
		got, err := ddl.PrepareTempTablespaces(auth.View, auth, tc.value, true)
		if err != nil || !slices.Equal(got, tc.want) { t.Fatalf("%q got %v want %v: %v", tc.value, got, tc.want, err) }
	}
	_, err := ddl.PrepareTempTablespaces(auth.View, auth, "missing", true)
	tempTablespacesUnitError(t, err, "42704", `tablespace "missing" does not exist`, "")
	_, err = ddl.PrepareTempTablespaces(auth.View, auth, "pg_default,", true)
	tempTablespacesUnitError(t, err, "22023", `invalid value for parameter "temp_tablespaces": "pg_default,"`, "List syntax is invalid.")
	// A genuine seeded nonsuperuser has no CREATE on pg_global. Default bypass
	// requires no fake destination or injected provider descriptor.
	auth.State.CurrentUser = auth.Roles.LookupRef("pg_signal_backend")
	_, err = ddl.PrepareTempTablespaces(auth.View, auth, "pg_global", true)
	tempTablespacesUnitError(t, err, "42501", "permission denied for tablespace pg_global", "")
	for _, tc := range []struct { value string; want []ids.OID }{
		{"pg_global", nil}, {"missing", nil}, {`missing, pg_default, pg_global, ""`, []ids.OID{0, 0}},
	} {
		got, err := ddl.PrepareTempTablespaces(auth.View, auth, tc.value, false)
		if err != nil || !slices.Equal(got, tc.want) { t.Fatalf("retained %q got %v want %v: %v", tc.value, got, tc.want, err) }
	}
	if e.Catalog.Current().State() != read.State() || read.Get(providerDefault.Address()) != providerDefault || read.Get(providerGlobal.Address()) != providerGlobal {
		t.Fatal("validation mutated actual provider identities")
	}
}

func TestTempTablespacesPublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	ctx, cancel := context.WithCancel(t.Context())
	op, err := s.Acquire(ctx)
	if err != nil { t.Fatal(err) }
	defer op.Done()
	store := s.Settings
	d, _ := e.Settings.Lookup("temp_tablespaces")
	v := settings.Value{Text: "pg_global"}
	candidate := session.NewTempTablespaceAssignment(s.Transactions.Ref, []ids.OID{1664})
	cancel()
	e.Mu.Lock()
	store.Mu.Lock()
	s.Mu.Lock()
	_, err = store.ApplyLocked(op, core.DomainOf(e), e.Settings, d, &v, nil, session.SettingSet)
	if err == nil { store.AcceptTempTablespacesLocked(candidate) }
	s.Mu.Unlock()
	store.Mu.Unlock()
	e.Mu.Unlock()
	if !errors.Is(err, context.Canceled) || store.TempTablespaces != nil || len(store.Actions) != 0 || store.Value("temp_tablespaces") != "" {
		t.Fatal("canceled publication installed assignment/journal", err)
	}
}

func TestTempTablespacesCreationConsumers(t *testing.T) {
	for _, kind := range []string{"table", "two-unique", "identity", "serial"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			op, err := s.Acquire(t.Context())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			e.Mu.Lock()
			defer e.Mu.Unlock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil { t.Fatal(err) }
			before := e.Catalog.Current()
			defaultSpace := before.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: 1663})
			globalSpace := before.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: 1664})
			sequences, indexes := len(e.Storage.Sequences), len(e.Storage.IndexRoots)
			column := &parser.Column{Name: "v", TypeName: parser.TypeName{Name: "int4"}}
			syntax := &parser.CreateTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "tt_unit"}}}, Persistence: "temporary", Elements: []parser.TableElement{{Column: column}}}
			spaces := []ids.OID{1664}
			switch kind {
			case "two-unique":
				syntax.Elements = append(syntax.Elements,
					parser.TableElement{Column: &parser.Column{Name: "w", TypeName: parser.TypeName{Name: "int4"}}},
					parser.TableElement{Unique: &parser.UniqueConstraint{Columns: []parser.Identifier{{Text: "v"}}}},
					parser.TableElement{Unique: &parser.UniqueConstraint{Columns: []parser.Identifier{{Text: "w"}}}})
				spaces = []ids.OID{0, 0, 1664}
			case "identity":
				column.Constraints = []parser.ColumnConstraint{{Identity: &parser.IdentityDefinition{Mode: parser.IdentityAlways}}}
				spaces = []ids.OID{0, 1664}
			case "serial":
				column.TypeName.Name = "serial"
				spaces = []ids.OID{0, 1664}
			}
			a := session.NewTempTablespaceAssignment(s.Transactions.Ref, spaces)
			op.TempTablespaceAssignment = a
			s.Transactions.TempTablespaces.Install(s.Transactions.Ref, a, spaces)
			// Normalize the unspecified initial phase by consuming through the
			// unique global entry. The next entry is deterministically first zero.
			aligned := false
			for range len(spaces) {
				if s.Transactions.TempTablespaces.Next() == 1664 { aligned = true; break }
			}
			if !aligned { t.Fatal("selector omitted global member") }
			journal := session.NewJournal()
			ns, err := e.RelationCreationNamespaceLocked(auth, syntax.Name, "temporary", journal)
			if err != nil { t.Fatal(err) }
			auth.Recapture(e.PrivateCatalogLocked(journal))
			auth.StagedTemporary = ns.Address
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			err = tablecmds.SchemaTableLocked(e, op, auth, stage, syntax, ns.Address, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
			tempTablespacesUnitError(t, err, "22023", "only shared relations can be placed in pg_global tablespace", "")
			if kind != "table" && s.Transactions.TempTablespaces.Next() != 0 { t.Fatal("failed owned consumer did not advance cursor") }
			if e.Catalog.Current().State() != before.State() || e.Catalog.Current().Epoch() != before.Epoch() || len(e.Storage.Sequences) != sequences || len(e.Storage.IndexRoots) != indexes {
				t.Fatal("failed composed staging published catalog/storage effects")
			}
			if before.Get(defaultSpace.Address()) != defaultSpace || before.Get(globalSpace.Address()) != globalSpace { t.Fatal("provider identity replaced") }
			for _, name := range []string{"tt_unit", "tt_unit_v_seq", "tt_unit_v_key", "tt_unit_w_key"} {
				if _, exists := before.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}); exists { t.Fatal("failed fragment published", name) }
			}
		})
	}
}

func TestTempTablespacesRetainedTransaction(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { t.Fatal(err) }
	// Never hand a unit-only incarnation to physical session cleanup.
	defer s.Transactions.Clear()
	auth.State.CurrentUser = auth.Roles.LookupRef("pg_signal_backend")
	old := core.DomainOf(e).Tx(s.SessionToken, 1, auth.Catalog.Epoch(), s.Transactions, 1)
	next := core.DomainOf(e).Tx(s.SessionToken, 2, auth.Catalog.Epoch(), s.Transactions, 2)
	a := session.NewTempTablespaceAssignment(old, []ids.OID{1664})
	op.TempTablespaceAssignment, op.TempTablespaces = a, "pg_global"
	s.Transactions.Ref = next
	h := core.DDLHost{E: e, Op: op}
	if err := h.CheckCreationTablespace(auth, "temporary", false); err != nil { t.Fatal("retained denied member did not fall back", err) }
	if !s.Transactions.TempTablespaces.Current(next, a) { t.Fatal("retained list was not cached") }
	// Same-transaction preparation is cached even if authority changes. The
	// retained fallback must not be re-resolved for each CREATE.
	auth.State.CurrentUser = auth.Roles.LookupRef("postgres")
	if err := h.CheckCreationTablespace(auth, "temporary", false); err != nil { t.Fatal("policy was rebuilt per relation", err) }
	third := core.DomainOf(e).Tx(s.SessionToken, 3, auth.Catalog.Epoch(), s.Transactions, 3)
	s.Transactions.Ref = third
	err = h.CheckCreationTablespace(auth, "temporary", false)
	tempTablespacesUnitError(t, err, "22023", "only shared relations can be placed in pg_global tablespace", "")
}

type tempTablespaceInvocationDefaults struct{}

func (tempTablespaceInvocationDefaults) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	if d.Name == "temp_tablespaces" {
		return "pg_global", true, nil
	}
	return "", false, nil
}

func TestTempTablespacesInvocationPreparation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, tempTablespaceInvocationDefaults{})
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	op.Restricted = &session.RestrictedInvocation{Owner: s.Authorization.State.CurrentUser}
	image := op.EffectiveSettings()
	op.RefreshInvocationSettingsLocked(image)
	if image.TempTablespaces == nil || image.TempTablespaces == s.Settings.TempTablespaces {
		t.Fatal("retained provider text lacks detached policy identity")
	}
	if _, accepted := op.AcceptedTempTablespaces(s.Transactions.Ref); accepted {
		t.Fatal("unprepared invocation reused fabricated accepted data")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { t.Fatal(err) }
	err = (core.DDLHost{E: e, Op: op}).CheckCreationTablespace(auth, "temporary", false)
	tempTablespacesUnitError(t, err, "22023", "only shared relations can be placed in pg_global tablespace", "")
	if !s.Transactions.TempTablespaces.Current(s.Transactions.Ref, image.TempTablespaces) {
		t.Fatal("retained invocation did not prepare its real provider destination")
	}
}
