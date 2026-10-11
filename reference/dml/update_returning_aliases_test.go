package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningAliases(t *testing.T) {
	t.Run("target alias Exec", func(t *testing.T) {
		p := fixture.NewScenario(t)
		p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
		p.Step(t, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		p.Step(t, fixture.Exec("UPDATE t x SET id=2 RETURNING x.id", 1))
		p.Step(t, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
	for _, tc := range []struct {
		options, output string
		want            []any
	}{
		{"", "*,old.*,new.*,t.*,public.t.*", []any{int64(1), int64(11), int64(1), int64(10), int64(1), int64(11), int64(1), int64(11), int64(1), int64(11)}},
		{`WITH (NEW AS n,OLD AS "O")`, `"O".a,n.a`, []any{int64(10), int64(11)}},
		{"WITH (OLD AS new)", "new.a,a", []any{int64(10), int64(11)}},
		{"WITH (NEW AS old)", "old.a,a", []any{int64(11), int64(11)}},
		{"WITH (OLD AS new,NEW AS old)", "new.a,old.a", []any{int64(10), int64(11)}},
		{"WITH (OLD AS o)", "o.a,new.a", []any{int64(10), int64(11)}},
		{"WITH (NEW AS n)", "old.a,n.a", []any{int64(10), int64(11)}},
	} {
		t.Run(tc.options, func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			names, types := []string{"a", "a"}, []string{"INT4", "INT4"}
			if tc.options == "" {
				names = []string{"id", "a", "id", "a", "id", "a", "id", "a", "id", "a"}
				types = []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}
			}
			for _, prepared := range []bool{false, true} {
				p.Step(t, fixture.Exec("UPDATE t SET a=10", 1))
				s := fixture.Select("UPDATE t SET a=a+1 RETURNING "+tc.options+" "+tc.output, names, types, [][]any{tc.want})
				s.Prepared = prepared
				p.Step(t, s)
			}
		})
	}
}
func TestReferenceUpdateReturningParameterOrder(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(2,'2')", 1))
	for _, q := range []string{"UPDATE t SET id=$1 WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated", "UPDATE t SET id=2 RETURNING $1::int4 AS typed,$1 AS repeated"} {
		p.Step(t, fixture.Prepared(fixture.Select(q, []string{"typed", "repeated"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}}, 2)))
	}
	p.Step(t, fixture.Prepared(fixture.ImplicitError("UPDATE t SET id=$1 WHERE v=$1 RETURNING $1", fixture.Diagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"}, "2")))
	p.Step(t, fixture.Prepared(fixture.Select("UPDATE t SET v=$1 WHERE id=$1 RETURNING old.v,new.v,$1", []string{"v", "v", "?column?"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"2", "2", int64(2)}}, 2)))
}

func TestReferenceUpdateReturningExpressions(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT,v TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(-10,NULL)", 1))
	p.Step(t, fixture.Select("UPDATE t SET a=11 RETURNING CAST(old.a AS BIGINT),new.a::BIGINT,abs(old.a),CASE WHEN old.a<0 THEN new.a ELSE old.a END,COALESCE(v,'fallback'),NULLIF(new.a,11),old.a IS DISTINCT FROM new.a,'literal',$1 AS \"Label\"", []string{"a", "a", "abs", "a", "coalesce", "nullif", "?column?", "?column?", "Label"}, []string{"INT8", "INT8", "INT4", "INT4", "TEXT", "INT4", "BOOL", "TEXT", "TEXT"}, [][]any{{int64(-10), int64(11), int64(10), int64(11), "fallback", nil, true, "literal", "parameter"}}, "parameter"))
}

func TestReferenceUpdateReturningExternalMechanisms(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
			if !backend.PG {
				run(fixture.Exec("CREATE TABLE triggered(a INT)", 0))
				run(fixture.Exec("INSERT INTO triggered VALUES(10)", 1))
				for _, q := range []string{"CREATE TRIGGER change BEFORE UPDATE ON triggered FOR EACH ROW EXECUTE FUNCTION adjust_update()", "UPDATE triggered SET a=2 RETURNING old", "UPDATE triggered SET a=2 RETURNING (old).*"} {
					result, err := db.ExecContext(p.Ctx, q)
					if result != nil || err == nil {
						t.Fatal("unsupported mechanism", q, result, err)
					}
				}
				run(fixture.Select("SELECT a FROM triggered", []string{"a"}, []string{"INT4"}, [][]any{{int64(10)}}))
				run(fixture.Select("INSERT INTO triggered VALUES(2) RETURNING a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
				return
			}
			for _, q := range []string{"CREATE TABLE triggered(a INT)", "INSERT INTO triggered VALUES(10)", "CREATE FUNCTION adjust_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.a=0 THEN RETURN NULL; END IF; NEW.a:=NEW.a+100; RETURN NEW; END $$", "CREATE TRIGGER adjust BEFORE UPDATE ON triggered FOR EACH ROW EXECUTE FUNCTION adjust_update()"} {
				if _, err := db.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Select("UPDATE triggered SET a=11 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(111)}}))
			fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Select("UPDATE triggered SET a=0 RETURNING old.a,new.a", []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{}))
			fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Select("UPDATE triggered SET a=12 RETURNING old::text AS predecessor,new::text AS replacement", []string{"predecessor", "replacement"}, []string{"TEXT", "TEXT"}, [][]any{{"(111)", "(112)"}}))
			run(fixture.Select("UPDATE triggered SET a=12 RETURNING (SELECT 7)", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("UPDATE triggered SET a=12 RETURNING (old).*", []string{"a"}, []string{"INT4"}, [][]any{{int64(112)}}))
			run(fixture.Select("INSERT INTO triggered VALUES(2) RETURNING a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	}
}
