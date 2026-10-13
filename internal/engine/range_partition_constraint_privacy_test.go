package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Catalog ACL manifests supplement the shared public GRANT coverage in
// TestReferenceRangePartitionConstraintPrivacy.
func TestRangePartitionConstraintPrivacy(t *testing.T) {
	for _, target := range []string{"rp_constraint_leaf", "rp_constraint_mid"} {
		for _, mode := range []string{"insert_only", "column_select", "all_column_select", "table_select", "supplied"} {
			for _, prepared := range []bool{false, true} {
				route := "direct"
				if prepared {
					route = "prepared"
				}
				t.Run(target+"/"+mode+"/"+route, func(t *testing.T) {
					e := New()
					admin, done := roleOperation(t, e, "postgres")
					defer done()
					for _, query := range []string{
						"CREATE USER rp_constraint_actor",
						"CREATE TABLE rp_constraint_root(k INT,payload TEXT DEFAULT 'private-value',readable INT DEFAULT 99) PARTITION BY RANGE(k)",
						"CREATE TABLE rp_constraint_leaf PARTITION OF rp_constraint_root FOR VALUES FROM(1) TO(3)",
						"CREATE TABLE rp_constraint_mid PARTITION OF rp_constraint_root FOR VALUES FROM(3) TO(5) PARTITION BY RANGE(readable)",
						"CREATE TABLE rp_constraint_deep PARTITION OF rp_constraint_mid FOR VALUES FROM(10) TO(20)",
						"INSERT INTO rp_constraint_root VALUES(1,'sentinel',11),(3,'sentinel',11)",
					} {
						roleExecute(t, e, admin, query)
					}
					actor, ok := e.Roles().Lookup("rp_constraint_actor")
					if !ok {
						t.Fatal("actor missing")
					}
					ref := roleTableRef(t, e, target)
					grant := func(attribute int32, privileges authorization.Privileges) {
						t.Helper()
						column := ref
						column.Address.SubID = attribute
						if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}); err != nil {
							t.Fatal(err)
						}
					}
					grant(0, authorization.InsertPrivilege)
					detail := "Failing row contains (k) = (9)."
					switch mode {
					case "column_select":
						grant(3, authorization.SelectPrivilege)
						detail = "Failing row contains (k, readable) = (9, 99)."
					case "all_column_select":
						for _, attribute := range []int32{1, 2, 3} {
							grant(attribute, authorization.SelectPrivilege)
						}
						detail = "Failing row contains (k, payload, readable) = (9, private-value, 99)."
					case "table_select":
						grant(0, authorization.InsertPrivilege|authorization.SelectPrivilege)
						detail = "Failing row contains (9, private-value, 99)."
					case "supplied":
						detail = "Failing row contains (k, payload) = (9, supplied-value)."
					}
					op, finish := roleOperation(t, e, "rp_constraint_actor")
					defer finish()
					query := "INSERT INTO " + target + "(k) VALUES(9)"
					count := 0
					if prepared {
						query = "INSERT INTO " + target + "(k) VALUES($1)"
						count = 1
					}
					if mode == "supplied" {
						query = "INSERT INTO " + target + "(payload,k) VALUES('supplied-value',9)"
						if prepared {
							query = "INSERT INTO " + target + "(payload,k) VALUES($1,$2)"
							count = 2
						}
					}
					ast := statement(t, query)
					var description *StatementDescription
					if prepared {
						desc, err := e.Describe(op, ast)
						if err != nil {
							t.Fatal(err)
						}
						description = &desc
					}
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					scope.Expected = description
					result, err := scope.Execute(ast, count, func(ctx context.Context, parameters ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
						if count == 0 {
							return nil
						}
						key := 0
						if mode == "supplied" {
							value, err := parameters.Types[0].Type.InputWithEnvironment(ctx, "supplied-value", -1, input)
							if err != nil {
								return err
							}
							frame[0], key = value, 1
						}
						value, err := sqltype.Integer(parameters.Types[key].Type, 9)
						frame[key] = value
						return err
					})
					err = scope.Finish(err)
					if result.Count != 0 || result.Rows != nil {
						t.Fatal("failed INSERT exposed a result", result)
					}
					// readable=99 has no descendant in the intermediate target:
					// the named-target ancestor violation must win before descent.
					nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "` + target + `" violates partition constraint`, Schema: "public", Table: target, Detail: detail})
					for _, name := range []string{"rp_constraint_root", "rp_constraint_leaf", "rp_constraint_mid", "rp_constraint_deep"} {
						want := [][]driver.Value{{int64(1), "sentinel", int64(11)}, {int64(3), "sentinel", int64(11)}}
						if name == "rp_constraint_leaf" {
							want = want[:1]
						} else if name != "rp_constraint_root" {
							want = want[1:]
						}
						rows := roleExecute(t, e, admin, "SELECT * FROM "+name+" ORDER BY k")
						if got := tuples(rows); !reflect.DeepEqual(got, want) {
							t.Fatalf("failed INSERT changed %s: %#v want %#v", name, got, want)
						}
						if err := rows.Rows.Close(); err != nil {
							t.Fatal(err)
						}
					}
					lockEmpty(t, e)
				})
			}
		}
	}
}

// Catalog ACL manifests supplement the shared public GRANT coverage in
// TestReferenceRangePartitionPhysicalConstraintPrivacy. Physical error identity
// and original named-target disclosure authority are intentionally different.
func TestRangePartitionPhysicalConstraintPrivacy(t *testing.T) {
	cases := []struct {
		name, target, leaf string
		check              bool
		bad, good          int64
		selectSource       bool
	}{
		{"root_nn", "rp_physical_root", "rp_physical_nn", false, 1, 5, false},
		{"mid_nn", "rp_physical_mid", "rp_physical_deep_nn", false, 3, 5, false},
		{"leaf_nn", "rp_physical_nn", "rp_physical_nn", false, 1, 2, false},
		{"ordinary_nn", "rp_physical_ordinary", "rp_physical_ordinary", false, 1, 2, false},
		{"root_check", "rp_physical_root", "rp_physical_check", true, 6, 1, false},
		{"mid_check", "rp_physical_mid", "rp_physical_check", true, 6, 3, false},
		{"leaf_check", "rp_physical_check", "rp_physical_check", true, 6, 5, false},
	}
	for _, c := range cases {
		c.name += "_select"
		c.selectSource = true
		cases = append(cases, c)
	}
	for _, c := range cases {
		for _, mode := range []string{"owner", "insert_only", "leaf_select", "column_select", "table_select", "supplied"} {
			if mode == "leaf_select" && c.target == c.leaf {
				continue
			}
			for _, prepared := range []bool{false, true} {
				route := "direct"
				if prepared {
					route = "prepared"
				}
				t.Run(c.name+"/"+mode+"/"+route, func(t *testing.T) {
					e := New()
					admin, done := roleOperation(t, e, "postgres")
					defer done()
					for _, query := range []string{
						"CREATE USER rp_physical_actor",
						"CREATE TABLE rp_physical_root(k INT,required INT,secret TEXT DEFAULT 'private-value') PARTITION BY RANGE(k)",
						"CREATE TABLE rp_physical_nn PARTITION OF rp_physical_root(required NOT NULL) FOR VALUES FROM(1) TO(3)",
						"CREATE TABLE rp_physical_mid PARTITION OF rp_physical_root FOR VALUES FROM(3) TO(7) PARTITION BY RANGE(k)",
						"CREATE TABLE rp_physical_deep_nn PARTITION OF rp_physical_mid(required NOT NULL) FOR VALUES FROM(3) TO(5)",
						"CREATE TABLE rp_physical_check PARTITION OF rp_physical_mid(CONSTRAINT rp_physical_key CHECK(k<>6)) FOR VALUES FROM(5) TO(7)",
						"CREATE TABLE rp_physical_ordinary(k INT,required INT NOT NULL,secret TEXT DEFAULT 'private-value')",
						"INSERT INTO rp_physical_root VALUES(1,7,'sentinel'),(3,7,'sentinel'),(5,7,'sentinel')",
						"INSERT INTO rp_physical_ordinary VALUES(0,7,'sentinel')",
					} {
						roleExecute(t, e, admin, query)
					}
					actor, ok := e.Roles().Lookup("rp_physical_actor")
					if !ok {
						t.Fatal("actor missing")
					}
					grant := func(table string, attribute int32, privileges authorization.Privileges) {
						t.Helper()
						ref := roleTableRef(t, e, table)
						ref.Address.SubID = attribute
						if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}); err != nil {
							t.Fatal(err)
						}
					}
					// Routed validation must not require leaf INSERT/SELECT.
					if c.target != c.leaf {
						grant(c.leaf, 0, 0)
					}
					grant(c.target, 0, authorization.InsertPrivilege)
					switch mode {
					case "leaf_select":
						grant(c.leaf, 0, authorization.SelectPrivilege)
					case "column_select":
						grant(c.target, 3, authorization.SelectPrivilege)
					case "table_select":
						grant(c.target, 0, authorization.InsertPrivilege|authorization.SelectPrivilege)
					}
					op := admin
					if mode != "owner" {
						var finish func()
						op, finish = roleOperation(t, e, "rp_physical_actor")
						defer finish()
					}
					for _, batch := range []bool{false, true} {
						if c.selectSource && batch {
							continue
						}
						columns := "k"
						values := strconv.FormatInt(c.bad, 10)
						raw := []string{strconv.FormatInt(c.bad, 10)}
						nulls := []bool{false}
						required, secret := "null", "private-value"
						names, visibleValues := "k", strconv.FormatInt(c.bad, 10)
						if prepared {
							values = "$1"
						}
						if batch {
							columns = "required,k"
							requiredSQL := "NULL"
							requiredNull := true
							if c.check {
								required, requiredSQL, requiredNull = "7", "7", false
							}
							values = "7," + strconv.FormatInt(c.good, 10) + "),(" + requiredSQL + "," + strconv.FormatInt(c.bad, 10)
							raw = []string{"7", strconv.FormatInt(c.good, 10), requiredSQL, strconv.FormatInt(c.bad, 10)}
							nulls = []bool{false, false, requiredNull, false}
							if prepared {
								values = "$1,$2),($3,$4"
							}
							names, visibleValues = "k, required", strconv.FormatInt(c.bad, 10)+", "+required
						}
						if mode == "supplied" {
							secret = "supplied-value"
							columns = "secret," + columns
							if batch {
								requiredSQL := "NULL"
								requiredNull := true
								if c.check {
									requiredSQL, requiredNull = "7", false
								}
								values = "'supplied-value',7," + strconv.FormatInt(c.good, 10) + "),('supplied-value'," + requiredSQL + "," + strconv.FormatInt(c.bad, 10)
								raw = []string{secret, "7", strconv.FormatInt(c.good, 10), secret, requiredSQL, strconv.FormatInt(c.bad, 10)}
								nulls = []bool{false, false, false, false, requiredNull, false}
								if prepared {
									values = "$1,$2,$3),($4,$5,$6"
								}
							} else {
								values = "'supplied-value'," + strconv.FormatInt(c.bad, 10)
								raw, nulls = []string{secret, strconv.FormatInt(c.bad, 10)}, []bool{false, false}
								if prepared {
									values = "$1,$2"
								}
							}
						}
						if mode == "supplied" || mode == "column_select" {
							names += ", secret"
							visibleValues += ", " + secret
						}
						detail := "Failing row contains (" + names + ") = (" + visibleValues + ")."
						if mode == "owner" || mode == "table_select" {
							detail = "Failing row contains (" + strconv.FormatInt(c.bad, 10) + ", " + required + ", " + secret + ")."
						}
						query := "INSERT INTO " + c.target + "(" + columns + ") VALUES(" + values + ")"
						if c.selectSource {
							query = "INSERT INTO " + c.target + "(" + columns + ") SELECT " + values
						}
						ast := statement(t, query)
						var description *StatementDescription
						count := 0
						if prepared {
							desc, err := e.Describe(op, ast)
							if err != nil {
								t.Fatal(err)
							}
							description, count = &desc, len(raw)
						}
						scope, err := e.StartStatement(op)
						if err != nil {
							t.Fatal(err)
						}
						scope.Expected = description
						result, err := scope.Execute(ast, count, func(ctx context.Context, parameters ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
							for i := range count {
								if nulls[i] {
									frame[i] = sqltype.Null(parameters.Types[i].Type)
									continue
								}
								value, err := parameters.Types[i].Type.InputWithEnvironment(ctx, raw[i], -1, input)
								if err != nil {
									return err
								}
								frame[i] = value
							}
							return nil
						})
						err = scope.Finish(err)
						if result.Count != 0 || result.Rows != nil {
							t.Fatal("failed physical validation exposed a result", result)
						}
						want := sqlstate.Error{Code: "23502", Message: `null value in column "required" of relation "` + c.leaf + `" violates not-null constraint`, Schema: "public", Table: c.leaf, Column: "required", Detail: detail}
						if c.check {
							want.Code, want.Message = "23514", `new row for relation "`+c.leaf+`" violates check constraint "rp_physical_key"`
							want.Column, want.Constraint = "", "rp_physical_key"
						}
						nullCountUnitDiagnostic(t, err, want)
						for _, table := range []string{"rp_physical_root", "rp_physical_nn", "rp_physical_mid", "rp_physical_deep_nn", "rp_physical_check", "rp_physical_ordinary"} {
							wantRows := [][]driver.Value{{int64(1), int64(7), "sentinel"}, {int64(3), int64(7), "sentinel"}, {int64(5), int64(7), "sentinel"}}
							switch table {
							case "rp_physical_nn":
								wantRows = wantRows[:1]
							case "rp_physical_mid":
								wantRows = wantRows[1:]
							case "rp_physical_deep_nn":
								wantRows = wantRows[1:2]
							case "rp_physical_check":
								wantRows = wantRows[2:]
							case "rp_physical_ordinary":
								wantRows = [][]driver.Value{{int64(0), int64(7), "sentinel"}}
							}
							rows := roleExecute(t, e, admin, "SELECT * FROM "+table+" ORDER BY k")
							if got := tuples(rows); !reflect.DeepEqual(got, wantRows) {
								t.Fatalf("failed physical INSERT changed %s: %#v want %#v", table, got, wantRows)
							}
							if err := rows.Rows.Close(); err != nil {
								t.Fatal(err)
							}
						}
						lockEmpty(t, e)
					}
				})
			}
		}
	}
}
