package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
)

// Exact names were independently observed on pinned PostgreSQL 18.0 in tests-17.
// REL_18_0 pg_type.c makeArrayTypeName and moveArrayTypeName explain why
// displacement seeds from the occupied ARRAY name before the new element's array.
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/pg_type.c
func regtypeDisplacementScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn, *referenceRecorder)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, recorder := noticeReferencePool(t, p, backend.pg, &notices)
			t.Cleanup(func() {
				if _, err := backend.db.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS regtype_displacement CASCADE"); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"CREATE SCHEMA regtype_displacement", "SET search_path TO regtype_displacement,public"} {
				referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
			}
			body(t, p, backend, conn, recorder)
		})
	}
}

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

func regtypeDisplacementQualified(name string) string {
	if strings.HasSuffix(name, "[]") {
		return "regtype_displacement." + regtypeIdentifier(strings.TrimSuffix(name, "[]")) + "[]"
	}
	return "regtype_displacement." + regtypeIdentifier(name)
}

func regtypeDisplacementCreate(c regtypeDisplacementCase, name string) referenceStep {
	n := regtypeDisplacementQualified(name)
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
	s := referenceExec(q, count)
	s.prepared = c.prepared
	return s
}

func regtypeDisplacementOID(t *testing.T, p *referencePair, conn *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := conn.QueryRowContext(p.ctx, "SELECT $1::regtype::bigint", regtypeDisplacementQualified(name)).Scan(&oid); err != nil {
		t.Fatal(name, err)
	}
	if oid <= 0 {
		t.Fatalf("nonpositive identity for %q: %d", name, oid)
	}
	return oid
}

// pg_type is an oracle observation, not a requested native catalog projection.
// The shared assertions below prove the same association through public input,
// numeric identity, array spelling and symbolic output.
func regtypeDisplacementCatalog(t *testing.T, p *referencePair, conn *sql.Conn, base, array string) {
	t.Helper()
	var elem, associated, arrayID int64
	var name string
	q := "SELECT e.oid::bigint,e.typarray::bigint,a.oid::bigint,a.typname::text FROM pg_catalog.pg_type e JOIN pg_catalog.pg_type a ON a.typelem=e.oid WHERE e.oid=$1::regtype::oid AND a.oid=e.typarray"
	if err := conn.QueryRowContext(p.ctx, q, regtypeDisplacementQualified(base)).Scan(&elem, &associated, &arrayID, &name); err != nil {
		t.Fatal("reciprocal oracle association", base, err)
	}
	if name != array || associated != arrayID || elem == arrayID {
		t.Fatalf("array association for %q: element=%d associated=%d array=%d name=%q want=%q", base, elem, associated, arrayID, name, array)
	}
}

