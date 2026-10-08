//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceVisibilityInquiryStoredChecks(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
		for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
			name := fmt.Sprintf("pgv_stored_%d", kind)
			constraint := name + "_check"
			run(referenceExec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))", 0))
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
				step := referenceSelect(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
				run(step)
				run(referencePrepared(step))
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
					step := referenceSelect(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
					actual := run(step).rows[0][0].(string)
					run(referencePrepared(step))
					suffix := actual
					if item.want == expression {
						suffix = "CHECK (" + actual + ")"
					}
					copyName := fmt.Sprintf("%s_%t_%t", name, pretty, item.want == expression)
					run(referenceExec("CREATE TABLE "+copyName+"(id OID,CONSTRAINT copied "+suffix+")", 0))
					run(referenceExec(fmt.Sprintf("INSERT INTO %s VALUES(%d)", copyName, valid), 1))
					step = referenceImplicitError("INSERT INTO "+copyName+" VALUES(0)", referenceDiagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, copyName), Detail: "Failing row contains (0).", Schema: "public", Table: copyName, Constraint: "copied"})
					step.errorStage = "execute"
					run(step)
					nullFailure := referenceImplicitError("INSERT INTO "+copyName+" VALUES(NULL)", referenceDiagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, copyName), Detail: "Failing row contains (null).", Schema: "public", Table: copyName, Constraint: "copied"})
					nullFailure.errorStage = "execute"
					run(nullFailure)
					run(referenceSelect("SELECT id::bigint FROM "+copyName, []string{"id"}, []string{"INT8"}, [][]any{{valid}}))
				}
			}
			if kind == 0 {
				run(referenceExec("SET search_path=pg_catalog", 0))
				run(referenceSelect("SELECT pg_table_is_visible($1)", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{false}}, valid))
				run(referenceExec(fmt.Sprintf("INSERT INTO public.%s VALUES(%d)", name, valid), 1))
				run(referenceExec("SET search_path=public,pg_catalog", 0))
			}
		}
	})
}

func TestReferenceVisibilityInquiryAlterCheckCapture(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
		for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
			for _, prepared := range []bool{false, true} {
				name := fmt.Sprintf("pgv_alter_%d_%t", kind, prepared)
				constraint := name + "_check"
				run(referenceExec("CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))", 0))
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
				run(referenceExec(fmt.Sprintf("INSERT INTO %s VALUES(%d),(%d)", name, valid, valid), 2))
				success := referenceExec("ALTER TABLE "+name+" ALTER COLUMN id TYPE BIGINT USING id::bigint", 0)
				success.prepared = prepared
				run(success)
				baseline := referenceSelect("SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{valid}, {valid}})
				run(baseline)
				run(referencePrepared(baseline))
				var before string
				if err := conn.QueryRowContext(context.Background(), "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'").Scan(&before); err != nil {
					t.Fatal(err)
				}
				failure := referenceImplicitError("ALTER TABLE "+name+" ALTER COLUMN id TYPE INTEGER USING 0", referenceDiagnostic{Code: "23514", Message: fmt.Sprintf(`check constraint "%s" of relation "%s" is violated by some row`, constraint, name), Schema: "public", Table: name, Constraint: constraint})
				failure.errorStage = "execute"
				failure.prepared = prepared
				run(failure)
				run(baseline)
				run(referencePrepared(baseline))
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
						step := referenceSelect(item.query, []string{"d"}, []string{"TEXT"}, [][]any{{item.want}})
						run(step)
						run(referencePrepared(step))
					}
				}
			}
		}
	})
}
