package parser

import "testing"

func TestMathSignatures(t *testing.T) {
	for _, query := range []string{
		"SELECT round(2.5)", "SELECT ROUND(-2.5)", "SELECT \"round\"(1.25,1)",
		"SELECT scale(8.4100),min_scale(8.4100),trim_scale(8.4100),trunc(1.25,1),sign(-1.0),mod(9,4)",
		"SELECT round(trim_scale(8.4100),1)", "SELECT round($1,$2)",
		"SELECT length(NULL),bit_length($1),octet_length('x'),btrim(NULL,NULL),ltrim(NULL,NULL),rtrim(NULL,NULL)",
	} {
		s, err := Parse(query)
		if err != nil || len(s) != 1 {
			t.Fatal(query, err)
		}
		selectStatement, ok := s[0].(*Select)
		if !ok || selectStatement.From != nil || selectStatement.Values != nil {
			t.Fatal(query, s)
		}
	}
	for _, query := range []string{"SELECT round(1+1)", "SELECT 1"} {
		s, err := Parse(query)
		if err != nil || len(s) != 1 || s[0].(*Select).From != nil || len(s[0].(*Select).Targets) != 1 {
			t.Fatal("no-FROM expression syntax", query, s, err)
		}
	}
	for _, query := range []string{"CREATE TABLE m(round INT,scale INT)", "SELECT round,scale FROM m"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("selected name as identifier", query, err)
		}
	}
	for _, query := range []string{
		"CREATE TABLE binary_names(length BYTEA,btrim BYTEA)",
		"SELECT v||s,length(v),btrim(v,s),ltrim(v,s),rtrim(v,s) FROM t",
		"SELECT pg_catalog.length(v),v OPERATOR(pg_catalog.||) s FROM t",
		"SELECT btrim(v,$1) FROM t",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}

func TestBinaryBasicScalarConsumers(t *testing.T) {
	for _, name := range []string{"length", "octet_length", "bit_length", "btrim", "ltrim", "rtrim"} {
		args, params := "v", "$1"
		if name == "btrim" || name == "ltrim" || name == "rtrim" {
			args += ",s"
			params += ",$2"
		}
		call := name + "(" + args + ")"
		for _, query := range []string{
			"SELECT v FROM t WHERE " + call + "=NULL",
			"SELECT CASE WHEN " + call + "=NULL THEN 1 ELSE 0 END FROM t",
			"SELECT " + call + ",count(*) FROM t GROUP BY " + call,
			"INSERT INTO dst SELECT v FROM t WHERE " + call + "=NULL",
			"INSERT INTO dst VALUES(" + name + "(" + params + "))",
			"UPDATE t SET v=" + call + " RETURNING " + call,
		} {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, err)
			}
		}
	}
}

func TestBinaryBasicScalarInputChildBoundary(t *testing.T) {
	keywordDiagnostic(t, "INSERT INTO dst VALUES(length(v))", "0A000", "column references are unsupported in scalar inputs", "31")
	keywordDiagnostic(t, "INSERT INTO dst VALUES(btrim(v,$1))", "0A000", "column references are unsupported in scalar inputs", "30")
}
