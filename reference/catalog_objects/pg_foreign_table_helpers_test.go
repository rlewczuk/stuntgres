package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const pgftQuery = "SELECT * FROM pg_catalog.pg_foreign_table WHERE ftrelid=$1"
const pgftArray = "{remote=private=value,empty=,unicode=é🙂}"
var pgftColumns = []string{"ftrelid", "ftserver", "ftoptions"}
var pgftTypes = []string{"OID", "OID", "_TEXT"}
var pgftOptions = []string{"remote=private=value", "empty=", "unicode=é🙂"}
var pgftDrop = []string{
	"DROP SCHEMA IF EXISTS pgft_a CASCADE", "DROP SCHEMA IF EXISTS pgft_b CASCADE",
	"DROP SERVER IF EXISTS pgft_server CASCADE", "DROP SERVER IF EXISTS pgft_renamed CASCADE", "DROP SERVER IF EXISTS pgft_other CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgft_wrapper CASCADE",
}

type pgftCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	conn *sql.Conn
	statements []*sql.Stmt
}

func pgftScenario(t *testing.T, body func(*pgftCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"pgft_reader"})
	t.Cleanup(func() {
		if !fixture.Started { return }
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second); defer cancel()
		for _, q := range append([]string{"GRANT SELECT ON pg_catalog.pg_foreign_table TO PUBLIC"}, pgftDrop...) {
			if _, err := fixture.Admin.ExecContext(ctx, q); err != nil { t.Error(err) }
		}
	})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
			c := &pgftCase{t: t, p: p, b: b, conn: conn}
			defer func() {
				for _, s := range c.statements { s.Close() }
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second); defer cancel()
				for _, q := range append([]string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "GRANT SELECT ON pg_catalog.pg_foreign_table TO PUBLIC", "REVOKE SELECT(ftrelid) ON pg_catalog.pg_foreign_table FROM PUBLIC"}, pgftDrop...) {
					if _, err := conn.ExecContext(ctx, q); err != nil { t.Error(err) }
				}
			}()
			body(c)
		})
	}
}
func (c *pgftCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper(); fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *pgftCase) exec(queries ...string) {
	c.t.Helper(); for _, q := range queries { c.run(c.conn, fixture.Exec(q, 0)) }
}
func (c *pgftCase) both(e fixture.Executor, s fixture.Step) {
	c.t.Helper(); c.run(e, s); c.run(e, fixture.Prepared(s))
}
func (c *pgftCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
	c.t.Helper(); s, err := e.PrepareContext(c.p.Ctx, query); if err != nil { c.t.Fatal(err) }
	c.statements = append(c.statements, s)
	return fixture.HeldStatement{Executor: e, Statement: s, SQL: query}
}
func (c *pgftCase) seed() {
	c.t.Helper()
	c.exec("CREATE FOREIGN DATA WRAPPER pgft_wrapper NO HANDLER NO VALIDATOR OPTIONS(scope 'wrapper')",
		"CREATE SERVER pgft_server FOREIGN DATA WRAPPER pgft_wrapper OPTIONS(scope 'server')",
		"CREATE USER MAPPING FOR PUBLIC SERVER pgft_server OPTIONS(scope 'mapping')",
		"CREATE SCHEMA pgft_a", "CREATE SCHEMA pgft_b",
		"CREATE FOREIGN TABLE pgft_a.item(a integer OPTIONS(scope 'column')) SERVER pgft_server OPTIONS(remote 'private=value',empty '',unicode 'é🙂')",
		"CREATE FOREIGN TABLE pgft_b.item() SERVER pgft_server",
		"CREATE TABLE pgft_a.ordinary(a integer)", "CREATE VIEW pgft_a.decoy AS SELECT a FROM pgft_a.ordinary")
}
func (c *pgftCase) oid(schema, name string) int64 {
	c.t.Helper(); var oid int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT c.oid FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2", schema, name).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 { c.t.Fatal("actual relation identity", oid, err) }
	return oid
}
func (c *pgftCase) server(name string) int64 {
	c.t.Helper(); var oid int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_catalog.pg_foreign_server WHERE srvname=$1", name).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 { c.t.Fatal("actual server identity", oid, err) }
	return oid
}
func (c *pgftCase) raw(e fixture.Executor, oid int64, want [][]any) {
	c.t.Helper()
	step := fixture.Select(pgftQuery, pgftColumns, pgftTypes, want, oid)
	fixture.OidReferenceRows(c.t, c.p.Ctx, c.b, e, step)
	if _, held := e.(fixture.HeldStatement); !held { fixture.OidReferenceRows(c.t, c.p.Ctx, c.b, e, fixture.Prepared(step)) }
}
func (c *pgftCase) options(e fixture.Executor, oid int64, want []string) {
	c.t.Helper()
	fields, columns, types := []string{}, []string{}, []string{}
	row := []any{}
	for i, value := range want {
		fields = append(fields, fmt.Sprintf("ftoptions[%d] AS option%d", i+1, i+1))
		columns, types, row = append(columns, fmt.Sprintf("option%d", i+1)), append(types, "TEXT"), append(row, value)
	}
	fields = append(fields, "ftoptions IS NULL AS absent", "array_length(ftoptions,1) AS length", "array_lower(ftoptions,1) AS lower", "array_upper(ftoptions,1) AS upper")
	columns, types = append(columns, "absent", "length", "lower", "upper"), append(types, "BOOL", "INT4", "INT4", "INT4")
	if len(want) == 0 { row = append(row, true, nil, nil, nil) } else { row = append(row, false, int64(len(want)), int64(1), int64(len(want))) }
	c.both(e, fixture.Select("SELECT "+strings.Join(fields, ",")+" FROM pg_catalog.pg_foreign_table WHERE ftrelid=$1", columns, types, [][]any{row}, oid))
}
func pgftMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper(); if len(columns) != 3 { t.Fatal("width", len(columns)) }
	for i, column := range columns {
		scan := reflect.TypeFor[int64](); if i == 2 { scan = reflect.TypeFor[string]() }; if pg { scan = reflect.TypeFor[any]() }
		if column.Name() != pgftColumns[i] || column.DatabaseTypeName() != pgftTypes[i] || column.ScanType() != scan { t.Fatal("metadata", i, column) }
		if nullable, known := column.Nullable(); known != !pg || !pg && nullable != (i == 2) { t.Fatal("nullability", i, nullable, known) }
		if n, known := column.Length(); n != 0 || known { t.Fatal("length", i, n, known) }
		if p, s, known := column.DecimalSize(); p != 0 || s != 0 || known { t.Fatal("decimal size", i, p, s, known) }
	}
}
func (c *pgftCase) shape(e fixture.Executor, oid int64, populated bool) {
	c.t.Helper(); rows, err := e.QueryContext(c.p.Ctx, pgftQuery, oid); if err != nil { c.t.Fatal(err) }; defer rows.Close()
	columns, err := rows.ColumnTypes(); if err != nil { c.t.Fatal(err) }; pgftMetadata(c.t, columns, c.b.PG)
	if rows.Next() != populated { c.t.Fatal("cardinality", rows.Err()) }
	if populated {
		var a, b, options any
		if err := rows.Scan(&a, &b, &options); err != nil { c.t.Fatal(err) }
		for i, v := range []any{a, b, options} { fixture.OidReferenceValue(c.t, c.b.PG, pgftTypes[i], v) }
		if rows.Next() { c.t.Fatal("extra row") }
	}
	if rows.Err() != nil { c.t.Fatal(rows.Err()) }; if err := rows.Close(); err != nil { c.t.Fatal(err) }; pgftMetadata(c.t, columns, c.b.PG)
}
func (c *pgftCase) facts() {
	c.t.Helper()
	c.both(c.conn, fixture.Select("SELECT attname,atttypid::integer,atttypmod,attnotnull,attndims,attcollation::integer FROM pg_catalog.pg_attribute WHERE attrelid=3118 AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attnotnull", "attndims", "attcollation"}, []string{"NAME", "INT4", "INT4", "BOOL", "INT2", "INT4"}, [][]any{{"ftrelid", int64(26), int64(-1), true, int64(0), int64(0)}, {"ftserver", int64(26), int64(-1), true, int64(0), int64(0)}, {"ftoptions", int64(1009), int64(-1), false, int64(1), int64(950)}}))
	c.both(c.conn, fixture.Select("SELECT oid::integer,relkind,relhasindex FROM pg_catalog.pg_class WHERE oid=3118", []string{"oid", "relkind", "relhasindex"}, []string{"INT4", "CHAR", "BOOL"}, [][]any{{int64(3118), "r", true}}))
}
func (c *pgftCase) identity() {
	c.t.Helper()
	c.both(c.conn, fixture.Select("SELECT n.nspname,c.relname,c.relkind,s.srvname,f.ftrelid=c.oid AS relation_link,f.ftserver=s.oid AS server_link FROM pg_catalog.pg_foreign_table f JOIN pg_catalog.pg_class c ON c.oid=f.ftrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_foreign_server s ON s.oid=f.ftserver WHERE n.nspname IN ('pgft_a','pgft_b') ORDER BY n.nspname,c.relname", []string{"nspname", "relname", "relkind", "srvname", "relation_link", "server_link"}, []string{"NAME", "NAME", "CHAR", "NAME", "BOOL", "BOOL"}, [][]any{{"pgft_a", "item", "f", "pgft_server", true, true}, {"pgft_b", "item", "f", "pgft_server", true, true}}))
}
