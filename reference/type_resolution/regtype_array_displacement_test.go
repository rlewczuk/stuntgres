package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type regtypeDisplacementCase struct {
	kind, base, named, baseArray, namedArray string
	arrayFirst, prepared                     bool
}

func regtypeDisplacementCases() []regtypeDisplacementCase {
	var cases []regtypeDisplacementCase
	for _, kind := range []string{"table", "query_table", "domain", "enum", "view"} {
		for _, first := range []bool{true, false} {
			for _, prepared := range []bool{false, true} {
				base := fmt.Sprintf("d_%s_%t_%t", kind, first, prepared)
				c := regtypeDisplacementCase{kind: kind, base: base, named: "_" + base, arrayFirst: first, prepared: prepared}
				if first {
					c.baseArray, c.namedArray = "__"+base, "__"+base+"_1"
				} else {
					c.baseArray, c.namedArray = "_"+base+"_1", "__"+base
				}
				cases = append(cases, c)
			}
		}
	}
	return cases
}

func regtypeDisplacementCreate(c regtypeDisplacementCase, name string) fixture.Step {
	n := fixture.RegtypeDisplacementQualified(name)
	var q string
	var count int64
	switch c.kind {
	case "table":
		q = "CREATE TABLE " + n + "(v integer)"
	case "query_table":
		q, count = "CREATE TABLE "+n+" AS SELECT 7 AS v", 1
	case "domain":
		q = "CREATE DOMAIN " + n + " AS integer"
	case "enum":
		q = "CREATE TYPE " + n + " AS ENUM ('ok','sad')"
	case "view":
		q = "CREATE VIEW " + n + " AS SELECT 7 AS v"
	}
	s := fixture.Exec(q, count)
	s.Prepared = c.prepared
	return s
}

// The SQL catalog witnesses the actual canonical array association; independent
// REGTYPE input and numeric identities above remain separate authorities.
func regtypeDisplacementCatalog(t *testing.T, p *fixture.Pair, conn *sql.Conn, base, array string) {
	t.Helper()
	var elem, associated, arrayID int64
	var name string
	// Restrict the existing element predicate before joining, in the same
	// captured statement. Keep every matching companion; never use LIMIT 1.
	q := "SELECT e.oid::bigint,e.typarray::bigint,a.oid::bigint,a.typname FROM (SELECT oid,typarray FROM pg_catalog.pg_type WHERE oid=$1::regtype::oid) e JOIN pg_catalog.pg_type a ON a.typelem=e.oid WHERE a.oid=e.typarray"
	if err := conn.QueryRowContext(p.Ctx, q, fixture.RegtypeDisplacementQualified(base)).Scan(&elem, &associated, &arrayID, &name); err != nil {
		t.Fatal("reciprocal oracle association", base, err)
	}
	if name != array || associated != arrayID || elem == arrayID {
		t.Fatalf("array association for %q: element=%d associated=%d array=%d name=%q want=%q", base, elem, associated, arrayID, name, array)
	}
}