func regtypeDisplacementCheck(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, c regtypeDisplacementCase, baseID, arrayID int64) {
	t.Helper()
	namedID := regtypeDisplacementOID(t, p, conn, c.named)
	namedArrayID := regtypeDisplacementOID(t, p, conn, c.named+"[]")
	if baseID == namedID || arrayID == namedID || namedArrayID == arrayID || namedArrayID == namedID {
		t.Fatalf("distinct element/array identities collapsed: base=%d oldarray=%d named=%d namedarray=%d", baseID, arrayID, namedID, namedArrayID)
	}
	q := "SELECT ($1::bigint=" + regtypeLiteral(regtypeDisplacementQualified(c.base)) + "::regtype::bigint) AS a,($2::bigint=" + regtypeLiteral(regtypeDisplacementQualified(c.base+"[]")) + "::regtype::bigint) AS b,($2::bigint=" + regtypeLiteral(regtypeDisplacementQualified(c.baseArray)) + "::regtype::bigint) AS c,($3::bigint=" + regtypeLiteral(regtypeDisplacementQualified(c.namedArray)) + "::regtype::bigint) AS d"
	regtypeBoth(t, p, conn, recorder, referenceSelect(q, []string{"a", "b", "c", "d"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, baseID, arrayID, namedArrayID))
	q = "SELECT " + regtypeLiteral(regtypeDisplacementQualified(c.baseArray)) + "::regtype AS a," + regtypeLiteral(regtypeDisplacementQualified(c.named)) + "::regtype AS b," + regtypeLiteral(regtypeDisplacementQualified(c.namedArray)) + "::regtype AS c"
	regtypeBoth(t, p, conn, recorder, regtypeSelect(q, c.base+"[]", c.named, c.named+"[]"))
	if backend.pg {
		regtypeDisplacementCatalog(t, p, conn, c.base, c.baseArray)
		regtypeDisplacementCatalog(t, p, conn, c.named, c.namedArray)
	}
	if c.kind == "query_table" || c.kind == "view" {
		for _, n := range []string{c.base, c.named} {
			regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT v FROM "+regtypeDisplacementQualified(n), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
}

func regtypeDisplacementReferences(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, c regtypeDisplacementCase) {
	t.Helper()
	// Keep both a data-only reference and an early-bound stored definition to
	// the array that will be displaced. Neither may retarget to the new scalar.
	literal := regtypeLiteral(regtypeDisplacementQualified(c.base + "[]"))
	for _, q := range []string{
		"CREATE TABLE " + regtypeDisplacementQualified("w_"+c.base) + "(r regtype DEFAULT " + literal + "::regtype)",
		"CREATE VIEW " + regtypeDisplacementQualified("v_"+c.base) + " AS SELECT " + literal + "::regtype AS r",
	} {
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO "+regtypeDisplacementQualified("w_"+c.base)+" DEFAULT VALUES", 1))
}

func regtypeDisplacementMetadata(t *testing.T, rows *sql.Rows, postgres, stored bool, names, families []string) {
	t.Helper()
	regtypeFamilyMetadata(t, rows, postgres, families)
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

func regtypeDisplacementReferenceCheck(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, c regtypeDisplacementCase, arrayID int64, rows int) {
	t.Helper()
	for _, prefix := range []string{"w_", "v_"} {
		want := [][]any{{c.base + "[]", true}}
		if prefix == "w_" && rows == 2 {
			want = append(want, []any{c.base + "[]", true})
		}
		q := "SELECT r,(r::bigint=$1) AS stable FROM " + regtypeDisplacementQualified(prefix+c.base)
		s := referenceSelect(q, []string{"r", "stable"}, []string{"REGTYPE", "BOOL"}, want, arrayID)
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			// Both sources expose declared relation columns. bindViewOutput
			// publishes nullable view columns even for a constant projection;
			// reading r is not a new computed REGTYPE expression.
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			nullable, known := columns[0].Nullable()
			wantKnown := recorder.rawBackend != referenceRawPostgres
			if nullable != wantKnown || known != wantKnown {
				t.Fatalf("reference source %s nullable=%t/%t want %t/%t", prefix+c.base, nullable, known, wantKnown, wantKnown)
			}
		}
		regtypeBoth(t, p, conn, recorder, s)
	}
}

// Logical replay preserves named elements and their reciprocal arrays, not the
// historical generated aliases chosen by a different declaration order.
func regtypeDisplacementLogicalCheck(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, c regtypeDisplacementCase) {
	t.Helper()
	baseID := regtypeDisplacementOID(t, p, conn, c.base)
	arrayID := regtypeDisplacementOID(t, p, conn, c.base+"[]")
	namedID := regtypeDisplacementOID(t, p, conn, c.named)
	namedArrayID := regtypeDisplacementOID(t, p, conn, c.named+"[]")
	ids := []int64{baseID, arrayID, namedID, namedArrayID}
	for i, id := range ids {
		for _, other := range ids[:i] {
			if id == other {
				t.Fatalf("destination element/array identities collapsed: %v", ids)
			}
		}
	}
	q := "SELECT " + regtypeLiteral(regtypeDisplacementQualified(c.base)) + "::regtype AS a," + regtypeLiteral(regtypeDisplacementQualified(c.base+"[]")) + "::regtype AS b," + regtypeLiteral(regtypeDisplacementQualified(c.named)) + "::regtype AS c," + regtypeLiteral(regtypeDisplacementQualified(c.named+"[]")) + "::regtype AS d"
	regtypeBoth(t, p, conn, recorder, regtypeSelect(q, c.base, c.base+"[]", c.named, c.named+"[]"))
	if backend.pg {
		for _, element := range []string{c.base, c.named} {
			q := "SELECT (a.typelem=e.oid) AS reciprocal,(a.oid=" + regtypeLiteral(regtypeDisplacementQualified(element+"[]")) + "::regtype::oid) AS associated FROM pg_catalog.pg_type e JOIN pg_catalog.pg_type a ON a.oid=e.typarray WHERE e.oid=" + regtypeLiteral(regtypeDisplacementQualified(element)) + "::regtype::oid"
			regtypeBoth(t, p, conn, recorder, referenceSelect(q, []string{"reciprocal", "associated"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		}
	}
	if c.kind == "query_table" || c.kind == "view" {
		for _, name := range []string{c.base, c.named} {
			regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT v FROM "+regtypeDisplacementQualified(name), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	regtypeDisplacementReferenceCheck(t, p, conn, recorder, c, arrayID, 1)
}

func TestReferenceRegtypeArrayDisplacement(t *testing.T) {
	for _, c := range regtypeDisplacementCases() {
		t.Run(c.base, func(t *testing.T) {
			regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
				first, second := c.base, c.named
				if !c.arrayFirst {
					first, second = second, first
				}
				referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(c, first))
				firstID := regtypeDisplacementOID(t, p, conn, first)
				firstArrayID := regtypeDisplacementOID(t, p, conn, first+"[]")
				var retained *sql.Rows
				if !backend.pg {
					var err error
					retained, err = conn.QueryContext(p.ctx, "SELECT $1::bigint::regtype AS a,$2::bigint::regtype AS b", firstID, firstArrayID)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
				}
				if c.arrayFirst {
					regtypeDisplacementReferences(t, p, conn, recorder, c)
				}
				referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(c, second))
				baseID, arrayID := firstID, firstArrayID
				if !c.arrayFirst {
					baseID = regtypeDisplacementOID(t, p, conn, c.base)
					arrayID = regtypeDisplacementOID(t, p, conn, c.base+"[]")
					regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT ($1::bigint="+regtypeLiteral(regtypeDisplacementQualified(c.named))+"::regtype::bigint) AS a,($2::bigint="+regtypeLiteral(regtypeDisplacementQualified(c.namedArray))+"::regtype::bigint) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, firstID, firstArrayID))
				}
				regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)
				if c.arrayFirst {
					referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO "+regtypeDisplacementQualified("w_"+c.base)+" DEFAULT VALUES", 1))
					regtypeDisplacementReferenceCheck(t, p, conn, recorder, c, arrayID, 2)
				}
				if retained != nil {
					regtypeAssertRowsWithMetadata(t, retained, [][]any{{first, first + "[]"}}, false, func(t *testing.T, rows *sql.Rows) {
						regtypeDisplacementMetadata(t, rows, false, false, []string{"a", "b"}, []string{"REGTYPE", "REGTYPE"})
					})
				}
			})
		})
	}
}

func TestReferenceRegtypeArrayDisplacementCollisions(t *testing.T) {
	regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
		// The desired displacement destination already belongs to a named type.
		// Its identity remains unchanged; both generated names choose suffixes.
		c := regtypeDisplacementCase{kind: "domain", base: "occupied", named: "_occupied", baseArray: "__occupied_1", namedArray: "__occupied_2", arrayFirst: true}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TYPE regtype_displacement.__occupied AS ENUM ('ok')", 0))
		occupied := regtypeDisplacementOID(t, p, conn, "__occupied")
		referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(c, c.base))
		baseID, arrayID := regtypeDisplacementOID(t, p, conn, c.base), regtypeDisplacementOID(t, p, conn, c.base+"[]")
		referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(c, c.named))
		regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)
		if got := regtypeDisplacementOID(t, p, conn, "__occupied"); got != occupied {
			t.Fatalf("named collision displaced: %d want %d", got, occupied)
		}
		// A nested displacement may move the first displaced array again.
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_displacement.__occupied_1 AS integer", 0))
		c.baseArray = "___occupied_1"
		regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)

		// Row identity and its array are reserved before self-referential defaults.
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE regtype_displacement.self(v integer)", 0))
		selfArray := regtypeDisplacementOID(t, p, conn, "self[]")
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE regtype_displacement._self(r regtype DEFAULT 'regtype_displacement._self'::regtype,a regtype DEFAULT 'regtype_displacement._self[]'::regtype)", 0))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_displacement._self DEFAULT VALUES", 1))
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT r,a,(r::oid='regtype_displacement._self'::regtype::oid) AS own_row,(a::oid='regtype_displacement._self[]'::regtype::oid) AS own_array,($1::bigint='regtype_displacement.__self'::regtype::bigint) AS old_array FROM regtype_displacement._self", []string{"r", "a", "own_row", "own_array", "old_array"}, []string{"REGTYPE", "REGTYPE", "BOOL", "BOOL", "BOOL"}, [][]any{{"_self", "_self[]", true, true, true}}, selfArray))

		// At the 63-byte boundary prefixing must clip, then suffix after clipping.
		long := strings.Repeat("x", 63)
		clipped := regtypeDisplacementCase{kind: "domain", base: long, named: "_" + long[:62], baseArray: "__" + long[:61], namedArray: "__" + long[:59] + "_1", arrayFirst: true}
		referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(clipped, clipped.base))
		longID, longArray := regtypeDisplacementOID(t, p, conn, long), regtypeDisplacementOID(t, p, conn, long+"[]")
		referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(clipped, clipped.named))
		regtypeDisplacementCheck(t, p, backend, conn, recorder, clipped, longID, longArray)

		// UTF8 clipping must stop before a complete code point, not byte63.
		unicodeBase := strings.Repeat("é", 31) + "z"
		unicodeNamed := "_" + strings.Repeat("é", 31)
		unicodeMoved := "__" + strings.Repeat("é", 30)
		unicodeNewArray := "__" + strings.Repeat("é", 29) + "_1"
		u := regtypeDisplacementCase{kind: "domain"}
		referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(u, unicodeBase))
		unicodeArrayID := regtypeDisplacementOID(t, p, conn, unicodeBase+"[]")
		referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(u, unicodeNamed))
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT ($1::bigint=$2::text::regtype::bigint) AS a,($1::bigint=$3::text::regtype::bigint) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, unicodeArrayID, regtypeDisplacementQualified(unicodeMoved), regtypeDisplacementQualified(unicodeBase)+"[]"))
		s := regtypeSelect("SELECT $1::text::regtype AS a,$2::text::regtype AS b", regtypeIdentifier(unicodeBase)+"[]", regtypeIdentifier(unicodeNamed)+"[]")
		s.args = []any{regtypeDisplacementQualified(unicodeMoved), regtypeDisplacementQualified(unicodeNewArray)}
		regtypeBoth(t, p, conn, recorder, s)
		if backend.pg {
			regtypeDisplacementCatalog(t, p, conn, unicodeBase, unicodeMoved)
			regtypeDisplacementCatalog(t, p, conn, unicodeNamed, unicodeNewArray)
		}
	})
}

func TestReferenceRegtypeArrayDisplacementAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
				for _, q := range []string{"CREATE DOMAIN regtype_displacement.late AS integer", "CREATE TABLE regtype_displacement.sentinel(v integer)", "INSERT INTO regtype_displacement.sentinel VALUES(42)"} {
					count := int64(0)
					if strings.HasPrefix(q, "INSERT") {
						count = 1
					}
					referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, count))
				}
				arrayID := regtypeDisplacementOID(t, p, conn, "_late")
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
							stmt, err = conn.PrepareContext(p.ctx, f.query)
							if err != nil {
								stage = "prepare"
								if stmt != nil {
									stmt.Close()
									t.Error("failed Prepare exposed a statement")
								}
							} else {
								result, err = stmt.ExecContext(p.ctx)
								if closeErr := stmt.Close(); closeErr != nil {
									t.Fatal(closeErr)
								}
							}
						} else {
							result, err = conn.ExecContext(p.ctx, f.query)
						}
						if err == nil {
							t.Fatal("invalid later declaration succeeded")
						}
						// tests-17 observed PostgreSQL utility Execute. Native view
						// source analysis retains its established eager Prepare
						// contract (docs/QUERIES.md); all other creators execute.
						wantStage := "execute"
						if prepared && !backend.pg && f.code == "42703" {
							wantStage = "prepare"
						}
						if stage != wantStage {
							t.Errorf("failure stage got %q want %q", stage, wantStage)
						}
						if result != nil {
							t.Error("failed declaration exposed a result")
						}
						want := controlsReferenceDiagnostic(f.code, f.message)
						if f.token != "" {
							want.Position = strconv.Itoa(strings.Index(f.query, f.token) + 1)
						}
						if f.code == "23505" {
							// Only the newly allocated enum OID varies. Validate its
							// unsigned decimal grammar and every other detail byte.
							got := referenceExtractDiagnostic(t, err)
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
						regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT ($1::bigint='regtype_displacement._late'::regtype::bigint) AS a,($1::bigint='regtype_displacement.late[]'::regtype::bigint) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, arrayID))
						regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT v FROM regtype_displacement.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
						if backend.pg {
							regtypeDisplacementCatalog(t, p, conn, "late", "_late")
						}
						// No renamed or new-array identity may escape a failed stage.
						for _, name := range []string{"__late", "__late_1"} {
							_, lookupErr := conn.ExecContext(p.ctx, "SELECT $1::regtype", regtypeDisplacementQualified(name))
							referenceState(t, lookupErr, "42704")
						}
						referenceAssertDiagnostic(t, err, &want)
					})
				}
				// Isolate every genuine conflict so an expectation mismatch for
				// one creator cannot suppress coverage of the remaining creators.
				// Keep the independent conflict diagnostics unchanged: tests-17
				// does not print their full records in the quiet success path.
				t.Run("genuine_conflict_domain", func(t *testing.T) {
					q := "CREATE DOMAIN regtype_displacement.late AS missing_displacement_base"
					s := referenceImplicitError(q, referenceDiagnostic{Code: "42710", Message: `type "late" already exists`})
					s.prepared = prepared
					referenceRunStep(t, p.ctx, conn, recorder, s)
					if got := regtypeDisplacementOID(t, p, conn, "_late"); got != arrayID {
						t.Fatalf("genuine conflict changed associated array: %d want %d", got, arrayID)
					}
				})
				for _, kind := range []string{"table", "query_table", "enum", "view"} {
					t.Run("genuine_conflict_"+kind, func(t *testing.T) {
						c := regtypeDisplacementCase{kind: kind, prepared: prepared}
						q := regtypeDisplacementCreate(c, "late").query
						want := referenceDiagnostic{Code: "42710", Message: `type "late" already exists`}
						if kind != "enum" {
							want.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
						}
						s := referenceImplicitError(q, want)
						s.prepared = prepared
						referenceRunStep(t, p.ctx, conn, recorder, s)
						if got := regtypeDisplacementOID(t, p, conn, "_late"); got != arrayID {
							t.Fatalf("genuine conflict changed associated array: %d want %d", got, arrayID)
						}
					})
				}
			})
		})
	}
}

