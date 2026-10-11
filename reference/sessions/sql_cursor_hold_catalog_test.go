package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLCursorHoldCatalogChanges(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		holdCatalogChanges(t, p, b, c, false)
	})
}

func TestReferenceSQLCursorHoldRecursiveCatalogChanges(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		holdCatalogChanges(t, p, b, c, true)
	})
}

func holdCatalogChanges(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, recursive bool) {
	t.Helper()
	sqlCursorSetup(t, p, b, c)
	holdRun(t, p, b, c, "CREATE TABLE hold_oid_target(v INTEGER)")
	var relationOID, typeOID int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'hold_oid_target'::REGCLASS::BIGINT,'hold_oid_target'::REGTYPE::BIGINT").Scan(&relationOID, &typeOID); err != nil {
		t.Fatal(err)
	}
	if relationOID <= 0 || typeOID <= 0 {
		t.Fatal("referent relation and row type must have positive identities", relationOID, typeOID)
	}
	columns := []string{"i", "r", "typ"}
	types := []string{"INT4", "REGCLASS", "REGTYPE"}
	projection := "i,'hold_oid_target'::REGCLASS AS r,'hold_oid_target'::REGTYPE AS typ"
	if recursive {
		for _, query := range []string{
			"CREATE DOMAIN hold_oid_reg AS REGCLASS",
			"CREATE DOMAIN hold_oid_typ AS REGTYPE",
			"CREATE DOMAIN hold_oid_ref_array AS REGCLASS[]",
			"CREATE DOMAIN hold_oid_type_array AS REGTYPE[]",
			"CREATE TYPE hold_oid_record AS (r REGCLASS,t REGTYPE,a REGCLASS[],b REGTYPE[])",
			"CREATE DOMAIN hold_oid_record_domain AS hold_oid_record",
		} {
			holdRun(t, p, b, c, query)
		}
		columns = append(columns, "a", "b", "dr", "dt", "da", "db", "wrapped")
		types = append(types, "_REGCLASS", "_REGTYPE", "REGCLASS", "REGTYPE", "_REGCLASS", "_REGTYPE", "")
		projection += ",ARRAY['hold_oid_target'::REGCLASS,NULL::REGCLASS] AS a" +
			",ARRAY['hold_oid_target'::REGTYPE,NULL::REGTYPE] AS b" +
			",'hold_oid_target'::hold_oid_reg AS dr,'hold_oid_target'::hold_oid_typ AS dt" +
			",'{hold_oid_target,NULL}'::hold_oid_ref_array AS da" +
			",'{hold_oid_target,NULL}'::hold_oid_type_array AS db" +
			`, '(hold_oid_target,hold_oid_target,"{hold_oid_target,NULL}","{hold_oid_target,NULL}")'::hold_oid_record_domain AS wrapped`
	}
	// The producer keeps typed OIDs, never text conversions that would freeze
	// their spellings at commit. The table row type independently exercises REGTYPE.
	holdRun(t, p, b, c, "DECLARE catalog_hold NO SCROLL CURSOR WITH HOLD FOR SELECT "+projection+" FROM cursor_rows ORDER BY i")
	peer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	metadata := func(t *testing.T, rows *sql.Rows) {
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, columns) {
			t.Fatal("held OID columns", names, err, columns)
		}
		holdScopeMetadata(t, rows, b.PG, types)
	}
	want := func(id int64, relation, typ string) [][]any {
		row := []any{id, relation, typ}
		if recursive {
			ra, ta := "{"+relation+",NULL}", "{"+typ+",NULL}"
			record := fmt.Sprintf("(%s,%s,\"%s\",\"%s\")", relation, typ, ra, ta)
			row = append(row, ra, ta, relation, typ, ra, ta, record)
		}
		return [][]any{row}
	}
	first, err := c.QueryContext(p.Ctx, "FETCH 1 FROM catalog_hold")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	holdRun(t, p, b, peer, "ALTER TABLE hold_oid_target RENAME TO hold_oid_renamed")
	// Consume only after the rename: a delivered detached batch must retain
	// the output map captured by its earlier FETCH, not consult a live catalog.
	fixture.RegtypeAssertRowsWithMetadata(t, first, want(1, "hold_oid_target", "hold_oid_target"), b.PG, metadata)
	second, err := c.QueryContext(p.Ctx, "FETCH 1 FROM catalog_hold")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	holdRun(t, p, b, peer, "DROP TABLE hold_oid_renamed")
	holdRun(t, p, b, peer, "DROP TABLE cursor_rows")
	// The second detached batch likewise retains renamed presentation after
	// DROP. The future stored FETCH has no producer/source available to replay.
	fixture.RegtypeAssertRowsWithMetadata(t, second, want(2, "hold_oid_renamed", "hold_oid_renamed"), b.PG, metadata)
	missing := want(3, strconv.FormatInt(relationOID, 10), strconv.FormatInt(typeOID, 10))
	step := fixture.Select("FETCH ALL FROM catalog_hold", columns, types, missing)
	step.CheckMetadata = metadata
	step.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 0 {
			return false
		}
		fixture.RegtypeRaw(t, b.PG, value)
		return true
	}
	sqlCursorStep(t, p, b, c, step)
	step.Rows = [][]any{}
	sqlCursorStep(t, p, b, c, step)
	holdRun(t, p, b, c, "CLOSE catalog_hold")
}
