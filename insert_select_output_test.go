package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestInsertSelectCapturedConstraintOutput(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, query := range []string{
		"CREATE TABLE s(v BYTEA,n INT)",
		`INSERT INTO s VALUES('\x00',NULL)`,
		"CREATE TABLE t(v BYTEA,n INT NOT NULL)",
	} {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ setting, detail string }{
		{"hex", `Failing row contains (\x00, null).`},
		{"escape", `Failing row contains (\000, null).`},
	} {
		if _, err := conn.ExecContext(ctx, "SET bytea_output="+tc.setting); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{
			"INSERT INTO t SELECT v,n FROM s",
			"INSERT INTO t SELECT v,NULL FROM s",
		} {
			want := stuntgres.Error{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: tc.detail, Schema: "public", Table: "t", Column: "n"}
			_, err := conn.ExecContext(ctx, query)
			typeCoreDiagnostic(t, err, want)
			stmt, err := conn.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.ExecContext(ctx)
			stmt.Close()
			typeCoreDiagnostic(t, err, want)
		}
	}
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT num_nulls(v) FROM t").Scan(&count); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed INSERT did not preserve the empty destination", err)
	}
}