func regtypeDisplacementCheck(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, c regtypeDisplacementCase, baseID, arrayID int64) {
	t.Helper()
	namedID := fixture.RegtypeDisplacementOID(t, p, conn, c.named)
	namedArrayID := fixture.RegtypeDisplacementOID(t, p, conn, c.named+"[]")
	if baseID == namedID || arrayID == namedID || namedArrayID == arrayID || namedArrayID == namedID {
		t.Fatalf("distinct element/array identities collapsed: base=%d oldarray=%d named=%d namedarray=%d", baseID, arrayID, namedID, namedArrayID)
	}
	q := "SELECT ($1::bigint=" + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.base)) + "::regtype::bigint) AS a,($2::bigint=" + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.base+"[]")) + "::regtype::bigint) AS b,($2::bigint=" + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.baseArray)) + "::regtype::bigint) AS c,($3::bigint=" + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.namedArray)) + "::regtype::bigint) AS d"
	fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select(q, []string{"a", "b", "c", "d"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, baseID, arrayID, namedArrayID))
	q = "SELECT " + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.baseArray)) + "::regtype AS a," + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.named)) + "::regtype AS b," + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.namedArray)) + "::regtype AS c"
	fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect(q, c.base+"[]", c.named, c.named+"[]"))
	regtypeDisplacementCatalog(t, p, conn, c.base, c.baseArray)
	regtypeDisplacementCatalog(t, p, conn, c.named, c.namedArray)
	if c.kind == "query_table" || c.kind == "view" {
		for _, n := range []string{c.base, c.named} {
			fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT v FROM "+fixture.RegtypeDisplacementQualified(n), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
}

func regtypeDisplacementReferences(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, c regtypeDisplacementCase) {
	t.Helper()
	// Keep both a data-only reference and an early-bound stored definition to
	// the array that will be displaced. Neither may retarget to the new scalar.
	literal := fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.base + "[]"))
	for _, q := range []string{
		"CREATE TABLE " + fixture.RegtypeDisplacementQualified("w_"+c.base) + "(r regtype DEFAULT " + literal + "::regtype)",
		"CREATE VIEW " + fixture.RegtypeDisplacementQualified("v_"+c.base) + " AS SELECT " + literal + "::regtype AS r",
	} {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO "+fixture.RegtypeDisplacementQualified("w_"+c.base)+" DEFAULT VALUES", 1))
}

func regtypeDisplacementMetadata(t *testing.T, rows *sql.Rows, postgres, stored bool, names, families []string) {
	t.Helper()
	fixture.RegtypeFamilyMetadata(t, rows, postgres, families)
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(names) {
		t.Fatalf("column count got %d want %d", len(columns), len(names))
	}
	for i, column := range columns {
		nullable, known := column.Nullable()
		wantKnown := stored && !postgres
		if column.Name() != names[i] || nullable != wantKnown || known != wantKnown {
			t.Fatalf("column %d name=%q nullable=%t/%t want name=%q nullable=%t/%t", i, column.Name(), nullable, known, names[i], wantKnown, wantKnown)
		}
	}
}

func regtypeDisplacementReferenceCheck(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, c regtypeDisplacementCase, arrayID int64, rows int) {
	t.Helper()
	for _, prefix := range []string{"w_", "v_"} {
		want := [][]any{{c.base + "[]", true}}
		if prefix == "w_" && rows == 2 {
			want = append(want, []any{c.base + "[]", true})
		}
		q := "SELECT r,(r::bigint=$1) AS stable FROM " + fixture.RegtypeDisplacementQualified(prefix+c.base)
		s := fixture.Select(q, []string{"r", "stable"}, []string{"REGTYPE", "BOOL"}, want, arrayID)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			// Both sources expose declared relation columns. bindViewOutput
			// publishes nullable view columns even for a constant projection;
			// reading r is not a new computed REGTYPE expression.
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			nullable, known := columns[0].Nullable()
			wantKnown := recorder.RawBackend != fixture.RawPostgres
			if nullable != wantKnown || known != wantKnown {
				t.Fatalf("reference source %s nullable=%t/%t want %t/%t", prefix+c.base, nullable, known, wantKnown, wantKnown)
			}
		}
		fixture.RegtypeBoth(t, p, conn, recorder, s)
	}
}

