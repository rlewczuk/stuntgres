package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func validateConstraintCases() []addConstraintCase {
	all := addConstraintCases()
	return []addConstraintCase{all[0], all[1], all[4]}
}

func validateConstraintName(tc addConstraintCase) string {
	switch tc.kind {
	case "c":
		return "ck"
	case "n":
		return "nn"
	default:
		return "fk"
	}
}

func validateConstraintDefinition(tc addConstraintCase) string {
	switch tc.kind {
	case "c":
		return "CHECK ((a > 0))"
	case "n":
		return "NOT NULL a"
	default:
		return "FOREIGN KEY (a) REFERENCES p(id)"
	}
}

func validateConstraintFlag(table, name string, valid bool) fixture.Step {
	return fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid=$1::regclass::oid AND conname=$2", []string{"convalidated"}, []string{"BOOL"}, [][]any{{valid}}, table, name)
}

func validateConstraintIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, oid int64, valid bool, definition string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT convalidated,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE oid=$1", []string{"convalidated", "definition"}, []string{"BOOL", "TEXT"}, [][]any{{valid, definition}}, oid))
}

// Catalog snapshots include validity and inheritance origins, not just names.
func validateConstraintSnapshot(t *testing.T, p *fixture.Pair, c *sql.Conn) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT oid,conname,conrelid,convalidated,conislocal,coninhcount,conparentid,pg_get_constraintdef(oid) FROM pg_constraint WHERE connamespace='public'::regnamespace::oid ORDER BY oid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		var oid, rel, count, parent int64
		var name, def string
		var valid, local bool
		if err := rows.Scan(&oid, &name, &rel, &valid, &local, &count, &parent, &def); err != nil {
			t.Fatal(err)
		}
		out = append(out, []any{oid, name, rel, valid, local, count, parent, def})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func validateConstraintOrigins(t *testing.T, p *fixture.Pair, c *sql.Conn, before [][]any) {
	t.Helper()
	after := validateConstraintSnapshot(t, p, c)
	if len(before) != len(after) {
		t.Fatal("validation changed constraint inventory", before, after)
	}
	for i := range before {
		for _, j := range []int{0, 1, 2, 4, 5, 6} {
			if !reflect.DeepEqual(before[i][j], after[i][j]) {
				t.Fatal("validation changed identity/origin", before[i], after[i])
			}
		}
		if strings.TrimSuffix(before[i][7].(string), " NOT VALID") != strings.TrimSuffix(after[i][7].(string), " NOT VALID") {
			t.Fatal("validation changed definition", before[i], after[i])
		}
	}
}

func validateConstraintHeldRun(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}

func validateConstraintRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, nullable bool, rows ...[]any) {
	t.Helper()
	s := addConstraintRows(rows...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, false, s.Query, s.Columns, s.Types, []bool{nullable, true}, len(rows))
	fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, s.Query, s.Columns, s.Types, []bool{nullable, true}, len(rows))
}
