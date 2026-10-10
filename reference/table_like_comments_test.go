package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTableLikeComments(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE src(dead TEXT,a INT,b TEXT)", 0))
		run(referenceExec("ALTER TABLE src DROP COLUMN dead", 0))
		run(referenceExec("COMMENT ON TABLE src IS 'not copied'", 0))
		run(referenceExec("COMMENT ON COLUMN src.a IS ' O''Brien é\n  '", 0))
		run(referenceExec("COMMENT ON COLUMN src.b IS ''", 0))
		run(referenceExec("CREATE TABLE clone(lead BOOL,LIKE src INCLUDING COMMENTS)", 0))
		run(referenceExec("CREATE TABLE excluded(LIKE src INCLUDING ALL EXCLUDING COMMENTS)", 0))
		want := referenceSelect("SELECT obj_description('clone'::regclass,'pg_class') AS table_comment,col_description('clone'::regclass,2) AS a_comment,col_description('clone'::regclass,3) AS b_comment", []string{"table_comment", "a_comment", "b_comment"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nil, " O'Brien é\n  ", nil}})
		run(want)
		run(referenceSelect("SELECT col_description('excluded'::regclass,1) AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}))
		run(referenceExec("ALTER TABLE src RENAME COLUMN a TO renamed", 0))
		run(referenceExec("COMMENT ON COLUMN src.renamed IS 'changed'", 0))
		run(referenceExec("DROP TABLE src", 0))
		run(want)
	})
}
