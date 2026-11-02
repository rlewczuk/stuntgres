package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// REL_18_6 btree_gin--1.1--1.2.sql declares gin_enum_cmp(anyenum,anyenum)
// as a real STRICT IMMUTABLE SQL-callable comparator. Unlike the extraction
// helpers its signature has no internal ABI arguments. Rank, not label or OID,
// determines the independently expected signs below.
func TestReferenceBtreeGINCompoundEnumComparator(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"compound_cmp_reader"})
	fixture.SchemaReferenceCleanup(t, "compound_cmp_schema")
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
		run(fixture.Exec("CREATE TYPE compound_cmp_enum AS ENUM('z','a','m')", 0))
		run(fixture.Exec("ALTER TYPE compound_cmp_enum ADD VALUE 'q' BEFORE 'a'", 0))
		run(fixture.Exec("CREATE TYPE compound_cmp_other AS ENUM('m','a','q','z')", 0))
		run(fixture.Exec("CREATE SCHEMA compound_cmp_schema", 0))
		run(fixture.Exec("CREATE ROLE compound_cmp_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public,compound_cmp_schema TO compound_cmp_reader", 0))
		comparatorStep := func(q string, want any, args ...any) fixture.Step {
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{want}}, args...)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4"}, []bool{true}, 0)
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.ScalarAssertDescription(t, cols, []string{"v"}, []string{"INT4"})
				precision, scale, known := cols[0].DecimalSize()
				if precision != 0 || scale != 0 || known {
					t.Fatalf("comparator decimal metadata %d,%d/%t", precision, scale, known)
				}
			}
			return s
		}
		current := "public.gin_enum_cmp"
		check := func() {
			t.Helper()
			q := "SELECT " + current + "($1::compound_cmp_enum,$2::compound_cmp_enum) AS v"
			held := classHeld(t, p, conn, q)
			for _, tc := range []struct{ left, right, want any }{
				{"z", "q", int64(-1)}, {"q", "a", int64(-1)}, {"a", "q", int64(1)},
				{"a", "a", int64(0)}, {"m", "z", int64(1)}, {nil, "a", nil}, {"a", nil, nil}, {nil, nil, nil},
			} {
				s := comparatorStep(q, tc.want, tc.left, tc.right)
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			for _, tc := range []struct {
				q    string
				want any
			}{
				{"SELECT " + current + "('z'::compound_cmp_enum,'a'::compound_cmp_enum) AS v", int64(-1)},
				{"SELECT " + current + "('z'::compound_cmp_other,'a'::compound_cmp_other) AS v", int64(1)},
				{"SELECT " + current + "(NULL::compound_cmp_enum,'a'::compound_cmp_enum) AS v", nil},
			} {
				classRoutes(t, p, b, conn, comparatorStep(tc.q, tc.want))
			}
			empty := fixture.Select("SELECT "+current+"('z'::compound_cmp_enum,'a'::compound_cmp_enum) AS v WHERE false", []string{"v"}, []string{"INT4"}, nil)
			empty.CheckMetadata = comparatorStep(q, nil).CheckMetadata
			classRoutes(t, p, b, conn, empty)
		}
		check()
		// Resolve both concrete enums independently, but reject mixed enum identities
		// and non-enum arguments through normal polymorphic routine selection.
		const functionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ q, signature string }{
			{"SELECT public.gin_enum_cmp('a'::compound_cmp_enum,'a'::compound_cmp_other)", "public.gin_enum_cmp(compound_cmp_enum, compound_cmp_other)"},
			{"SELECT public.gin_enum_cmp('a'::text,'a'::text)", "public.gin_enum_cmp(text, text)"},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + tc.q
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				d.Hint = functionHint
				d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "public.gin_enum_cmp")]) + 1)
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				s.Prepared = true
				s.ErrorStage = "prepare"
				run(s)
			}
		}
		run(fixture.Exec("CREATE VIEW compound_cmp_view AS SELECT public.gin_enum_cmp('z'::compound_cmp_enum,'q'::compound_cmp_enum) AS v", 0))
		const viewSQL = "SELECT v FROM compound_cmp_view"
		viewHeld := classHeld(t, p, conn, viewSQL)
		viewStep := comparatorStep(viewSQL, int64(-1))
		// A view source column has known nullable native metadata, unlike a computed
		// call. Preserve that existing public adapter distinction explicitly.
		viewStep.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4"}, []bool{true})
		}
		fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, viewStep)
		run(fixture.Exec("ALTER FUNCTION public.gin_enum_cmp(anyenum,anyenum) RENAME TO compound_enum_compare", 0))
		current = "public.compound_enum_compare"
		check()
		fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, viewStep)
		run(fixture.Exec("ALTER FUNCTION public.compound_enum_compare(anyenum,anyenum) SET SCHEMA compound_cmp_schema", 0))
		current = "compound_cmp_schema.compound_enum_compare"
		check()
		fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, viewStep)
		classRoutes(t, p, b, conn, fixture.Select("SELECT p.proname,n.nspname,p.proargtypes::text,p.prorettype::bigint,p.provolatile,p.proisstrict,p.proparallel,p.prosrc,p.probin FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid='compound_cmp_schema.compound_enum_compare(anyenum,anyenum)'::regprocedure", []string{"proname", "nspname", "proargtypes", "prorettype", "provolatile", "proisstrict", "proparallel", "prosrc", "probin"}, []string{"NAME", "NAME", "TEXT", "INT8", "CHAR", "BOOL", "CHAR", "TEXT", "TEXT"}, [][]any{{"compound_enum_compare", "compound_cmp_schema", "3500 3500", int64(23), "i", true, "u", "gin_enum_cmp", "$libdir/btree_gin"}}))
		run(fixture.Exec("ALTER EXTENSION btree_gin DROP FUNCTION compound_cmp_schema.compound_enum_compare(anyenum,anyenum)", 0))
		check() // Removing current membership cannot rewrite original authority.
		run(fixture.Exec("ALTER EXTENSION btree_gin ADD FUNCTION compound_cmp_schema.compound_enum_compare(anyenum,anyenum)", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION compound_cmp_schema.compound_enum_compare(anyenum,anyenum) FROM PUBLIC", 0))
		run(fixture.Exec("SET ROLE compound_cmp_reader", 0))
		denied := fixture.ErrorDiagnostic("SELECT compound_cmp_schema.compound_enum_compare('z'::compound_cmp_enum,'a'::compound_cmp_enum) AS v", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function compound_enum_compare"))
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		classRoutes(t, p, b, conn, denied)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION compound_cmp_schema.compound_enum_compare(anyenum,anyenum) TO compound_cmp_reader", 0))
		run(fixture.Exec("SET ROLE compound_cmp_reader", 0))
		check()
		run(fixture.Exec("RESET ROLE", 0))
		if !b.PG {
			const q = "SELECT compound_cmp_schema.compound_enum_compare($1::compound_cmp_enum,$2::compound_cmp_enum) AS v"
			retained := classHeld(t, p, conn, q)
			fixture.RunStep(t, p.Ctx, retained, b.Recorder, comparatorStep(q, int64(-1), "z", "a"))
			eager, err := conn.QueryContext(p.Ctx, "SELECT compound_cmp_schema.compound_enum_compare('z'::compound_cmp_enum,'a'::compound_cmp_enum) AS v")
			if err != nil {
				t.Fatal(err)
			}
			defer eager.Close()
			eagerStep := comparatorStep("SELECT compound_cmp_schema.compound_enum_compare('z'::compound_cmp_enum,'a'::compound_cmp_enum) AS v", int64(-1))
			eagerStep.CheckMetadata(t, eager)
			metadata, err := eager.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, viewStep)
			rows, err := retained.Statement.QueryContext(p.Ctx, "z", "a")
			if rows != nil {
				rows.Close()
				t.Fatal("retained enum-parameter comparator exposed rows")
			}
			d := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type")
			fixture.AssertDiagnostic(t, err, &d)
			eagerStep.CheckMetadata(t, eager)
			fixture.DeleteReferenceConsume(t, false, eager, nil, eagerStep)
			fixture.ScalarAssertDescription(t, metadata, []string{"v"}, []string{"INT4"})
			run(fixture.Exec("SET ROLE compound_cmp_reader", 0))
			check()
			run(fixture.Exec("RESET ROLE", 0))
		}
		check()
		fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, viewStep)
	})
}
