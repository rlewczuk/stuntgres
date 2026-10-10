package reference_test

import "testing"

func TestReferenceSimilarConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		for _, s := range []referenceStep{
			referenceExec(`CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT)`, 0),
			referenceExec(`INSERT INTO t VALUES(1,'abc','a%','#'),(2,'b','a%','#'),(3,NULL,'a%','#')`, 3),
		} {
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
		for _, s := range []referenceStep{
			referenceSelect(`SELECT id FROM t WHERE v SIMILAR TO p ESCAPE e ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			referenceSelect(`SELECT v SIMILAR TO p ESCAPE e AS m,count(*) AS n FROM t GROUP BY v SIMILAR TO p ESCAPE e ORDER BY m`, []string{"m", "n"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(1)}, {true, int64(1)}, {nil, int64(1)}}),
			referenceSelect(`SELECT count(v SIMILAR TO p ESCAPE e) AS n FROM t`, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			referenceSelect(`SELECT CASE WHEN v SIMILAR TO p ESCAPE e THEN 'yes' ELSE 'no' END AS c,COALESCE(v SIMILAR TO p ESCAPE e,FALSE) AS m FROM t ORDER BY id`, []string{"c", "m"}, []string{"TEXT", "BOOL"}, [][]any{{"yes", true}, {"no", false}, {"no", false}}),
			referenceSelect(`SELECT substring(v SIMILAR p ESCAPE e) AS piece FROM t ORDER BY id`, []string{"piece"}, []string{"TEXT"}, [][]any{{"abc"}, {nil}, {nil}}),
		} {
			similarReferenceRoutes(t, p, b, s)
		}
		for _, prepared := range []bool{false, true} {
			s := referenceExec(`UPDATE t SET v='abc' WHERE v NOT SIMILAR TO p ESCAPE e`, 1)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`UPDATE t SET v='b' WHERE id=2`, 1))
		}
	}
}

func TestReferenceSimilarCharacterCoercions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		for _, s := range []referenceStep{
			referenceSelect(`SELECT 'a'::char(3) SIMILAR TO 'a__' AS padded,'a'::char(3) SIMILAR TO 'a' AS exact,'a'::varchar(3) SIMILAR TO 'a' AS varying`, []string{"padded", "exact", "varying"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true}}),
			referenceSelect(`SELECT 'a' SIMILAR TO 'a'::char(3) AS pattern,'a%b' SIMILAR TO 'a!%b' ESCAPE '!'::char(3) AS escaped,substring('a'::char(3) SIMILAR '%' ESCAPE '#') AS piece`, []string{"pattern", "escaped", "piece"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{true, true, "a"}}),
		} {
			similarReferenceRoutes(t, p, b, s)
		}
	}
}

func TestReferenceSimilarExtensions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		for _, c := range []struct {
			source, pattern string
			match           bool
		}{
			{"5", `\d`, true}, {"a", `\D`, true}, {"\n", `\s`, true}, {"a", `\S`, true}, {"_", `\w`, true}, {"-", `\W`, true},
			{"-abc-", `%\mabc\M%`, true}, {"xabcy", `%\mabc\M%`, false}, {"abc", `\yabc\y`, true}, {"xabcy", `%\Yabc\Y%`, true},
			{"\b", `\b`, true}, {`\`, `\B`, true}, {"a", `\x61`, true}, {"a", `\u0061`, true}, {"a", `\U00000061`, true}, {"a", `\141`, true},
		} {
			similarReferenceRoutes(t, p, b, referenceSelect(`SELECT $1 SIMILAR TO $2 AS matched`, []string{"matched"}, []string{"BOOL"}, [][]any{{c.match}}, c.source, c.pattern))
		}
	}
}
