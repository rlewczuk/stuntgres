package parser

import "testing"

// Syntax-only admission. Public behavior is independently specified in reference/.
func TestDatetimeZoneParserAdmission(t *testing.T) {
	for _, query := range []string{
		"SELECT timezone('UTC',TIMESTAMP '2004-01-01'),pg_catalog.timezone(TIMETZ '12:00+00')",
		"SELECT date_add($1,$2,$3),date_subtract(TIMESTAMPTZ '2004-01-01+00',INTERVAL '1 day')",
		"SELECT TIMESTAMP '2004-01-01' AT LOCAL,TIMETZ '12:00+00' AT TIME ZONE INTERVAL '2 hours'",
		"CREATE TABLE dz(z timestamptz DEFAULT date_add(CURRENT_TIMESTAMP,INTERVAL '1 day'),t timestamp GENERATED ALWAYS AS (timezone('UTC',z)) STORED,CHECK(date_subtract(z,INTERVAL '0 days','UTC')=z))",
		"CREATE VIEW dz_view AS SELECT timezone('UTC',z),date_add(z,INTERVAL '1 day','UTC') FROM dz",
		"CREATE INDEX dz_index ON dz ((date_subtract(z,INTERVAL '1 day','UTC')))",
	} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal(query, err)
		}
	}
	statements, err := Parse(`SELECT "TimeZone"(TIMESTAMP '2004-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	target := statements[0].(*Select).Targets[0].(FunctionExpr)
	if target.Name.Parts[0].Text != "TimeZone" {
		t.Fatal("quoted name case lost", target.Name)
	}
}
