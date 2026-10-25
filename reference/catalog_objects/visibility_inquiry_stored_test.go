package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryStoredChecks(t *testing.T) {
	fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(fixture.Step) fixture.Observed) {
		for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
			name := fmt.Sprintf("pgv_stored_%d", kind)
			constraint := name + "_check"
			run(fixture.Exec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))", 0))
			valid := int64(23)
			switch kind {
			case 0:
				if err := conn.QueryRowContext(context.Background(), "SELECT oid FROM pg_catalog.pg_class WHERE relname='"+name+"'").Scan(&valid); err != nil {
					t.Fatal(err)
				}
			case 2:
				valid = 950
			case 3:
				valid = 4402
			}
			var tree string
			structural, err := conn.QueryContext(context.Background(), "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'")
			if err != nil {
				t.Fatal(err)
			}
			types, err := structural.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "PG_NODE_TREE" {
				t.Fatal(types, err)
			}
			if !structural.Next() {
				t.Fatal("missing structural row", structural.Err())
			}
			if err := structural.Scan(&tree); err != nil {
				t.Fatal(err)
			}
			if structural.Next() || structural.Err() != nil {
				t.Fatal("unexpected structural rows", structural.Err())
			}
			structural.Close()
			if postgres {
				if tree == "" {
					t.Fatal("empty PG structural catalog value")
				}
			} else {
				want := fmt.Sprintf("{type 16 typmod -1 collation 0 derivation 0 node NullTest not true {type 16 typmod -1 collation 0 derivation 0 node VisibilityInquiry %d {type 26 typmod -1 collation 0 derivation 0 node Var attnum 1}}}", kind)
				if tree != want {
					t.Fatal(tree, want)
				}
			}
			for _, item := range []struct{ query, want string }{
				{"SELECT pg_get_expr(conbin,conrelid) AS d FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'", "(" + fn + "(id) IS NOT NULL)"},
				{"SELECT pg_get_constraintdef(oid) AS d FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'", "CHECK ((" + fn + "(id) IS NOT NULL))"},
			} {
				step := fixture.Select(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
				step.CheckMetadata = fixture.SessionIdentityMetadata(postgres)
				run(step)
				run(fixture.Prepared(step))
			}
			for _, pretty := range []bool{false, true} {
				expression := fn + "(id) IS NOT NULL"
				if !pretty {
					expression = "(" + expression + ")"
				}
				for _, item := range []struct{ query, want string }{
					{fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), expression},
					{fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "CHECK (" + expression + ")"},
				} {
					step := fixture.Select(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
					step.CheckMetadata = fixture.SessionIdentityMetadata(postgres)
					actual := run(step).Rows[0][0].(string)
					if err := conn.QueryRowContext(t.Context(), item.query).Scan(&actual); err != nil || actual != item.want {
						t.Fatal(actual, item.want, err)
					}
					run(fixture.Prepared(step))
					suffix := actual
					if item.want == expression {
						suffix = "CHECK (" + actual + ")"
					}
					copyName := fmt.Sprintf("%s_%t_%t", name, pretty, item.want == expression)
					run(fixture.Exec("CREATE TABLE "+copyName+"(id OID,CONSTRAINT copied "+suffix+")", 0))
					run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%d)", copyName, valid), 1))
					step = fixture.ImplicitError("INSERT INTO "+copyName+" VALUES(0)", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, copyName), Detail: "Failing row contains (0).", Schema: "public", Table: copyName, Constraint: "copied"})
					step.ErrorStage = "execute"
					run(step)
					nullFailure := fixture.ImplicitError("INSERT INTO "+copyName+" VALUES(NULL)", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, copyName), Detail: "Failing row contains (null).", Schema: "public", Table: copyName, Constraint: "copied"})
					nullFailure.ErrorStage = "execute"
					run(nullFailure)
					run(fixture.Select("SELECT id::bigint FROM "+copyName, []string{"id"}, []string{"INT8"}, [][]any{{valid}}))
				}
			}
			if kind == 0 {
				run(fixture.Exec("SET search_path=pg_catalog", 0))
				run(fixture.Select("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{false}}, valid))
				run(fixture.Exec(fmt.Sprintf("INSERT INTO public.%s VALUES(%d)", name, valid), 1))
				run(fixture.Exec("SET search_path=public,pg_catalog", 0))
			}
		}
	})
}

func TestReferenceVisibilityInquiryAlterCheckCapture(t *testing.T) {
	fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(fixture.Step) fixture.Observed) {
		for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
			for _, prepared := range []bool{false, true} {
				name := fmt.Sprintf("pgv_alter_%d_%t", kind, prepared)
				constraint := name + "_check"
				run(fixture.Exec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))", 0))
				valid := int64(23)
				switch kind {
				case 0:
					if err := conn.QueryRowContext(context.Background(), "SELECT oid FROM pg_catalog.pg_class WHERE relname='"+name+"'").Scan(&valid); err != nil {
						t.Fatal(err)
					}
				case 2:
					valid = 950
				case 3:
					valid = 4402
				}
				run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%d),(%d)", name, valid, valid), 2))
				success := fixture.Exec("ALTER TABLE "+name+" ALTER COLUMN id TYPE BIGINT USING id::bigint", 0)
				success.Prepared = prepared
				run(success)
				baseline := fixture.Select("SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{valid}, {valid}})
				run(baseline)
				run(fixture.Prepared(baseline))
				var before string
				if err := conn.QueryRowContext(context.Background(), "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'").Scan(&before); err != nil {
					t.Fatal(err)
				}
				if !postgres && !strings.Contains(before, fmt.Sprintf("VisibilityInquiry %d", kind)) {
					t.Fatal(before)
				}
				failure := fixture.ImplicitError("ALTER TABLE "+name+" ALTER COLUMN id TYPE INTEGER USING 0", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`check constraint "%s" of relation "%s" is violated by some row`, constraint, name), Schema: "public", Table: name, Constraint: constraint})
				failure.ErrorStage = "execute"
				failure.Prepared = prepared
				run(failure)
				run(baseline)
				run(fixture.Prepared(baseline))
				var after string
				if err := conn.QueryRowContext(context.Background(), "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'").Scan(&after); err != nil {
					t.Fatal(err)
				}
				if before != after {
					t.Fatal("failed ALTER changed stored tree", before, after)
				}
				for _, pretty := range []bool{false, true} {
					expr := fn + "(id::oid) IS NOT NULL"
					if !pretty {
						expr = "(" + fn + "((id)::oid) IS NOT NULL)"
					}
					for _, item := range []struct{ query, want string }{
						{fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), expr},
						{fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "CHECK (" + expr + ")"},
					} {
						step := fixture.Select(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
						step.CheckMetadata = fixture.SessionIdentityMetadata(postgres)
						run(step)
						run(fixture.Prepared(step))
					}
				}
			}
		}
	})
}
