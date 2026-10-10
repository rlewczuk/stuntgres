package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

var pgDependColumns = []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}
var pgDependTypes = []string{"OID", "OID", "INT4", "OID", "OID", "INT4", "CHAR"}

func pgDependBackends(t *testing.T, p *referencePair, body func(*testing.T, referenceBackend)) {
	t.Helper()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, backend) })
	}
}

func pgDependExec(t *testing.T, p *referencePair, b referenceBackend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(q, 0))
	}
}

func pgDependID(t *testing.T, b referenceBackend, q string, args ...any) int64 {
	t.Helper()
	return deparseSourceDefinitionID(t, b.db, q, args...)
}
func pgDependRelation(t *testing.T, b referenceBackend, name string) int64 {
	t.Helper()
	// Fixture inputs are simple known identifiers, not arbitrary SQL names.
	// Require a unique basename across this scenario's live schemas.
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		name = name[dot+1:]
	}
	rows, err := b.db.Query("SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", name)
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
func pgDependNamespace(t *testing.T, b referenceBackend, relation int64) int64 {
	return pgDependID(t, b, "SELECT relnamespace FROM pg_catalog.pg_class WHERE oid=$1", relation)
}
func pgDependConstraint(t *testing.T, b referenceBackend, table int64, name string) int64 {
	return pgDependID(t, b, "SELECT oid FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname=$2", table, name)
}
func pgDependDefinition(t *testing.T, b referenceBackend, table, attribute int64) int64 {
	return pgDependID(t, b, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=$2", table, attribute)
}
func pgDependRule(t *testing.T, b referenceBackend, view int64) int64 {
	return pgDependID(t, b, "SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class=$1 AND rulename='_RETURN'", view)
}
func pgDependEdge(class, object, sub, refclass, ref, refsub int64, kind string) []any {
	return []any{class, object, sub, refclass, ref, refsub, kind}
}
func pgDependSelect(q string, rows [][]any, args ...any) referenceStep {
	s := referenceSelect(q, pgDependColumns, pgDependTypes, rows, args...)
	s.bag = true
	return s
}
func pgDependBag(t *testing.T, p *referencePair, b referenceBackend, class, object int64, want ...[]any) {
	t.Helper()
	s := pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=$1 AND objid=$2", want, class, object)
	if want == nil {
		s.rows = [][]any{}
	}
	pgDescriptionExecutorRows(t, p, b, b.db, s)
	pgDescriptionExecutorRows(t, p, b, b.db, referencePrepared(s))
}
func pgDependError(t *testing.T, p *referencePair, b referenceBackend, q string, want referenceDiagnostic) {
	t.Helper()
	referenceRunStep(t, p.ctx, b.db, b.recorder, referenceErrorDiagnostic(q, want))
}
func pgDependRestrict(object, detail string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

// Expectations are handwritten construction edges; only generated identities
// come from existing public owner catalogs, never from the collector under test.
func pgDependCore(t *testing.T, p *referencePair, b referenceBackend, schema string) {
	t.Helper()
	pgDependExec(t, p, b, "CREATE SCHEMA "+schema, "SET search_path TO "+schema+",public",
		"CREATE TABLE t(prefix INT,a INT CONSTRAINT ck CHECK(a>0),b INT DEFAULT 7,g INT GENERATED ALWAYS AS(a+1) STORED,u INT CONSTRAINT uq UNIQUE,serialcol SERIAL,id INT GENERATED ALWAYS AS IDENTITY,nn INT CONSTRAINT nn_required NOT NULL,CONSTRAINT truth CHECK(TRUE))",
		"CREATE INDEX ordinary ON t(a) INCLUDE(b)", "CREATE INDEX expression ON t((a+1))", "CREATE VIEW v AS SELECT a FROM t")
	pgDependCheckCore(t, p, b, schema)
}
func pgDependCheckCore(t *testing.T, p *referencePair, b referenceBackend, schema string) {
	t.Helper()
	table := pgDependRelation(t, b, schema+".t")
	ns := pgDependNamespace(t, b, table)
	pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
	for _, tc := range []struct {
		name string
		sub  int64
	}{{"ck", 2}, {"nn_required", 8}, {"t_serialcol_not_null", 6}, {"t_id_not_null", 7}} {
		id := pgDependConstraint(t, b, table, tc.name)
		want := [][]any{pgDependEdge(2606, id, 0, 1259, table, tc.sub, "a")}
		if tc.name == "ck" {
			want = append(want, pgDependEdge(2606, id, 0, 1259, table, 2, "n"))
		}
		pgDependBag(t, p, b, 2606, id, want...)
	}
	truth := pgDependConstraint(t, b, table, "truth")
	pgDependBag(t, p, b, 2606, truth, pgDependEdge(2606, truth, 0, 1259, table, 0, "a"))
	uq := pgDependConstraint(t, b, table, "uq")
	idx := pgDependRelation(t, b, schema+".uq")
	pgDependBag(t, p, b, 2606, uq, pgDependEdge(2606, uq, 0, 1259, table, 5, "a"))
	pgDependBag(t, p, b, 1259, idx, pgDependEdge(1259, idx, 0, 2606, uq, 0, "i"))
	for _, tc := range []struct {
		name string
		subs []int64
	}{{"ordinary", []int64{2, 3}}, {"expression", []int64{0, 2}}} {
		id := pgDependRelation(t, b, schema+"."+tc.name)
		want := [][]any{}
		for _, sub := range tc.subs {
			want = append(want, pgDependEdge(1259, id, 0, 1259, table, sub, "a"))
		}
		pgDependBag(t, p, b, 1259, id, want...)
	}
	d := pgDependDefinition(t, b, table, 3)
	pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 3, "a"))
	g := pgDependDefinition(t, b, table, 4)
	pgDependBag(t, p, b, 2604, g, pgDependEdge(2604, g, 0, 1259, table, 4, "i"), pgDependEdge(2604, g, 0, 1259, table, 2, "n"))
	serial := pgDependRelation(t, b, schema+".t_serialcol_seq")
	identity := pgDependRelation(t, b, schema+".t_id_seq")
	for _, tc := range []struct {
		id, sub int64
		kind    string
	}{{serial, 6, "a"}, {identity, 7, "i"}} {
		pgDependBag(t, p, b, 1259, tc.id, pgDependEdge(1259, tc.id, 0, 2615, ns, 0, "n"), pgDependEdge(1259, tc.id, 0, 1259, table, tc.sub, tc.kind))
	}
	d = pgDependDefinition(t, b, table, 6)
	pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 6, "a"), pgDependEdge(2604, d, 0, 1259, serial, 0, "n"))
	v := pgDependRelation(t, b, schema+".v")
	rule := pgDependRule(t, b, v)
	pgDependBag(t, p, b, 1259, v, pgDependEdge(1259, v, 0, 2615, ns, 0, "n"))
	pgDependBag(t, p, b, 2618, rule, pgDependEdge(2618, rule, 0, 1259, v, 0, "i"), pgDependEdge(2618, rule, 0, 1259, table, 2, "n"))
	pgDescriptionExecutorRows(t, p, b, b.db, referenceSelect(fmt.Sprintf("SELECT count(*) FROM pg_catalog.pg_attrdef WHERE adrelid=%d AND adnum=7", table), []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func pgDependRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	got := [][]any{}
	for rows.Next() {
		var class, object, sub, rc, ref, rs int64
		var kind string
		if err := rows.Scan(&class, &object, &sub, &rc, &ref, &rs, &kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, pgDependEdge(class, object, sub, rc, ref, rs, kind))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(got, want, true); !ok {
		t.Fatalf("retained dependency bag got %#v want %#v", got, want)
	}
}