func TestReferenceRegtypeArrayDisplacementControls(t *testing.T) {
	var emitted []byte
	regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
		cases := regtypeDisplacementCases()
		for _, c := range cases {
			first, second := c.base, c.named
			if !c.arrayFirst {
				first, second = second, first
			}
			referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(c, first))
			if c.arrayFirst {
				regtypeDisplacementReferences(t, p, conn, recorder, c)
			}
			referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(c, second))
			if !c.arrayFirst {
				regtypeDisplacementReferences(t, p, conn, recorder, c)
			}
		}
		check := func(source bool) {
			for _, c := range cases {
				if source {
					baseID := regtypeDisplacementOID(t, p, conn, c.base)
					arrayID := regtypeDisplacementOID(t, p, conn, c.base+"[]")
					regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)
					regtypeDisplacementReferenceCheck(t, p, conn, recorder, c, arrayID, 1)
				} else {
					regtypeDisplacementLogicalCheck(t, p, backend, conn, recorder, c)
				}
			}
		}
		check(true)
		if backend.pg {
			if emitted == nil {
				t.Fatal("actual native displacement Dump stream required")
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP SCHEMA regtype_displacement CASCADE", 0))
			if _, err := conn.ExecContext(p.ctx, string(emitted)); err != nil {
				t.Fatal("actual displacement stream PostgreSQL replay", err)
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_displacement,public", 0))
			check(false)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		retained, err := conn.QueryContext(p.ctx, "SELECT r FROM regtype_displacement.w_d_domain_true_false")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(false)
		regtypeAssertRows(t, retained, [][]any{{"d_domain_true_false[]"}}, false)
		// Restore into a separately allocated engine exercises destination type
		// identity remapping rather than relying on source OID reuse.
		target := newReferenceFixture(t, false, true)
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("CREATE DOMAIN public.displacement_spacer AS integer", 0))
		if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		targetConn, err := target.memory.Conn(target.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceExec("SET search_path TO regtype_displacement,public", 0))
		for _, c := range cases {
			regtypeDisplacementLogicalCheck(t, target, target.backends()[0], targetConn, target.memoryRecord, c)
		}
		// A late declaration after tentative displacement cannot replace the
		// destination generation, its original array names, or its saved data.
		bad := "CREATE SCHEMA regtype_displacement; CREATE DOMAIN regtype_displacement.late AS integer; CREATE TABLE regtype_displacement._late(v integer); CREATE TABLE regtype_displacement.invalid(v integer DEFAULT 'bad');"
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid displacement Restore succeeded")
		}
		check(false)
	})
}

