package reference_test

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

// One shared SQL body, with genuine database/sql Prepare on the prepared route.
func partitionInheritedScenario(t *testing.T, body func(*partitionInheritedCase)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					s := &partitionInheritedCase{t: t, p: p, b: b, prepared: prepared}
					_, s.conn, s.recorder = fixture.NoticeReferencePool(t, p, b.PG, &s.notices)
					body(s)
				})
			}
		})
	}
}

type partitionInheritedCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	recorder *fixture.Recorder
	prepared bool
	notices  []fixture.Diagnostic
}

func (s *partitionInheritedCase) run(step fixture.Step) {
	s.t.Helper()
	step.Prepared = s.prepared
	fixture.RunStep(s.t, s.p.Ctx, s.conn, s.recorder, step)
}

func (s *partitionInheritedCase) exec(q string, count int64, args ...any) {
	s.t.Helper()
	s.run(fixture.Exec(q, count, args...))
}

func (s *partitionInheritedCase) fail(q string, d fixture.Diagnostic, args ...any) {
	s.t.Helper()
	step := fixture.ImplicitError(q, d, args...)
	step.ErrorStage = "execute"
	s.run(step)
}

// Every field is independently specified; own OIDs are checked relationally below.
func (s *partitionInheritedCase) facts(table string, rows [][]any) {
	s.t.Helper()
	sort.Slice(rows, func(i, j int) bool { return rows[i][0].(string) < rows[j][0].(string) })
	s.run(fixture.Select("SELECT conname,contype,conislocal,coninhcount,connoinherit,convalidated,conkey::text,conparentid=0::oid AS no_parent,conindid=0::oid AS no_index FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid ORDER BY conname",
		[]string{"conname", "contype", "conislocal", "coninhcount", "connoinherit", "convalidated", "conkey", "no_parent", "no_index"},
		[]string{"NAME", "CHAR", "BOOL", "INT2", "BOOL", "BOOL", "TEXT", "BOOL", "BOOL"}, rows))
}

func partitionInheritedFact(name, kind string, local bool, count int64, key string) []any {
	return []any{name, kind, local, count, false, true, key, true, true}
}

func (s *partitionInheritedCase) oid(table, name string) string {
	s.t.Helper()
	var oid string
	if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT oid::text FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid AND conname=$1", name).Scan(&oid); err != nil {
		s.t.Fatal(err)
	}
	return oid
}

func (s *partitionInheritedCase) sameOID(table, name, previous string) {
	s.t.Helper()
	if got := s.oid(table, name); got != previous {
		s.t.Fatalf("%s/%s OID changed: %s -> %s", table, name, previous, got)
	}
}

func (s *partitionInheritedCase) expectNotices(want ...fixture.Diagnostic) {
	s.t.Helper()
	if !reflect.DeepEqual(s.notices, want) {
		s.t.Fatalf("notices got %#v want %#v", s.notices, want)
	}
	s.notices = nil
}

func partitionInheritedMergeNotice(name string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `merging constraint "` + name + `" with inherited definition`}
}

func (s *partitionInheritedCase) values(q string, rows [][]any, nullable []bool, args ...any) {
	s.t.Helper()
	step := fixture.Select(q, []string{"k", "a", "b"}, []string{"INT4", "INT4", "INT4"}, rows, args...)
	step.CheckMetadata = referenceRangeMetadata(s.b.PG, step.Columns, step.Types, nullable)
	s.run(step)
}

func partitionInheritedCheckFailure(table, name, row string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + name + `"`, Detail: "Failing row contains (" + row + ").", Schema: "public", Table: table, Constraint: name}
}

func partitionInheritedNullFailure(table, row string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "` + table + `" violates not-null constraint`, Detail: "Failing row contains (" + row + ").", Schema: "public", Table: table, Column: "a"}
}

func (s *partitionInheritedCase) tree(definition, midDefinition string) {
	s.t.Helper()
	s.exec("CREATE TABLE pi_p(k INT,a INT,b INT"+definition+") PARTITION BY RANGE(k)", 0)
	s.exec("CREATE TABLE pi_mid PARTITION OF pi_p"+midDefinition+" DEFAULT PARTITION BY RANGE(k)", 0)
	s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid FOR VALUES FROM(0) TO(10)", 0)
	s.exec("CREATE TABLE pi_hi PARTITION OF pi_mid FOR VALUES FROM(10) TO(20)", 0)
}

func (s *partitionInheritedCase) nullability(table string, notnull bool) {
	s.t.Helper()
	s.run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='"+table+"'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{notnull}}))
}
