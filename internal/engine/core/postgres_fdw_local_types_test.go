package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

// Capture only a genuine gateway's admitted private capability, then reject
// Open. This unit fabricates neither successful connections nor foreign rows.
type postgresLocalTypeRejector struct {
	captured chan catalog.PostgresFDWSession
	failure  error
}

func (p *postgresLocalTypeRejector) Open(ctx context.Context, _ catalog.ExternalSQLConnectRequest) (catalog.ExternalSQLConnection, error) {
	capability, ok := catalog.ExternalSQLContextCapability(ctx).(catalog.PostgresFDWSession)
	if !ok {
		return nil, errors.New("actual stock gateway omitted local type capability")
	}
	p.captured <- capability
	return nil, p.failure
}

func TestPostgresFDWLocalTypes(t *testing.T) {
	e := engine.New()
	provider := &postgresLocalTypeRejector{captured: make(chan catalog.PostgresFDWSession, 1), failure: errors.New("negative-only local type capture")}
	bindings := &catalog.ExternalSQLBindings{PostgresFDW: &catalog.ExternalSQLProviderDefinition{Name: "local-type-unit", Version: "1", Provider: provider}}
	if err := e.InstallForeignDefinitions(bindings, nil, nil,
		func(context.Context, driver.Value, sqltype.Usage, sqltype.InputEnvironment) (sqltype.Value, error) {
			return sqltype.Value{}, errors.New("negative-only local type unit imported rows")
		},
		func(context.Context, sqltype.Value, sqltype.OutputEnvironment) (driver.Value, error) {
			return nil, errors.New("negative-only local type unit exported parameters")
		}); err != nil {
		t.Fatal(err)
	}
	// SQL is private setup for actual gateway/capture ownership, not duplicated
	// public SQL result or diagnostic assertions. Attribute 1 is truly retired;
	// nested domains and reordered demand cannot be indexed as dense ordinals.
	for _, query := range []string{
		"CREATE EXTENSION postgres_fdw",
		"CREATE SERVER local_type_server FOREIGN DATA WRAPPER postgres_fdw",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER local_type_server",
		"CREATE DOMAIN local_type_base AS bytea",
		"CREATE DOMAIN local_type_nested AS local_type_base",
		"CREATE FOREIGN TABLE local_type_table(retired integer,local_text text,local_bytes local_type_nested) SERVER local_type_server",
		"ALTER FOREIGN TABLE local_type_table DROP COLUMN retired",
		"ALTER FOREIGN TABLE local_type_table ADD COLUMN local_added integer",
	} {
		enginetest.Run(t, e, query)
	}
	capture := func() catalog.PostgresFDWSession {
		t.Helper()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		result, err := e.Execute(op, enginetest.Statement(t, "SELECT local_added,local_bytes,local_text FROM local_type_table"), 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("negative-only gateway published rows")
		}
		if !errors.Is(err, provider.failure) {
			t.Fatal("genuine gateway did not reach rejecting transport", err)
		}
		select {
		case capability := <-provider.captured:
			return capability
		default:
			t.Fatal("genuine gateway did not capture its local type facts")
			return nil
		}
	}
	assertType := func(capability catalog.PostgresFDWSession, attribute int32, expected sqltype.ID) {
		t.Helper()
		actual, err := capability.LocalColumnBaseType(attribute)
		if err != nil || actual != expected {
			t.Fatal("admitted local base type", attribute, actual, expected, err)
		}
	}
	assertAbsent := func(capability catalog.PostgresFDWSession, attributes ...int32) {
		t.Helper()
		for _, attribute := range attributes {
			if _, err := capability.LocalColumnBaseType(attribute); err == nil {
				t.Fatal("unadmitted local attribute", attribute)
			}
		}
	}
	old := capture()
	assertType(old, 2, sqltype.Text)
	assertType(old, 3, sqltype.Bytea)
	assertType(old, 4, sqltype.Int4)
	assertAbsent(old, 0, 1, 5)
	enginetest.Run(t, e, "ALTER FOREIGN TABLE local_type_table DROP COLUMN local_bytes")
	enginetest.Run(t, e, "ALTER FOREIGN TABLE local_type_table ADD COLUMN local_bytes text")
	fresh := capture()
	assertType(fresh, 2, sqltype.Text)
	assertType(fresh, 4, sqltype.Int4)
	assertType(fresh, 5, sqltype.Text)
	assertAbsent(fresh, 0, 1, 3)
	// Retained semantic facts are immutable values, not a borrowed current
	// descriptor or permission to reopen an ended operation's transport.
	assertType(old, 3, sqltype.Bytea)
	assertAbsent(old, 5)
}
