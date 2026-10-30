package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const pgfsQuery = "SELECT * FROM pg_catalog.pg_foreign_server WHERE srvname=$1"
const pgfsSeed = "CREATE SERVER pgfs_server TYPE '' VERSION '18' FOREIGN DATA WRAPPER pgfs_wrapper OPTIONS(password 'private=value',empty '',remote 'é=🙂')"

var pgfsColumns = []string{"oid", "srvname", "srvowner", "srvfdw", "srvtype", "srvversion", "srvacl", "srvoptions"}
var pgfsTypes = []string{"OID", "NAME", "OID", "OID", "TEXT", "TEXT", "_ACLITEM", "_TEXT"}
var pgfsDrop = []string{
	"DROP SERVER IF EXISTS pgfs_server CASCADE",
	"DROP SERVER IF EXISTS pgfs_renamed CASCADE",
	"DROP SERVER IF EXISTS pgfs_empty CASCADE",
	"DROP SERVER IF EXISTS pgfs_tx CASCADE",
	"DROP SERVER IF EXISTS pgfs_bad CASCADE",
	"DROP SERVER IF EXISTS pgfs_other CASCADE",
	`DROP SERVER IF EXISTS "PGFS Quoted" CASCADE`,
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfs_wrapper CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfs_renamed_wrapper CASCADE",
	"DROP FOREIGN DATA WRAPPER IF EXISTS pgfs_other_wrapper CASCADE",
}

type pgfsCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	conn *sql.Conn
	statements []*sql.Stmt
	owner int64
}

func pgfsScenario(t *testing.T, body func(*pgfsCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"pgfs_reader", "pgfs_owner", "pgfs_newowner", "pgfs_member"})
	// Global objects outlive public-schema reset. Register before the scenario,
	// so cleanup still runs after a failed body and after its pools have closed.
	t.Cleanup(func() {
		if !fixture.Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range append([]string{"GRANT SELECT ON pg_catalog.pg_foreign_server TO PUBLIC"}, pgfsDrop...) {
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
			c := &pgfsCase{t: t, p: p, b: b, conn: conn}
			defer func() {
				for _, statement := range c.statements { statement.Close() }
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range append([]string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "GRANT SELECT ON pg_catalog.pg_foreign_server TO PUBLIC"}, pgfsDrop...) {
					if _, err := conn.ExecContext(ctx, q); err != nil { t.Error(err) }
				}
			}()
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&c.owner); err != nil { t.Fatal(err) }
			body(c)
		})
	}
}

