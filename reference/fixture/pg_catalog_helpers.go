package fixture

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func PGAttributeCheck(t *testing.T, p *Pair, b Backend, db Executor, s Step) {
	t.Helper()
	want := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		want[i] = append([]any(nil), row...)
		for j, v := range row {
			if b.PG && s.Types[j] == "OID" && v != nil {
				want[i][j] = strconv.FormatInt(v.(int64), 10)
			}
		}
	}
	s.Rows = want
	RunStep(t, p.Ctx, db, b.Recorder, s)
}

// Cast and raw NAME inquiries retain their independent result metadata.
func PGAttributeNameCastWitness(t *testing.T, p *Pair, b Backend, original Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		step := original
		step.Prepared = prepared
		RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
	raw := original
	raw.Query = strings.Replace(original.Query, "attname::text AS name", "attname AS name", 1)
	raw.Query = strings.Replace(raw.Query, "::regclass AND", "::regclass::oid AND", 1)
	raw.Types = append([]string(nil), original.Types...)
	raw.Types[0] = "NAME"
	RunStep(t, p.Ctx, b.DB, b.Recorder, raw)
	RunStep(t, p.Ctx, b.DB, b.Recorder, Prepared(raw))
}

func PGClassExec(t *testing.T, p *Pair, b Backend, db Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		RunStep(t, p.Ctx, db, b.Recorder, Exec(q, 0))
	}
}

func PGCastParameterError(query, typ, value string, args ...any) Step {
	portal := "unnamed portal parameter $1 = '...'"
	if value == "" {
		portal = "unnamed portal parameter $1 = ''"
	}
	s := ImplicitError(query, Diagnostic{Code: "22P02", Message: "invalid input syntax for type " + typ + ": \"" + value + "\"", Context: portal}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func PGDatabaseCheck(t *testing.T, p *Pair, b Backend, db Executor, s Step) {
	t.Helper()
	rows := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		rows[i] = append([]any(nil), row...)
		for j, v := range row {
			if b.PG && s.Types[j] == "OID" && v != nil {
				rows[i][j] = strconv.FormatInt(v.(int64), 10)
			}
		}
	}
	s.Rows = rows
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if v == nil {
			return false
		}
		switch s.Types[col] {
		case "XID":
			bytes, ok := v.([]byte)
			if !ok {
				t.Fatalf("XID raw: %T %#v", v, v)
			}
			if _, err := strconv.ParseUint(string(bytes), 10, 32); err != nil {
				t.Fatal(err)
			}
			return true
		case "OID":
			if b.PG {
				if _, ok := v.([]byte); !ok {
					t.Fatalf("pq OID raw: %T", v)
				}
			} else if _, ok := v.(int64); !ok {
				t.Fatalf("native OID raw: %T", v)
			}
		case "TEXT", "CHAR":
			if _, ok := v.(string); !ok {
				t.Fatalf("column %d %s raw: %T", col+1, s.Types[col], v)
			}
		case "NAME", "_ACLITEM":
			if b.PG {
				if _, ok := v.([]byte); !ok {
					t.Fatalf("pq column %d %s raw: %T", col+1, s.Types[col], v)
				}
			} else if _, ok := v.(string); !ok {
				t.Fatalf("native column %d %s raw: %T", col+1, s.Types[col], v)
			}
		}
		return false
	}
	RunStep(t, p.Ctx, db, b.Recorder, s)
}

func PGDescriptionRelationID(t *testing.T, p *Pair, backend Backend, name string) int64 {
	t.Helper()
	var oid int64
	if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", name).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
		t.Fatal("relation identity", name, oid, err)
	}
	return oid
}

// Identity comes from a separately owned relation, never the catalog under test.
func PGShDescriptionCreateRole(t *testing.T, p *Pair, backend Backend, name, schema string) int64 {
	t.Helper()
	for _, q := range []string{"CREATE ROLE " + name, "CREATE SEQUENCE " + schema + "." + name + "_identity", "ALTER SEQUENCE " + schema + "." + name + "_identity OWNER TO " + name} {
		RunStep(t, p.Ctx, backend.DB, backend.Recorder, Exec(q, 0))
	}
	var id int64
	if err := backend.DB.QueryRowContext(p.Ctx, "SELECT relowner FROM pg_catalog.pg_class WHERE relname=$1", name+"_identity").Scan(&id); err != nil || id <= 0 || id > math.MaxUint32 {
		t.Fatal("independent role identity", id, err)
	}
	RunStep(t, p.Ctx, backend.DB, backend.Recorder, Exec("DROP SEQUENCE "+schema+"."+name+"_identity", 0))
	return id
}
