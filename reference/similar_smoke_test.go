package reference_test

import "testing"

func TestReferenceSimilarFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`CREATE TABLE t(id INT,v TEXT,p TEXT,e TEXT,sentinel INT)`, 0))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`INSERT INTO t VALUES (1,'foobar','%#"o_b#"%','#',7),(2,'ab','a#"#"b','#',7),(3,NULL,'%','#',7)`, 3))
			similarReferenceRoutes(t, p, b, referenceSelect(`SELECT id,v SIMILAR TO p ESCAPE e AS matched,v NOT SIMILAR TO p ESCAPE e AS unmatched,substring(v SIMILAR p ESCAPE e) AS piece FROM t ORDER BY id`, []string{"id", "matched", "unmatched", "piece"}, []string{"INT4", "BOOL", "BOOL", "TEXT"}, [][]any{{int64(1), true, false, "oob"}, {int64(2), true, false, ""}, {int64(3), nil, nil, nil}}))
			similarReferenceRoutes(t, p, b, referenceSelect(`SELECT 'abc' SIMILAR TO 'b' AS matched,substring('abc' SIMILAR 'b' ESCAPE '#') AS piece,'é' SIMILAR TO '_' AS unicode,'é' SIMILAR TO '_' AS decomposed,'abab' SIMILAR TO '(ab){2}' AS repeated`, []string{"matched", "piece", "unicode", "decomposed", "repeated"}, []string{"BOOL", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{false, nil, true, false, true}}))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`UPDATE t SET p='[' WHERE id=2`, 1))
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct-atomicity", true: "prepared-atomicity"}[prepared], func(t *testing.T) {
					failure := referenceImplicitError(`UPDATE t SET sentinel=99 WHERE v SIMILAR TO p ESCAPE e`, referenceDiagnostic{Code: "2201B", Message: "invalid regular expression: brackets [] not balanced"})
					failure.prepared = prepared
					failure.errorStage = "execute"
					referenceRunStep(t, p.ctx, b.db, b.recorder, failure)
					similarReferenceRoutes(t, p, b, referenceSelect(`SELECT id,sentinel FROM t ORDER BY id`, []string{"id", "sentinel"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}}))
				})
			}
		})
	}
}
