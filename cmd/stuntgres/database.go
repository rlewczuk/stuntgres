package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Each managed database owns an independent catalog and role set.
// Network identities are allocated by the physical server, bootstrap uses 1.
type database struct {
	engine    *core.Engine
	settings  map[string]struct{} // Immutable definition names, never effective values.
	reference *referenceConfig    // Immutable; non-nil runs every connection in dual-backend mode.
}

type startupDefaults map[string]string

func (d startupDefaults) SessionDefault(_ context.Context, definition settings.Definition) (string, bool, error) {
	v, ok := d[definition.Name]
	return v, ok, nil
}

type startupDefaultValues struct {
	startupDefaults
	applicationName settings.Value
	applicationSet  bool
}

func (d startupDefaultValues) SessionDefaultValue(_ context.Context, definition settings.Definition) (settings.Value, bool, error) {
	if definition.Name == "application_name" {
		return d.applicationName, d.applicationSet, nil
	}
	return settings.Value{}, false, nil
}

type settingNames map[string]struct{}

func (n settingNames) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	n[d.Name] = struct{}{}
	return "", false, nil
}

func newDatabases(ctx context.Context, users accounts, restored []string) (map[string]*database, error) {
	names := make(map[string]bool) // Value marks a selected restore file.
	for _, a := range users {
		for _, name := range a.databases {
			names[name] = false
		}
	}
	for _, name := range restored {
		if err := validName(name, true); err != nil {
			return nil, err
		}
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	databases := make(map[string]*database, len(ordered))
	for _, name := range ordered {
		d := &database{engine: engine.NewWithSettings(nil, name, false), settings: make(map[string]struct{})}
		if err := d.provision(ctx, name, users, names[name]); err != nil {
			return nil, fmt.Errorf("database %q: %w", name, err)
		}
		databases[name] = d
	}
	return databases, nil
}
func allowed(a account, name string) bool {
	for _, db := range a.databases {
		if db == name {
			return true
		}
	}
	return false
}
func (d *database) provision(ctx context.Context, name string, users accounts, restoring bool) (err error) {
	postgres, err := d.engine.ResolveLogin("postgres")
	if err != nil {
		return err
	}
	admission, err := d.engine.AdmitDatabase(ctx, postgres, core.DatabaseSelector{})
	if err != nil {
		return err
	}
	defer admission.Release()
	boot, err := admission.Target().StartSession(ctx, admission, session.SessionStart{ID: 1, Defaults: settingNames(d.settings)})
	if err != nil {
		return err
	}
	defer func() {
		boot.RequestEnd(session.CleanClose)
		err = errors.Join(err, boot.WaitEnd(context.Background()))
	}()
	ordered := make([]string, 0, len(users))
	for username, a := range users {
		// Native dumps retain owners but exclude principals. Seed every known
		// file principal for restored ownership; network admission still checks
		// the unchanged per-database userfile grant before resolving a login.
		if restoring || allowed(a, name) {
			ordered = append(ordered, username)
		}
	}
	sort.Strings(ordered)
	for _, username := range ordered {
		if username == "postgres" {
			continue
		}
		if _, exists := d.engine.Roles().Lookup(username); exists {
			return fmt.Errorf("account %q collides with a predefined role", username)
		}
		if err := bootstrapExecute(ctx, d.engine, boot, "CREATE ROLE "+quoteIdentifier(username)+" LOGIN SUPERUSER"); err != nil {
			return err
		}
	}
	return nil
}
func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
func bootstrapExecute(ctx context.Context, e *core.Engine, sess *session.Session, source string) (err error) {
	op, options, err := sess.AcquireCommand(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return op.CommandError(err)
	}
	defer func() { err = op.CommandError(scope.Finish(err)) }()
	statements, err := parser.ParseWithOptions(source, options)
	if err != nil {
		return err
	}
	if len(statements) != 1 {
		return errors.New("bootstrap requires one statement")
	}
	result, err := scope.Execute(statements[0], 0, nil)
	if result.Rows != nil {
		err = errors.Join(err, result.Rows.Close())
	}
	return err
}
func (d *database) startSession(ctx context.Context, id uint32, user string, defaults startupDefaults) (*session.Session, *sqlstate.Error, error) {
	for name := range defaults {
		if _, ok := d.settings[name]; !ok {
			return nil, nil, &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "42704", Message: fmt.Sprintf(`unrecognized configuration parameter "%s"`, name)}
		}
	}
	principal, err := d.engine.ResolveLogin(user)
	if err != nil {
		return nil, nil, err
	}
	admission, err := d.engine.AdmitDatabase(ctx, principal, core.DatabaseSelector{})
	if err != nil {
		return nil, nil, err
	}
	defer admission.Release()
	values := startupDefaultValues{startupDefaults: defaults}
	var notice *sqlstate.Error
	if raw, present := defaults["application_name"]; present {
		values.applicationName, notice, err = settings.StartupApplicationName(raw)
		if err != nil {
			return nil, notice, err
		}
		values.applicationSet = true
	}
	s, err := admission.Target().StartSession(ctx, admission, session.SessionStart{ID: ids.SessionID(id), Defaults: values})
	return s, notice, err
}

// attachReferences enables dual-backend mode for each configured database.
func attachReferences(databases map[string]*database, references map[string]referenceConfig) error {
	for name, c := range references {
		d, ok := databases[name]
		if !ok {
			return fmt.Errorf("reference for unknown database %q", name)
		}
		d.reference = &c
	}
	return nil
}
func databaseNames(databases map[string]*database) []string {
	names := make([]string, 0, len(databases))
	for name := range databases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
