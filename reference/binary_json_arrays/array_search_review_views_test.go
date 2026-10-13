package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const arraySearchReviewInitialDefinition = " SELECT id,\n    array_replace(a::integer[]::numeric[], n::numeric, array_position(a::integer[]::bigint[], n)::numeric) AS r,\n    array_positions(array_remove(a::integer[]::bigint[], n), n) AS ps,\n    array_position(array_replace(ARRAY[t], t, 'y'::text), 'y'::text) AS p\n   FROM as_review_inputs;"
const arraySearchReviewRenamedDefinition = " SELECT id,\n    array_replace(choices::integer[]::numeric[], n::numeric, array_position(choices::integer[]::bigint[], n)::numeric) AS r,\n    array_positions(array_remove(choices::integer[]::bigint[], n), n) AS ps,\n    array_position(array_replace(ARRAY[t], t, 'y'::text), 'y'::text) AS p\n   FROM as_review_renamed;"

// Safe Dump assigns one quoted __view_source_0 alias and freezes original column
// aliases (including a). Native presentation preserves those explicit quotes/
// labels; PostgreSQL's normalized presentation of the same SQL is observed below.
const arraySearchReviewNativeRestoredDefinition = " SELECT \"__view_source_0\".\"id\" AS id,\n    array_replace(\"__view_source_0\".\"a\"::integer[]::numeric[], \"__view_source_0\".\"n\"::numeric, array_position(\"__view_source_0\".\"a\"::integer[]::bigint[], \"__view_source_0\".\"n\")::numeric) AS r,\n    array_positions(array_remove(\"__view_source_0\".\"a\"::integer[]::bigint[], \"__view_source_0\".\"n\"), \"__view_source_0\".\"n\") AS ps,\n    array_position(array_replace(ARRAY[\"__view_source_0\".\"t\"], \"__view_source_0\".\"t\", 'y'::text), 'y'::text) AS p\n   FROM as_review_renamed AS \"__view_source_0\"(\"id\", \"a\", \"n\", \"t\");"
const arraySearchReviewPostgresReplayDefinition = " SELECT id,\n    array_replace(a::integer[]::numeric[], n::numeric, array_position(a::integer[]::bigint[], n)::numeric) AS r,\n    array_positions(array_remove(a::integer[]::bigint[], n), n) AS ps,\n    array_position(array_replace(ARRAY[t], t, 'y'::text), 'y'::text) AS p\n   FROM as_review_renamed __view_source_0(id, a, n, t);"

func TestReferenceArraySearchNestedViews(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		if !b.PG {
			// Source-only allocator holes make the later native descriptor and
			// collation transition relationally observable without pinning OIDs.
			for i := range 8 {
				name := fmt.Sprintf("as_review_spacer_%d", i)
				run(fixture.Exec("CREATE DOMAIN "+name+" AS INT", 0))
				run(fixture.Exec("DROP DOMAIN "+name, 0))
			}
		}
		for _, q := range []string{
			`CREATE COLLATION as_review_collation FROM pg_catalog."C"`,
			"CREATE DOMAIN as_review_array AS INT[]",
			"CREATE TABLE as_review_inputs(id INT,a as_review_array,n BIGINT,t TEXT COLLATE as_review_collation)",
			"CREATE VIEW as_review_view AS SELECT id,array_replace(a::int[],n,array_position(a::int[],n)::numeric) AS r,array_positions(array_remove(a::int[],n),n) AS ps,array_position(array_replace(ARRAY[t],t,'y'), 'y') AS p FROM as_review_inputs",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO as_review_inputs VALUES (1,'[0:2]={1,2,1}',1,'x'),(2,'{3,NULL}',3,NULL),(3,NULL,NULL,'z')", 3))
		check := arraySearchSelect(b, "SELECT id,r,ps,p FROM as_review_view ORDER BY id", []string{"id", "r", "ps", "p"}, []string{"INT4", "_NUMERIC", "_INT4", "INT4"}, [][]any{
			{int64(1), "[0:2]={0,2,0}", "{}", int64(1)},
			{int64(2), "{1,NULL}", "{}", int64(1)},
			{int64(3), nil, nil, int64(1)},
		})
		check = arraySearchReviewNumericMetadata(check, b.PG)
		arraySearchBoth(t, p, b, c, check)
		stmt, err := c.PrepareContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		})
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: check.Query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		t.Run("initial_definition", func(t *testing.T) { arraySearchReviewViewDefinition(t, p, b, c, arraySearchReviewInitialDefinition) })
		for _, q := range []string{
			"ALTER TABLE as_review_inputs RENAME COLUMN a TO choices",
			"ALTER TABLE as_review_inputs RENAME TO as_review_renamed",
			"ALTER COLLATION as_review_collation RENAME TO as_review_collation_renamed",
		} {
			run(fixture.Exec(q, 0))
		}
		arraySearchBoth(t, p, b, c, check)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		t.Run("renamed_definition", func(t *testing.T) { arraySearchReviewViewDefinition(t, p, b, c, arraySearchReviewRenamedDefinition) })
		if b.PG {
			return
		} // PostgreSQL has no native catalog-replacement API.
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		identity := func(q string) int64 {
			var id int64
			if err := c.QueryRowContext(p.Ctx, q).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id <= 0 {
				t.Fatalf("invalid catalog identity %d", id)
			}
			return id
		}
		const typeIdentity = "SELECT 'as_review_array'::regtype::bigint"
		const collationIdentity = "SELECT oid::bigint FROM pg_catalog.pg_collation WHERE collname='as_review_collation_renamed'"
		oldType, oldCollation := identity(typeIdentity), identity(collationIdentity)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if p.Postgres != nil {
			defer func() {
				for _, q := range []string{"DROP TABLE IF EXISTS as_review_renamed CASCADE", "DROP DOMAIN IF EXISTS as_review_array CASCADE", "DROP COLLATION IF EXISTS as_review_collation_renamed CASCADE"} {
					if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual nested emitted-SQL replay", err)
			}
			pgCheck := arraySearchReviewNumericMetadata(fixture.ArrayReferenceSelect(check.Query, check.Columns, check.Types, check.Types, check.Rows, true), true)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pgCheck))
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("replayed_definition", func(t *testing.T) {
				arraySearchReviewViewDefinition(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg, arraySearchReviewPostgresReplayDefinition)
			})
			if err := pg.Close(); err != nil {
				t.Fatal(err)
			}
		}
		retained, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		check.CheckMetadata(t, retained)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if current := identity(typeIdentity); current == oldType {
			t.Fatal("domain remap witness reused source identity")
		}
		if current := identity(collationIdentity); current == oldCollation {
			t.Fatal("collation remap witness reused source identity")
		}
		fixture.ArrayReferenceConsumeHeld(t, retained, check.Rows, check.Types)
		arraySearchBoth(t, p, b, c, check)
		t.Run("restored_definition", func(t *testing.T) {
			arraySearchReviewViewDefinition(t, p, b, c, arraySearchReviewNativeRestoredDefinition)
		})
	})
}

func arraySearchReviewNumericMetadata(s fixture.Step, pg bool) fixture.Step {
	basic := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		basic(t, rows)
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		precision, scale, known := columns[1].DecimalSize()
		wantPrecision, wantScale, wantKnown := int64(0), int64(0), false
		if pg {
			wantPrecision, wantScale, wantKnown = 65535, 65531, true
		}
		if precision != wantPrecision || scale != wantScale || known != wantKnown {
			t.Fatalf("nested unsized numeric array decimal=%d,%d/%t want %d,%d/%t", precision, scale, known, wantPrecision, wantScale, wantKnown)
		}
	}
	return s
}
