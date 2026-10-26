package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type accessRelationsSignature struct {
	name  string
	types []string
}

func accessRelationsSignatures() []accessRelationsSignature {
	var out []accessRelationsSignature
	for _, name := range []string{"has_table_privilege", "has_sequence_privilege", "has_any_column_privilege", "has_column_privilege"} {
		for _, actor := range []string{"name", "oid", ""} {
			for _, relation := range []string{"text", "oid"} {
				columns := []string{""}
				if name == "has_column_privilege" {
					columns = []string{"text", "smallint"}
				}
				for _, column := range columns {
					var types []string
					if actor != "" {
						types = append(types, actor)
					}
					types = append(types, relation)
					if column != "" {
						types = append(types, column)
					}
					types = append(types, "text")
					out = append(out, accessRelationsSignature{name, types})
				}
			}
		}
	}
	return out
}
func (s accessRelationsSignature) target() string {
	return "pg_catalog." + s.name + "(" + strings.Join(s.types, ",") + ")"
}
func (s accessRelationsSignature) query() string {
	args := make([]string, len(s.types))
	for i, typ := range s.types {
		args[i] = fmt.Sprintf("$%d::%s", i+1, typ)
	}
	return "SELECT " + s.name + "(" + strings.Join(args, ",") + ") AS v"
}
func accessRelationsScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ar_reader", "ar_owner", "ar_group", "ar_set", "ar_delegate"})
	fixture.SchemaReferenceCleanup(t, "ar_scope")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		func() {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for range 3 {
				run(fixture.Exec("CREATE TABLE ar_pressure(v INT)", 0))
				run(fixture.Exec("DROP TABLE ar_pressure", 0))
			}
			for _, q := range []string{"CREATE ROLE ar_reader", "CREATE ROLE ar_owner", "CREATE ROLE ar_group", "CREATE ROLE ar_set", "CREATE ROLE ar_delegate", "GRANT USAGE ON SCHEMA public TO PUBLIC", "GRANT CREATE ON SCHEMA public TO ar_owner", "CREATE TABLE ar_t(a INT,b INT)", "INSERT INTO ar_t VALUES(7,9)", "ALTER TABLE ar_t OWNER TO ar_owner", "CREATE SEQUENCE ar_s START 17", "ALTER SEQUENCE ar_s OWNER TO ar_owner"} {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			body(p, b, c, run)
		}()
	}
}
func accessRelationsArgs(t *testing.T, p *fixture.Pair, c *sql.Conn, s accessRelationsSignature) []any {
	t.Helper()
	var role, relation int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='ar_reader'").Scan(&role); err != nil {
		t.Fatal(err)
	}
	object := "ar_t"
	if s.name == "has_sequence_privilege" {
		object = "ar_s"
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid", object).Scan(&relation); err != nil {
		t.Fatal(err)
	}
	var args []any
	i := 0
	explicit := len(s.types) == 3
	if s.name == "has_column_privilege" {
		explicit = len(s.types) == 4
	}
	if explicit {
		v := any("ar_reader")
		if s.types[0] == "oid" {
			v = role
		}
		args = append(args, v)
		i++
	}
	v := any(object)
	if s.types[i] == "oid" {
		v = relation
	}
	args = append(args, v)
	i++
	if s.name == "has_column_privilege" {
		v = any("a")
		if s.types[i] == "smallint" {
			v = int64(1)
		}
		args = append(args, v)
	}
	return append(args, "SELECT")
}
func accessRelationsRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	held := accessExecutionHeld(t, p, c, s.Query)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}
func accessRelationsError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func TestReferenceAccessRelationsOverloads(t *testing.T) {
	for _, s := range accessRelationsSignatures() {
		t.Run(s.target(), func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				args := accessRelationsArgs(t, p, c, s)
				base := map[string]int64{"has_table_privilege": 1922, "has_sequence_privilege": 2181, "has_column_privilege": 3012, "has_any_column_privilege": 3024}[s.name]
				offset := int64(0)
				for _, candidate := range accessRelationsSignatures() {
					if candidate.name != s.name {
						continue
					}
					if candidate.target() == s.target() {
						break
					}
					offset++
				}
				run(fixture.Select("SELECT oid::bigint AS id,prorettype::bigint AS result,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel FROM pg_proc WHERE oid=$1::regprocedure", []string{"id", "result", "proisstrict", "volatility", "parallel"}, []string{"INT8", "INT8", "BOOL", "TEXT", "TEXT"}, [][]any{{base + offset, int64(16), true, "s", "s"}}, s.target()))
				run(fixture.Exec("GRANT SELECT ON ar_t,ar_s TO ar_reader", 0))
				fixture.RoleGrantActor(run, "ar_reader", func() { accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(s.query(), true, args...)) })
				args[len(args)-1] = "UPDATE"
				fixture.RoleGrantActor(run, "ar_reader", func() { accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(s.query(), false, args...)) })
			})
		})
	}
}
func TestReferenceAccessRelationsMetadata(t *testing.T) {
	for _, sig := range accessRelationsSignatures() {
		t.Run(sig.target(), func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				args := accessRelationsArgs(t, p, c, sig)
				run(fixture.Exec("SET ROLE ar_reader", 0))
				for _, empty := range []bool{false, true} {
					q := sig.query()
					rows := [][]any{{false}}
					if empty {
						q += " WHERE FALSE"
						rows = nil
					}
					s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, rows, args...)
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
						types, err := r.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if len(types) != 1 || types[0].DatabaseTypeName() != "BOOL" {
							t.Fatal(types)
						}
						nullable, known := types[0].Nullable()
						if b.PG {
							if known {
								t.Fatal("pq nullability must be unknown")
							}
						} else if !known || !nullable || types[0].ScanType() != reflect.TypeOf(false) {
							t.Fatal("native BOOL description", nullable, known, types[0].ScanType())
						}
					}
					accessRelationsRoutes(t, p, b, c, s)
				}
				run(fixture.Select(strings.Replace(sig.query(), " AS v", "", 1), []string{sig.name}, []string{"BOOL"}, [][]any{{false}}, args...))
				held := accessExecutionHeld(t, p, c, sig.query())
				for _, null := range []bool{false, true, false} {
					a := append([]any(nil), args...)
					v := any(false)
					if null {
						a[0] = nil
						v = nil
					}
					s := objectGrantAccessValue(sig.query(), v, a...)
					s.CheckScan = func(t *testing.T, r *sql.Rows, _ int) {
						var value sql.NullBool
						if err := r.Scan(&value); err != nil {
							t.Fatal(err)
						}
						if value.Valid == null || value.Bool {
							t.Fatal("BOOL specialized scan", value)
						}
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		})
	}
}
func TestReferenceAccessRelationsNulls(t *testing.T) {
	for _, sig := range accessRelationsSignatures() {
		t.Run(sig.target(), func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				args := accessRelationsArgs(t, p, c, sig)
				run(fixture.Exec("SET ROLE ar_reader", 0))
				for i := range args {
					a := append([]any(nil), args...)
					a[i] = nil
					accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(sig.query(), nil, a...))
					if i != len(a)-1 {
						a[len(a)-1] = "bad"
					}
					accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(sig.query(), nil, a...))
				}
				bad := append([]any(nil), args...)
				bad[len(bad)-1] = "bad"
				held := accessExecutionHeld(t, p, c, sig.query())
				fixture.RunStep(t, p.Ctx, held, b.Recorder, accessRelationsError(sig.query(), "22023", `unrecognized privilege type: "bad"`, bad...))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(sig.query(), false, args...))
			})
		})
	}
}
