package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestPrimaryKeyAttachments(t *testing.T) {
	for _, tc := range []struct {
		query, name string
		keys        []string
		column      bool
	}{
		{`CREATE TABLE t(a INT PRIMARY KEY)`, "", nil, true},
		{`CREATE TABLE t(a INT CONSTRAINT "Named" PRIMARY /* key */ KEY CONSTRAINT nn NOT NULL)`, "Named", nil, true},
		{`CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(b,a))`, "", []string{"b", "a"}, false},
		{`CREATE TABLE t(CONSTRAINT "Named" PRIMARY KEY(b,a),a INT,b TEXT)`, "Named", []string{"b", "a"}, false},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		table := statements[0].(*CreateTable)
		var primary *UniqueConstraint
		for _, element := range table.Elements {
			if !tc.column && element.Unique != nil {
				primary = element.Unique
			}
			if tc.column && element.Column != nil {
				for _, declaration := range element.Column.Constraints {
					if declaration.Unique != nil {
						primary = declaration.Unique
					}
				}
			}
		}
		if primary == nil || !primary.Primary || primary.NullsNotDistinct || primary.Name.Text != tc.name {
			t.Fatal(tc.query, primary)
		}
		var keys []string
		for _, key := range primary.Columns {
			if key.Pos.Character <= 0 {
				t.Fatal("key lost source position", key)
			}
			keys = append(keys, key.Text)
		}
		if !reflect.DeepEqual(keys, tc.keys) {
			t.Fatal(keys, tc.keys)
		}
		token := "PRIMARY"
		if tc.name != "" {
			token = "CONSTRAINT"
		}
		if primary.Pos.Character != utf8.RuneCountInString(tc.query[:strings.Index(tc.query, token)])+1 {
			t.Fatal("declaration lost start position", primary.Pos)
		}
	}
	for _, query := range []string{`SELECT 1 AS primary FROM t`, `SELECT 1 primary FROM t`, `CREATE TABLE "primary"(key INT PRIMARY KEY)`} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}

func TestPrimaryKeyCapabilityRecords(t *testing.T) {
	for _, tc := range []struct{ query, token, message string }{
		{"CREATE TABLE t(a INT PRIMARY KEY DEFERRABLE INITIALLY DEFERRED)", "DEFERRABLE", "PRIMARY KEY deferrability is unsupported"},
		{"CREATE TABLE t(a INT PRIMARY KEY NOT DEFERRABLE)", "NOT", "PRIMARY KEY deferrability is unsupported"},
		{"CREATE TABLE 界(a INT,PRIMARY KEY(a) INITIALLY IMMEDIATE)", "INITIALLY", "PRIMARY KEY deferrability is unsupported"},
		{"CREATE TABLE t(a INT,b INT,PRIMARY KEY(a,b WITHOUT OVERLAPS))", "WITHOUT", "PRIMARY KEY WITHOUT OVERLAPS is unsupported"},
		{"CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(a) INCLUDE(b))", "INCLUDE", "PRIMARY KEY index parameters are unsupported"},
		{"CREATE TABLE t(a INT PRIMARY KEY WITH(fillfactor=90))", "WITH", "PRIMARY KEY index parameters are unsupported"},
		{"CREATE TABLE t(a INT,PRIMARY KEY(a) USING INDEX TABLESPACE pg_default)", "USING", "PRIMARY KEY index parameters are unsupported"},
		{"ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY(a) DEFERRABLE", "DEFERRABLE", "PRIMARY KEY deferrability is unsupported"},
		{"ALTER TABLE t ADD COLUMN a INT PRIMARY KEY", "ADD", "ALTER TABLE action is unsupported"},
		{"ALTER TABLE t ADD COLUMN IF NOT EXISTS a INT PRIMARY KEY DEFERRABLE", "ADD", "ALTER TABLE action is unsupported"},
	} {
		statements, err := Parse(tc.query)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message, Position: strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)}
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
			t.Fatalf("%s: statements=%#v diagnostic=%#v error=%v want=%#v", tc.query, statements, diagnostic, err, want)
		}
	}
}

func TestPrimaryKeyWholeInputValidation(t *testing.T) {
	for _, query := range []string{
		`CREATE TABLE primary(a INT)`,
		`CREATE TABLE t(a INT PRIMARY "key")`,
		`CREATE TABLE t(a INT PRIMARY wrong)`,
		`CREATE TABLE t(a INT,PRIMARY KEY())`,
		`CREATE TABLE t(a INT,PRIMARY KEY(a,))`,
		`CREATE TABLE t(a INT,PRIMARY KEY(a+1))`,
		`CREATE TABLE t(a INT PRIMARY KEY NULLS NOT DISTINCT)`,
		`CREATE TABLE t(a INT PRIMARY KEY NOT)`,
		`CREATE TABLE t(a INT PRIMARY KEY "initially" "deferred")`,
		`CREATE TABLE t(a INT,PRIMARY KEY(a) USING random)`,
		`CREATE TABLE t(a INT,PRIMARY KEY(a) TABLESPACE pg_default)`,
		`CREATE TABLE t(a INT PRIMARY KEY DEFERRABLE) nonsense`,
		`ALTER TABLE t ADD PRIMARY KEY(a) nonsense`,
		`ALTER TABLE t ADD COLUMN a INT PRIMARY KEY DEFERRABLE nonsense`,
		`CREATE TABLE earlier(a INT PRIMARY KEY); CREATE TABLE t(a INT PRIMARY KEY DEFERRABLE) nonsense`,
	} {
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if statements != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("malformed input admitted or capability hid syntax", query, statements, err)
		}
	}
}

func TestPrimaryKeyReservedSuffixConsumers(t *testing.T) {
	for _, tc := range []struct{ query, message string }{
		{"SELECT * FROM t PRIMARY KEY", "SELECT suffix is unsupported"},
		{"INSERT INTO t VALUES(1) PRIMARY KEY", "INSERT suffix is unsupported"},
		{"UPDATE t SET a=1 PRIMARY KEY", "UPDATE suffix is unsupported"},
		{"DELETE FROM t PRIMARY KEY", "DELETE suffix is unsupported"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message, Position: strconv.Itoa(strings.Index(tc.query, "PRIMARY") + 1)}
		if statements != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
			t.Fatal("dedicated PRIMARY lost prior reserved suffix consumer", tc.query, statements, diagnostic, err)
		}
	}
}
