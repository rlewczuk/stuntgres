package parser

import "testing"

func TestUUIDGenerationCallAdmission(t *testing.T) {
	for _, query := range []string{
		"SELECT gen_random_uuid()",
		"SELECT pg_catalog.uuidv4()",
		"SELECT uuidv7($1)",
		"SELECT uuidv7(shift=>interval '0')",
		"SELECT uuidv7(shift:=interval '0')",
		"CREATE TABLE u(v uuid DEFAULT uuidv7(interval '0'))",
		"CREATE TABLE u(v uuid DEFAULT coalesce(uuidv7(),uuidv4()))",
		"CREATE VIEW v AS SELECT uuidv7(interval '1 day') AS v",
		"ALTER TABLE u ALTER COLUMN v SET DEFAULT uuidv7(interval '0')",
		"SELECT uuidv7(interval '0') IS NOT NULL",
		"SELECT CASE WHEN false THEN uuidv7(interval 'infinity') ELSE NULL::uuid END",
		"SELECT coalesce(uuidv7(),uuidv4())",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
	statements, err := Parse("SELECT gen_random_uuid,uuidv4,uuidv7 FROM u")
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"gen_random_uuid", "uuidv4", "uuidv7"} {
		column, ok := statements[0].(*Select).Targets[i].(ColumnExpr)
		if !ok || column.Name.Parts[0].Text != name {
			t.Fatal("function spelling ceased being column identifier", name, statements)
		}
	}
	if _, err := Parse("CREATE TABLE u(v float8 DEFAULT random())"); err == nil {
		t.Fatal("UUID admission widened unrelated volatile defaults")
	}
}
