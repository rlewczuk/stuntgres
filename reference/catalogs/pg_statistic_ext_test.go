package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var statisticExtNames = []string{"oid", "stxrelid", "stxname", "stxnamespace", "stxowner", "stxkeys", "stxstattarget", "stxkind", "stxexprs"}
var statisticExtTypes = []string{"OID", "OID", "NAME", "OID", "OID", "INT2VECTOR", "INT2", "_CHAR", "PG_NODE_TREE"}
const statisticExtQuery = "SELECT * FROM pg_catalog.pg_statistic_ext WHERE stxrelid=$1"

func statisticExtMetadata(t *testing.T, pg bool, columns []*sql.ColumnType) {
	t.Helper()
	if len(columns) != 9 { t.Fatalf("catalog width %d", len(columns)) }
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		switch statisticExtTypes[i] {
		case "OID", "INT2": scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
			if i == 6 { scan = reflect.TypeFor[int16]() }
		}
		nullable, known := c.Nullable()
		wantNullable, wantKnown := i == 6 || i == 8, true
		if pg { wantNullable, wantKnown = false, false }
		if c.Name() != statisticExtNames[i] || c.DatabaseTypeName() != statisticExtTypes[i] || c.ScanType() != scan || nullable != wantNullable || known != wantKnown {
			t.Fatalf("column %d: %s %s %v nullable(%t,%t)", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), nullable, known)
		}
	}
}

func statisticExtRead(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, query string, args ...any) {
	t.Helper()
	var saved []*sql.ColumnType
	s := fixture.Select(query, statisticExtNames, statisticExtTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		var err error
		saved, err = rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		statisticExtMetadata(t, b.PG, saved)
	}
	fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
	statisticExtMetadata(t, b.PG, saved)
}

func TestReferencePGStatisticExtReads(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE stx_read_owner(a INT,b INT)", 0))
		owner := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='stx_read_owner'")
		if owner <= 0 || owner > 4294967295 { t.Fatal("invalid relation identity", owner) }
		for _, query := range []string{statisticExtQuery, "SELECT oid,stxrelid,stxname,stxnamespace,stxowner,stxkeys,stxstattarget,stxkind,stxexprs FROM pg_catalog.pg_statistic_ext WHERE stxrelid=$1", "SELECT * FROM pg_statistic_ext WHERE stxrelid=$1"} {
			statisticExtRead(t, p, b, conn, query, owner)
			s := fixture.Prepared(fixture.Select(query, statisticExtNames, statisticExtTypes, nil, owner))
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }; statisticExtMetadata(t, b.PG, columns) }
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		stmt, err := conn.PrepareContext(p.Ctx, statisticExtQuery); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{conn, stmt, statisticExtQuery}
		for _, id := range []int64{owner, 0, owner} { statisticExtRead(t, p, b, held, statisticExtQuery, id) }
		s := fixture.Select(statisticExtQuery, statisticExtNames, statisticExtTypes, nil, owner); s.NonContext = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE pg_statistic_ext(v INT)", 0))
		statisticExtRead(t, p, b, conn, statisticExtQuery, owner)
		if !b.PG { statisticExtRead(t, p, b, conn, "SELECT * FROM pg_catalog.pg_statistic_ext") }
	}) }
}

func TestReferencePGStatisticExtLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE stx_lifecycle(a INT,b INT)", 0))
		owner := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='stx_lifecycle'")
		stmt, err := conn.PrepareContext(p.Ctx, statisticExtQuery); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{conn, stmt, statisticExtQuery}
		statisticExtRead(t, p, b, held, statisticExtQuery, owner)
		for _, q := range []struct{ sql string; count int64 }{{"INSERT INTO stx_lifecycle VALUES(1,2),(3,4)",2}, {"ANALYZE stx_lifecycle",0}, {"ALTER TABLE stx_lifecycle RENAME TO stx_lifecycle_renamed",0}, {"ALTER TABLE stx_lifecycle_renamed RENAME a TO renamed_a",0}, {"DROP TABLE stx_lifecycle_renamed",0}} {
			run(fixture.Exec(q.sql,q.count)); statisticExtRead(t,p,b,held,statisticExtQuery,owner)
		}
	}) }
}

func TestReferencePGStatisticExtDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		for _, tc := range []struct{ query, position string }{{"SELECT missing FROM pg_catalog.pg_statistic_ext","8"},{"SELECT /*é*/ missing FROM pg_catalog.pg_statistic_ext","14"}} {
			for _, prepared := range []bool{false,true} {
				want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`); want.Position = tc.position
				s := fixture.ErrorDiagnostic(tc.query,want); s.SelectRows, s.ErrorStage = true,"execute"
				if prepared { s = fixture.Prepared(s); s.ErrorStage = "prepare" }
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
				statisticExtRead(t,p,b,b.DB,statisticExtQuery,int64(0))
			}
		}
	}) }
}