// Read all expectation identities once per catalog checkpoint, not once per
// assertion. Values remain backend-local probes, never allocator goldens, and
// callers refresh them after Restore or a failed replacement.
func regtypeDisplacementIdentities(t *testing.T, p *fixture.Pair, conn *sql.Conn, cases []regtypeDisplacementCase) [][4]int64 {
	t.Helper()
	identities := make([][4]int64, len(cases))
	args := make([]any, 0, len(cases)*4)
	destinations := make([]any, 0, len(cases)*4)
	var query strings.Builder
	query.WriteString("SELECT ")
	for i, c := range cases {
		for j, name := range []string{c.base, c.base + "[]", c.named, c.named + "[]"} {
			if len(args) != 0 {
				query.WriteByte(',')
			}
			query.WriteByte('$')
			query.WriteString(strconv.Itoa(len(args) + 1))
			query.WriteString("::regtype::bigint")
			args = append(args, fixture.RegtypeDisplacementQualified(name))
			destinations = append(destinations, &identities[i][j])
		}
	}
	if err := conn.QueryRowContext(p.Ctx, query.String(), args...).Scan(destinations...); err != nil {
		t.Fatal("displacement identity checkpoint", err)
	}
	for i, ids := range identities {
		for _, id := range ids {
			if id <= 0 {
				t.Fatalf("nonpositive identity for %s: %v", cases[i].base, ids)
			}
		}
	}
	return identities
}

// Logical replay preserves named elements and their reciprocal arrays, not the
// historical generated aliases chosen by a different declaration order.
func regtypeDisplacementLogicalCheck(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, c regtypeDisplacementCase, ids [4]int64) {
	t.Helper()
	for i, id := range ids {
		for _, other := range ids[:i] {
			if id == other {
				t.Fatalf("destination element/array identities collapsed: %v", ids)
			}
		}
	}
	q := "SELECT " + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.base)) + "::regtype AS a," + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.base+"[]")) + "::regtype AS b," + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.named)) + "::regtype AS c," + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.named+"[]")) + "::regtype AS d"
	fixture.RegtypeBoth(t, p, conn, recorder, fixture.RegtypeSelect(q, c.base, c.base+"[]", c.named, c.named+"[]"))
	for _, element := range []string{c.base, c.named} {
		q := "SELECT (a.typelem=e.oid) AS reciprocal,(a.oid=" + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(element+"[]")) + "::regtype::oid) AS associated FROM (SELECT oid,typarray FROM pg_catalog.pg_type WHERE oid=" + fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(element)) + "::regtype::oid) e JOIN pg_catalog.pg_type a ON a.oid=e.typarray"
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select(q, []string{"reciprocal", "associated"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	}
	if c.kind == "query_table" || c.kind == "view" {
		for _, name := range []string{c.base, c.named} {
			fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT v FROM "+fixture.RegtypeDisplacementQualified(name), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	regtypeDisplacementReferenceCheck(t, p, conn, recorder, c, ids[1], 1)
}

func TestReferenceRegtypeArrayDisplacement(t *testing.T) {
	for _, c := range regtypeDisplacementCases() {
		t.Run(c.base, func(t *testing.T) {
			fixture.RegtypeDisplacementScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
				first, second := c.base, c.named
				if !c.arrayFirst {
					first, second = second, first
				}
				fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(c, first))
				firstID := fixture.RegtypeDisplacementOID(t, p, conn, first)
				firstArrayID := fixture.RegtypeDisplacementOID(t, p, conn, first+"[]")
				var retained *sql.Rows
				if !backend.PG {
					var err error
					retained, err = conn.QueryContext(p.Ctx, "SELECT $1::bigint::regtype AS a,$2::bigint::regtype AS b", firstID, firstArrayID)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
				}
				if c.arrayFirst {
					regtypeDisplacementReferences(t, p, conn, recorder, c)
				}
				fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(c, second))
				baseID, arrayID := firstID, firstArrayID
				if !c.arrayFirst {
					baseID = fixture.RegtypeDisplacementOID(t, p, conn, c.base)
					arrayID = fixture.RegtypeDisplacementOID(t, p, conn, c.base+"[]")
					fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT ($1::bigint="+fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.named))+"::regtype::bigint) AS a,($2::bigint="+fixture.RegtypeLiteral(fixture.RegtypeDisplacementQualified(c.namedArray))+"::regtype::bigint) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, firstID, firstArrayID))
				}
				regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)
				if c.arrayFirst {
					fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO "+fixture.RegtypeDisplacementQualified("w_"+c.base)+" DEFAULT VALUES", 1))
					regtypeDisplacementReferenceCheck(t, p, conn, recorder, c, arrayID, 2)
				}
				if retained != nil {
					fixture.RegtypeAssertRowsWithMetadata(t, retained, [][]any{{first, first + "[]"}}, false, func(t *testing.T, rows *sql.Rows) {
						regtypeDisplacementMetadata(t, rows, false, false, []string{"a", "b"}, []string{"REGTYPE", "REGTYPE"})
					})
				}
			})
		})
	}
}

