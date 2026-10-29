package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestLanguageValidatorRemovalOwnership(t *testing.T) {
	for _, retired := range []bool{false, true} {
		name := "present_validator"
		if retired {
			name = "retired_validator"
		}
		t.Run(name, func(t *testing.T) {
			source, destination := engine.New(), engine.New()
			owner := enginetest.StartSession(t, source, nil)
			for _, q := range []string{
				"CREATE TRUSTED LANGUAGE removal_language HANDLER pg_catalog.plpgsql_call_handler INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator",
				"CREATE FUNCTION pg_catalog.removal_checked(x integer) RETURNS integer LANGUAGE removal_language AS 'BEGIN RETURN x+2; END'",
				"CREATE OR REPLACE TRUSTED LANGUAGE removal_language HANDLER pg_catalog.plpgsql_call_handler",
				"CREATE FUNCTION pg_catalog.removal_good(x integer) RETURNS integer LANGUAGE removal_language AS 'BEGIN RETURN x+1; END'",
				"CREATE FUNCTION pg_catalog.removal_bad() RETURNS integer LANGUAGE removal_language AS 'BEGIN RETURN 1;'",
				"CREATE FUNCTION pg_catalog.removal_result() RETURNS cstring LANGUAGE removal_language AS 'BEGIN RETURN NULL; END'",
			} {
				enginetest.SessionSQL(t, owner, q)
			}
			pl, err := catalog.LookupLanguage(source.CatalogRead(), "plpgsql")
			if err != nil {
				t.Fatal(err)
			}
			validator := pl.Validator
			if retired {
				for _, q := range []string{
					"CREATE OR REPLACE TRUSTED LANGUAGE plpgsql HANDLER pg_catalog.plpgsql_call_handler INLINE pg_catalog.plpgsql_inline_handler",
					"ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_validator(oid)",
					"DROP FUNCTION pg_catalog.plpgsql_validator(oid)",
					"CREATE FUNCTION pg_catalog.plpgsql_validator(oid) RETURNS integer LANGUAGE sql AS 'SELECT 7'",
				} {
					enginetest.SessionSQL(t, owner, q)
				}
			}
			names := []string{"removal_checked", "removal_good", "removal_bad", "removal_result"}
			bodies := []string{"BEGIN RETURN x+2; END", "BEGIN RETURN x+1; END", "BEGIN RETURN 1;", "BEGIN RETURN NULL; END"}
			find := func(read catalog.Read, name string) *catalog.Procedure {
				t.Helper()
				for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, name) {
					return d
				}
				t.Fatal("missing removal replay declaration", name)
				return nil
			}
			original := source.CatalogRead()
			retained := make([]*catalog.Procedure, len(names))
			definitions := make([]*proc.Definition, len(names))
			for i, name := range names {
				retained[i] = find(original, name)
				definitions[i] = retained[i].SQL
			}
			checkedFormal, goodFormal := &definitions[0].Formals[0], &definitions[1].Formals[0]
			if retired && original.Get(validator) != nil {
				t.Fatal("retirement fixture retained genuine validator")
			}
			check := func(e *core.Engine, previous []*catalog.Procedure) []*catalog.Procedure {
				t.Helper()
				read := e.CatalogRead()
				language, err := catalog.LookupLanguage(read, "removal_language")
				if err != nil || !language.Trusted || !language.Procedural || language.Inline != (ids.ObjectAddress{}) || language.Validator != (ids.ObjectAddress{}) {
					t.Fatal("replay lost final zero optional language routes", language, err)
				}
				call, ok := read.Procedure(language.Call)
				if !ok || call.PLHandler != catalog.PLCallHandler || catalog.ValidatePLHandler(read, call) != nil {
					t.Fatal("replay lost genuine current call authority")
				}
				validators := 0
				for _, descriptor := range read.Descriptors() {
					if d, ok := descriptor.(*catalog.Procedure); ok && d.PLHandler == catalog.PLValidator {
						validators++
						if retired || d.Name() != "plpgsql_validator" || catalog.ValidatePLHandler(read, d) != nil {
							t.Fatal("replay resurrected retired or changed genuine validator", d)
						}
					}
				}
				if !retired && validators != 1 {
					t.Fatal("replay lost genuine validator")
				}
				if retired {
					installed, err := catalog.LookupLanguage(read, "plpgsql")
					if err != nil || installed.Origin == nil || !installed.Origin.Valid(installed.Address()) || call.Origin == nil || installed.Origin.Installation.Cohort != call.Origin.Installation.Cohort || installed.Validator != (ids.ObjectAddress{}) {
						t.Fatal("validator retirement lost surviving installed language provenance", installed, err)
					}
					replacement := find(read, "plpgsql_validator")
					if replacement.PLHandler != 0 || replacement.SQL == nil || replacement.SQL.Kind != proc.SQLBody || replacement.SQL.Source != "SELECT 7" {
						t.Fatal("replay confused same-name SQL replacement with retired genuine validator")
					}
				}
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				current := make([]*catalog.Procedure, len(names))
				for i, name := range names {
					d := find(read, name)
					current[i] = d
					definition := d.SQL
					if definition == nil || definition.Source != bodies[i] || definition.Kind != proc.PLpgSQLBody || d.Language != language.Address() {
						t.Fatal("replay lost declaration/source identity", name)
					}
					if err := (stored.ProcedureProvider{}).Validate(read, d); err != nil {
						t.Fatal("stored admission rejected removed-validator state", name, err)
					}
					if i == 0 {
						if definition.PL == nil || definition.PLSyntax == nil || definition.PL.ValidateDefinition(definition) != nil {
							t.Fatal("validator removal silently converted checked declaration to source-only")
						}
					} else if definition.PL != nil || definition.PLSyntax != nil {
						t.Fatal("replay globally compiled source-only sibling", name)
					}
					if read.State() != original.State() && (d == retained[i] || definition == definitions[i] || i == 0 && definition.PL == definitions[i].PL) {
						t.Fatal("replacement borrowed original descriptor/definition/program backing", name)
					}
					if previous != nil && (d == previous[i] || definition == previous[i].SQL || i == 0 && definition.PL == previous[i].SQL.PL) {
						t.Fatal("repeated replay borrowed previous backing", name)
					}
					if e != source && d.Owner == retained[i].Owner {
						t.Fatal("independent replay borrowed source principal", name)
					}
					if read.State() != original.State() && (i == 0 && &definition.Formals[0] == checkedFormal || i == 1 && &definition.Formals[0] == goodFormal) {
						t.Fatal("replacement borrowed original formal backing", name)
					}
					e.Mu.Lock()
					auth, compileErr := e.AuthorizationLocked(op)
					var executable *catalog.Procedure
					if compileErr == nil {
						executable, compileErr = e.PreparePLRoutineLocked(op, auth, d)
					}
					e.Mu.Unlock()
					if i < 2 {
						if compileErr != nil || executable == nil || executable.SQL.PL == nil || executable.SQL.PL.ValidateDefinition(executable.SQL) != nil {
							t.Fatal("valid checked/deferred consumer failed", name, compileErr)
						}
						if i == 1 && (executable == d || executable.SQL == definition || &executable.SQL.Formals[0] == &definition.Formals[0]) {
							t.Fatal("deferred consumer borrowed published backing")
						}
					} else {
						code, message := "42601", "syntax error at end of input"
						if i == 3 {
							code, message = "0A000", "PL/pgSQL functions cannot return type cstring"
						}
						var diagnostic *sqlstate.Error
						if executable != nil || !errors.As(compileErr, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != "" || diagnostic.Context != "compilation of PL/pgSQL function \""+name+"\" near line 1" {
							t.Fatal("source-only reached diagnostic changed", name, compileErr)
						}
						if i == 2 && (diagnostic.InternalPosition != "16" || diagnostic.InternalQuery != bodies[2]) {
							t.Fatal("malformed source lost internal coordinates", diagnostic)
						}
					}
					if e.CatalogRead().State() != read.State() || read.Get(d.Address()) != d || d.SQL != definition || i > 0 && (definition.PL != nil || definition.PLSyntax != nil) {
						t.Fatal("reached compilation changed published declaration generation/backing", name)
					}
				}
				return current
			}
			check(source, nil)
			var saved bytes.Buffer
			if err := engine.Dump(context.Background(), source, &saved); err != nil {
				t.Fatal(err)
			}
			atomic := func(e *core.Engine) {
				t.Helper()
				before := e.CatalogRead()
				snapshots := make([]catalog.Procedure, len(names))
				payloads := make([]proc.Definition, len(names))
				for i, name := range names {
					d := find(before, name)
					snapshots[i], payloads[i] = *d, *d.SQL
				}
				if strings.Count(saved.String(), bodies[0]) != 1 {
					t.Fatal("checked corruption body is not unique")
				}
				corrupt := strings.Replace(saved.String(), bodies[0], bodies[2], 1)
				var diagnostic *sqlstate.Error
				if err := engine.Restore(context.Background(), e, strings.NewReader(corrupt)); !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at end of input" {
					t.Fatal("removed validator suppressed checked-body Restore failure", err)
				}
				if err := engine.Restore(context.Background(), e, strings.NewReader(saved.String()+"; CREATE LANGUAGE removal_invalid HANDLER removal_absent;")); err == nil {
					t.Fatal("late-invalid removed-validator restore accepted")
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := engine.Restore(ctx, e, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled removed-validator restore", err)
				}
				if e.CatalogRead().State() != before.State() {
					t.Fatal("failed restore changed destination generation")
				}
				for i, name := range names {
					d := find(before, name)
					if e.CatalogRead().Get(d.Address()) != d || d.ObjectRevision != snapshots[i].ObjectRevision || d.SQL != snapshots[i].SQL || d.SQL.Source != payloads[i].Source || d.SQL.PL != payloads[i].PL || d.SQL.PLSyntax != payloads[i].PLSyntax {
						t.Fatal("failed restore changed descriptor/definition/program backing", name)
					}
					if i < 2 && &d.SQL.Formals[0] != &payloads[i].Formals[0] {
						t.Fatal("failed restore changed formal backing", name)
					}
				}
			}
			atomic(source)
			var previous []*catalog.Procedure
			for range 2 {
				if err := engine.Restore(context.Background(), destination, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				previous = check(destination, previous)
				atomic(destination)
			}
			if source.CatalogRead().State() != original.State() {
				t.Fatal("independent replay changed source generation")
			}
			previous = nil
			for range 2 {
				if err := engine.Reset(context.Background(), source); err != nil {
					t.Fatal(err)
				}
				if err := engine.Restore(context.Background(), source, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				previous = check(source, previous)
				atomic(source)
			}
			for i, d := range retained {
				if original.Get(d.Address()) != d || d.SQL != definitions[i] || d.SQL.Source != bodies[i] || i > 0 && (d.SQL.PL != nil || d.SQL.PLSyntax != nil) {
					t.Fatal("replay mutated retained original backing", names[i])
				}
			}
		})
	}
}
