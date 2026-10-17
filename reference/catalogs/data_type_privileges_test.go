package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var dtpColumns = []string{"object_catalog", "object_schema", "object_name", "object_type", "dtd_identifier"}
var dtpTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "NAME"}
var dtpRoles = []string{"dtp_reader", "dtp_owner", "dtp_member", "dtp_set"}

// Every backend executes the same independently expected scenario.
func dtpScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, dtpRoles)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer func() {
						if !b.PG {
							return
						}
						if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
							t.Error(err)
						}
						// Defaults belonging to a case role outlive public-schema reset.
						for _, role := range dtpRoles {
							if _, err := c.ExecContext(p.Ctx, "DROP OWNED BY "+role+" CASCADE"); err != nil {
								t.Error(err)
							}
						}
					}()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
							t.FailNow()
						}
					}
					for _, role := range dtpRoles {
						run(fixture.Exec("CREATE ROLE "+role, 0))
					}
					body(t, p, b, c, run)
				})
			}
		})
	}
}

func dtpActor(run func(fixture.Step), actor string, body func()) {
	run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
	body()
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}

// Expectations contain only literal descriptor facts. The environment-dependent
// database is obtained independently, never learned from the view under test.
func dtpProjection(t *testing.T, p *fixture.Pair, c *sql.Conn, where string, want [][]any) fixture.Step {
	t.Helper()
	var database string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database == "" {
		t.Fatal("empty database identity")
	}
	rows := make([][]any, 0, len(want))
	for _, row := range want {
		if len(row) != 4 {
			t.Fatal("invalid descriptor expectation", row)
		}
		rows = append(rows, append([]any{database}, row...))
	}
	s := fixture.Select("SELECT * FROM information_schema.data_type_privileges WHERE "+where, dtpColumns, dtpTypes, rows)
	s.Bag = true
	return s
}

func dtpRows(schema, name, kind string, ids ...string) [][]any {
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{schema, name, kind, id})
	}
	return rows
}

func TestReferenceDataTypePrivilegesShape(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE dtp_shape(a integer,b varchar(7)[])", 0))
		for _, name := range []string{"dtp_shape", "dtp_absent"} {
			var want [][]any
			if name == "dtp_shape" {
				want = dtpRows("public", name, "TABLE", "1", "2")
			}
			s := dtpProjection(t, p, c, "object_schema='public' AND object_name='"+name+"'", want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, types, dtpColumns, dtpTypes, []bool{true, true, true, true, true})
				for i, typ := range types {
					length, known := typ.Length()
					wantLength, wantKnown := int64(0), false
					if i == 3 {
						wantLength, wantKnown = math.MaxInt64, true
						if b.PG {
							wantLength = -5
						}
					}
					if length != wantLength || known != wantKnown {
						t.Fatalf("column %d length=%d/%t want %d/%t", i, length, known, wantLength, wantKnown)
					}
					if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
						t.Fatalf("column %d decimal=%d/%d/%t", i, precision, scale, known)
					}
				}
			}
			s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
				want := reflect.TypeFor[string]()
				if b.PG && column != 3 {
					want = reflect.TypeFor[[]byte]()
				}
				if reflect.TypeOf(value) != want {
					t.Fatalf("column %d raw %T want %v", column, value, want)
				}
				return true
			}
			run(s)
		}
		if b.PG {
			// PostgreSQL declares information-schema domains, independently of
			// pq's base-carrier protocol metadata.
			var declared [][]any
			for i, name := range dtpColumns {
				typeName := "sql_identifier"
				if i == 3 {
					typeName = "character_data"
				}
				declared = append(declared, []any{name, typeName})
			}
			run(fixture.Select("SELECT a.attname,t.typname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid WHERE a.attrelid='information_schema.data_type_privileges'::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "typname"}, []string{"NAME", "NAME"}, declared))
		} else {
			// Native declares existing builtin carriers; information-schema
			// sql_identifier/character_data domains remain lookup-only.
			var attributes [][]any
			for i, name := range dtpColumns {
				typeOID, collation := int64(19), int64(950)
				if i == 3 {
					typeOID = 1043
				}
				attributes = append(attributes, []any{name, int64(i + 1), typeOID, false, collation})
			}
			run(fixture.Select("SELECT attname,attnum,atttypid,attnotnull,attcollation FROM pg_catalog.pg_attribute WHERE attrelid='information_schema.data_type_privileges'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "attnotnull", "attcollation"}, []string{"NAME", "INT2", "OID", "BOOL", "OID"}, attributes))
		}
	})
}

func TestReferenceDataTypePrivilegesDescriptors(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE DOMAIN dtp_domain AS varchar(7)",
			"CREATE TYPE dtp_enum AS ENUM ('a','b')",
			"CREATE TYPE dtp_composite AS (a integer,b varchar(7),c integer[])",
			"CREATE TYPE dtp_empty AS ()",
			"CREATE TABLE dtp_mixed(a integer,b varchar(7),c dtp_domain,d dtp_enum,e dtp_composite,f integer[][],g dtp_domain[])",
		} {
			run(fixture.Exec(q, 0))
		}
		want := dtpRows("public", "dtp_domain", "DOMAIN", "1")
		want = append(want, dtpRows("public", "dtp_composite", "USER-DEFINED TYPE", "1", "2", "3")...)
		want = append(want, dtpRows("public", "dtp_mixed", "TABLE", "1", "2", "3", "4", "5", "6", "7")...)
		run(dtpProjection(t, p, c, "object_schema='public' AND object_name IN ('dtp_domain','dtp_enum','_dtp_enum','dtp_composite','dtp_empty','dtp_mixed')", want))
		if !b.PG {
			// Native has no routine parameter/return descriptor capability.
			run(dtpProjection(t, p, c, "object_type='ROUTINE'", nil))
		}
	})
}