func TestReferenceRegtypeArrayDisplacementCollisions(t *testing.T) {
	fixture.RegtypeDisplacementScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		// The desired displacement destination already belongs to a named type.
		// Its identity remains unchanged; both generated names choose suffixes.
		c := regtypeDisplacementCase{kind: "domain", base: "occupied", named: "_occupied", baseArray: "__occupied_1", namedArray: "__occupied_2", arrayFirst: true}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TYPE regtype_displacement.__occupied AS ENUM ('ok')", 0))
		occupied := fixture.RegtypeDisplacementOID(t, p, conn, "__occupied")
		fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(c, c.base))
		baseID, arrayID := fixture.RegtypeDisplacementOID(t, p, conn, c.base), fixture.RegtypeDisplacementOID(t, p, conn, c.base+"[]")
		fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(c, c.named))
		regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)
		if got := fixture.RegtypeDisplacementOID(t, p, conn, "__occupied"); got != occupied {
			t.Fatalf("named collision displaced: %d want %d", got, occupied)
		}
		// A nested displacement may move the first displaced array again.
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_displacement.__occupied_1 AS integer", 0))
		c.baseArray = "___occupied_1"
		regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)

		// Row identity and its array are reserved before self-referential defaults.
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE regtype_displacement.self(v integer)", 0))
		selfArray := fixture.RegtypeDisplacementOID(t, p, conn, "self[]")
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE regtype_displacement._self(r regtype DEFAULT 'regtype_displacement._self'::regtype,a regtype DEFAULT 'regtype_displacement._self[]'::regtype)", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_displacement._self DEFAULT VALUES", 1))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT r,a,(r::oid='regtype_displacement._self'::regtype::oid) AS own_row,(a::oid='regtype_displacement._self[]'::regtype::oid) AS own_array,($1::bigint='regtype_displacement.__self'::regtype::bigint) AS old_array FROM regtype_displacement._self", []string{"r", "a", "own_row", "own_array", "old_array"}, []string{"REGTYPE", "REGTYPE", "BOOL", "BOOL", "BOOL"}, [][]any{{"_self", "_self[]", true, true, true}}, selfArray))

		// At the 63-byte boundary prefixing must clip, then suffix after clipping.
		long := strings.Repeat("x", 63)
		clipped := regtypeDisplacementCase{kind: "domain", base: long, named: "_" + long[:62], baseArray: "__" + long[:61], namedArray: "__" + long[:59] + "_1", arrayFirst: true}
		fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(clipped, clipped.base))
		longID, longArray := fixture.RegtypeDisplacementOID(t, p, conn, long), fixture.RegtypeDisplacementOID(t, p, conn, long+"[]")
		fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(clipped, clipped.named))
		regtypeDisplacementCheck(t, p, backend, conn, recorder, clipped, longID, longArray)

		// UTF8 clipping must stop before a complete code point, not byte63.
		unicodeBase := strings.Repeat("é", 31) + "z"
		unicodeNamed := "_" + strings.Repeat("é", 31)
		unicodeMoved := "__" + strings.Repeat("é", 30)
		unicodeNewArray := "__" + strings.Repeat("é", 29) + "_1"
		u := regtypeDisplacementCase{kind: "domain"}
		fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(u, unicodeBase))
		unicodeArrayID := fixture.RegtypeDisplacementOID(t, p, conn, unicodeBase+"[]")
		fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(u, unicodeNamed))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT ($1::bigint=$2::text::regtype::bigint) AS a,($1::bigint=$3::text::regtype::bigint) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, unicodeArrayID, fixture.RegtypeDisplacementQualified(unicodeMoved), fixture.RegtypeDisplacementQualified(unicodeBase)+"[]"))
		s := fixture.RegtypeSelect("SELECT $1::text::regtype AS a,$2::text::regtype AS b", fixture.RegtypeIdentifier(unicodeBase)+"[]", fixture.RegtypeIdentifier(unicodeNamed)+"[]")
		s.Args = []any{fixture.RegtypeDisplacementQualified(unicodeMoved), fixture.RegtypeDisplacementQualified(unicodeNewArray)}
		fixture.RegtypeBoth(t, p, conn, recorder, s)
		if backend.PG {
			regtypeDisplacementCatalog(t, p, conn, unicodeBase, unicodeMoved)
			regtypeDisplacementCatalog(t, p, conn, unicodeNamed, unicodeNewArray)
		}
	})
}

func TestReferenceRegtypeArrayDisplacementAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			fixture.RegtypeDisplacementScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
				for _, q := range []string{"CREATE DOMAIN regtype_displacement.late AS integer", "CREATE TABLE regtype_displacement.sentinel(v integer)", "INSERT INTO regtype_displacement.sentinel VALUES(42)"} {
					count := int64(0)
					if strings.HasPrefix(q, "INSERT") {
						count = 1
					}
					fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, count))
				}
				arrayID := fixture.RegtypeDisplacementOID(t, p, conn, "_late")
				failures := []struct{ query, code, message, token string }{
					{"CREATE TABLE regtype_displacement._late(r regtype DEFAULT 'integer'::regtype,broken integer DEFAULT 'bad')", "22P02", `invalid input syntax for type integer: "bad"`, "'bad'"},
					{"CREATE TABLE regtype_displacement._late AS SELECT 1/0 AS v", "22012", "division by zero", ""},
					{"CREATE DOMAIN regtype_displacement._late AS missing_displacement_base", "42704", `type "missing_displacement_base" does not exist`, "missing_displacement_base"},
					{"CREATE TYPE regtype_displacement._late AS ENUM ('ok','ok')", "23505", `duplicate key value violates unique constraint "pg_enum_typid_label_index"`, ""},
					{"CREATE VIEW regtype_displacement._late AS SELECT missing_displacement_column", "42703", `column "missing_displacement_column" does not exist`, "missing_displacement_column"},
				}
				for _, f := range failures {
					t.Run(f.query, func(t *testing.T) {
						stage := "execute"
						var result sql.Result
						var err error
						if prepared {
							var stmt *sql.Stmt
							stmt, err = conn.PrepareContext(p.Ctx, f.query)
							if err != nil {
								stage = "prepare"
								if stmt != nil {
									stmt.Close()
									t.Error("failed Prepare exposed a statement")
								}
							} else {
								result, err = stmt.ExecContext(p.Ctx)
								if closeErr := stmt.Close(); closeErr != nil {
									t.Fatal(closeErr)
								}
							}
						} else {
							result, err = conn.ExecContext(p.Ctx, f.query)
						}
						if err == nil {
							t.Fatal("invalid later declaration succeeded")
						}
						// CREATE VIEW body semantics, like the other utility
						// creators here, are checked when the command executes.
						wantStage := "execute"
						if stage != wantStage {
							t.Errorf("failure stage got %q want %q", stage, wantStage)
						}
						if result != nil {
							t.Error("failed declaration exposed a result")
						}
						want := fixture.ControlsReferenceDiagnostic(f.code, f.message)
						if f.token != "" {
							want.Position = strconv.Itoa(strings.Index(f.query, f.token) + 1)
						}
						if f.code == "23505" {
							// Only the newly allocated enum OID varies. Validate its
							// unsigned decimal grammar and every other detail byte.
							got := fixture.ExtractDiagnostic(t, err)
							const prefix = "Key (enumtypid, enumlabel)=("
							const suffix = ", ok) already exists."
							if !strings.HasPrefix(got.Detail, prefix) || !strings.HasSuffix(got.Detail, suffix) {
								t.Fatalf("enum diagnostic detail: %#v", got)
							}
							number := strings.TrimSuffix(strings.TrimPrefix(got.Detail, prefix), suffix)
							oid, parseErr := strconv.ParseUint(number, 10, 32)
							if parseErr != nil || oid == 0 || strconv.FormatUint(oid, 10) != number {
								t.Fatalf("enum diagnostic OID: %q", number)
							}
							want.Detail = prefix + strconv.FormatUint(oid, 10) + suffix
							want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_enum", "pg_enum_typid_label_index"
						}
						fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT ($1::bigint='regtype_displacement._late'::regtype::bigint) AS a,($1::bigint='regtype_displacement.late[]'::regtype::bigint) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, arrayID))
						fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT v FROM regtype_displacement.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
						if backend.PG {
							regtypeDisplacementCatalog(t, p, conn, "late", "_late")
						}
						// No renamed or new-array identity may escape a failed stage.
						for _, name := range []string{"__late", "__late_1"} {
							_, lookupErr := conn.ExecContext(p.Ctx, "SELECT $1::regtype", fixture.RegtypeDisplacementQualified(name))
							fixture.State(t, lookupErr, "42704")
						}
						fixture.AssertDiagnostic(t, err, &want)
					})
				}
				// Isolate every genuine conflict so an expectation mismatch for
				// one creator cannot suppress coverage of the remaining creators.
				// Keep the independent conflict diagnostics unchanged: tests-17
				// does not print their full records in the quiet success path.
				t.Run("genuine_conflict_domain", func(t *testing.T) {
					q := "CREATE DOMAIN regtype_displacement.late AS missing_displacement_base"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42710", Message: `type "late" already exists`})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
					if got := fixture.RegtypeDisplacementOID(t, p, conn, "_late"); got != arrayID {
						t.Fatalf("genuine conflict changed associated array: %d want %d", got, arrayID)
					}
				})
				for _, kind := range []string{"table", "query_table", "enum", "view"} {
					t.Run("genuine_conflict_"+kind, func(t *testing.T) {
						c := regtypeDisplacementCase{kind: kind, prepared: prepared}
						q := regtypeDisplacementCreate(c, "late").Query
						want := fixture.Diagnostic{Code: "42710", Message: `type "late" already exists`}
						if kind != "enum" {
							want.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
						}
						s := fixture.ImplicitError(q, want)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, recorder, s)
						if got := fixture.RegtypeDisplacementOID(t, p, conn, "_late"); got != arrayID {
							t.Fatalf("genuine conflict changed associated array: %d want %d", got, arrayID)
						}
					})
				}
			})
		})
	}
}

