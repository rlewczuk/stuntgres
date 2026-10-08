package stuntgres_test

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestCheckMixedConstraintDiagnostics(t *testing.T) {
	for _, tc := range []struct{ query, name string }{
		{"CREATE TABLE t(a INT CONSTRAINT c NOT NULL,CONSTRAINT c CHECK(TRUE))", "c"},
		{"CREATE TABLE t(a INT CONSTRAINT t_a_check NOT NULL CHECK(a>0))", "t_a_check"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+strconv.FormatBool(prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				var err error
				if prepared {
					stmt, e := db.Prepare(tc.query)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					result, e := stmt.Exec()
					err = e
					if result != nil {
						t.Fatal("failed CREATE exposed result")
					}
				} else {
					result, e := db.Exec(tc.query)
					err = e
					if result != nil {
						t.Fatal("failed CREATE exposed result")
					}
				}
				var got *stuntgres.Error
				if !errors.As(err, &got) {
					t.Fatalf("expected SQL diagnostic, got %v", err)
				}
				prefix := "Key (conrelid, contypid, conname)=("
				suffix := ", 0, " + tc.name + ") already exists."
				if !strings.HasPrefix(got.Detail, prefix) || !strings.HasSuffix(got.Detail, suffix) {
					t.Fatalf("invalid detail %q", got.Detail)
				}
				id := strings.TrimSuffix(strings.TrimPrefix(got.Detail, prefix), suffix)
				oid, e := strconv.ParseUint(id, 10, 32)
				if e != nil || oid == 0 || strconv.FormatUint(oid, 10) != id {
					t.Fatalf("invalid allocated OID %q", id)
				}
				notNullDiagnostic(t, err, stuntgres.Error{Code: "23505", Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`, Detail: prefix + id + suffix, Schema: "pg_catalog", Table: "pg_constraint", Constraint: "pg_constraint_conrelid_contypid_conname_index"})
				tableCoreExec(t, db, false, "CREATE TABLE t(a INT)", 0)
			})
		}
	}
}
