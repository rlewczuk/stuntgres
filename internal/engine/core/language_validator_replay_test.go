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

func TestLanguageValidatorReplayOwnership(t *testing.T) {
	source, destination := engine.New(), engine.New()
	owner := enginetest.StartSession(t, source, nil)
	enginetest.SessionSQL(t, owner, "CREATE TRUSTED LANGUAGE validator_replay HANDLER pg_catalog.plpgsql_call_handler")
	names := []string{"validator_replay_bad", "validator_replay_result", "validator_replay_argument", "validator_replay_good", "validator_replay_checked"}
	bodies := []string{"BEGIN RETURN 1;", "BEGIN RETURN NULL; END", "BEGIN RETURN NULL; END", "BEGIN RETURN x+1; END", "BEGIN RETURN x+2; END"}
	for _, q := range []string{
		"CREATE FUNCTION pg_catalog.validator_replay_bad() RETURNS integer LANGUAGE validator_replay AS 'BEGIN RETURN 1;'",
		"CREATE FUNCTION pg_catalog.validator_replay_result() RETURNS cstring LANGUAGE validator_replay AS 'BEGIN RETURN NULL; END'",
		"CREATE FUNCTION pg_catalog.validator_replay_argument(x cstring) RETURNS integer LANGUAGE validator_replay AS 'BEGIN RETURN NULL; END'",
		"CREATE FUNCTION pg_catalog.validator_replay_good(x integer) RETURNS integer LANGUAGE validator_replay AS 'BEGIN RETURN x+1; END'",
		"CREATE OR REPLACE TRUSTED LANGUAGE validator_replay HANDLER pg_catalog.plpgsql_call_handler INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator",
		"CREATE FUNCTION pg_catalog.validator_replay_checked(x integer) RETURNS integer LANGUAGE validator_replay AS 'BEGIN RETURN x+2; END'",
	} {
		enginetest.SessionSQL(t, owner, q)
	}
	find := func(read catalog.Read, name string) *catalog.Procedure {
		t.Helper()
		for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, name) {
			return d
		}
		t.Fatal("missing validator replay declaration", name)
		return nil
	}
	original := source.CatalogRead()
	retained := make([]*catalog.Procedure, len(names))
	definitions := make([]*proc.Definition, len(names))
	for i, name := range names {
		retained[i] = find(original, name)
		definitions[i] = retained[i].SQL
	}
	formal := &definitions[2].Formals[0]
	language, err := catalog.LookupLanguage(original, "validator_replay")
	if err != nil {
		t.Fatal(err)
	}
	check := func(e *core.Engine, previous []*catalog.Procedure) []*catalog.Procedure {
		t.Helper()
		read := e.CatalogRead()
		currentLanguage, err := catalog.LookupLanguage(read, "validator_replay")
		if err != nil || !currentLanguage.Trusted || !currentLanguage.Procedural {
			t.Fatal("replay lost final language identity/trust", currentLanguage, err)
		}
		pl, err := catalog.LookupLanguage(read, "plpgsql")
		if err != nil || currentLanguage.Call != pl.Call || currentLanguage.Inline != pl.Inline || currentLanguage.Validator != pl.Validator || currentLanguage.Validator == (ids.ObjectAddress{}) {
			t.Fatal("replay failed to restore final genuine language routes", currentLanguage, err)
		}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		current := make([]*catalog.Procedure, len(names))
		for i, name := range names {
			d := find(read, name)
			current[i] = d
			definition := d.SQL
			if definition == nil || definition.Kind != proc.PLpgSQLBody || definition.Source != bodies[i] || d.Language != currentLanguage.Address() {
				t.Fatal("replay lost declaration source or language identity", name)
			}
			if err := (stored.ProcedureProvider{}).Validate(read, d); err != nil {
				t.Fatal("stored admission rejected legitimate validator replay state", name, err)
			}
			if i == 4 {
				if definition.PL == nil || definition.PLSyntax == nil || definition.PL.ValidateDefinition(definition) != nil {
					t.Fatal("replay suppressed genuine checked declaration reconstruction")
				}
			} else if definition.PL != nil || definition.PLSyntax != nil {
				t.Fatal("later validator eagerly changed source-only declaration admission", name)
			}
			if read.State() != original.State() && (d == retained[i] || definition == definitions[i]) {
				t.Fatal("replacement borrowed original descriptor/definition backing", name)
			}
			if e != source && d.Owner == retained[i].Owner {
				t.Fatal("independent replay borrowed source principal", name)
			}
			if previous != nil && (d == previous[i] || definition == previous[i].SQL) {
				t.Fatal("repeated replay borrowed previous published backing", name)
			}
			if i == 2 && read.State() != original.State() && &definition.Formals[0] == formal {
				t.Fatal("replacement borrowed original formal backing")
			}
			e.Mu.Lock()
			auth, compileErr := e.AuthorizationLocked(op)
			var executable *catalog.Procedure
			if compileErr == nil {
				executable, compileErr = e.PreparePLRoutineLocked(op, auth, d)
			}
			e.Mu.Unlock()
			if i < 3 {
				var diagnostic *sqlstate.Error
				code, message := "0A000", "PL/pgSQL functions cannot return type cstring"
				if i == 0 {
					code, message = "42601", "syntax error at end of input"
				} else if i == 2 {
					message = "PL/pgSQL functions cannot accept type cstring"
				}
				if executable != nil || !errors.As(compileErr, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != "" || diagnostic.Context != "compilation of PL/pgSQL function \""+name+"\" near line 1" {
					t.Fatal("reached restored source lost exact deferred diagnostic", name, executable, compileErr)
				}
				if i == 0 && (diagnostic.InternalPosition != "16" || diagnostic.InternalQuery != bodies[0]) {
					t.Fatal("malformed integer source lost internal diagnostic coordinates", diagnostic)
				}
			} else if compileErr != nil || executable == nil || executable.SQL.PL == nil || executable.SQL.PL.ValidateDefinition(executable.SQL) != nil {
				t.Fatal("valid restored consumer could not compile", name, compileErr)
			} else if i == 3 && (executable == d || executable.SQL == definition || &executable.SQL.Formals[0] == &definition.Formals[0]) {
				t.Fatal("valid deferred consumer borrowed published executable/formals")
			}
			if e.CatalogRead().State() != read.State() || read.Get(d.Address()) != d || d.SQL != definition || i < 4 && (definition.PL != nil || definition.PLSyntax != nil) {
				t.Fatal("private reached compilation changed published declaration/generation", name)
			}
		}
		return current
	}
	check(source, nil)
	// The only capture occurs after validator addition and checked creation.
	var saved bytes.Buffer
	if err := engine.Dump(context.Background(), source, &saved); err != nil {
		t.Fatal(err)
	}
	assertAtomic := func(e *core.Engine) {
		t.Helper()
		before := e.CatalogRead()
		descriptors := make([]catalog.Procedure, len(names))
		snapshots := make([]proc.Definition, len(names))
		for i, name := range names {
			d := find(before, name)
			descriptors[i], snapshots[i] = *d, *d.SQL
		}
		// Corrupt only the checked body. Source-only replay must not turn off
		// its ordinary admission merely because it shares this language.
		if strings.Count(saved.String(), bodies[4]) != 1 {
			t.Fatal("checked body corruption witness is not unique")
		}
		corrupt := strings.Replace(saved.String(), bodies[4], bodies[0], 1)
		var diagnostic *sqlstate.Error
		if err := engine.Restore(context.Background(), e, strings.NewReader(corrupt)); !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at end of input" {
			t.Fatal("checked malformed replay was globally suppressed", err)
		}
		if e.CatalogRead().State() != before.State() {
			t.Fatal("checked replay failure replaced published catalog generation")
		}
		if err := engine.Restore(context.Background(), e, strings.NewReader(saved.String()+"; CREATE LANGUAGE validator_replay_invalid HANDLER absent_validator_handler;")); err == nil {
			t.Fatal("late-invalid restore accepted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := engine.Restore(ctx, e, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled validator replay", err)
		}
		if e.CatalogRead().State() != before.State() {
			t.Fatal("failed or canceled restore changed published catalog generation")
		}
		for i, name := range names {
			d := find(before, name)
			snapshot := &snapshots[i]
			if e.CatalogRead().Get(d.Address()) != d || d.SQL != descriptors[i].SQL || d.ObjectRevision != descriptors[i].ObjectRevision || d.SQL.Source != snapshot.Source || d.SQL.PL != snapshot.PL || d.SQL.PLSyntax != snapshot.PLSyntax {
				t.Fatal("failed restore replaced retained descriptor/backing", name)
			}
			if i >= 2 && &d.SQL.Formals[0] != &snapshot.Formals[0] {
				t.Fatal("failed restore replaced retained formal backing", name)
			}
		}
	}
	var previous []*catalog.Procedure
	for range 2 {
		if err := engine.Restore(context.Background(), destination, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal("restore after validator addition", err)
		}
		previous = check(destination, previous)
		assertAtomic(destination)
	}
	if source.CatalogRead().State() != original.State() || original.Get(language.Address()) != language {
		t.Fatal("independent replay changed source generation/language")
	}
	previous = nil
	for range 2 {
		if err := engine.Reset(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		if err := engine.Restore(context.Background(), source, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal("source reset/restore after validator addition", err)
		}
		previous = check(source, previous)
		assertAtomic(source)
	}
	for i, d := range retained {
		if original.Get(d.Address()) != d || d.SQL != definitions[i] || i < 4 && (d.SQL.PL != nil || d.SQL.PLSyntax != nil) {
			t.Fatal("replacement mutated retained original declaration backing", names[i])
		}
	}
	if &definitions[2].Formals[0] != formal {
		t.Fatal("replacement mutated retained original formals")
	}
}