func TestReferenceRegtypeArrayDisplacementControls(t *testing.T) {
	for _, kind := range []string{"table", "query_table", "domain", "enum", "view"} {
		t.Run(kind, func(t *testing.T) {
			var cases []regtypeDisplacementCase
			for _, c := range regtypeDisplacementCases() {
				if c.kind == kind {
					cases = append(cases, c)
				}
			}
			var emitted []byte
			sourceOK := t.Run("source", func(t *testing.T) {
				regtypeDisplacementControlsScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
					mark := regtypeDisplacementControlsPhase(t, p)
					for _, c := range cases {
						mark("setup/" + c.base)
						first, second := c.base, c.named
						if !c.arrayFirst {
							first, second = second, first
						}
						fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(c, first))
						if c.arrayFirst {
							regtypeDisplacementReferences(t, p, conn, recorder, c)
						}
						fixture.RunStep(t, p.Ctx, conn, recorder, regtypeDisplacementCreate(c, second))
						if !c.arrayFirst {
							regtypeDisplacementReferences(t, p, conn, recorder, c)
						}
					}
					check := func(source bool) {
						mark("identity-checkpoint")
						identities := regtypeDisplacementIdentities(t, p, conn, cases)
						for i, c := range cases {
							mark("logical-check/" + c.base)
							if source {
								baseID, arrayID := identities[i][0], identities[i][1]
								regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)
								regtypeDisplacementReferenceCheck(t, p, conn, recorder, c, arrayID, 1)
							} else {
								regtypeDisplacementLogicalCheck(t, p, backend, conn, recorder, c, identities[i])
							}
						}
					}
					check(true)
					if backend.PG {
						if emitted == nil {
							t.Fatal("actual native displacement Dump stream required")
						}
						mark("PG-replay/drop-source-and-execute-actual-dump")
						fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP SCHEMA regtype_displacement CASCADE", 0))
						if _, err := conn.ExecContext(p.Ctx, string(emitted)); err != nil {
							t.Fatal("actual displacement stream PostgreSQL replay", err)
						}
						fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_displacement,public", 0))
						check(false)
						return
					}
					mark("dump")
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = bytes.Clone(dump.Bytes())
					retainedCase := cases[0]
					mark("retained-cursor/" + retainedCase.base)
					retained, err := conn.QueryContext(p.Ctx, "SELECT r FROM "+fixture.RegtypeDisplacementQualified("w_"+retainedCase.base))
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					mark("same-reset")
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					mark("same-restore")
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
						t.Fatal(err)
					}
					check(false)
					mark("retained-rows/" + retainedCase.base)
					fixture.RegtypeAssertRows(t, retained, [][]any{{retainedCase.base + "[]"}}, false)
					// Complete the dependent source chain before independent target work.
					mark("failed-restore")
					bad := "CREATE SCHEMA regtype_displacement; CREATE DOMAIN regtype_displacement.late AS integer; CREATE TABLE regtype_displacement._late(v integer); CREATE TABLE regtype_displacement.invalid(v integer DEFAULT 'bad');"
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
						t.Fatal("invalid displacement Restore succeeded")
					}
					check(false)
				})
			})
			if !sourceOK {
				return
			}
			t.Run("fresh-target", func(t *testing.T) {
				if len(emitted) == 0 {
					t.Fatal("actual native displacement Dump stream required")
				}
				// Restore into a separately allocated engine exercises destination type
				// identity remapping rather than relying on source OID reuse.
				target := fixture.NewFixture(t, false, true)
				mark := regtypeDisplacementControlsPhase(t, target)
				mark("fresh-target/spacer-and-restore")
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE DOMAIN public.displacement_spacer AS integer", 0))
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				targetConn, err := target.Memory.Conn(target.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer targetConn.Close()
				fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Exec("SET search_path TO regtype_displacement,public", 0))
				mark("fresh-target/identity-checkpoint")
				identities := regtypeDisplacementIdentities(t, target, targetConn, cases)
				for i, c := range cases {
					mark("fresh-target/logical-check/" + c.base)
					regtypeDisplacementLogicalCheck(t, target, target.Backends()[0], targetConn, target.MemoryRecord, c, identities[i])
				}
			})
		})
	}
}

