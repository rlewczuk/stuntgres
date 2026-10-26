package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var ircColumns = []string{"constraint_catalog", "constraint_schema", "constraint_name", "unique_constraint_catalog", "unique_constraint_schema", "unique_constraint_name", "match_option", "update_rule", "delete_rule"}
var ircTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR"}
var ircRoles = []string{"irc_owner", "irc_reader", "irc_member", "irc_noinherit", "irc_group"}

func ircScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"irc_scope", "irc_other", "irc_moved", "irc_private"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, ircRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS irc_scope CASCADE", "DROP SCHEMA IF EXISTS irc_other CASCADE", "DROP SCHEMA IF EXISTS irc_moved CASCADE", "DROP SCHEMA IF EXISTS irc_private CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error(err) }
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" { t.Fatalf("database=%q error=%v", database, err) }
			body(t, p, b, c, r, database)
		})
	}
}

func ircMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 9 { t.Fatalf("columns=%d want 9", len(columns)) }
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan := reflect.TypeFor[string]()
		if pg && i < 6 { scan = reflect.TypeFor[any]() }
		wantLength := int64(0)
		if i >= 6 { wantLength = math.MaxInt64; if pg { wantLength = -5 } }
		if c.Name() != ircColumns[i] || c.DatabaseTypeName() != ircTypes[i] || c.ScanType() != scan || length != wantLength || lengthOK != (i >= 6) || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func ircProjection(pg bool, where string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(ircColumns, ",")+" FROM information_schema.referential_constraints WHERE "+where, ircColumns, ircTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }; ircMetadata(t, columns, pg) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			if column < 3 || column >= 6 { t.Fatalf("unexpected NULL column %d", column) }
			return true
		}
		text, ok := value.(string)
		if pg && column < 6 { bytes, yes := value.([]byte); text, ok = string(bytes), yes }
		if !ok { t.Fatalf("raw column %d=%T(%v)", column, value, value) }
		for _, candidate := range want { if candidate[column] == text { return true } }
		t.Fatalf("unexpected raw column %d=%q", column, text)
		return true
	}
	return s
}

func ircRow(db, schema, name, targetSchema, target, match, update, deleteRule string) []any {
	return []any{db, schema, name, db, targetSchema, target, match, update, deleteRule}
}
func ircNullRow(db, schema, name string) []any { return []any{db, schema, name, nil, nil, nil, "NONE", "NO ACTION", "NO ACTION"} }
func ircSeed(run func(fixture.Step)) {
	for _, q := range []string{"CREATE SCHEMA irc_scope", "CREATE TABLE irc_scope.parent(id int CONSTRAINT parent_pk PRIMARY KEY)", "CREATE TABLE irc_scope.child(pid int,extra int,CONSTRAINT child_fk FOREIGN KEY(pid) REFERENCES irc_scope.parent)"} { run(fixture.Exec(q, 0)) }
}
func ircHold(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, query); if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = s.Close() })
	return fixture.HeldStatement{Executor:c, Statement:s, SQL:query}
}
func ircCheck(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t,p.Ctx,c,r,s)
	fixture.RunStep(t,p.Ctx,c,r,fixture.Prepared(s))
	fixture.RunStep(t,p.Ctx,held,r,s)
}
