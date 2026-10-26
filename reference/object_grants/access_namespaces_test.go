package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

// Each scenario establishes its ACLs; retained cluster objects are restored even
// on assertion failure. Dynamic identities are discovered, never allocator goldens.
func accessNamespacesSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) []accessExecutionObject {
	t.Helper()
	var database string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if b.PG {
		var dbACL, spcACL sql.NullString
		var dbOwner, spcOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT datacl::text,datdba::regrole::text FROM pg_database WHERE datname=$1", database).Scan(&dbACL, &dbOwner); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT spcacl::text,spcowner::regrole::text FROM pg_tablespace WHERE spcname='pg_default'").Scan(&spcACL, &spcOwner); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			conn, err := b.DB.Conn(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			accessExecutionResetConnection(t, conn)
			// Ownership must be restored before removing grants: GRANT/REVOKE
			// updates shared-role dependencies, unlike the exact raw ACL repair.
			for _, q := range []string{"ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(dbOwner), "ALTER TABLESPACE pg_default OWNER TO " + pq.QuoteIdentifier(spcOwner)} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Error(err)
				}
			}
			for _, q := range []string{"REVOKE ALL ON DATABASE " + pq.QuoteIdentifier(database) + " FROM og_object_reader,og_object_other CASCADE", "REVOKE ALL ON TABLESPACE pg_default FROM og_object_reader,og_object_other CASCADE"} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Error(err)
				}
			}
			if _, err := conn.ExecContext(ctx, "UPDATE pg_database SET datacl=$1::aclitem[],datdba=$2::regrole WHERE datname=$3", dbACL, dbOwner, database); err != nil {
				t.Error(err)
			}
			if _, err := conn.ExecContext(ctx, "UPDATE pg_tablespace SET spcacl=$1::aclitem[],spcowner=$2::regrole WHERE spcname='pg_default'", spcACL, spcOwner); err != nil {
				t.Error(err)
			}
		})
	}
	// The reference fixture recreates public without its usual PUBLIC USAGE.
	// Actor sessions must resolve the independently created input relation.
	if b.PG {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
	}
	for _, q := range []string{"CREATE SCHEMA an_private", "CREATE DOMAIN an_private.d AS integer", "GRANT USAGE ON SCHEMA an_private TO og_object_reader"} {
		run(fixture.Exec(q, 0))
	}
	objects := []accessExecutionObject{
		{"has_database_privilege", database, "DATABASE " + pq.QuoteIdentifier(database), "CONNECT", "pg_database", "datname=" + pq.QuoteLiteral(database), 0, false},
		{"has_schema_privilege", "an_private", "SCHEMA an_private", "USAGE", "pg_namespace", "nspname='an_private'", 0, false},
		{"has_type_privilege", "an_private.d", "TYPE an_private.d", "USAGE", "pg_type", "oid='an_private.d'::regtype", 0, false},
		{"has_tablespace_privilege", "pg_default", "TABLESPACE pg_default", "CREATE", "pg_tablespace", "spcname='pg_default'", 0, false},
	}
	for i := range objects {
		o := &objects[i]
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM "+o.catalog+" WHERE "+o.key).Scan(&o.oid); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("REVOKE ALL ON "+o.target+" FROM PUBLIC,og_object_reader,og_object_other", 0))
	}
	return objects
}

func accessNamespacesMetadata(b fixture.Backend) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(types) != 1 || types[0].DatabaseTypeName() != "BOOL" {
			t.Fatal(types)
		}
		if b.PG {
			if _, known := types[0].Nullable(); known {
				t.Fatal("pq nullability unexpectedly known")
			}
		} else {
			if types[0].ScanType() != reflect.TypeOf(false) {
				t.Fatal(types[0].ScanType())
			}
			if nullable, known := types[0].Nullable(); !known || !nullable {
				t.Fatal(nullable, known)
			}
		}
	}
}