// Diagnostics are local to Controls; successful runs remain quiet.
func regtypeDisplacementControlsPhase(t *testing.T, p *fixture.Pair) func(string) {
	t.Helper()
	started := time.Now()
	phase := "setup"
	t.Cleanup(func() {
		if t.Failed() {
			deadline, _ := p.Ctx.Deadline()
			t.Logf("owner=%s routes=direct+prepared phase=%s elapsed=%s remaining=%s context=%v", t.Name(), phase, time.Since(started), time.Until(deadline), p.Ctx.Err())
		}
	})
	return func(next string) { phase = next }
}

// Kinds are independent, but each kind's four creation-order/DDL cases and
// source replacement chain share one setup-born context per backend.
func regtypeDisplacementControlsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		name := "memory"
		if postgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "regtype_displacement")
			p := fixture.NewFixture(t, postgres, true)
			mark := regtypeDisplacementControlsPhase(t, p)
			for _, b := range p.Backends() {
				if b.PG != postgres {
					continue
				}
				var notices []fixture.Diagnostic
				_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				for _, query := range []string{"CREATE SCHEMA regtype_displacement", "SET search_path TO regtype_displacement,public"} {
					mark("setup/" + query)
					fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(query, 0))
				}
				mark("source-body")
				body(t, p, b, conn, recorder)
			}
		})
	}
}

