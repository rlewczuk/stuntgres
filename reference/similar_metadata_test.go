package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceSimilarMetadataDeparse(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`CREATE TABLE t(v TEXT,p TEXT,e TEXT, CONSTRAINT ck CHECK (v SIMILAR TO '%a%' AND v NOT SIMILAR TO p ESCAPE e))`, 0))
		for _, step := range []referenceStep{
			referenceSelect(`SELECT 'a' SIMILAR TO 'a', 'a' NOT SIMILAR TO 'b', substring('abc' SIMILAR '%' ESCAPE '#'), substring('abc' FROM '%' FOR '#'), substring('abc','%','#'), pg_catalog.substring('abc','%','#')`, []string{"?column?", "?column?", "substring", "substring", "substring", "substring"}, []string{"BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{true, true, "abc", "abc", "abc", "abc"}}),
			referenceSelect(`SELECT v SIMILAR TO p, v NOT SIMILAR TO p ESCAPE e, substring(v SIMILAR p ESCAPE e), substring(v FROM p FOR e), substring(v,p,e) FROM t`, []string{"?column?", "?column?", "substring", "substring", "substring"}, []string{"BOOL", "BOOL", "TEXT", "TEXT", "TEXT"}, nil),
			referenceSelect(`SELECT pg_get_constraintdef(oid),pg_get_constraintdef(oid,true) FROM pg_constraint WHERE conname='ck'`, []string{"pg_get_constraintdef", "pg_get_constraintdef"}, []string{"TEXT", "TEXT"}, [][]any{{`CHECK (((v ~ similar_to_escape('%a%'::text)) AND (v !~ similar_to_escape(p, e))))`, `CHECK (v ~ similar_to_escape('%a%'::text) AND v !~ similar_to_escape(p, e))`}}),
		} {
			step.checkMetadata = similarReferenceMetadata
			if step.columns[0] == "pg_get_constraintdef" {
				step.checkMetadata = similarReferenceDeclaredMetadata(b.pg)
			}
			similarReferenceRoutes(t, p, b, step)
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`CREATE VIEW sv AS SELECT v SIMILAR TO '%a%' AS folded, v NOT SIMILAR TO p ESCAPE e AS symbolic, substring(v SIMILAR p ESCAPE e) AS piece, pg_catalog.substring(v,p,e) AS qualified, COALESCE(v,'') SIMILAR TO p ESCAPE e AS wrapped FROM t`, 0))
		compact := " SELECT (v ~ similar_to_escape('%a%'::text)) AS folded,\n    (v !~ similar_to_escape(p, e)) AS symbolic,\n    SUBSTRING(v SIMILAR p ESCAPE e) AS piece,\n    \"substring\"(v, p, e) AS qualified,\n    (COALESCE(v, ''::text) ~ similar_to_escape(p, e)) AS wrapped\n   FROM t;"
		pretty := " SELECT v ~ similar_to_escape('%a%'::text) AS folded,\n    v !~ similar_to_escape(p, e) AS symbolic,\n    SUBSTRING(v SIMILAR p ESCAPE e) AS piece,\n    \"substring\"(v, p, e) AS qualified,\n    COALESCE(v, ''::text) ~ similar_to_escape(p, e) AS wrapped\n   FROM t;"
		similarReferenceRoutes(t, p, b, referenceSelect(`SELECT pg_get_viewdef('sv'::regclass),pg_get_viewdef('sv'::regclass,true)`, []string{"pg_get_viewdef", "pg_get_viewdef"}, []string{"TEXT", "TEXT"}, [][]any{{compact, pretty}}))
		// Definition admission is symbolic: malformed constants are not evaluated by
		// CREATE or Prepare. Their later demanded execution remains a separate gate.
		for _, query := range []string{
			`CREATE TABLE bad_similar(v TEXT CHECK (v SIMILAR TO '['))`,
			`CREATE TABLE bad_similar_null(v TEXT CHECK (NULL::text SIMILAR TO 'a' ESCAPE 'ab'))`,
			`CREATE VIEW bad_similar_view AS SELECT 'a' SIMILAR TO '[' AS matched`,
			`CREATE TABLE bad_similar_default(v BOOLEAN DEFAULT ('a' SIMILAR TO '['))`,
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t/%s", b.name, prepared, query), func(t *testing.T) {
					step := referenceExec(query, 0)
					step.prepared = prepared
					referenceRunStep(t, p.ctx, b.db, b.recorder, step)
				})
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`DROP VIEW IF EXISTS bad_similar_view`, 0))
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`DROP TABLE IF EXISTS bad_similar, bad_similar_null, bad_similar_default`, 0))
			}
		}
	}
}