func (c *pgfsCase) run(e fixture.Executor, step fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, step)
}
func (c *pgfsCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries { c.run(c.conn, fixture.Exec(q, 0)) }
}
func (c *pgfsCase) both(e fixture.Executor, step fixture.Step) {
	c.t.Helper()
	c.run(e, step)
	c.run(e, fixture.Prepared(step))
}
func (c *pgfsCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := e.PrepareContext(c.p.Ctx, query)
	if err != nil { c.t.Fatal(err) }
	c.statements = append(c.statements, stmt)
	return fixture.HeldStatement{Executor: e, Statement: stmt, SQL: query}
}
func (c *pgfsCase) oid(name string) int64 {
	c.t.Helper()
	var oid int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_catalog.pg_foreign_server WHERE srvname=$1", name).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
		c.t.Fatal("actual server identity", oid, err)
	}
	return oid
}
func (c *pgfsCase) raw(e fixture.Executor, name string, want [][]any) {
	c.t.Helper()
	fixture.OidReferenceRows(c.t, c.p.Ctx, c.b, e, fixture.Select(pgfsQuery, pgfsColumns, pgfsTypes, want, name))
}
func (c *pgfsCase) options(e fixture.Executor, name string, want []string) {
	c.t.Helper()
	q := "SELECT srvoptions[1] AS first,srvoptions[2] AS second,srvoptions[3] AS third,array_length(srvoptions,1) AS length FROM pg_catalog.pg_foreign_server WHERE srvname=$1"
	row := []any{nil, nil, nil, nil}
	for i, value := range want { row[i] = value }
	if len(want) != 0 { row[3] = int64(len(want)) }
	c.run(e, fixture.Select(q, []string{"first", "second", "third", "length"}, []string{"TEXT", "TEXT", "TEXT", "INT4"}, [][]any{row}, name))
}
func (c *pgfsCase) wrapper() {
	c.t.Helper()
	c.exec("CREATE FOREIGN DATA WRAPPER pgfs_wrapper NO HANDLER NO VALIDATOR")
}
func (c *pgfsCase) wrapperOID() int64 {
	c.t.Helper()
	var oid int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname='pgfs_wrapper'").Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 { c.t.Fatal("actual wrapper identity", oid, err) }
	return oid
}
func (c *pgfsCase) identity(name string, oid, owner, wrapper int64) {
	c.t.Helper()
	c.both(c.conn, fixture.Select("SELECT s.oid=$2::oid AS same_oid,s.srvowner=r.oid AS actual_owner,s.srvfdw=w.oid AS actual_wrapper FROM pg_catalog.pg_foreign_server s JOIN pg_roles r ON r.oid=$3::oid JOIN pg_catalog.pg_foreign_data_wrapper w ON w.oid=$4::oid WHERE s.srvname=$1", []string{"same_oid", "actual_owner", "actual_wrapper"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, name, oid, owner, wrapper))
}
func (c *pgfsCase) row(name string, oid, owner, wrapper int64, kind, version, acl, options any) []any {
	return []any{oid, name, owner, wrapper, kind, version, acl, options}
}
func pgfsMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 8 { t.Fatal("width", len(columns)) }
	for i, column := range columns {
		scan := reflect.TypeFor[string]()
		if pg && pgfsTypes[i] != "TEXT" { scan = reflect.TypeFor[any]() }
		if i == 0 || i >= 2 && i <= 3 {
			scan = reflect.TypeFor[int64]()
			if pg { scan = reflect.TypeFor[any]() }
		}
		if column.Name() != pgfsColumns[i] || column.DatabaseTypeName() != pgfsTypes[i] || column.ScanType() != scan { t.Fatal("column metadata", i, column) }
		nullable, known := column.Nullable()
		if known != !pg || !pg && nullable != (i >= 4) { t.Fatal("source nullability", i, nullable, known) }
		length, lengthKnown := int64(0), false
		if pgfsTypes[i] == "TEXT" {
			length, lengthKnown = math.MaxInt64, true
		}
		if n, known := column.Length(); n != length || known != lengthKnown {
			t.Fatal("source length", i, n, known, "want", length, lengthKnown)
		}
		if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("source decimal size", i, precision, scale, known)
		}
	}
}
func (c *pgfsCase) shape(e fixture.Executor, name string, populated bool) {
	c.t.Helper()
	rows, err := e.QueryContext(c.p.Ctx, pgfsQuery, name)
	if err != nil { c.t.Fatal(err) }
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil { c.t.Fatal(err) }
	pgfsMetadata(c.t, columns, c.b.PG)
	if rows.Next() != populated { c.t.Fatal("shape cardinality", rows.Err()) }
	if populated {
		values, destinations := make([]any, 8), make([]any, 8)
		for i := range values { destinations[i] = &values[i] }
		if err := rows.Scan(destinations...); err != nil { c.t.Fatal(err) }
		for i, value := range values { fixture.AssertRawValue(c.t, c.b.PG, pgfsTypes[i], value) }
		if rows.Next() { c.t.Fatal("extra server") }
	}
	if rows.Err() != nil { c.t.Fatal(rows.Err()) }
	if err := rows.Close(); err != nil { c.t.Fatal(err) }
	pgfsMetadata(c.t, columns, c.b.PG)
}
func (c *pgfsCase) error(e fixture.Executor, q, code, message string, prepared bool) {
	c.t.Helper()
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	step.Prepared, step.ErrorStage = prepared, "execute"
	c.run(e, step)
}
