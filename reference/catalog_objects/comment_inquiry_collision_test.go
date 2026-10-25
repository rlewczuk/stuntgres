//go:build reference

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryLegacyCollision(t *testing.T) {
	t.Run("Oracle", func(t *testing.T) {
		p := fixture.NewPair(t)
		// Genuine description storage with a private high OID, scoped by a
		// transaction so every injected row is removed even on an assertion failure.
		tx, err := p.Postgres.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(p.Ctx, "INSERT INTO pg_description(objoid,classoid,objsubid,description) VALUES(4294967294,1259,0,'relation candidate'),(4294967294,2615,0,'namespace candidate'),(4294967294,1259,1,'column excluded')"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(p.Ctx, "INSERT INTO pg_shdescription(objoid,classoid,description) VALUES(4294967294,1260,'shared excluded')"); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, fixture.Select("SELECT shobj_description(4294967294::oid,'pg_authid')", []string{"shobj_description"}, []string{"TEXT"}, [][]any{{"shared excluded"}}))
		for _, tc := range []struct{ q, want string }{{"SELECT obj_description(4294967294::oid,'pg_class')", "relation candidate"}, {"SELECT obj_description(4294967294::oid,'pg_namespace')", "namespace candidate"}, {"SELECT col_description(4294967294::oid,1)", "column excluded"}} {
			fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, fixture.Select(tc.q, []string{map[bool]string{true: "col_description", false: "obj_description"}[tc.want == "column excluded"]}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		var got string
		if err := tx.QueryRowContext(p.Ctx, "SELECT obj_description(4294967294::oid)").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "relation candidate" && got != "namespace candidate" {
			t.Fatal("legacy candidate membership", got)
		}
		if _, err := tx.ExecContext(p.Ctx, "CREATE TABLE collision_input(a INT)"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(p.Ctx, "INSERT INTO collision_input VALUES(1),(1),(2)"); err != nil {
			t.Fatal(err)
		}
		queryRows := func(query string, prepared bool) *sql.Rows {
			t.Helper()
			if !prepared {
				rows, err := tx.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				return rows
			}
			stmt, err := tx.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			// The transaction closes this statement at rollback; retain it until
			// its returned rows have been consumed.
			rows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			return rows
		}
		checkTypes := func(rows *sql.Rows, want []string) {
			t.Helper()
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != len(want) {
				t.Fatal(types, err)
			}
			for i, typ := range types {
				if typ.DatabaseTypeName() != want[i] {
					t.Fatal("independent result type", i, typ.DatabaseTypeName(), want[i])
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			query := "SELECT obj_description(4294967294::oid),obj_description(4294967294::oid) FROM collision_input"
			rows := queryRows(query, prepared)
			checkTypes(rows, []string{"TEXT", "TEXT"})
			var winner string
			n := 0
			for rows.Next() {
				var first, second string
				if err := rows.Scan(&first, &second); err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					winner = first
				}
				if (first != "relation candidate" && first != "namespace candidate") || first != winner || second != winner {
					t.Fatal("same-statement equal inputs", first, second, winner)
				}
				n++
			}
			if err := rows.Err(); err != nil || n != 3 {
				t.Fatal(n, err)
			}
			rows.Close()
			group := "SELECT obj_description(4294967294::oid) AS description,count(*) AS n FROM collision_input GROUP BY obj_description(4294967294::oid)"
			// Select the candidate independently for this statement: neither a
			// prior execution nor the native backend dictates its winner.
			rows = queryRows(group, prepared)
			checkTypes(rows, []string{"TEXT", "INT8"})
			if !rows.Next() {
				t.Fatal("missing group", rows.Err())
			}
			var candidate string
			var count int64
			if err := rows.Scan(&candidate, &count); err != nil || count != 3 || (candidate != "relation candidate" && candidate != "namespace candidate") {
				t.Fatal("one equal-input group", candidate, count, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("equal inputs split groups", rows.Err())
			}
			rows.Close()
		}
	})
}
