package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWRegistration(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
		if b.PG {
			return
		} // Registration is an embedding API, not a PostgreSQL C ABI.
		hostError := func(err error) {
			t.Helper()
			var sqlErr *stuntgres.Error
			if err == nil || errors.As(err, &sqlErr) {
				t.Fatalf("registration must return an ordinary host error: %v", err)
			}
		}
		before := r.snapshot()
		d := stuntgres.NewDriver()
		definition := stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "fdw_sql_handler"}, Wrapper: r}
		var nilReader *fdwSQLReader
		bad := definition
		bad.Wrapper = nilReader
		hostError(d.RegisterForeignDataWrapper(bad))
		bad.Wrapper = nil
		hostError(d.RegisterForeignDataWrapper(bad))
		for _, name := range []string{"", strings.Repeat("x", 64), "bad\x00name", string([]byte{255})} {
			bad = definition
			bad.HandlerName.Name = name
			hostError(d.RegisterForeignDataWrapper(bad))
		}
		if err := r.configure(d); err != nil {
			t.Fatal(err)
		}
		hostError(d.RegisterForeignDataWrapper(definition))
		hostError(d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Name: "missing"}}))
		hostError(d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Name: "fdw_sql_validator"}, Validate: r.validate}))
		connector, err := d.OpenConnector("fdw-registration")
		if err != nil {
			t.Fatal(err)
		}
		if after := r.snapshot(); after != before {
			t.Fatal("registration or connector initialization entered application callbacks", before, after)
		}
		hostError(d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "late"}, Wrapper: r}))
		recorder := &fixture.Recorder{RawBackend: fixture.RawNative}
		pool := fdwRecordedPool(t, connector)
		defer pool.Close()
		for _, q := range []string{"CREATE FOREIGN DATA WRAPPER fdw_registered HANDLER fdw_sql_handler VALIDATOR fdw_sql_validator", "CREATE SERVER registered_server FOREIGN DATA WRAPPER fdw_registered", "CREATE USER MAPPING FOR CURRENT_USER SERVER registered_server", "CREATE FOREIGN TABLE registered_data(id integer,label text,flag boolean) SERVER registered_server OPTIONS(schema_name 'fdw_source',table_name 'data')"} {
			fixture.RunStep(t, p.Ctx, pool, recorder, fixture.Exec(q, 0))
		}
		if after := r.snapshot(); after.begins != before.begins {
			t.Fatal("registration/DDL opened a scan", before, after)
		}
		secondConnector, err := d.OpenConnector("fdw-registration")
		if err != nil {
			t.Fatal(err)
		}
		second := fdwRecordedPool(t, secondConnector)
		defer second.Close()
		fixture.RunStep(t, p.Ctx, second, recorder, fdwRows("SELECT id,label,flag FROM registered_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
		otherConnector, err := d.OpenConnector("fdw-registration-other")
		if err != nil {
			t.Fatal(err)
		}
		other := fdwRecordedPool(t, otherConnector)
		defer other.Close()
		fixture.RunStep(t, p.Ctx, other, recorder, fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='fdw_sql_handler'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, other, recorder, fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='fdw_registered'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		isolatedConnector, err := stuntgres.NewDriver().OpenConnector("fdw-registration")
		if err != nil {
			t.Fatal(err)
		}
		isolated := fdwRecordedPool(t, isolatedConnector)
		defer isolated.Close()
		fixture.RunStep(t, p.Ctx, isolated, recorder, fixture.ErrorDiagnostic("CREATE FOREIGN DATA WRAPPER fdw_registered HANDLER fdw_sql_handler", fdwDiagnostic("42883", "function fdw_sql_handler() does not exist")))
	})
}

func TestReferenceFDWValidator(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fixture.Prepared(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER COLUMN id OPTIONS(column_name 'id')", 0)))
		run(fixture.Select("SELECT attname,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_data'::regclass::oid AND attname='id'", []string{"attname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"id", "{column_name=id}"}}))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data OPTIONS(SET table_name 'data')", 0))
		run(fixture.Select("SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid='fdw_data'::regclass::oid", []string{"options"}, []string{"TEXT"}, [][]any{{"{schema_name=fdw_source,table_name=data}"}}))
		if b.PG {
			return
		}
		r.mu.Lock()
		events := append([]fdwValidation(nil), r.validations...)
		r.mu.Unlock()
		kinds := map[stuntgres.ForeignObjectKind]bool{}
		for _, event := range events {
			if event.nilOptions {
				t.Fatal("validator received nil options", event)
			}
			kinds[event.kind] = true
		}
		for _, kind := range []stuntgres.ForeignObjectKind{stuntgres.ForeignWrapper, stuntgres.ForeignServer, stuntgres.ForeignUserMapping, stuntgres.ForeignTable, stuntgres.ForeignAttribute} {
			if !kinds[kind] {
				t.Fatal("missing validator class", kind, events)
			}
		}
		want := []stuntgres.ForeignOption{{Name: "schema_name", Value: "fdw_source"}, {Name: "table_name", Value: "data"}}
		if !reflect.DeepEqual(events[len(events)-1].options, want) {
			t.Fatal("incomplete resulting options", events)
		}
		rejected := errors.New("reader validator rejects changed remote table")
		r.mu.Lock()
		r.validateHook = func(ctx context.Context, kind stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
			if kind == stuntgres.ForeignTable && fdwOption(options, "table_name") == "absent" {
				// Real peer SQL must progress while publication is suspended; it
				// must still see the committed descriptor, not the candidate.
				var value string
				if err := b.DB.QueryRowContext(ctx, "SELECT ftoptions::text FROM pg_foreign_table WHERE ftrelid='fdw_data'::regclass::oid").Scan(&value); err != nil {
					return err
				}
				if value != "{schema_name=fdw_source,table_name=data}" {
					return errors.New("validator saw unpublished metadata")
				}
				options[0].Value = "callback-owned mutation"
				return rejected
			}
			return nil
		}
		r.mu.Unlock()
		defer func() { r.mu.Lock(); r.validateHook = nil; r.mu.Unlock() }()
		_, err := c.ExecContext(p.Ctx, "ALTER FOREIGN TABLE fdw_data OPTIONS(SET table_name 'absent')")
		if !errors.Is(err, rejected) {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT ftoptions::text AS options FROM pg_foreign_table WHERE ftrelid='fdw_data'::regclass::oid", []string{"options"}, []string{"TEXT"}, [][]any{{"{schema_name=fdw_source,table_name=data}"}}))
		run(fixture.Select("SELECT id FROM fdw_data WHERE id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
