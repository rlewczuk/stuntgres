package fixture

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

var PgDependColumns = []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}
var PgDependTypes = []string{"OID", "OID", "INT4", "OID", "OID", "INT4", "CHAR"}

func PgDependBackends(t *testing.T, p *Pair, body func(*testing.T, Backend)) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, backend) })
	}
}

func PgDependExec(t *testing.T, p *Pair, b Backend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		RunStep(t, p.Ctx, b.DB, b.Recorder, Exec(q, 0))
	}
}

func PgDependID(t *testing.T, b Backend, q string, args ...any) int64 {
	t.Helper()
	return DeparseSourceDefinitionID(t, b.DB, q, args...)
}
func PgDependRelation(t *testing.T, b Backend, name string) int64 {
	t.Helper()
	// Fixture inputs are simple known identifiers, not arbitrary SQL names.
	// Require a unique basename across this scenario's live schemas.
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:]
	}
	rows, err := b.DB.Query("SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id int64
	if !rows.Next() {
		t.Fatal("missing relation identity", name)
	}
	if err := rows.Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		t.Fatal(id, err)
	}
	if rows.Next() {
		t.Fatal("ambiguous relation identity", name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return id
}
func PgDependNamespace(t *testing.T, b Backend, relation int64) int64 {
	return PgDependID(t, b, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", relation)
}
func PgDependConstraint(t *testing.T, b Backend, table int64, name string) int64 {
	return PgDependID(t, b, "SELECT oid FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname=$2", table, name)
}
func PgDependDefinition(t *testing.T, b Backend, table, attribute int64) int64 {
	return PgDependID(t, b, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=$2", table, attribute)
}
func PgDependRule(t *testing.T, b Backend, view int64) int64 {
	return PgDependID(t, b, "SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class=$1 AND rulename='_RETURN'", view)
}
func PgDependEdge(class, object, sub, refclass, ref, refsub int64, kind string) []any {
	return []any{class, object, sub, refclass, ref, refsub, kind}
}
func PgDependSelect(q string, rows [][]any, args ...any) Step {
	s := Select(q, PgDependColumns, PgDependTypes, rows, args...)
	s.Bag = true
	return s
}
func PgDependBag(t *testing.T, p *Pair, b Backend, class, object int64, want ...[]any) {
	t.Helper()
	s := PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=$1 AND objid=$2", want, class, object)
	if want == nil {
		s.Rows = [][]any{}
	}
	PgDescriptionExecutorRows(t, p, b, b.DB, s)
	PgDescriptionExecutorRows(t, p, b, b.DB, Prepared(s))
}
func PgDependError(t *testing.T, p *Pair, b Backend, q string, want Diagnostic) {
	t.Helper()
	RunStep(t, p.Ctx, b.DB, b.Recorder, ErrorDiagnostic(q, want))
}
func PgDependRestrict(object, detail string) Diagnostic {
	return Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

// Expectations are handwritten construction edges; only generated identities
// come from existing public owner catalogs, never from the collector under test.
func PgDependCore(t *testing.T, p *Pair, b Backend, schema string) {
	t.Helper()
	PgDependExec(t, p, b, "CREATE SCHEMA "+schema, "SET search_path TO "+schema+",public",
		"CREATE TABLE t(prefix INT,a INT CONSTRAINT ck CHECK(a>0),b INT DEFAULT 7,g INT GENERATED ALWAYS AS(a+1) STORED,u INT CONSTRAINT uq UNIQUE,serialcol SERIAL,id INT GENERATED ALWAYS AS IDENTITY,nn INT CONSTRAINT nn_required NOT NULL,CONSTRAINT truth CHECK(TRUE))",
		"CREATE INDEX ordinary ON t(a) INCLUDE(b)", "CREATE INDEX expression ON t((a+1))", "CREATE VIEW v AS SELECT a FROM t")
	PgDependCheckCore(t, p, b, schema)
}
func PgDependCheckCore(t *testing.T, p *Pair, b Backend, schema string) {
	t.Helper()
	table := PgDependRelation(t, b, schema+".t")
	ns := PgDependNamespace(t, b, table)
	PgDependBag(t, p, b, 1259, table, PgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
	for _, tc := range []struct {
		name string
		sub  int64
	}{{"ck", 2}, {"nn_required", 8}, {"t_serialcol_not_null", 6}, {"t_id_not_null", 7}} {
		id := PgDependConstraint(t, b, table, tc.name)
		want := [][]any{PgDependEdge(2606, id, 0, 1259, table, tc.sub, "a")}
		if tc.name == "ck" {
			want = append(want, PgDependEdge(2606, id, 0, 1259, table, 2, "n"))
		}
		PgDependBag(t, p, b, 2606, id, want...)
	}
	truth := PgDependConstraint(t, b, table, "truth")
	PgDependBag(t, p, b, 2606, truth, PgDependEdge(2606, truth, 0, 1259, table, 0, "a"))
	uq := PgDependConstraint(t, b, table, "uq")
	idx := PgDependRelation(t, b, schema+".uq")
	PgDependBag(t, p, b, 2606, uq, PgDependEdge(2606, uq, 0, 1259, table, 5, "a"))
	PgDependBag(t, p, b, 1259, idx, PgDependEdge(1259, idx, 0, 2606, uq, 0, "i"))
	for _, tc := range []struct {
		name string
		subs []int64
	}{{"ordinary", []int64{2, 3}}, {"expression", []int64{0, 2}}} {
		id := PgDependRelation(t, b, schema+"."+tc.name)
		want := [][]any{}
		for _, sub := range tc.subs {
			want = append(want, PgDependEdge(1259, id, 0, 1259, table, sub, "a"))
		}
		PgDependBag(t, p, b, 1259, id, want...)
	}
	d := PgDependDefinition(t, b, table, 3)
	PgDependBag(t, p, b, 2604, d, PgDependEdge(2604, d, 0, 1259, table, 3, "a"))
	g := PgDependDefinition(t, b, table, 4)
	PgDependBag(t, p, b, 2604, g, PgDependEdge(2604, g, 0, 1259, table, 4, "i"), PgDependEdge(2604, g, 0, 1259, table, 2, "n"))
	serial := PgDependRelation(t, b, schema+".t_serialcol_seq")
	identity := PgDependRelation(t, b, schema+".t_id_seq")
	for _, tc := range []struct {
		id, sub int64
		kind    string
	}{{serial, 6, "a"}, {identity, 7, "i"}} {
		PgDependBag(t, p, b, 1259, tc.id, PgDependEdge(1259, tc.id, 0, 2615, ns, 0, "n"), PgDependEdge(1259, tc.id, 0, 1259, table, tc.sub, tc.kind))
	}
	d = PgDependDefinition(t, b, table, 6)
	PgDependBag(t, p, b, 2604, d, PgDependEdge(2604, d, 0, 1259, table, 6, "a"), PgDependEdge(2604, d, 0, 1259, serial, 0, "n"))
	v := PgDependRelation(t, b, schema+".v")
	rule := PgDependRule(t, b, v)
	PgDependBag(t, p, b, 1259, v, PgDependEdge(1259, v, 0, 2615, ns, 0, "n"))
	PgDependBag(t, p, b, 2618, rule, PgDependEdge(2618, rule, 0, 1259, v, 0, "i"), PgDependEdge(2618, rule, 0, 1259, table, 2, "n"))
	PgDescriptionExecutorRows(t, p, b, b.DB, Select(fmt.Sprintf("SELECT count(*) FROM pg_catalog.pg_attrdef WHERE adrelid=%d AND adnum=7", table), []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func PgDependRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	got := [][]any{}
	for rows.Next() {
		var class, object, sub, rc, ref, rs int64
		var kind string
		if err := rows.Scan(&class, &object, &sub, &rc, &ref, &rs, &kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, PgDependEdge(class, object, sub, rc, ref, rs, kind))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := CompareRows(got, want, true); !ok {
		t.Fatalf("retained dependency bag got %#v want %#v", got, want)
	}
}
