package rolecmds_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
)

func TestDatabaseSettingsExecutor(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	db, _ := e.CatalogRead().Database(e.Catalog.DatabaseAddress())
	key := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
	s := &parser.AlterDatabaseSettings{Database: parser.Identifier{Text: db.Name()}, Set: &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{"first"}}}
	execute := func() error {
		_, err := rolecmds.ExecuteDatabaseSettings(e, &core.StatementScope{Op: op}, s, 0)
		return err
	}
	preflight := e.RoleStore.Published
	description, err := e.Describe(op, &parser.AlterDatabaseSettings{Database: parser.Identifier{Text: "missing_database"}, Set: &parser.Set{Name: parser.Identifier{Text: "absent_setting"}, Values: []string{"invalid"}}})
	if err != nil || !reflect.DeepEqual(description, core.Description{}) || e.RoleStore.Published != preflight || len(op.Notices) != 0 {
		t.Fatal("Describe resolved authority/value or published effects", description, err)
	}
	if _, err := rolecmds.ExecuteDatabaseSettings(e, &core.StatementScope{Op: op}, s, 1); err == nil || e.RoleStore.Published != preflight {
		t.Fatal("argument count accepted or published", err)
	}
	issuer := op.EffectiveSettings()
	override := "issuer-only"
	if _, err := e.Execute(op, &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{override}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	issuer.Mu.Lock()
	live, defaults := issuer.Value("application_name"), maps.Clone(issuer.Defaults)
	issuer.Mu.Unlock()
	original := e.RoleStore.Published
	if err := execute(); err != nil {
		t.Fatal(err)
	}
	first := e.RoleStore.Published
	row := first.Settings.Lookup(key)
	if first == original || !first.SameAuthority(original) || len(row) != 1 || row[0] != (access.RoleSetting{Name: "application_name", Value: "first"}) || original.Settings.Lookup(key) != nil {
		t.Fatal("database-only immutable publication", row)
	}
	// The older SQL spelling must replace precisely the same owned row.
	_, err = rolecmds.ExecuteRoleSettings(e, &core.StatementScope{Op: op}, &parser.AlterRoleSettings{All: true, Database: &s.Database, Set: &parser.Set{Name: s.Set.Name, Values: []string{"second"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	second := e.RoleStore.Published
	if second.Settings.Lookup(key)[0].Value != "second" || row[0].Value != "first" || &row[0] == &second.Settings.Lookup(key)[0] {
		t.Fatal("shared row replacement mutated backing")
	}
	s.Set = &parser.Set{Name: parser.Identifier{Text: "statement_timeout"}, Values: []string{"invalid"}}
	frontier := e.RoleStore.NextRevision()
	if err := execute(); err == nil || e.RoleStore.Published != second || e.RoleStore.NextRevision() != frontier {
		t.Fatal("validation published partial metadata", err)
	}
	s.Set = nil
	s.Reset = &parser.Reset{All: true}
	if err := execute(); err != nil {
		t.Fatal(err)
	}
	if e.RoleStore.Published.Settings.Lookup(key) != nil || !slices.Equal(second.Settings.Lookup(key), []access.RoleSetting{{Name: "application_name", Value: "second"}}) {
		t.Fatal("last removal retained an empty row or changed predecessor")
	}
	issuer.Mu.Lock()
	unchanged := issuer.Value("application_name") == live && maps.Equal(issuer.Defaults, defaults)
	issuer.Mu.Unlock()
	if !unchanged {
		t.Fatal("persisted mutation changed issuer override/reset image")
	}
	before := e.RoleStore.Published
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.Ctx = ctx
	s.Reset = nil
	s.Set = &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{"canceled"}}
	if err := execute(); !errors.Is(err, context.Canceled) || e.RoleStore.Published != before {
		t.Fatal("canceled execution published", err)
	}
}
