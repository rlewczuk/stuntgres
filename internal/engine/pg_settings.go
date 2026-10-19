package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native relation identity, not a PostgreSQL allocator promise.
const pgSettingsOID ids.OID = 16202

func (e *Engine) initializePGSettings() error {
	columns := []struct {
		name string
		typ  sqltype.ID
	}{
		{"name", sqltype.Text}, {"setting", sqltype.Text}, {"unit", sqltype.Text},
		{"category", sqltype.Text}, {"short_desc", sqltype.Text}, {"extra_desc", sqltype.Text},
		{"context", sqltype.Text}, {"vartype", sqltype.Text}, {"source", sqltype.Text},
		{"min_val", sqltype.Text}, {"max_val", sqltype.Text}, {"enumvals", sqltype.ID(1009)},
		{"boot_val", sqltype.Text}, {"reset_val", sqltype.Text}, {"sourcefile", sqltype.Text},
		{"sourceline", sqltype.Int4}, {"pending_restart", sqltype.Bool},
	}
	d := e.catalogSourceDescriptor(pgSettingsOID, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "pg_settings", len(columns))
	d.VirtualView = true
	for i, column := range columns {
		typ, ok := e.catalog.read(e.catalog.state).LookupType(column.typ)
		if !ok {
			return fmt.Errorf("stuntgres: missing pg_settings carrier")
		}
		d.Columns[i] = catalog.Column{Name: column.name, Usage: typ.Usage(), Nullable: true, NullabilityKnown: true, OriginTable: uint32(pgSettingsOID), OriginAttribute: int16(i + 1)}
		d.Index[column.name] = i
	}
	e.registerCatalogSource(d)
	return nil
}

// The supported inventory is exactly row_security. Capture one coherent physical
// session observation; returned rows retain neither settings locks nor authority.
func capturePGSettings(ctx context.Context, ref ids.ObjectRef, d *catalog.Relation, settings *sessionSettings) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	settings.mu.Lock()
	value, reset := settings.value("row_security"), settings.defaults["row_security"]
	source := "default"
	if settings.rowSecurityProviderDefault {
		source = "client"
	}
	if _, present := settings.values["row_security"]; present {
		source = "session"
	}
	settings.mu.Unlock()
	row := make([]sqltype.Value, len(d.Columns))
	for i, column := range d.Columns {
		row[i] = sqltype.Null(column.Usage.Type)
	}
	for _, field := range []struct {
		index int
		value string
	}{
		{0, "row_security"}, {1, value}, {3, "Client Connection Defaults / Statement Behavior"},
		{4, "Enables row security."}, {5, "When enabled, row security will be applied to all users."},
		{6, "user"}, {7, "bool"}, {8, source}, {12, "on"}, {13, reset},
	} {
		row[field.index] = sqltype.String(d.Columns[field.index].Usage.Type, field.value)
	}
	row[16] = sqltype.Boolean(d.Columns[16].Usage.Type, false)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{row}}, nil
}
