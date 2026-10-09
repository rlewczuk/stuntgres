package reference_test

import "testing"

func TestReferenceLikeConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
	}
	run(referenceExec("CREATE TABLE t(id INT,v TEXT,p TEXT,e TEXT)", 0))
	run(referenceExec("INSERT INTO t VALUES(1,'ab','a%','!'),(2,'b','a%','!'),(3,NULL,NULL,NULL)", 3))
	run(referenceExec("CREATE TABLE patterns(id INT,p TEXT,e TEXT)", 0))
	run(referenceExec("INSERT INTO patterns VALUES(1,'a%','!'),(2,'b%','!')", 2))
	for _, s := range []referenceStep{
		referenceSelect("SELECT COUNT(v LIKE p ESCAPE e) AS n,COUNT(v NOT ILIKE p ESCAPE e) AS negative FROM t", []string{"n", "negative"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}),
		referenceSelect("SELECT (CASE COUNT(*) WHEN 3 THEN 'Ab' ELSE 'x' END) ILIKE (CASE COUNT(*) WHEN 3 THEN 'A%' ELSE 'y' END) ESCAPE (CASE COUNT(*) WHEN 3 THEN '!' ELSE '?' END) AS matched FROM t", []string{"matched"}, []string{"BOOL"}, [][]any{{true}}),
		referenceSelect("SELECT (CASE COUNT(*) WHEN 0 THEN NULL::text ELSE 'x' END) LIKE (CASE COUNT(*) WHEN 0 THEN NULL::text ELSE 'x' END) ESCAPE 'ab' AS matched FROM t WHERE FALSE", []string{"matched"}, []string{"BOOL"}, [][]any{{nil}}),
		referenceSelect("SELECT a.id AS lhs,b.id AS pattern FROM t a JOIN patterns b ON a.v LIKE b.p ESCAPE b.e ORDER BY a.id,b.id", []string{"lhs", "pattern"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
		referenceSelect("SELECT id FROM t ORDER BY v LIKE 'a%',id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}}),
		referenceSelect("SELECT v OPERATOR(\"pg_catalog\".~~) p AS matched FROM t ORDER BY id", []string{"matched"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}}),
	} {
		t.Run(s.query, func(t *testing.T) { run(s); run(referencePrepared(s)) })
	}
	run(referenceExec("SET search_path TO public,pg_catalog", 0))
	s := referenceSelect("SELECT v ILIKE p ESCAPE e AS matched FROM t ORDER BY id", []string{"matched"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
	run(s)
	run(referencePrepared(s))
}
