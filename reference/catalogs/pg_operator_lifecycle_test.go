package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

const pgOperatorLifeQuery = "SELECT * FROM pg_catalog.pg_operator WHERE oid=$1::oid"

func pgOperatorDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	b := p.Backends()[0]
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE operator_life(n INT,s TEXT)", 0),
		fixture.Exec("INSERT INTO operator_life VALUES(7,'kept'),(8,'also')", 2),
		fixture.Exec("CREATE VIEW operator_life_view AS SELECT oid,oprname,oprcode FROM pg_catalog.pg_operator WHERE oid=551", 0),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE VIEW") || !strings.Contains(dump.String(), "operator_life_view") || strings.Contains(dump.String(), "CREATE TABLE pg_catalog.pg_operator") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_operator") {
		t.Fatal("not a logical user-object dump", dump.String())
	}
	return dump.String()
}

func pgOperatorLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, dump string) {
	t.Helper()
	checkUser := func(executor fixture.Executor) {
		s := fixture.Select("SELECT n,s FROM operator_life ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), "also"}})
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Prepared(s))
		s = fixture.Select("SELECT oid,oprname,oprcode FROM operator_life_view", []string{"oid", "oprname", "oprcode"}, []string{"OID", "NAME", "REGPROC"}, [][]any{{int64(551), "+", "int4pl"}})
		fixture.PgDescriptionExecutorRows(t, p, b, executor, s)
		fixture.PgDescriptionExecutorRows(t, p, b, executor, fixture.Prepared(s))
	}
	if b.PG {
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		if _, err := conn.ExecContext(p.Ctx, dump); err != nil {
			t.Fatal("PG rejected actual native user dump", err)
		}
		checkUser(conn)
		pgOperatorInventory(t, p, b)
		return
	}
	old, err := b.DB.QueryContext(p.Ctx, pgOperatorLifeQuery, int64(551))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgOperatorMetadata(t, old, false)
	columns, err := old.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgOperatorLifeQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOperatorLifeQuery}
	checkCatalog := func() {
		pgOperatorInventory(t, p, b)
		for _, arg := range []any{int64(551), int64(0), nil} {
			var want [][]any
			if arg == int64(551) {
				want = [][]any{{int64(551), "+", int64(11), int64(10), "b", false, false, int64(23), int64(23), int64(23), int64(551), int64(0), "int4pl", "-", "-"}}
			}
			s := fixture.Select(pgOperatorLifeQuery, pgOperatorColumns, pgOperatorTypes, want, arg)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgOperatorMetadata(t, rows, false) }
			fixture.PgDescriptionExecutorRows(t, p, b, held, s)
		}
	}
	checkUser(b.DB)
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"CREATE SEQUENCE operator_invalid CACHE 0;"))
	wantError := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
	fixture.AssertDiagnostic(t, err, &wantError)
	checkCatalog()
	checkUser(b.DB)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore", err)
	}
	if err := stuntgres.Reset(ctx, p.Native); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Reset", err)
	}
	checkCatalog()
	checkUser(b.DB)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	checkCatalog()
	s := fixture.ImplicitError("SELECT * FROM operator_life", fixture.Diagnostic{Code: "42P01", Message: `relation "operator_life" does not exist`, Position: "15"})
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	checkCatalog()
	checkUser(b.DB)
	pgOperatorMetadata(t, old, false)
	if !old.Next() {
		t.Fatal("retained row missing", old.Err())
	}
	cells, dest := make([]any, 15), make([]any, 15)
	for i := range dest {
		dest[i] = &cells[i]
	}
	if err := old.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	want := []any{int64(551), "+", int64(11), int64(10), "b", false, false, int64(23), int64(23), int64(23), int64(551), int64(0), "int4pl", "-", "-"}
	if !reflect.DeepEqual(cells, want) {
		t.Fatal("retained typed/symbolic row", cells)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained EOF", old.Err())
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, pgOperatorColumns) {
		t.Fatal("retained columns", columns)
	}
	pgOperatorSavedMetadata(t, types, false)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump {
		t.Fatal("restored logical dump differs", again.String(), err)
	}
}

func TestReferencePGOperatorLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	dump := pgOperatorDump(t, p)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { pgOperatorLifecycle(t, p, b, dump) })
	}
}

func TestReferencePGOperatorFeatureSmoke(t *testing.T) {
	// Reuse the permanent scenarios and their genuine controls/replay rather
	// than a separate smoke implementation or a baseline alias-only query.
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"shape", TestReferencePGOperatorShape},
		{"inventory", TestReferencePGOperatorInventory},
		{"signatures", TestReferencePGOperatorResolverAgreement},
		{"polymorphic", TestReferencePGOperatorPolymorphicIdentity},
		{"polymorphic-relationships", TestReferencePGOperatorPolymorphicRelationships},
		{"references", TestReferencePGOperatorReferences},
		{"boundaries", TestReferencePGOperatorBoundaries},
		{"diagnostics", TestReferencePGOperatorDiagnostics},
		{"permissions", TestReferencePGOperatorPermissions},
		{"held", TestReferencePGOperatorHeldExecution},
		{"lifecycle", TestReferencePGOperatorLifecycle},
	} {
		t.Run(tc.name, tc.run)
	}
}
