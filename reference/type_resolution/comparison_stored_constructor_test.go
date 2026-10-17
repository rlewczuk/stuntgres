package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func comparisonStoredConstructorSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, q := range []string{
		"CREATE TYPE csc_pair AS (id integer,n integer)",
		"CREATE TYPE csc_single AS (id integer)",
		"CREATE DOMAIN csc_domain AS csc_pair",
		"CREATE TYPE csc_short AS (v varchar(2))",
		"CREATE TABLE csc_strings(v text)",
		"CREATE TABLE csc_outer(id integer,n integer)",
		"CREATE TABLE csc_rhs(id integer,n integer)",
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO csc_outer VALUES(1,NULL),(2,NULL)", 2))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO csc_rhs VALUES(1,NULL)", 1))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO csc_strings VALUES('abcd')", 1))
}

func comparisonStoredConstructorRows(t *testing.T, p *fixture.Pair, b fixture.Backend, second bool) {
	t.Helper()
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT row_value,single_value,domain_value FROM csc_saved", []string{"row_value", "single_value", "domain_value"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{nil, true, true}}))
	want := [][]any{{int64(1), nil, nil}, {int64(2), false, false}}
	if second {
		want = [][]any{{int64(1), false, false}, {int64(2), nil, nil}}
	}
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT id,cast_value,expanded_value FROM csc_correlated ORDER BY id", []string{"id", "cast_value", "expanded_value"}, []string{"INT4", "BOOL", "BOOL"}, want))
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT scalar_value,expanded_value FROM csc_coercions", []string{"scalar_value", "expanded_value"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT v FROM csc_enum_saved", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
}

func TestReferenceComparisonStoredConstructor(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		want             any
	}{
		{"row", "ROW(1,NULL)::csc_pair = ANY(SELECT 1,NULL::integer)", nil},
		{"single", "ROW(1)::csc_single = ANY(SELECT 1)", true},
		{"domain", "ROW(1,NULL)::csc_domain = ANY(SELECT ROW(1,NULL)::csc_pair)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				comparisonStoredConstructorSetup(t, p, b)
				comparisonCheckpointBool(t, p, b, tc.expression, tc.want)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW csc_value AS SELECT "+tc.expression+" AS v", 0))
				comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT v FROM csc_value", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}))
			})
		})
	}
}

func TestReferenceComparisonStoredConstructorLifecycle(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE TYPE csc_lifecycle_pair AS (id integer,n integer)",
			"CREATE TABLE csc_lifecycle_rhs(id integer,n integer)",
			"CREATE VIEW csc_lifecycle_view AS SELECT ROW(1,NULL)::csc_lifecycle_pair = ANY(SELECT id,n FROM csc_lifecycle_rhs) AS v",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		q := "SELECT v FROM csc_lifecycle_view"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		check := func(want any) {
			t.Helper()
			s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		check(false)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO csc_lifecycle_rhs VALUES(1,NULL)", 1))
		check(nil)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER TYPE csc_lifecycle_pair RENAME ATTRIBUTE n TO renamed", 0))
		check(nil)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE csc_lifecycle_rhs SET id=2", 1))
		check(false)
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		// The quantified comparison keeps the coerced fields, not the named
		// constructor's outer identity. Dropping that type must not drop or
		// reinterpret the already-normalized view.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TYPE csc_lifecycle_pair RESTRICT", 0))
		comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TYPE csc_lifecycle_pair AS (id integer,n integer)", 0))
		comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE OR REPLACE VIEW csc_lifecycle_view AS SELECT ROW(2,NULL)::csc_lifecycle_pair = ANY(SELECT id,n FROM csc_lifecycle_rhs) AS v", 0))
		comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TYPE csc_lifecycle_pair CASCADE", 0))
		comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
	})
}