func TestReferenceDataTypePrivilegesIdentity(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE TABLE dtp_identity(a varchar(7),b varchar,c varchar(7)[])",
			"CREATE TYPE dtp_identity_type AS (a integer,b varchar(7),c integer[])",
		} {
			run(fixture.Exec(q, 0))
		}
		check := func(table, typ string, ids ...string) {
			want := dtpRows("public", table, "TABLE", ids...)
			want = append(want, dtpRows("public", typ, "USER-DEFINED TYPE", ids...)...)
			run(dtpProjection(t, p, c, "object_schema='public' AND object_name IN ('"+table+"','"+typ+"')", want))
		}
		check("dtp_identity", "dtp_identity_type", "1", "2", "3")
		for _, q := range []string{
			"ALTER TABLE dtp_identity DROP COLUMN b",
			"ALTER TABLE dtp_identity ADD COLUMN d varchar",
			"ALTER TABLE dtp_identity RENAME COLUMN a TO renamed",
			"ALTER TABLE dtp_identity ALTER COLUMN renamed TYPE varchar(12)",
			"ALTER TABLE dtp_identity RENAME TO dtp_identity_renamed",
			"ALTER TYPE dtp_identity_type DROP ATTRIBUTE b",
			"ALTER TYPE dtp_identity_type ADD ATTRIBUTE d varchar(7)[]",
			"ALTER TYPE dtp_identity_type RENAME ATTRIBUTE a TO renamed",
			"ALTER TYPE dtp_identity_type ALTER ATTRIBUTE renamed TYPE bigint",
		} {
			run(fixture.Exec(q, 0))
		}
		check("dtp_identity_renamed", "dtp_identity_type", "1", "3", "4")
		rename := "ALTER TYPE dtp_identity_type RENAME TO dtp_identity_type_renamed"
		if b.PG {
			run(fixture.Exec(rename, 0))
			check("dtp_identity_renamed", "dtp_identity_type_renamed", "1", "3", "4")
		} else {
			run(fixture.ImplicitError(rename, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "TO"`, Position: "37"}))
			check("dtp_identity_renamed", "dtp_identity_type", "1", "3", "4")
			run(dtpProjection(t, p, c, "object_schema='public' AND object_name='dtp_identity_type_renamed'", nil))
		}
	})
}

func TestReferenceDataTypePrivilegesBuiltinDomains(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		dtpActor(run, "dtp_reader", func() {
			var want [][]any
			for _, name := range []string{"cardinal_number", "character_data", "sql_identifier", "time_stamp", "yes_or_no"} {
				want = append(want, dtpRows("information_schema", name, "DOMAIN", "1")...)
			}
			run(dtpProjection(t, p, c, "object_schema='information_schema' AND object_type='DOMAIN' AND object_name IN ('cardinal_number','character_data','sql_identifier','time_stamp','yes_or_no')", want))
			run(dtpProjection(t, p, c, "object_schema='pg_catalog' AND object_name IN ('int4','text','_int4','varchar')", nil))
		})
	})
}
