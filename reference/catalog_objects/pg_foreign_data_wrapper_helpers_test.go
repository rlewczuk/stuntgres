package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const pgfdwQuery = "SELECT * FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname=$1"
const pgfdwSeed = "CREATE FOREIGN DATA WRAPPER pgfdw_wrapper NO HANDLER NO VALIDATOR OPTIONS(password 'private=value',empty '',remote 'é=🙂')"

var pgfdwColumns = []string{"oid", "fdwname", "fdwowner", "fdwhandler", "fdwvalidator", "fdwacl", "fdwoptions"}
var pgfdwTypes = []string{"OID", "NAME", "OID", "OID", "OID", "_ACLITEM", "_TEXT"}
var pgfdwDrop = []string{
	"DROP SERVER IF EXISTS pgfdw_server CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfdw_wrapper CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfdw_renamed CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfdw_empty CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfdw_tx CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfdw_bad CASCADE",
}

type pgfdwCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	conn *sql.Conn
	statements []*sql.Stmt
	owner int64
}

func pgfdwScenario(t *testing.T, body func(*pgfdwCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"pgfdw_reader", "pgfdw_owner", "pgfdw_newowner"})
	// Global objects outlive public-schema reset. Register before the scenario,
	// so cleanup still runs after a failed body and after its pools have closed.
	t.Cleanup(func() {
		if !fixture.Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range append([]string{"GRANT SELECT ON pg_catalog.pg_foreign_data_wrapper TO PUBLIC"}, pgfdwDrop...) {
			if _, err := fixture.Admin.ExecContext(ctx, q); err != nil {
				t.Error(err)
			}
		}
	})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer conn.Close()
			c := &pgfdwCase{t: t, p: p, b: b, conn: conn}
			defer func() {
				for _, statement := range c.statements { statement.Close() }
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range append([]string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "GRANT SELECT ON pg_catalog.pg_foreign_data_wrapper TO PUBLIC"}, pgfdwDrop...) {
					if _, err := conn.ExecContext(ctx, q); err != nil { t.Error(err) }
				}
			}()
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&c.owner); err != nil { t.Fatal(err) }
			body(c)
		})
	}
}

func (c *pgfdwCase) run(e fixture.Executor, step fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, step)
}
func (c *pgfdwCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries { c.run(c.conn, fixture.Exec(q, 0)) }
}
func (c *pgfdwCase) both(e fixture.Executor, step fixture.Step) {
	c.t.Helper()
	c.run(e, step)
	c.run(e, fixture.Prepared(step))
}
func (c *pgfdwCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := e.PrepareContext(c.p.Ctx, query)
	if err != nil { c.t.Fatal(err) }
	c.statements = append(c.statements, stmt)
	return fixture.HeldStatement{Executor: e, Statement: stmt, SQL: query}
}
func (c *pgfdwCase) oid(name string) int64 {
	c.t.Helper()
	var oid int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname=$1", name).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
		c.t.Fatal("actual wrapper identity", oid, err)
	}
	return oid
}
func (c *pgfdwCase) raw(e fixture.Executor, name string, want [][]any) {
	c.t.Helper()
	fixture.OidReferenceRows(c.t, c.p.Ctx, c.b, e, fixture.Select(pgfdwQuery, pgfdwColumns, pgfdwTypes, want, name))
}
func (c *pgfdwCase) options(e fixture.Executor, name string, want []string) {
	c.t.Helper()
	q := "SELECT fdwoptions[1] AS first,fdwoptions[2] AS second,fdwoptions[3] AS third,array_length(fdwoptions,1) AS length FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname=$1"
	row := []any{nil, nil, nil, nil}
	for i, value := range want { row[i] = value }
	if len(want) != 0 { row[3] = int64(len(want)) }
	c.run(e, fixture.Select(q, []string{"first", "second", "third", "length"}, []string{"TEXT", "TEXT", "TEXT", "INT4"}, [][]any{row}, name))
}
func (c *pgfdwCase) identity(name string, oid, owner int64) {
	c.t.Helper()
	c.both(c.conn, fixture.Select("SELECT w.oid=$2::oid AS same_oid,w.fdwowner=r.oid AS actual_owner,w.fdwhandler=0 AS no_handler,w.fdwvalidator=0 AS no_validator FROM pg_catalog.pg_foreign_data_wrapper w JOIN pg_roles r ON r.oid=$3::oid WHERE w.fdwname=$1", []string{"same_oid", "actual_owner", "no_handler", "no_validator"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, name, oid, owner))
}
func pgfdwMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 7 { t.Fatal("width", len(columns)) }
	for i, column := range columns {
		scan := reflect.TypeFor[string]()
		if pg { scan = reflect.TypeFor[any]() }
		if i == 0 || i >= 2 && i <= 4 {
			scan = reflect.TypeFor[int64]()
			if pg { scan = reflect.TypeFor[any]() }
		}
		if column.Name() != pgfdwColumns[i] || column.DatabaseTypeName() != pgfdwTypes[i] || column.ScanType() != scan { t.Fatal("column metadata", i, column) }
		nullable, known := column.Nullable()
		if known != !pg || !pg && nullable != (i >= 5) { t.Fatal("source nullability", i, nullable, known) }
		if _, ok := column.Length(); ok { t.Fatal("unexpected length", i) }
		if _, _, ok := column.DecimalSize(); ok { t.Fatal("unexpected decimal size", i) }
	}
}
func (c *pgfdwCase) shape(e fixture.Executor, name string, populated bool) {
	c.t.Helper()
	rows, err := e.QueryContext(c.p.Ctx, pgfdwQuery, name)
	if err != nil { c.t.Fatal(err) }
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil { c.t.Fatal(err) }
	pgfdwMetadata(c.t, columns, c.b.PG)
	if rows.Next() != populated { c.t.Fatal("shape cardinality", rows.Err()) }
	if populated {
		values, destinations := make([]any, 7), make([]any, 7)
		for i := range values { destinations[i] = &values[i] }
		if err := rows.Scan(destinations...); err != nil { c.t.Fatal(err) }
		for i, value := range values { fixture.AssertRawValue(c.t, c.b.PG, pgfdwTypes[i], value) }
		if rows.Next() { c.t.Fatal("extra wrapper") }
	}
	if rows.Err() != nil { c.t.Fatal(rows.Err()) }
	if err := rows.Close(); err != nil { c.t.Fatal(err) }
	pgfdwMetadata(c.t, columns, c.b.PG)
}
func (c *pgfdwCase) error(e fixture.Executor, q, code, message string, prepared bool) {
	c.t.Helper()
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	step.Prepared, step.ErrorStage = prepared, "execute"
	c.run(e, step)
}