func TestReferenceComparisonStoredConstructorReplay(t *testing.T) {
	var emitted []byte
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		comparisonStoredConstructorSetup(t, p, b)
		for _, q := range []string{
			"CREATE TYPE csc_enum AS ENUM ('z','a')",
			"CREATE TYPE csc_enum_pair AS (v csc_enum)",
			"CREATE VIEW csc_saved AS SELECT ROW(1,NULL)::csc_pair = ANY(SELECT 1,NULL::integer) AS row_value,ROW(1)::csc_single = ANY(SELECT 1) AS single_value,ROW(1,NULL)::csc_domain = ANY(SELECT ROW(1,NULL)::csc_pair) AS domain_value",
			"CREATE VIEW csc_correlated AS SELECT o.id,ROW(o.id,NULL)::csc_pair = ANY(SELECT r.id,r.n FROM csc_rhs r WHERE r.id=o.id) AS cast_value,ROW(o.*)::csc_pair = ANY(SELECT r.id,r.n FROM csc_rhs r WHERE r.id=o.id) AS expanded_value FROM csc_outer o",
			"CREATE VIEW csc_coercions AS SELECT ROW(s.v)::csc_short = ANY(SELECT 'ab'::text) AS scalar_value,ROW(s.*)::csc_short = ANY(SELECT 'ab'::text) AS expanded_value FROM csc_strings s",
			"CREATE VIEW csc_enum_saved AS SELECT ROW('z')::csc_enum_pair < ANY(SELECT 'a'::csc_enum) AS v",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		comparisonStoredConstructorRows(t, p, b, false)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TYPE csc_short RESTRICT", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TYPE csc_enum_pair RESTRICT", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER TYPE csc_enum RENAME VALUE 'z' TO 'zz'", 0))
		comparisonStoredConstructorRows(t, p, b, false)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE csc_rhs SET id=2", 1))
		comparisonStoredConstructorRows(t, p, b, true)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native dump required for pinned PostgreSQL replay")
			}
			for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			if _, err := b.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("PostgreSQL replay of actual native dump", err)
			}
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(emitted, redump.Bytes()) {
				t.Fatal("stored constructor logical redump changed")
			}
		}
		comparisonStoredConstructorRows(t, p, b, true)
	})
}

func TestReferenceComparisonStoredConstructorDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, setup, view, drop, retained string
	}{
		{
			"member", "CREATE TYPE csc_member AS ENUM ('a','b'); CREATE TYPE csc_dep_pair AS (v csc_member)",
			"SELECT ROW('a')::csc_dep_pair = ANY(SELECT 'a'::csc_member) AS v",
			"DROP TYPE csc_dep_pair RESTRICT", "csc_member",
		},
		{
			"nested_member", "CREATE TYPE csc_inner AS (v integer); CREATE TYPE csc_dep_pair AS (v csc_inner)",
			"SELECT ROW(ROW(1)::csc_inner)::csc_dep_pair = ANY(SELECT ROW(1)::csc_inner) AS v",
			"DROP TYPE csc_dep_pair RESTRICT", "csc_inner",
		},
		{
			"whole_value", "CREATE TYPE csc_dep_pair AS (v integer)",
			"SELECT ROW(1)::csc_dep_pair = ANY(SELECT 1) AS v,(SELECT ROW(1)::csc_dep_pair)=(SELECT ROW(1)::csc_dep_pair) AS kept",
			"", "csc_dep_pair",
		},
		{
			"unused_cte", "CREATE TYPE csc_dep_pair AS (v integer)",
			"WITH unused AS (SELECT ROW(1)::csc_dep_pair AS kept) SELECT ROW(1)::csc_dep_pair = ANY(SELECT 1) AS v",
			"", "csc_dep_pair",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				if _, err := b.DB.ExecContext(p.Ctx, tc.setup); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW csc_dependency AS "+tc.view, 0))
				check := func() {
					t.Helper()
					comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT v FROM csc_dependency", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
				}
				check()
				if tc.drop != "" {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(tc.drop, 0))
					check()
				}
				// Fixed released class OIDs: RewriteRelationId2618, TypeRelationId1247.
				// Catalog class-name regclass input is not a prerequisite here.
				dependency := "SELECT EXISTS(SELECT 1 FROM pg_depend d JOIN pg_rewrite r ON d.objid=r.oid WHERE d.classid=2618::oid AND r.ev_class='csc_dependency'::regclass AND d.refclassid=1247::oid AND d.refobjid='" + tc.retained + "'::regtype) AS retained"
				comparisonCheckpointRoutes(t, p, b, fixture.Select(dependency, []string{"retained"}, []string{"BOOL"}, [][]any{{true}}))
				d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type "+tc.retained+" because other objects depend on it")
				d.Detail = "view csc_dependency depends on type " + tc.retained
				d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				if !b.PG && tc.name == "member" {
					// Direct enum DROP is an existing non-comparison DDL limit;
					// the public dependency edge and retained values are still required.
					d = fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported")
					d.Position = "1"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic("DROP TYPE "+tc.retained+" RESTRICT", d))
				check()
			})
		})
	}
}