// An array-name change must reown live executable consumers, not merely make
// REGTYPE lookup succeed. Values and an already-open cursor keep their contents.
func TestReferenceRegtypeArrayDisplacementExecutable(t *testing.T) {
	regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
		for _, q := range []string{
			"CREATE DOMAIN regtype_displacement.payload AS integer",
			"CREATE DOMAIN regtype_displacement.payload_array AS regtype_displacement.payload[]",
			"CREATE TABLE regtype_displacement.payload_values(a regtype_displacement.payload[] DEFAULT '{1,NULL,2}',d regtype_displacement.payload_array DEFAULT '{3,4}')",
			"CREATE VIEW regtype_displacement.payload_view AS SELECT a,d FROM regtype_displacement.payload_values",
		} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_displacement.payload_values DEFAULT VALUES", 1))
		arrayID := regtypeDisplacementOID(t, p, conn, "payload[]")
		check := func(count int, displaced bool) {
			family := "_PAYLOAD"
			if displaced {
				family = "__PAYLOAD"
			}
			if backend.pg {
				// pq has no built-in name/scan adapter for either user array.
				family = ""
			}
			want := make([][]any, count)
			for i := range want {
				want[i] = []any{"{1,NULL,2}", "{3,4}"}
			}
			for _, source := range []string{"payload_values", "payload_view"} {
				s := referenceSelect("SELECT a,d FROM regtype_displacement."+source, []string{"a", "d"}, []string{family, family}, want)
				s.checkRaw = func(t *testing.T, column, row int, value any) bool {
					t.Helper()
					regtypeRaw(t, backend.pg, value)
					return true
				}
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
					t.Helper()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, column := range columns {
						scan := reflect.TypeFor[string]()
						if backend.pg {
							scan = reflect.TypeFor[any]()
						}
						if column.ScanType() != scan {
							t.Fatalf("array ScanType=%v want %v", column.ScanType(), scan)
						}
						nullable, known := column.Nullable()
						if nullable != !backend.pg || known != !backend.pg {
							t.Fatalf("stored array nullable=%t/%t", nullable, known)
						}
						length, lengthOK := column.Length()
						precision, scale, decimalOK := column.DecimalSize()
						if length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
							t.Fatalf("array sizing=%d/%t decimal=%d,%d/%t", length, lengthOK, precision, scale, decimalOK)
						}
					}
				}
				regtypeBoth(t, p, conn, recorder, s)
			}
		}
		check(1, false)
		var retained *sql.Rows
		if !backend.pg {
			var err error
			retained, err = backend.db.QueryContext(p.ctx, "SELECT a,d FROM regtype_displacement.payload_values")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_displacement._payload AS integer", 0))
		if got := regtypeDisplacementOID(t, p, conn, "__payload"); got != arrayID {
			t.Fatalf("executable array identity changed: %d want %d", got, arrayID)
		}
		check(1, true)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_displacement.payload_values DEFAULT VALUES", 1))
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

func TestReferenceRegtypeArrayDisplacementMixedReplay(t *testing.T) {
	var emitted []byte
	regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
		var cases []regtypeDisplacementCase
		var dataRelations []string
		kinds := []string{"table", "query_table", "domain", "enum", "view"}
		for _, baseKind := range kinds {
			for _, namedKind := range kinds {
				if baseKind == namedKind {
					continue
				}
				for _, first := range []bool{true, false} {
					base := fmt.Sprintf("mixed_%s_%s_%t", baseKind, namedKind, first)
					c := regtypeDisplacementCase{base: base, named: "_" + base, arrayFirst: first}
					baseCreator, namedCreator := c, c
					baseCreator.kind, namedCreator.kind = baseKind, namedKind
					for _, creator := range []regtypeDisplacementCase{baseCreator, namedCreator} {
						if creator.kind == "query_table" || creator.kind == "view" {
							name := c.base
							if creator.kind == namedKind {
								name = c.named
							}
							dataRelations = append(dataRelations, name)
						}
					}
					if first {
						c.baseArray, c.namedArray = "__"+base, "__"+base+"_1"
						referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(baseCreator, c.base))
						regtypeDisplacementReferences(t, p, conn, recorder, c)
						referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(namedCreator, c.named))
					} else {
						c.baseArray, c.namedArray = "_"+base+"_1", "__"+base
						referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(namedCreator, c.named))
						referenceRunStep(t, p.ctx, conn, recorder, regtypeDisplacementCreate(baseCreator, c.base))
						regtypeDisplacementReferences(t, p, conn, recorder, c)
					}
					namedReference := c
					namedReference.base = c.named
					regtypeDisplacementReferences(t, p, conn, recorder, namedReference)
					cases = append(cases, c)
				}
			}
		}
		check := func(source bool) {
			for _, c := range cases {
				if source {
					baseID := regtypeDisplacementOID(t, p, conn, c.base)
					arrayID := regtypeDisplacementOID(t, p, conn, c.base+"[]")
					regtypeDisplacementCheck(t, p, backend, conn, recorder, c, baseID, arrayID)
					regtypeDisplacementReferenceCheck(t, p, conn, recorder, c, arrayID, 1)
				} else {
					regtypeDisplacementLogicalCheck(t, p, backend, conn, recorder, c)
				}
				namedReference := c
				namedReference.base = c.named
				regtypeDisplacementReferenceCheck(t, p, conn, recorder, namedReference, regtypeDisplacementOID(t, p, conn, c.named+"[]"), 1)
			}
			for _, name := range dataRelations {
				regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT v FROM "+regtypeDisplacementQualified(name), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		}
		check(true)
		if backend.pg {
			if len(emitted) == 0 {
				t.Fatal("actual native mixed-creator Dump required")
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP SCHEMA regtype_displacement CASCADE", 0))
			if _, err := conn.ExecContext(p.ctx, string(emitted)); err != nil {
				t.Fatal("actual mixed-creator PostgreSQL replay", err)
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_displacement,public", 0))
			check(false)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal("mixed-creator native Dump", err)
		}
		emitted = bytes.Clone(dump.Bytes())
		retained, err := conn.QueryContext(p.ctx, "SELECT r FROM "+regtypeDisplacementQualified("v_"+cases[0].base))
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("mixed-creator native Restore", err)
		}
		check(false)
		regtypeAssertRows(t, retained, [][]any{{cases[0].base + "[]"}}, false)
		regtypeDisplacementForeignReplay(t, emitted, cases, dataRelations)
	})
}
