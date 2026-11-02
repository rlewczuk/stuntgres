package reference_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderContracts(t *testing.T) {
	// Absence is a typed ordinary native capability failure, not a transport
	// replacement or a made-up PostgreSQL SQLSTATE.
	d := stuntgres.NewDriver()
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "external_sql_unavailable_handler"}, ExternalProvider: "not_registered", Wrapper: &externalSQLHandler{}}); err != nil {
		t.Fatal(err)
	}
	_, err := d.OpenConnector("opaque external SQL unavailable contract")
	var unavailable *stuntgres.ExternalSQLUnavailableError
	if !errors.As(err, &unavailable) || unavailable.Capability != "not_registered" {
		t.Fatal("missing external capability succeeded or lied", err)
	}
	if _, ok := err.(interface{ SQLState() string }); ok {
		t.Fatal("unavailable capability fabricated SQLSTATE", err)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := externalSQLProviderConnection(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE FOREIGN DATA WRAPPER external_sql_metadata NO HANDLER NO VALIDATOR", 0),
				fixture.Exec("CREATE SERVER external_sql_metadata_server FOREIGN DATA WRAPPER external_sql_metadata OPTIONS(dbname 'external_sql_metadata_only')", 0),
				fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER external_sql_metadata_server OPTIONS(user 'metadata_only')", 0),
				fixture.Exec("CREATE FOREIGN TABLE external_sql_metadata_data(id integer NOT NULL,label text,flag boolean,bytes bytea) SERVER external_sql_metadata_server OPTIONS(schema_name 'external_sql_source',table_name 'data')", 0),
				fixture.Exec("CREATE TABLE external_sql_local(id integer NOT NULL,label text,flag boolean,bytes bytea)", 0),
				fixture.Exec("INSERT INTO external_sql_local VALUES(1,'x',true,decode('00ff','hex')),(2,NULL,false,NULL),(2,NULL,false,NULL),(3,'',NULL,decode('','hex'))", 4),
				fixture.Exec("CREATE VIEW external_sql_saved AS SELECT id,label,flag,bytes FROM external_sql_local", 0),
				fixture.Exec("CREATE EXTENSION dblink", 0),
				fixture.Exec("CREATE TABLE external_sql_key(id integer PRIMARY KEY,label text)", 0),
				fixture.Exec("INSERT INTO external_sql_key VALUES(7,'kept')", 1),
			} {
				run(s)
			}
			externalSQLColumnOptionSetup(run)

			check := func() {
				t.Helper()
				for _, prepared := range []bool{false, true} {
					for _, s := range externalSQLProviderCatalogContracts() {
						s.Prepared = prepared
						run(s)
					}
					// A declaration without executable support is not a working
					// external capability and must not publish empty success rows.
					d := fixture.ControlsReferenceDiagnostic("55000", `foreign-data wrapper "external_sql_metadata" has no handler`)
					missing := fixture.ErrorDiagnostic("SELECT * FROM external_sql_metadata_data", d)
					missing.SelectRows, missing.Prepared = true, prepared
					run(missing)
					failedInsert := fixture.ErrorDiagnostic("INSERT INTO external_sql_local SELECT * FROM external_sql_metadata_data", d)
					failedInsert.Prepared = prepared
					run(failedInsert)
					sentinel := fixture.Select("SELECT id,label,flag,bytes FROM external_sql_saved ORDER BY id", []string{"id", "label", "flag", "bytes"}, []string{"INT4", "TEXT", "BOOL", "BYTEA"}, externalSQLProviderExpectedRows())
					sentinel.Prepared = prepared
					run(sentinel)
				}
			}
			check()
			externalSQLProviderValues(t, p, b, c, "external_sql_local")
			if !b.PG {
				// Only persistent declarations, local data and current installed
				// dblink identities exist in this Docker-free control phase.
				// There is no remote handle or fake provider to serialize.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				for _, s := range []fixture.Step{
					fixture.Select("SELECT count(*) AS n FROM pg_foreign_data_wrapper WHERE fdwname='external_sql_metadata'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
					fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='external_sql_local' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
					fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='dblink'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
				} {
					run(s)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				check()
				externalSQLProviderValues(t, p, b, c, "external_sql_local")
			}
		})
	}
}

func externalSQLProviderCatalogContracts() []fixture.Step {
	return append([]fixture.Step{
		fixture.Select("SELECT w.fdwhandler=0 AS no_handler,w.fdwvalidator=0 AS no_validator,s.srvoptions::text AS options FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid WHERE w.fdwname='external_sql_metadata' AND s.srvname='external_sql_metadata_server'", []string{"no_handler", "no_validator", "options"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{true, true, "{dbname=external_sql_metadata_only}"}}),
		fixture.Select("SELECT m.umuser=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS caller_mapping,m.umoptions::text AS options FROM pg_user_mapping m JOIN pg_foreign_server s ON s.oid=m.umserver WHERE s.srvname='external_sql_metadata_server'", []string{"caller_mapping", "options"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{user=metadata_only}"}}),
		fixture.Select("SELECT attname::text AS name,atttypid::bigint AS oid,atttypmod,attnotnull FROM pg_attribute WHERE attrelid='external_sql_metadata_data'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"name", "oid", "atttypmod", "attnotnull"}, []string{"TEXT", "INT8", "INT4", "BOOL"}, [][]any{
			{"id", int64(23), int64(-1), true},
			{"label", int64(25), int64(-1), false},
			{"flag", int64(16), int64(-1), false},
			{"bytes", int64(17), int64(-1), false},
		}),
		fixture.Select("SELECT position,colname FROM dblink_get_pkey('external_sql_key') ORDER BY position", []string{"position", "colname"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "id"}}),
		fixture.Select("SELECT position,colname FROM dblink_get_pkey(NULL::text) ORDER BY position", []string{"position", "colname"}, []string{"INT4", "TEXT"}, nil),
		fixture.Select("SELECT dblink_build_sql_delete('external_sql_key','1'::int2vector,1,'{7}'::text[]) AS sql", []string{"sql"}, []string{"TEXT"}, [][]any{{"DELETE FROM external_sql_key WHERE id = '7'"}}),
		fixture.Select("SELECT dblink_build_sql_delete(NULL::text,NULL::int2vector,NULL::integer,NULL::text[]) AS del,dblink_build_sql_insert(NULL::text,NULL::int2vector,NULL::integer,NULL::text[],NULL::text[]) AS ins,dblink_build_sql_update(NULL::text,NULL::int2vector,NULL::integer,NULL::text[],NULL::text[]) AS upd", []string{"del", "ins", "upd"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil}}),
		fixture.Select("SELECT e.extversion,p.prorettype=t.oid AS nominal_result,c.reltype=t.oid AS backing,a.typelem=t.oid AS array_element,p.proowner=e.extowner AS same_owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_proc p ON p.pronamespace=n.oid AND p.proname='dblink_get_pkey' AND p.proargtypes::text='25' JOIN pg_type t ON t.typnamespace=n.oid AND t.typname='dblink_pkey_results' JOIN pg_class c ON c.oid=t.typrelid JOIN pg_type a ON a.oid=t.typarray JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.refclassid=3079 AND d.refobjid=e.oid AND d.deptype='e' WHERE e.extname='dblink'", []string{"extversion", "nominal_result", "backing", "array_element", "same_owner"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.2", true, true, true, true}}),
	}, externalSQLColumnOptionChecks()...)
}
