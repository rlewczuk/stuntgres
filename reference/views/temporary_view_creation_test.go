package reference_test

import (
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

// Uses independent expectations on both public drivers and both execution routes.
func temporaryViewScenario(t *testing.T, body func(*fixture.TempReferenceSession, *[]fixture.Diagnostic)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					db, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					peer, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { peer.Close() })
					body(&fixture.TempReferenceSession{T: t, P: p, DB: db, C: c, Peer: peer, Record: record, Prepared: prepared, Postgres: b.PG}, &notices)
				})
			}
		})
	}
}
func temporaryViewNotices(t *testing.T, got *[]fixture.Diagnostic, names ...string) {
	t.Helper()
	want := make([]fixture.Diagnostic, 0, len(names))
	for _, name := range names {
		want = append(want, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `view "` + name + `" will be a temporary view`})
	}
	if len(*got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(*got, want)) {
		t.Fatalf("notices got %#v want %#v", *got, want)
	}
	*got = nil
}
func temporaryViewSelect(s *fixture.TempReferenceSession, c fixture.Executor, q string, columns, types []string, rows [][]any) {
	s.T.Helper()
	step := fixture.Select(q, columns, types, rows)
	step.Prepared = s.Prepared
	fixture.RunStep(s.T, s.P.Ctx, c, s.Record, step)
}
func temporaryViewSchema(s *fixture.TempReferenceSession, name string) string {
	s.T.Helper()
	var schema string
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT schemaname FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&schema); err != nil {
		s.T.Fatal(err)
	}
	return schema
}

func TestReferenceTemporaryViewCreation(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE creation_source(id INT,txt TEXT)", nil)
		s.Exec(s.C, "INSERT INTO creation_source VALUES(1,'a'),(2,NULL),(2,NULL)", 3)
		s.Utility("CREATE TEMP VIEW creation_v(k,label) WITH(security_barrier=true) AS SELECT id,txt FROM creation_source", nil)
		s.Utility("CREATE TEMPORARY VIEW creation_empty AS SELECT k,label FROM creation_v WHERE false", nil)
		temporaryViewNotices(t, n)
		temporaryViewSelect(s, s.C, "SELECT k,label FROM creation_v ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(2), nil}})
		temporaryViewSelect(s, s.C, "SELECT k,label FROM creation_empty", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{})
		s.Exec(s.C, "INSERT INTO creation_source VALUES(3,'b')", 1)
		temporaryViewSelect(s, s.C, "SELECT k FROM creation_v ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}})
		s.Utility("CREATE TEMP VIEW creation_zero AS SELECT FROM creation_source", nil)
		temporaryViewSelect(s, s.C, "SELECT FROM creation_zero", []string{}, []string{}, [][]any{{}, {}, {}, {}})
		// Prepare is not CREATE: it must neither emit a notice nor allocate a relation.
		q := "CREATE VIEW creation_prepared AS SELECT k FROM creation_v"
		stmt, err := s.C.PrepareContext(s.P.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		temporaryViewNotices(t, n)
		temporaryViewSelect(s, s.C, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='creation_prepared'", []string{"viewname"}, []string{"NAME"}, [][]any{})
		if r, err := stmt.ExecContext(s.P.Ctx); err != nil {
			t.Fatal(err)
		} else if count, err := r.RowsAffected(); err != nil || count != 0 {
			t.Fatal(count, err)
		}
		temporaryViewNotices(t, n, "creation_prepared")
		_, err = stmt.ExecContext(s.P.Ctx)
		fixture.AssertDiagnostic(t, err, fixture.TempReferenceDiagnostic("42P07", `relation "creation_prepared" already exists`, "", ""))
		temporaryViewNotices(t, n, "creation_prepared")
	})
}

func TestReferenceTemporaryViewInference(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE inference_public(v INT)", nil)
		s.Exec(s.C, "INSERT INTO inference_public VALUES(7)", 1)
		s.Utility("CREATE TEMP TABLE inference_base(v INT)", nil)
		s.Exec(s.C, "INSERT INTO inference_base VALUES(11)", 1)
		cases := []struct{ name, body string }{
			{"inference_direct", "SELECT v FROM inference_base"},
			{"inference_nested", "SELECT v FROM inference_direct"},
			{"inference_join", "SELECT p.v FROM inference_public p JOIN inference_base t ON p.v<t.v"},
			{"inference_false", "SELECT v FROM inference_base WHERE false"},
			{"inference_derived", "SELECT v FROM (SELECT v FROM inference_base) d"},
			{"inference_subquery", "SELECT v FROM inference_public WHERE EXISTS(SELECT v FROM inference_base)"},
			{"inference_union", "SELECT v FROM inference_public UNION ALL SELECT v FROM inference_base"},
			{"inference_zero", "SELECT FROM inference_base"},
		}
		var schema string
		for _, tc := range cases {
			s.Utility("CREATE VIEW "+tc.name+" AS "+tc.body, nil)
			temporaryViewNotices(t, n, tc.name)
			actual := temporaryViewSchema(s, tc.name)
			if schema == "" {
				schema = actual
			}
			if actual != schema || actual == "public" {
				t.Fatal(tc.name, actual, schema)
			}
		}
		s.Utility("CREATE VIEW pg_temp.inference_destination AS SELECT v FROM inference_public", nil)
		temporaryViewNotices(t, n)
		if actual := temporaryViewSchema(s, "inference_destination"); actual != schema {
			t.Fatal(actual, schema)
		}
		s.Utility("CREATE TEMP VIEW inference_explicit AS SELECT v FROM inference_base", nil)
		temporaryViewNotices(t, n)
		fixture.TemporaryTableRows(s, s.C, "inference_nested", 11)
	})
}

func TestReferenceTemporaryViewCatalog(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TEMP VIEW catalog_temp_v AS SELECT 11 AS v", nil)
		temporaryViewNotices(t, n)
		var oid, namespace int64
		var kind string
		var rules bool
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid,relnamespace,relkind,relhasrules FROM pg_catalog.pg_class WHERE relname='catalog_temp_v'").Scan(&oid, &namespace, &kind, &rules); err != nil || oid <= 0 || namespace <= 0 || kind != "v" || !rules {
			t.Fatal(oid, namespace, kind, rules, err)
		}
		var rule int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class=$1 AND rulename='_RETURN'", oid).Scan(&rule); err != nil || rule <= 0 {
			t.Fatal(rule, err)
		}
		s.Utility("DROP VIEW catalog_temp_v", nil)
		for _, q := range []string{"SELECT relname FROM pg_catalog.pg_class WHERE oid=" + strconv.FormatInt(oid, 10), "SELECT rulename FROM pg_catalog.pg_rewrite WHERE oid=" + strconv.FormatInt(rule, 10)} {
			rows, err := s.C.QueryContext(s.P.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("orphan catalog artifact", q, rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