func accessNamespacesOverloads(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), objects []accessExecutionObject) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE an_inputs(an name,ao oid,ot text,oo oid,pr text)", 0))
	run(fixture.Exec("GRANT SELECT ON an_inputs TO og_object_reader", 0))
	for _, o := range objects {
		run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader", 0))
		run(fixture.Exec("TRUNCATE an_inputs", 0))
		run(fixture.Exec("INSERT INTO an_inputs SELECT rolname,oid,$1::text,$2::oid,$3::text FROM pg_roles WHERE rolname='og_object_reader'", 1, o.name, o.oid, o.privilege))
		for _, call := range accessExecutionCalls(t, p, c, o) {
			held := accessExecutionHeld(t, p, c, call.query)
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				s := objectGrantAccessValue(call.query, true, call.args...)
				s.CheckMetadata = accessNamespacesMetadata(b)
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				literal := call.query
				for i, arg := range call.args {
					value := fmt.Sprint(arg)
					if text, ok := arg.(string); ok {
						value = pq.QuoteLiteral(text)
					}
					literal = strings.ReplaceAll(literal, fmt.Sprintf("$%d", i+1), value)
				}
				run(objectGrantAccessValue(literal, true))
				run(objectGrantAccessValue(accessExecutionColumnCall(call)+" FROM an_inputs", true))
				empty := fixture.Select(accessExecutionColumnCall(call)+" FROM an_inputs WHERE FALSE", []string{"v"}, []string{"BOOL"}, nil)
				empty.CheckMetadata = accessNamespacesMetadata(b)
				run(empty)
			})
		}
		q := "SELECT " + o.function + "('og_object_reader',$1::text,$2::text)"
		s := fixture.Select(q, []string{o.function}, []string{"BOOL"}, [][]any{{true}}, o.name, o.privilege)
		s.CheckMetadata = accessNamespacesMetadata(b)
		run(s)
	}
	// Inputs are scaffolding, not relation-ACL persistence state.
	run(fixture.Exec("DROP TABLE an_inputs", 0))
}

func TestReferenceAccessNamespacesOverloadsMetadata(t *testing.T) {
	objectGrantScenario(t, []string{"an_private"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		objects := accessNamespacesSetup(t, p, b, c, run)
		accessNamespacesOverloads(t, p, b, c, run, objects)
		for _, family := range []struct {
			name string
			base int64
		}{{"has_database_privilege", 2250}, {"has_schema_privilege", 2268}, {"has_tablespace_privilege", 2390}, {"has_type_privilege", 3138}} {
			for i, formal := range []string{"19 25 25", "19 26 25", "26 25 25", "26 26 25", "25 25", "26 25"} {
				suffix := []string{"_name_name", "_name_id", "_id_name", "_id_id", "_name", "_id"}[i]
				nargs := int64(3)
				if i >= 4 {
					nargs = 2
				}
				row := []any{family.base + int64(i), family.name, int64(11), int64(12), int64(16), nargs, true, "s", "s", false, "f", true, family.name + suffix}
				// pq exposes OID values as text; native exposes numeric values.
				if b.PG {
					for _, column := range []int{0, 2, 3, 4} {
						row[column] = fmt.Sprint(row[column])
					}
				}
				run(fixture.Select("SELECT oid,proname,pronamespace,prolang,prorettype,pronargs,proisstrict,provolatile::text,proparallel::text,proretset,prokind::text,proargnames IS NULL AS unnamed,prosrc FROM pg_proc WHERE oid=$1::oid", []string{"oid", "proname", "pronamespace", "prolang", "prorettype", "pronargs", "proisstrict", "provolatile", "proparallel", "proretset", "prokind", "unnamed", "prosrc"}, []string{"OID", "NAME", "OID", "OID", "OID", "INT2", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "BOOL", "TEXT"}, [][]any{row}, family.base+int64(i)))
				formals := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(formal, "19", "name"), "25", "text"), "26", "oid")
				signature := "pg_catalog." + family.name + "(" + strings.ReplaceAll(formals, " ", ",") + ")"
				run(fixture.Select("SELECT $1::regprocedure::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{family.base + int64(i)}}, signature))
			}
		}
	})
}
