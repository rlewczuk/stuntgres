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

// Only this phase uses native APIs; the ordinary SQL witnesses run on both
// backends. Real mode additionally executes the actual emitted native SQL.
func polygonControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step, later []fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("SET extra_float_digits=0", 0))
	retained, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "CREATE TYPE") || strings.Contains(dump.String(), "CREATE EXTENSION") {
		t.Fatal("builtin polygon emitted standalone", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("SET extra_float_digits=3", 0))
	run(check)
	run(fixture.Prepared(check))
	var old [][]any
	for retained.Next() {
		v := make([]any, len(check.Columns))
		dest := make([]any, len(v))
		for i := range v {
			dest[i] = &v[i]
		}
		if err := retained.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		old = append(old, v)
	}
	if err := retained.Err(); err != nil {
		t.Fatal(err)
	}
	retained.Close()
	wantOld := make([][]any, len(check.Rows))
	for i, row := range check.Rows {
		wantOld[i] = append([]any(nil), row...)
		for j, v := range row {
			if s, ok := v.(string); ok {
				wantOld[i][j] = strings.ReplaceAll(strings.ReplaceAll(s, "0.10000000000000002", "0.1"), "5e-324", "4.94065645841247e-324")
			}
		}
	}
	if !reflect.DeepEqual(old, wantOld) {
		t.Fatalf("retained polygon rows %#v want %#v", old, wantOld)
	}
	for i, col := range metadata {
		if col.Name() != check.Columns[i] || col.DatabaseTypeName() != check.Types[i] {
			t.Fatalf("retained column %d %s/%s", i, col.Name(), col.DatabaseTypeName())
		}
	}
	bad := "CREATE TABLE polygon_staged(v polygon); INSERT INTO polygon_staged VALUES('0,0'); INSERT INTO polygon_staged VALUES('bad');"
	d := polygonInputDiagnostic("bad")
	d.Position = fixture.Position(strings.Index(bad, "'bad'") + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	run(check)
	run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='polygon_staged'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual polygon dump SQL oracle replay", err)
		}
		pg := fixture.Backend{PG: true}
		pgCheck := polygonSelect(pg, check.Query, check.Columns, check.Types, check.Rows)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("SET extra_float_digits=3", 0))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pgCheck))
		for _, s := range later {
			if s.SelectRows {
				// Rebind raw-value and metadata checks to the replay backend.
				s = polygonSelect(pg, s.Query, s.Columns, s.Types, s.Rows, s.Args...)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

// Shared by P5 and the executable P6 smoke, including post-restore execution of
// the default, CHECK, and view rather than merely checking serialized text.
func polygonPersistentWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE polygon_controls(id integer NOT NULL,v polygon DEFAULT '((0,0),(4,0),(0,4))'::polygon,CONSTRAINT polygon_inside CHECK(v @> '(1,1)'::point OR v IS NULL))", 0))
	run(fixture.Exec(`CREATE VIEW polygon_view AS SELECT id,v,
		v ~= '((0,0),(4,0),(0,4))'::polygon AS same,
		v @> '(1,1)'::point AS inside,
		'(1,1)'::point <@ v AS contained,
		NULL::polygon ~= v AS same_null_left,
		v ~= NULL::polygon AS same_null_right,
		NULL::polygon ~= NULL::polygon AS same_null_both,
		NULL::polygon @> '(1,1)'::point AS contains_null_left,
		v @> NULL::point AS contains_null_right,
		NULL::polygon @> NULL::point AS contains_null_both,
		NULL::point <@ v AS contained_null_left,
		'(1,1)'::point <@ NULL::polygon AS contained_null_right,
		NULL::point <@ NULL::polygon AS contained_null_both
		FROM polygon_controls`, 0))
	run(fixture.Exec("INSERT INTO polygon_controls VALUES(1,'((0,0),(4,0),(0,4))'),(2,'((4,0),(0,4),(0,0))'),(3,NULL),(4,'((0.10000000000000002,-0),(4,0),(5e-324,4))')", 4))
	// Canonical public output hides NaN signs. These rows prove acceptance and
	// replay; SQLLiteral/SameDatum units, not these strings, prove exact bits.
	run(fixture.Exec("CREATE TABLE polygon_signed_nan(id integer NOT NULL,v polygon)", 0))
	run(fixture.Exec("INSERT INTO polygon_signed_nan VALUES(1,'-NaN,0'),(2,'0,-NaN'),(3,'-NaN,-NaN')", 3))
	signedNaNs := polygonSelect(b, "SELECT id,v FROM polygon_signed_nan ORDER BY id",
		[]string{"id", "v"}, []string{"INT4", "POLYGON"},
		[][]any{{int64(1), "((NaN,0))"}, {int64(2), "((0,NaN))"}, {int64(3), "((NaN,NaN))"}})
	run(signedNaNs)
	run(fixture.Prepared(signedNaNs))
	// Query the stored expressions themselves: ordinary SELECT constants would
	// not prove that dump rendering and rebinding retain their formal types.
	selection := `SELECT id,v,same,inside,contained,
		same_null_left,same_null_right,same_null_both,
		contains_null_left,contains_null_right,contains_null_both,
		contained_null_left,contained_null_right,contained_null_both FROM polygon_view`
	check := polygonSelect(b, selection+" ORDER BY id",
		[]string{"id", "v", "same", "inside", "contained", "same_null_left", "same_null_right", "same_null_both", "contains_null_left", "contains_null_right", "contains_null_both", "contained_null_left", "contained_null_right", "contained_null_both"},
		[]string{"INT4", "POLYGON", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
		[][]any{
			{int64(1), polygonTriangle, true, true, true, nil, nil, nil, nil, nil, nil, nil, nil, nil},
			{int64(2), polygonRotated, true, true, true, nil, nil, nil, nil, nil, nil, nil, nil, nil},
			{int64(3), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil},
			{int64(4), "((0.10000000000000002,-0),(4,0),(5e-324,4))", false, true, true, nil, nil, nil, nil, nil, nil, nil, nil, nil},
		})
	run(check)
	run(fixture.Prepared(check))
	invalid := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"polygon_controls\" violates check constraint \"polygon_inside\"")
	invalid.Detail = "Failing row contains (7, ((9,9)))."
	invalid.Schema, invalid.Table, invalid.Constraint = "public", "polygon_controls", "polygon_inside"
	later := []fixture.Step{
		signedNaNs,
		fixture.Prepared(signedNaNs),
		fixture.Exec("INSERT INTO polygon_controls(id) VALUES(5)", 1),
		fixture.Exec("INSERT INTO polygon_controls VALUES(6,'((0,0),(8,0),(0,8))')", 1),
		fixture.ErrorDiagnostic("INSERT INTO polygon_controls VALUES(7,'9,9')", invalid),
		polygonSelect(b, selection+" WHERE id>=5 ORDER BY id", check.Columns, check.Types, [][]any{
			{int64(5), polygonTriangle, true, true, true, nil, nil, nil, nil, nil, nil, nil, nil, nil},
			{int64(6), "((0,0),(8,0),(0,8))", false, true, true, nil, nil, nil, nil, nil, nil, nil, nil, nil},
		}),
	}
	polygonControlCycle(t, p, b, c, check, later)
	for _, s := range later {
		run(s)
	}
}

func TestReferencePolygonControls(t *testing.T) {
	polygonScenario(t, polygonPersistentWitness)
}
