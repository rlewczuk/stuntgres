package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Capture real database-local identities and independently validate their links.
// LOAD preserves the exact image; Restore may remap persistent object OIDs.
// A non-nil notices argument consumes each genuinely logged catalog query.
func autoExplainProviderIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) [][]any {
	t.Helper()
	var identity [][]any
	for _, query := range []string{
		"SELECT oid,extnamespace,extowner,extname FROM pg_extension WHERE extname='unaccent'",
		"SELECT classid,objid,refclassid,refobjid,deptype FROM pg_depend WHERE refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') ORDER BY classid,objid",
		"SELECT oid,relnamespace,relowner,relname,relkind FROM pg_class WHERE relname IN ('ae_provider_data','ae_translated') ORDER BY relname",
	} {
		rows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i := range values {
				values[i] = fixture.Normalize(values[i])
			}
			identity = append(identity, values)
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		want := 1
		if strings.Contains(query, "pg_depend") {
			want = 6
		}
		if strings.Contains(query, "pg_class") {
			want = 2
		}
		if count != want {
			t.Fatal("real installed identity/member population", query, count, want)
		}
		if notices != nil {
			autoExplainJSON(t, notices, query, nil, true, float64(count))
		}
	}
	// Membership must resolve to the six actual released objects, not merely
	// six edges carrying the restored extension's number. All owned objects in
	// this superuser installation belong to the installer; templates have no owner.
	for _, step := range []fixture.Step{
		fixture.Select(
			"SELECT n.nspname,r.rolname=current_user AS owned FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='unaccent' AND e.oid>0",
			[]string{"nspname", "owned"}, []string{"NAME", "BOOL"}, [][]any{{"public", true}}),
		fixture.Select(
			"SELECT p.proname,p.proargtypes::text,n.nspname,p.proowner=e.extowner AS owned FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_proc p ON p.oid=d.objid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE e.extname='unaccent' AND d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND d.objsubid=0 AND d.refobjsubid=0 AND p.oid>0 AND p.pronamespace=e.extnamespace ORDER BY p.proname,p.proargtypes::text",
			[]string{"proname", "proargtypes", "nspname", "owned"}, []string{"NAME", "TEXT", "NAME", "BOOL"},
			[][]any{{"unaccent", "25", "public", true}, {"unaccent", "3769 25", "public", true}, {"unaccent_init", "2281", "public", true}, {"unaccent_lexize", "2281 2281 2281 2281", "public", true}}),
		fixture.Select(
			"SELECT x.dictname,n.nspname,x.dictowner=e.extowner AS owned,t.tmplname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_ts_dict x ON x.oid=d.objid JOIN pg_namespace n ON n.oid=x.dictnamespace JOIN pg_ts_template t ON t.oid=x.dicttemplate WHERE e.extname='unaccent' AND d.classid=3600 AND d.refclassid=3079 AND d.deptype='e' AND d.objsubid=0 AND d.refobjsubid=0 AND x.oid>0 AND x.dictnamespace=e.extnamespace AND t.tmplnamespace=e.extnamespace",
			[]string{"dictname", "nspname", "owned", "tmplname"}, []string{"NAME", "NAME", "BOOL", "NAME"}, [][]any{{"unaccent", "public", true, "unaccent"}}),
		fixture.Select(
			"SELECT t.tmplname,n.nspname,i.proname,l.proname AS lexize FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_ts_template t ON t.oid=d.objid JOIN pg_namespace n ON n.oid=t.tmplnamespace JOIN pg_proc i ON i.oid=t.tmplinit JOIN pg_proc l ON l.oid=t.tmpllexize WHERE e.extname='unaccent' AND d.classid=3764 AND d.refclassid=3079 AND d.deptype='e' AND d.objsubid=0 AND d.refobjsubid=0 AND t.oid>0 AND t.tmplnamespace=e.extnamespace AND i.pronamespace=e.extnamespace AND l.pronamespace=e.extnamespace",
			[]string{"tmplname", "nspname", "proname", "lexize"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{"unaccent", "public", "unaccent_init", "unaccent_lexize"}}),
		fixture.Select(
			"SELECT c.relname,c.relkind,n.nspname,c.relowner=e.extowner AS owned FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_extension e ON e.extnamespace=c.relnamespace WHERE e.extname='unaccent' AND c.oid>0 AND c.relname IN ('ae_provider_data','ae_translated') ORDER BY c.relname",
			[]string{"relname", "relkind", "nspname", "owned"}, []string{"NAME", "CHAR", "NAME", "BOOL"}, [][]any{{"ae_provider_data", "r", "public", true}, {"ae_translated", "v", "public", true}}),
		fixture.Select(
			"SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.objid WHERE e.extname='unaccent' AND d.classid=3079 AND d.refclassid=2615 AND d.refobjid=e.extnamespace AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='n'",
			[]string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		if notices != nil {
			autoExplainJSON(t, notices, step.Query, nil, true, float64(len(step.Rows)))
		}
	}
	return identity
}

func TestReferenceAutoExplainControls(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, query := range []string{"CREATE EXTENSION unaccent", "CREATE TABLE ae_provider_data(id integer,v text)", "CREATE VIEW ae_translated AS SELECT id,public.unaccent(v) AS v FROM ae_provider_data"} {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec("INSERT INTO ae_provider_data VALUES(1,'Hôtel'),(2,NULL)", 2))
		run(fixture.Exec("CREATE TABLE ae_dump_literal(v text)", 0))
		run(fixture.Exec("INSERT INTO ae_dump_literal VALUES('auto_explain')", 1))
		before := autoExplainProviderIdentity(t, p, b, c, nil)
		run(fixture.Exec("LOAD 'auto_explain'", 0))
		if after := autoExplainProviderIdentity(t, p, b, c, nil); !reflect.DeepEqual(before, after) {
			t.Fatal("LOAD changed installed provider identities", before, after)
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='auto_explain'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		const query = "SELECT id,v FROM ae_translated WHERE id=$1 ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		values := func() {
			t.Helper()
			for _, tc := range []struct {
				id         int64
				rows       [][]any
				parameters string
			}{
				{1, [][]any{{int64(1), "Hotel"}}, "$1 = '1'"},
				{2, [][]any{{int64(2), nil}}, "$1 = '2'"},
				{99, nil, "$1 = '99'"},
			} {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, tc.rows, tc.id))
				autoExplainJSON(t, notices, query, &tc.parameters, true, float64(len(tc.rows)))
			}
		}
		autoExplainConfigure(t, p, b, c, notices)
		values()
		const direct = "SELECT id,public.unaccent(v) AS v FROM ae_provider_data ORDER BY id"
		run(fixture.Select(direct, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}}))
		autoExplainJSON(t, notices, direct, nil, true, 2)
		if b.PG {
			return
		}
		run(fixture.Exec("PREPARE ae_control_handle AS SELECT id FROM ae_provider_data", 0))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "LOAD 'auto_explain'") || strings.Contains(dump.String(), "auto_explain.log_") || strings.Contains(dump.String(), "ae_control_handle") || !strings.Contains(dump.String(), "auto_explain") {
			t.Fatal("dump serialized session state or stripped legitimate user data", dump.String())
		}
		restoredIdentity := func() {
			t.Helper()
			after := autoExplainProviderIdentity(t, p, b, c, notices)
			// Restore replaces persistent objects, but this engine's public
			// namespace and shared installer role retain their exact identities.
			// The validated member/relation links must use those same anchors.
			if !reflect.DeepEqual(before[0][1:3], after[0][1:3]) {
				t.Fatal("Restore changed destination namespace/owner identities", before[0], after[0])
			}
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		restoredIdentity()
		values()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		const constant = "SELECT 9::integer AS v"
		run(fixture.Select(constant, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
		autoExplainJSON(t, notices, constant, nil, true, 1)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		restoredIdentity()
		values()
		connector, err := stuntgres.NewDriver().OpenConnector("ae-independent-destination")
		if err != nil {
			t.Fatal(err)
		}
		recorded, other, recorder := fixture.DatabaseConnectorScenarioPool(t, connector, false)
		if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		destination, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		// Validate only destination-local links: independent engines have no
		// common allocator domain and need not preserve source object numbers.
		autoExplainProviderIdentity(t, p, fixture.Backend{Recorder: recorder}, destination, nil)
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT current_setting('auto_explain.log_min_duration',true) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select(direct, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}}))
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT count(*) FROM pg_prepared_statements WHERE name='ae_control_handle'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT v FROM ae_dump_literal", []string{"v"}, []string{"TEXT"}, [][]any{{"auto_explain"}}))
	})
}