// An array-name change must reown live executable consumers, not merely make
// REGTYPE lookup succeed. Values and an already-open cursor keep their contents.
func TestReferenceRegtypeArrayDisplacementExecutable(t *testing.T) {
	fixture.RegtypeDisplacementScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, q := range []string{
			"CREATE DOMAIN regtype_displacement.payload AS integer",
			"CREATE DOMAIN regtype_displacement.payload_array AS regtype_displacement.payload[]",
			"CREATE TABLE regtype_displacement.payload_values(a regtype_displacement.payload[] DEFAULT '{1,NULL,2}',d regtype_displacement.payload_array DEFAULT '{3,4}')",
			"CREATE VIEW regtype_displacement.payload_view AS SELECT a,d FROM regtype_displacement.payload_values",
		} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_displacement.payload_values DEFAULT VALUES", 1))
		arrayID := fixture.RegtypeDisplacementOID(t, p, conn, "payload[]")
		check := func(count int, displaced bool) {
			family := "_PAYLOAD"
			if displaced {
				family = "__PAYLOAD"
			}
			if backend.PG {
				// pq has no built-in name/scan adapter for either user array.
				family = ""
			}
			want := make([][]any, count)
			for i := range want {
				want[i] = []any{"{1,NULL,2}", "{3,4}"}
			}
			for _, source := range []string{"payload_values", "payload_view"} {
				s := fixture.Select("SELECT a,d FROM regtype_displacement."+source, []string{"a", "d"}, []string{family, family}, want)
				s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
					t.Helper()
					fixture.RegtypeRaw(t, backend.PG, value)
					return true
				}
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					t.Helper()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, column := range columns {
						scan := reflect.TypeFor[string]()
						if backend.PG {
							scan = reflect.TypeFor[any]()
						}
						if column.ScanType() != scan {
							t.Fatalf("array ScanType=%v want %v", column.ScanType(), scan)
						}
						nullable, known := column.Nullable()
						if nullable != !backend.PG || known != !backend.PG {
							t.Fatalf("stored array nullable=%t/%t", nullable, known)
						}
						length, lengthOK := column.Length()
						precision, scale, decimalOK := column.DecimalSize()
						if length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
							t.Fatalf("array sizing=%d/%t decimal=%d,%d/%t", length, lengthOK, precision, scale, decimalOK)
						}
					}
				}
				fixture.RegtypeBoth(t, p, conn, recorder, s)
			}
		}
		check(1, false)
		var retained *sql.Rows
		if !backend.PG {
			var err error
			retained, err = backend.DB.QueryContext(p.Ctx, "SELECT a,d FROM regtype_displacement.payload_values")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_displacement._payload AS integer", 0))
		if got := fixture.RegtypeDisplacementOID(t, p, conn, "__payload"); got != arrayID {
			t.Fatalf("executable array identity changed: %d want %d", got, arrayID)
		}
		check(1, true)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_displacement.payload_values DEFAULT VALUES", 1))
		check(2, true)
		if retained != nil {
			columns, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, column := range columns {
				if column.DatabaseTypeName() != "_PAYLOAD" || column.ScanType() != reflect.TypeFor[string]() {
					t.Fatalf("retained array metadata type=%q scan=%v", column.DatabaseTypeName(), column.ScanType())
				}
			}
			if !retained.Next() {
				t.Fatal("retained executable array cursor empty", retained.Err())
			}
			var a, b string
			if err := retained.Scan(&a, &b); err != nil {
				t.Fatal(err)
			}
			if a != "{1,NULL,2}" || b != "{3,4}" || retained.Next() || retained.Err() != nil {
				t.Fatalf("retained array values %q/%q err=%v", a, b, retained.Err())
			}
		}
	})
}
