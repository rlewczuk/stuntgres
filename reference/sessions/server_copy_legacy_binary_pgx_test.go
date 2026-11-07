//go:build !noservertests

package reference_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"stuntgres/reference/fixture"
)

func TestReferenceServerCopyLegacyBinaryPGX(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := pgx.Connect(f.Ctx, f.DSNFor(backend))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !conn.IsClosed() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if err := conn.Close(ctx); err != nil {
						t.Errorf("close pgx connection: %v", err)
					}
				}
			}()
			if _, err := conn.Exec(f.Ctx, "CREATE TEMP TABLE legacy_pgx_rows(id int4,v text)"); err != nil {
				t.Fatal(err)
			}
			// This is pgx's real CopyFrom, including its own legacy SQL emission.
			// No intercepted query, alternate encoder or custom source rewrites it.
			count, err := conn.CopyFrom(f.Ctx, pgx.Identifier{"legacy_pgx_rows"}, []string{"id", "v"}, pgx.CopyFromRows([][]any{
				{int32(1), "alpha"}, {int32(2), nil}, {int32(3), ""},
			}))
			if err != nil || count != 3 {
				t.Fatalf("pgx.CopyFrom count=%d error=%v; want 3 and nil", count, err)
			}
			rows, err := conn.Query(f.Ctx, "SELECT id,v FROM legacy_pgx_rows ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			fields := rows.FieldDescriptions()
			if len(fields) != 2 || fields[0].Name != "id" || fields[0].DataTypeOID != 23 || fields[1].Name != "v" || fields[1].DataTypeOID != 25 {
				t.Fatalf("int4/text metadata: %+v", fields)
			}
			var got [][]any
			for rows.Next() {
				values, err := rows.Values()
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, values)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			want := [][]any{{int32(1), "alpha"}, {int32(2), nil}, {int32(3), ""}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("typed pgx rows=%#v; want %#v", got, want)
			}
			var answer int32
			if err := conn.QueryRow(f.Ctx, "SELECT 42").Scan(&answer); err != nil || answer != 42 {
				t.Fatalf("subsequent SELECT=%d error=%v", answer, err)
			}
			if err := conn.Close(f.Ctx); err != nil {
				t.Fatalf("clean pgx close: %v", err)
			}
		})
	}
}
