package stuntgres_test

import (
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestVisibilityInquiryExecutionBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE empty_ids(id OID)")
	for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		q := "SELECT " + fn + "('bad') FROM empty_ids"
		nullCountError(t, db, q, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: strconv.Itoa(9 + len(fn))})
		q = "SELECT " + fn + "(4294967296::bigint) FROM empty_ids"
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "OID out of range"})
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		_, err = stmt.Query()
		stmt.Close()
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "OID out of range"})
		args := strings.TrimSuffix(strings.Repeat("1,", 101), ",")
		nullCountError(t, db, "SELECT "+fn+"("+args+")", stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
		badArgs := strings.Repeat("1,", 100) + "missing_column"
		badSQL := "SELECT " + fn + "(" + badArgs + ") FROM empty_ids"
		nullCountError(t, db, badSQL, stuntgres.Error{Code: "42703", Message: `column "missing_column" does not exist`, Position: strconv.Itoa(strings.Index(badSQL, "missing_column") + 1)})
		stmt, err = db.Prepare("SELECT " + fn + "(NULL),$1::oid FROM empty_ids")
		if err != nil {
			t.Fatal(err)
		}
		_, err = stmt.Query("bad")
		stmt.Close()
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"})
	}
	rows, err := db.Query("SELECT pg_type_is_visible(id) FROM empty_ids")
	visibilityInquiryRows(t, rows, err, "pg_type_is_visible", []any{})
	wantTuples(t, db, "SELECT current_schemas(true)", [][]any{{"{pg_catalog,public}"}})
	rows, err = db.Query("SELECT pg_type_is_visible(23)")
	visibilityInquiryRows(t, rows, err, "pg_type_is_visible", []any{true})
	wantTuples(t, db, "SELECT current_schemas(true)", [][]any{{"{pg_catalog,public}"}})
}

func TestVisibilityInquiryDiagnosticOrdering(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE empty_ids(id OID)")
	for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		t.Run(fn, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				args string
				want stuntgres.Error
			}{
				{"missing_child", "missing_column", stuntgres.Error{Code: "42703", Message: `column "missing_column" does not exist`}},
				{"argument_limit", strings.TrimSuffix(strings.Repeat("1,", 101), ","), stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
				{"over_limit_missing_child", strings.Repeat("1,", 100) + "missing_column", stuntgres.Error{Code: "42703", Message: `column "missing_column" does not exist`}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					q := "SELECT missing_schema." + fn + "(" + tc.args + ") FROM empty_ids"
					if tc.want.Code == "42703" {
						tc.want.Position = strconv.Itoa(strings.Index(q, "missing_column") + 1)
					}
					nullCountError(t, db, q, tc.want)
				})
			}
		})
	}
}

func TestVisibilityInquiryPrivateSequenceCatalog(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.same", "SET search_path=a,b"} {
		execSQL(t, db, q)
	}
	var b int64
	if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='same'").Scan(&b); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("CREATE SEQUENCE a.same"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query("SELECT pg_table_is_visible($1)", b)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{false})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT pg_table_is_visible($1)", b)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
}

func TestVisibilityInquiryOffPathCheck(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE SCHEMA off_path", "CREATE TABLE off_path.subject(id INT)", "CREATE TABLE relation_checks(id OID CONSTRAINT known CHECK(pg_table_is_visible(id) IS NOT NULL))"} {
		execSQL(t, db, q)
	}
	var oid int64
	if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='subject'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO relation_checks VALUES($1)", oid)
	wantTuples(t, db, "SELECT pg_table_is_visible(id) FROM relation_checks", [][]any{{false}})
	checkExecError(t, db, false, "INSERT INTO relation_checks VALUES(0)", checkViolation("relation_checks", "known", "0"))
	wantTuples(t, db, "SELECT count(*) FROM relation_checks", [][]any{{int64(1)}})
}
