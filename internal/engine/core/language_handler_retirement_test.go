package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestLanguageHandlerRetirementOwnership(t *testing.T) {
	for _, combined := range []bool{false, true} {
		for _, renamed := range []bool{false, true} {
			name := "inline"
			if combined {
				name = "inline_and_validator"
			}
			if renamed {
				name += "_parked"
			} else {
				name += "_current"
			}
			t.Run(name, func(t *testing.T) {
				source, destination := engine.New(), engine.New()
				seed := source.CatalogRead()
				baseline, err := catalog.LookupLanguage(seed, "plpgsql")
				if err != nil {
					t.Fatal(err)
				}
				owner := enginetest.StartSession(t, source, nil)
				for _, q := range []string{"CREATE ROLE handler_owner", "CREATE ROLE handler_reader", "GRANT USAGE,CREATE ON SCHEMA public TO handler_reader", "CREATE FUNCTION public.handler_checked(x integer) RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN x+2; END'"} {
					enginetest.SessionSQL(t, owner, q)
				}
				language := "plpgsql"
				if renamed {
					enginetest.SessionSQL(t, owner, "ALTER LANGUAGE plpgsql RENAME TO handler_retired_pl")
					language = "handler_retired_pl"
				}
				definition := "CREATE OR REPLACE TRUSTED LANGUAGE " + language + " HANDLER pg_catalog.plpgsql_call_handler"
				if !combined {
					definition += " VALIDATOR pg_catalog.plpgsql_validator"
				}
				for _, q := range []string{definition, "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_inline_handler(internal)", "DROP FUNCTION pg_catalog.plpgsql_inline_handler(internal)"} {
					enginetest.SessionSQL(t, owner, q)
				}
				if combined {
					for _, q := range []string{"ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_validator(oid)", "DROP FUNCTION pg_catalog.plpgsql_validator(oid)"} {
						enginetest.SessionSQL(t, owner, q)
					}
				}
				for _, q := range []string{
					"CREATE FUNCTION public.handler_fn(x integer) RETURNS integer LANGUAGE " + language + " AS 'BEGIN RETURN x+1; END'",
					"REVOKE USAGE ON LANGUAGE " + language + " FROM PUBLIC",
					"GRANT USAGE ON LANGUAGE " + language + " TO handler_reader WITH GRANT OPTION",
					"ALTER LANGUAGE " + language + " OWNER TO handler_owner",
					"ALTER FUNCTION public.handler_checked(integer) OWNER TO handler_reader",
					"ALTER FUNCTION public.handler_fn(integer) OWNER TO handler_reader",
				} {
					enginetest.SessionSQL(t, owner, q)
				}
				find := func(read catalog.Read, name string) *catalog.Procedure {
					t.Helper()
					namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
					for d := range catalog.DefinitionProceduresNamedAt(read, nil, namespace, name) {
						return d
					}
					t.Fatal("missing handler retirement consumer", name)
					return nil
				}
				type snapshot struct {
					language   *catalog.Language
					value      catalog.Language
					procedures [2]*catalog.Procedure
				}
				check := func(e *core.Engine, previous *snapshot) snapshot {
					t.Helper()
					read := e.CatalogRead()
					l, err := catalog.LookupLanguage(read, language)
					if err != nil || !l.Trusted || !l.Procedural || l.Inline != (ids.ObjectAddress{}) || l.Origin == nil || !l.Origin.Valid(l.Address()) || l.Origin.Declaration != 3 {
						t.Fatal("retirement lost genuine installed language/final routes", l, err)
					}
					call, ok := read.Procedure(l.Call)
					if !ok || call.PLHandler != catalog.PLCallHandler || catalog.ValidatePLHandler(read, call) != nil || call.Origin == nil || call.Origin.Installation != l.Origin.Installation {
						t.Fatal("surviving call capability split from genuine cohort")
					}
					ext, ok := read.Get(l.Extension).(*catalog.Extension)
					wantMembers := 3
					if combined {
						wantMembers = 2
					}
					if !ok || ext.Installation != l.Origin.Installation || len(ext.Members) != wantMembers || !slices.Contains(ext.Members, l.Address()) || !slices.Contains(ext.Members, l.Call) {
						t.Fatal("retirement lost reciprocal surviving membership")
					}
					released, ok := l.Origin.Installation.Declarations[3].(*catalog.Language)
					if !ok || released.Inline == (ids.ObjectAddress{}) || released.Validator == (ids.ObjectAddress{}) || len(l.Origin.Installation.Members) != 4 {
						t.Fatal("retirement rewrote immutable recipe")
					}
					inlineCount, validatorCount := 0, 0
					for _, descriptor := range read.Descriptors() {
						if d, ok := descriptor.(*catalog.Procedure); ok {
							if d.PLHandler == catalog.PLInlineHandler {
								inlineCount++
							}
							if d.PLHandler == catalog.PLValidator {
								validatorCount++
							}
						}
					}
					if inlineCount != 0 {
						t.Fatal("retired inline handler resurrected")
					}
					if combined {
						if l.Validator != (ids.ObjectAddress{}) || validatorCount != 0 {
							t.Fatal("combined retired validator resurrected")
						}
					} else {
						v, ok := read.Procedure(l.Validator)
						if !ok || validatorCount != 1 || v.PLHandler != catalog.PLValidator || catalog.ValidatePLHandler(read, v) != nil || v.Origin == nil || v.Origin.Installation != l.Origin.Installation || !slices.Contains(ext.Members, v.Address()) {
							t.Fatal("inline retirement lost retained genuine validator")
						}
					}
					if err := (catalog.LanguageProvider{}).Validate(read, l); err != nil {
						t.Fatal(err)
					}
					op, done := enginetest.RoleOperation(t, e, "postgres")
					defer done()
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(op)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					languageOwner, ok := auth.Roles.Lookup("handler_owner")
					if !ok || l.Owner != languageOwner.Ref || !l.ACL.Explicit || len(l.ACL.Entries) != 2 {
						t.Fatal("retirement lost language owner or explicit ACL")
					}
					reader, ok := auth.Roles.Lookup("handler_reader")
					if !ok {
						t.Fatal("missing reader")
					}
					for _, entry := range l.ACL.Entries {
						if entry.Grantor != l.Owner {
							t.Fatal("retirement lost ACL grantor ownership")
						}
					}
					result := snapshot{language: l, value: *l}
					result.value.ACL.Entries = slices.Clone(l.ACL.Entries)
					for i, name := range []string{"handler_checked", "handler_fn"} {
						d := find(read, name)
						result.procedures[i] = d
						if d.Owner != reader.Ref || d.Language != l.Address() || d.SQL == nil {
							t.Fatal("consumer lost current language/owner")
						}
						if err := (stored.ProcedureProvider{}).Validate(read, d); err != nil {
							t.Fatal(err)
						}
						deferred := combined && i == 1
						if deferred {
							if d.SQL.PL != nil || d.SQL.PLSyntax != nil {
								t.Fatal("combined replay eagerly checked source-only consumer")
							}
						} else if d.SQL.PL == nil || d.SQL.PLSyntax == nil || d.SQL.PL.ValidateDefinition(d.SQL) != nil {
							t.Fatal("retirement lost genuine checked program")
						}
						e.Mu.Lock()
						executable, err := e.PreparePLRoutineLocked(op, auth, d)
						e.Mu.Unlock()
						if err != nil || executable == nil || executable.SQL.PL == nil || executable.SQL.PL.ValidateDefinition(executable.SQL) != nil {
							t.Fatal("retired-handler consumer lost actual call capability", err)
						}
						if e.CatalogRead().State() != read.State() || read.Get(d.Address()) != d || deferred && (d.SQL.PL != nil || d.SQL.PLSyntax != nil || executable == d || executable.SQL == d.SQL || &executable.SQL.Formals[0] == &d.SQL.Formals[0]) {
							t.Fatal("private reached compilation changed/borrowed published backing")
						}
						if previous != nil && (d == previous.procedures[i] || d.SQL == previous.procedures[i].SQL || !deferred && d.SQL.PL == previous.procedures[i].SQL.PL) {
							t.Fatal("repeated restore borrowed old consumer backing")
						}
					}
					if previous != nil && (l == previous.language || l.Origin == previous.language.Origin || l.Origin.Installation == previous.language.Origin.Installation || &l.ACL.Entries[0] == &previous.language.ACL.Entries[0] || !reflect.DeepEqual(*previous.language, previous.value)) {
						t.Fatal("repeated restore borrowed/mutated old language/ACL/cohort")
					}
					return result
				}
				original := source.CatalogRead()
				retained := check(source, nil)
				if retained.language.Address() != baseline.Address() || retained.language.Origin != baseline.Origin || seed.Get(baseline.Address()) != baseline || baseline.Inline == (ids.ObjectAddress{}) || baseline.Validator == (ids.ObjectAddress{}) {
					t.Fatal("source retirement mutated retained bootstrap identity/recipe")
				}
				var saved bytes.Buffer
				if err := engine.Dump(context.Background(), source, &saved); err != nil {
					t.Fatal(err)
				}
				targetOwner := enginetest.StartSession(t, destination, nil)
				for _, q := range []string{"CREATE ROLE handler_owner", "CREATE ROLE handler_reader", "CREATE TABLE public.handler_filler(v integer)"} {
					enginetest.SessionSQL(t, targetOwner, q)
				}
				atomic := func(e *core.Engine) {
					t.Helper()
					before := e.CatalogRead()
					l, err := catalog.LookupLanguage(before, language)
					if err != nil {
						t.Fatal(err)
					}
					value := *l
					value.ACL.Entries = slices.Clone(l.ACL.Entries)
					d := find(before, "handler_checked")
					definition := d.SQL
					program, syntax, formal := definition.PL, definition.PLSyntax, &definition.Formals[0]
					if strings.Count(saved.String(), "BEGIN RETURN x+2; END") != 1 {
						t.Fatal("checked corruption body not unique")
					}
					corrupt := strings.Replace(saved.String(), "BEGIN RETURN x+2; END", "BEGIN RETURN 1;", 1)
					var diagnostic *sqlstate.Error
					if err := engine.Restore(context.Background(), e, strings.NewReader(corrupt)); !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at end of input" {
						t.Fatal("checked corrupted restore failed to retain genuine validator admission", err)
					}
					if err := engine.Restore(context.Background(), e, strings.NewReader(saved.String()+"; CREATE LANGUAGE handler_invalid HANDLER handler_absent;")); err == nil {
						t.Fatal("late-invalid retired-handler restore accepted")
					}
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					if err := engine.Restore(ctx, e, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
						t.Fatal("canceled retired-handler restore", err)
					}
					if e.CatalogRead().State() != before.State() || before.Get(l.Address()) != l || !reflect.DeepEqual(*l, value) || e.CatalogRead().Get(d.Address()) != d || d.SQL != definition || definition.PL != program || definition.PLSyntax != syntax || &definition.Formals[0] != formal {
						t.Fatal("failed restore changed retained generation/language/ACL/cohort/program/formals")
					}
				}
				for _, same := range []bool{false, true} {
					e := destination
					if same {
						e = source
					}
					var previous *snapshot
					for range 2 {
						if same {
							if err := engine.Reset(context.Background(), e); err != nil {
								t.Fatal(err)
							}
						}
						if err := engine.Restore(context.Background(), e, bytes.NewReader(saved.Bytes())); err != nil {
							t.Fatal(err)
						}
						current := check(e, previous)
						if current.language == retained.language || current.language.Origin.Installation == retained.language.Origin.Installation || &current.language.ACL.Entries[0] == &retained.language.ACL.Entries[0] || !same && current.language.Owner == retained.language.Owner {
							t.Fatal("replay borrowed original language/principal/ACL/installation")
						}
						for i, d := range current.procedures {
							if d == retained.procedures[i] || d.SQL == retained.procedures[i].SQL || &d.SQL.Formals[0] == &retained.procedures[i].SQL.Formals[0] {
								t.Fatal("replay borrowed original routine/formals")
							}
						}
						previous = &current
						atomic(e)
					}
					if !same && source.CatalogRead().State() != original.State() {
						t.Fatal("independent handler replay changed source publication generation")
					}
				}
				if original.Get(retained.language.Address()) != retained.language || !reflect.DeepEqual(*retained.language, retained.value) {
					t.Fatal("replay mutated original installed language backing")
				}
			})
		}
	}
	// The same shared rule must not resurrect a language that did not survive.
	t.Run("absent_language_call_retired", func(t *testing.T) {
		source, destination := engine.New(), engine.New()
		owner := enginetest.StartSession(t, source, nil)
		for _, q := range []string{"ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql", "DROP LANGUAGE plpgsql", "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()", "DROP FUNCTION pg_catalog.plpgsql_call_handler()"} {
			enginetest.SessionSQL(t, owner, q)
		}
		var saved bytes.Buffer
		if err := engine.Dump(context.Background(), source, &saved); err != nil {
			t.Fatal(err)
		}
		for _, e := range []*core.Engine{destination, source} {
			for range 2 {
				if err := engine.Restore(context.Background(), e, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				read := e.CatalogRead()
				if l, err := catalog.LookupLanguage(read, "plpgsql"); err == nil || l != nil {
					t.Fatal("missing-language guard resurrected installed language")
				}
				for _, descriptor := range read.Descriptors() {
					if d, ok := descriptor.(*catalog.Procedure); ok && d.PLHandler == catalog.PLCallHandler {
						t.Fatal("retired call handler resurrected without language")
					}
				}
			}
		}
	})
}
