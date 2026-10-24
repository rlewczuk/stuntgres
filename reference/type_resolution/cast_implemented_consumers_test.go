package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastImplementedConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			q := "SELECT CAST(CAST($1 AS text) AS uuid) AS u,CAST(CAST($2 AS integer) AS bytea) AS b,CAST(CAST($3 AS jsonb) AS integer) AS n"
			args := []any{"00000000000000000000000000000001", int64(42), "2.5"}
			expected := [][]any{{"00000000-0000-0000-0000-000000000001", "\x00\x00\x00*", int64(3)}}
			// Plain and Context DB/Tx consumers remain distinct public method paths.
			for _, plain := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(q, []string{"u", "b", "n"}, []string{"UUID", "BYTEA", "INT4"}, expected, args...)
					s.NonContext, s.Prepared = plain, prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
			}
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var owned []byte
			for _, contextual := range []bool{false, true} {
				var rows *sql.Rows
				if contextual {
					rows, err = stmt.QueryContext(p.Ctx, args...)
				} else {
					rows, err = stmt.Query(args...)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var u string
				var binary []byte
				var n int64
				if err = rows.Scan(&u, &binary, &n); err != nil {
					t.Fatal(err)
				}
				if u != "00000000-0000-0000-0000-000000000001" || !bytes.Equal(binary, []byte{0, 0, 0, 42}) || n != 3 {
					t.Fatal(u, binary, n)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra consumer row", rows.Err())
				}
				if err = rows.Close(); err != nil {
					t.Fatal(err)
				}
				if owned != nil && !bytes.Equal(owned, []byte{0, 0, 0, 42}) {
					t.Fatal("owned scan changed", owned)
				}
				owned = binary
				var nullU sql.NullString
				var nullB []byte
				var nullN sql.NullInt64
				if contextual {
					err = stmt.QueryRowContext(p.Ctx, nil, nil, nil).Scan(&nullU, &nullB, &nullN)
				} else {
					err = stmt.QueryRow(nil, nil, nil).Scan(&nullU, &nullB, &nullN)
				}
				if err != nil || nullU.Valid || nullB != nil || nullN.Valid {
					t.Fatal("typed NULL consumer", nullU, nullB, nullN, err)
				}
			}
			if !bytes.Equal(owned, []byte{0, 0, 0, 42}) {
				t.Fatal("NULL execution changed owned bytes", owned)
			}
			// Mutating a caller-owned scan must not mutate either the source or a later
			// result returned by the same held statement.
			owned[0] = 99
			var u string
			var next []byte
			var n int64
			if err = stmt.QueryRowContext(p.Ctx, args...).Scan(&u, &next, &n); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(next, []byte{0, 0, 0, 42}) || n != 3 {
				t.Fatal(next, n)
			}
		})
	}
}
