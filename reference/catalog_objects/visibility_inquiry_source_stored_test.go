package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func visibilitySourceTextScan(want string) func(*testing.T, *sql.Rows, int) {
	return func(t *testing.T, rows *sql.Rows, _ int) {
		t.Helper()
		var actual string
		if err := rows.Scan(&actual); err != nil || actual != want {
			t.Fatal(actual, want, err)
		}
	}
}

func TestReferenceVisibilityInquiryStoredSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			db.SetMaxOpenConns(1)
			run := func(s fixture.Step) fixture.Observed {
				s.NonContext = true
				if s.CheckMetadata == nil {
					s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				}
				if len(s.Types) == 1 && s.Types[0] == "TEXT" {
					s.CheckScan = visibilitySourceTextScan(s.Rows[0][0].(string))
				}
				return fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
			}
			for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
				name := fmt.Sprintf("visibility_stored_%d", kind)
				constraint := name + "_check"
				run(fixture.Exec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))", 0))
				valid := int64(23)
				switch kind {
				case 0:
					if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='" + name + "'").Scan(&valid); err != nil || valid <= 0 || valid > 4294967295 {
						t.Fatal(valid, err)
					}
				case 2:
					valid = 950
				case 3:
					valid = 4402
				}
				rows, err := db.Query("SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'")
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "PG_NODE_TREE" {
					rows.Close()
					t.Fatal(meta, err)
				}
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing structural row", rows.Err())
				}
				var tree string
				if err := rows.Scan(&tree); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("extra structural row", rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if backend.PG {
					if tree == "" {
						t.Fatal("empty genuine PG structural tree")
					}
				} else {
					want := fmt.Sprintf("{type 16 typmod -1 collation 0 derivation 0 node NullTest not true {type 16 typmod -1 collation 0 derivation 0 node VisibilityInquiry %d {type 26 typmod -1 collation 0 derivation 0 node Var attnum 1}}}", kind)
					if tree != want {
						t.Fatal(tree, want)
					}
				}
				for _, prepared := range []bool{false, true} {
					for _, item := range []struct{ query, label, want string }{
						{"SELECT pg_get_expr(conbin,conrelid) FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'", "pg_get_expr", "(" + fn + "(id) IS NOT NULL)"},
						{"SELECT pg_get_constraintdef(oid) FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'", "pg_get_constraintdef", "CHECK ((" + fn + "(id) IS NOT NULL))"},
					} {
						s := fixture.Select(item.query, []string{item.label}, []string{"TEXT"}, [][]any{{item.want}})
						s.Prepared = prepared
						run(s)
					}
				}
				for _, pretty := range []bool{false, true} {
					expr := fn + "(id) IS NOT NULL"
					if !pretty {
						expr = "(" + expr + ")"
					}
					for _, definition := range []bool{true, false} {
						label, query, want := "pg_get_expr", fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), expr
						if definition {
							label, query, want = "pg_get_constraintdef", fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "CHECK ("+expr+")"
						}
						for _, prepared := range []bool{false, true} {
							s := fixture.Select(query, []string{label}, []string{"TEXT"}, [][]any{{want}})
							s.Prepared = prepared
							run(s)
						}
						var actual string
						if err := db.QueryRow(query).Scan(&actual); err != nil || actual != want {
							t.Fatal(actual, want, err)
						}
						copyName := fmt.Sprintf("%s_copy_%t", name, pretty)
						if !definition {
							copyName = fmt.Sprintf("%s_expr_%t", name, pretty)
						}
						suffix := actual
						if !definition {
							suffix = "CHECK (" + actual + ")"
						}
						run(fixture.Exec("CREATE TABLE "+copyName+"(id OID,CONSTRAINT copied "+suffix+")", 0))
						run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%d)", copyName, valid), 1))
						for _, value := range []string{"0", "NULL"} {
							detail := strings.ToLower(value)
							s := fixture.ImplicitError("INSERT INTO "+copyName+" VALUES("+value+")", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, copyName), Detail: "Failing row contains (" + detail + ").", Schema: "public", Table: copyName, Constraint: "copied"})
							s.ErrorStage = "execute"
							run(s)
						}
						run(fixture.Select("SELECT id::bigint FROM "+copyName, []string{"id"}, []string{"INT8"}, [][]any{{valid}}))
					}
				}
				if kind == 0 {
					run(fixture.Exec("SET search_path=pg_catalog", 0))
					visible := fixture.Select(fmt.Sprintf("SELECT pg_table_is_visible(%d::oid)", valid), []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{false}})
					visible.CheckMetadata = fixture.VisibilityBoolMetadata
					run(visible)
					run(fixture.Exec(fmt.Sprintf("INSERT INTO public.%s VALUES(%d)", name, valid), 1))
					run(fixture.Exec("SET search_path=public,pg_catalog", 0))
				}
			}
		})
	}
}

func TestReferenceVisibilityInquiryAlterSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			db.SetMaxOpenConns(1)
			run := func(s fixture.Step) {
				s.NonContext = true
				s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				if len(s.Types) == 1 && s.Types[0] == "TEXT" {
					s.CheckScan = visibilitySourceTextScan(s.Rows[0][0].(string))
				}
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
			}
			for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
				for _, prepared := range []bool{false, true} {
					name := fmt.Sprintf("visibility_alter_%d_%t", kind, prepared)
					constraint := name + "_check"
					run(fixture.Exec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))", 0))
					valid := int64(23)
					switch kind {
					case 0:
						if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='" + name + "'").Scan(&valid); err != nil || valid <= 0 || valid > 4294967295 {
							t.Fatal(valid, err)
						}
					case 2:
						valid = 950
					case 3:
						valid = 4402
					}
					run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%d),(%d)", name, valid, valid), 2))
					s := fixture.Exec("ALTER TABLE "+name+" ALTER COLUMN id TYPE BIGINT USING id::bigint", 0)
					s.Prepared = prepared
					run(s)
					baseline := fixture.Select("SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{valid}, {valid}})
					run(baseline)
					var before, after string
					q := "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'"
					if err := db.QueryRow(q).Scan(&before); err != nil {
						t.Fatal(err)
					}
					if !backend.PG && !strings.Contains(before, fmt.Sprintf("VisibilityInquiry %d", kind)) {
						t.Fatal(before)
					}
					if backend.PG && before == "" {
						t.Fatal("empty PG tree")
					}
					bad := fixture.ImplicitError("ALTER TABLE "+name+" ALTER COLUMN id TYPE INTEGER USING 0", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`check constraint "%s" of relation "%s" is violated by some row`, constraint, name), Schema: "public", Table: name, Constraint: constraint})
					bad.Prepared, bad.ErrorStage = prepared, "execute"
					run(bad)
					run(baseline)
					descriptor, err := db.Query(baseline.Query)
					if err != nil {
						t.Fatal(err)
					}
					types, err := descriptor.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
						descriptor.Close()
						t.Fatal(types, err)
					}
					if err := descriptor.Close(); err != nil {
						t.Fatal(err)
					}
					if err := db.QueryRow(q).Scan(&after); err != nil || before != after {
						t.Fatal("failed ALTER changed CHECK", before, after, err)
					}
					for _, pretty := range []bool{false, true} {
						expr := fn + "(id::oid) IS NOT NULL"
						if !pretty {
							expr = "(" + fn + "((id)::oid) IS NOT NULL)"
						}
						for _, held := range []bool{false, true} {
							for _, item := range []struct{ query, label, want string }{
								{fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "pg_get_expr", expr},
								{fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "pg_get_constraintdef", "CHECK (" + expr + ")"},
							} {
								s := fixture.Select(item.query, []string{item.label}, []string{"TEXT"}, [][]any{{item.want}})
								s.Prepared = held
								run(s)
							}
						}
					}
				}
			}
		})
	}
}
