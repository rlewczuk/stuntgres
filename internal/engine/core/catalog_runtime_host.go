package core

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// CatalogRuntimeFields are the inputs of one catalog source runtime: the
// engine, the captured authority, the session registries and settings the
// selected producer reads, and its presentation.
type CatalogRuntimeFields struct {
	Engine             *Engine
	Auth               AuthorizationView
	Cursors            *cursorRegistry
	PreparedStatements *PreparedRegistry
	Settings           *session.SettingsStore
	Registry           settings.Registry
	Input              sqltype.InputEnvironment
	Database           string
	Temporary          ids.SessionToken
	Presentation       stored.Presentation
	SequenceBase       *catalog.State
	SequenceOwn        *session.Journal
	// engineLocked is a synchronous capture borrow, never inspection state.
	engineLocked bool
}

// catalogRuntime is the core Host of one Runtime. Both live in one
// allocation, as the boxed runtime value did before.
type catalogRuntime struct {
	rt                 catalogrt.Runtime
	engine             *Engine
	auth               AuthorizationView
	cursors            *cursorRegistry
	preparedStatements *PreparedRegistry
	settings           *session.SettingsStore
	registry           settings.Registry
	engineLocked       bool
}

// newCatalogRuntime builds the runtime a catalog producer reads.
func newCatalogRuntime(f CatalogRuntimeFields) *catalogrt.Runtime {
	c := &catalogRuntime{engine: f.Engine, auth: f.Auth, cursors: f.Cursors, preparedStatements: f.PreparedStatements, settings: f.Settings, registry: f.Registry, engineLocked: f.engineLocked}
	c.rt = catalogrt.Runtime{Host: c, View: f.Auth.View, Input: f.Input, Database: f.Database, Temporary: f.Temporary, Presentation: f.Presentation, SequenceBase: f.SequenceBase, SequenceOwn: f.SequenceOwn}
	return &c.rt
}

func (r *catalogRuntime) fields() CatalogRuntimeFields {
	return CatalogRuntimeFields{Engine: r.engine, Auth: r.auth, Cursors: r.cursors, PreparedStatements: r.preparedStatements, Settings: r.settings, Registry: r.registry, Input: r.rt.Input, Database: r.rt.Database, Temporary: r.rt.Temporary, Presentation: r.rt.Presentation, SequenceBase: r.rt.SequenceBase, SequenceOwn: r.rt.SequenceOwn}
}

func (r *catalogRuntime) TemporaryNamespace() ids.ObjectAddress { return r.auth.TemporaryNamespace() }
func (r *catalogRuntime) ReferencePath() ([]ids.ObjectAddress, error) {
	return r.auth.ReferencePath()
}
func (r *catalogRuntime) RestrictedRelations() settings.RestrictedRelationKinds {
	return r.auth.RestrictedRelations()
}
func (r *catalogRuntime) EngineOwner() *catalog.Owner {
	if r.engine == nil {
		return nil
	}
	return &r.engine.Catalog.Owner
}

func CatalogSourceTemporaryOwner(auth AuthorizationView) ids.SessionToken {
	if auth.Session != nil {
		return auth.Session.SessionToken
	}
	return ids.SessionToken{}
}

func (r *catalogRuntime) CursorCount() int { return len(r.cursors.Entries) }
func (r *catalogRuntime) VisitCursors(visit func(pgcatalog.CursorFact) error) error {
	for name, cursor := range r.cursors.Entries {
		if err := visit(pgcatalog.CursorFact{name, cursor.statement, cursor.hold, cursor.scroll, cursor.created}); err != nil {
			return err
		}
	}
	return nil
}
func (r *catalogRuntime) PreparedStatementCount() int {
	count := len(r.preparedStatements.definitions)
	if r.preparedStatements.Lookup("") != nil {
		count--
	}
	return count
}
func (r *catalogRuntime) VisitPreparedStatements(ctx context.Context, visit func(pgcatalog.PreparedStatementFact) error) error {
	for name, entry := range r.preparedStatements.Definitions() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "" {
			continue
		}
		d := entry.Description
		if err := visit(pgcatalog.PreparedStatementFact{
			Name: name, Statement: entry.Source, Created: entry.Created, FromSQL: entry.FromSQL,
			ParameterCount: d.Parameters.Count, ParameterTypes: d.Parameters.Types,
			ParameterOIDs: entry.ParameterOIDs, Results: d.Results,
			HasResults: session.ProtocolHasRows(entry.Body, d.Results),
		}); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (r *catalogRuntime) SessionSettings(ctx context.Context) ([]pgcatalog.SettingsFact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	readRestricted, err := r.auth.canReadRestrictedSettings()
	if err != nil {
		return nil, err
	}
	s := r.settings
	s.Mu.Lock()
	facts := make([]pgcatalog.SettingsFact, 0, len(r.registry.Order()))
	err = s.VisitDefinitionsLocked(r.registry, func(d settings.Definition) error {
		name := d.Name
		if d.ReadRestricted && !readRestricted {
			return nil
		}
		value := s.TypedValue(name)
		switch name {
		case "is_superuser":
			text, err := r.auth.settingValue(s, name)
			if err != nil {
				return err
			}
			value = settings.Value{Boolean: text == "on"}
		case "transaction_isolation":
			if s.CurrentIsolation != "" {
				value = settings.Value{Text: s.CurrentIsolation}
			}
		case "transaction_read_only":
			value = settings.Value{Boolean: s.CurrentReadOnly}
		case "transaction_deferrable":
			value = settings.Value{Boolean: s.CurrentDeferrable}
		}
		source := "default"
		if d.NativeStartup != nil && !d.NativeStartup.ResetNull {
			source = "override"
			if d.NativeStartup.Source != "" {
				source = d.NativeStartup.Source
			}
		}
		if captured, present := s.DefaultSources[name]; present {
			source = captured.String()
		}
		if s.ProviderDefaults[name] {
			source = "client"
		}
		if _, present := s.Values[name]; present {
			source = "session"
		}
		facts = append(facts, pgcatalog.SettingsFact{Definition: d, Value: value, Reset: s.DefaultTyped[name], Source: source, ResetCaptured: s.ProviderDefaults[name] || s.DefaultSources[name] != session.DefaultSourceDefault})
		return nil
	})
	s.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}
func (r *catalogRuntime) PreparedCount() int { return r.engine.prepared.Len() }
func (r *catalogRuntime) VisitPreparedTransactions(ctx context.Context, visit func(pgcatalog.PreparedFact) error) error {
	for _, entry := range r.engine.prepared.All() {
		if err := ctx.Err(); err != nil {
			return err
		}
		fact := pgcatalog.PreparedFact{XID: r.engine.Visibility.AssignedXIDLocked(entry.Ref), GID: entry.GID, Prepared: entry.Prepared, Database: entry.Database}
		if role, ok := r.engine.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Find(entry.Owner); ok {
			fact.Owner, fact.OwnerPresent = role.Name, true
		}
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}
func (r *catalogRuntime) ValidateParameter(view catalog.Read, d *catalog.Parameter) error {
	return (role.ParameterProvider{Settings: &r.engine.Settings}).Validate(view, d)
}
