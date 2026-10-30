package reference_test

import (
 "fmt"
 "regexp"
 "strconv"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

var attributeStatsRows=regexp.MustCompile(`rows=([0-9]+)`)
func(s attributeStatsScenario) estimate(predicate string,want int64){
 s.t.Helper();rows,err:=s.conn.QueryContext(s.p.Ctx,"EXPLAIN SELECT * FROM ast_t WHERE "+predicate);if err!=nil{s.t.Fatal(err)};defer rows.Close();var lines []string
 for rows.Next(){var line string;if err=rows.Scan(&line);err!=nil{s.t.Fatal(err)};lines=append(lines,line)};if err=rows.Err();err!=nil{s.t.Fatal(err)}
 match:=attributeStatsRows.FindStringSubmatch(strings.Join(lines,"\n"));if len(match)!=2{s.t.Fatal(lines)};n,err:=strconv.ParseInt(match[1],10,64);if err!=nil||n!=want{s.t.Fatalf("estimate %d want %d: %v (%v)",n,want,lines,err)}
}
func attributeStatsPopulation(s attributeStatsScenario){
 s.exec("CREATE TABLE ast_t(a INT,b INT)");var rows []string;for i:=range 100{rows=append(rows,fmt.Sprintf("(%d,%d)",i+1,i+1))};s.run(fixture.Exec("INSERT INTO ast_t VALUES"+strings.Join(rows,","),100));s.exec("ANALYZE ast_t");s.clear("a",false)
}
func TestReferenceAttributeStatisticsPlanner(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 attributeStatsPopulation(s)
 s.restore("'null_frac',.25::real,'avg_width',4::integer,'n_distinct',10::real,'most_common_vals','{1}','most_common_freqs','{0.5}'::real[]",true)
 s.estimate("a IS NULL",25);s.estimate("a=1",50);s.estimate("a=2",3)
 s.restore("'n_distinct',-.1::real",true);s.estimate("a=2",3)
 s.restore("'null_frac',0::real,'n_distinct',0::real",true);s.estimate("a IS NULL",1)
 // REL_18_6 get_variable_numdistinct: explicit zero uses min(200,100).
 // Residual mass .5/(100-1) yields a one-row equality estimate, not 6 rows
 // from the previously restored ten-distinct override.
 s.estimate("a=2",1)
 s.restore("'most_common_vals','{1}','most_common_freqs','{0.25}'::real[]",true);s.estimate("a=1",25)
 s.clear("a",false);s.estimate("a=1",1)
})}
func TestReferenceAttributeStatisticsTransactions(t *testing.T){withAttributeStats(t,func(s attributeStatsScenario){
 s.exec("CREATE TABLE ast_t(a INT,b INT)")
 s.run(fixture.Exec("INSERT INTO ast_t VALUES(1,1),(2,2),(3,3),(4,4),(5,5),(6,6),(7,7),(8,8)",8))
 s.exec("ANALYZE ast_t");s.clear("a",false);s.clear("b",false);s.restore("'null_frac',.25::real",true)
 peer,err:=s.db.Conn(s.p.Ctx);if err!=nil{s.t.Fatal(err)};defer peer.Close()
 peerFraction:=func(want float64){s.t.Helper();var got float64;if err:=peer.QueryRowContext(s.p.Ctx,"SELECT null_frac FROM pg_stats WHERE schemaname='public' AND tablename='ast_t' AND attname='a' AND NOT inherited").Scan(&got);err!=nil||got!=want{s.t.Fatalf("peer fraction=%v want %v: %v",got,want,err)}}
 s.exec("BEGIN");s.restore("'null_frac',.5::real",true);s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)});peerFraction(.25)
 s.estimate("a IS NULL",4)
 observer:=s;observer.conn=peer;observer.estimate("a IS NULL",2)
 s.exec("SAVEPOINT ast_save");s.clear("a",false);s.scalars();s.exec("ROLLBACK TO ast_save");s.scalars([]any{"a",false,float64(.5),int64(0),float64(0)})
 s.estimate("a IS NULL",4)
 s.exec("ROLLBACK");peerFraction(.25)
 s.estimate("a IS NULL",2);observer.estimate("a IS NULL",2)
 s.exec("BEGIN");s.restore("'null_frac',.75::real",true);s.exec("COMMIT");peerFraction(.75)
 s.estimate("a IS NULL",6);observer.estimate("a IS NULL",6)
 s.exec("BEGIN");s.clear("a",false);s.exec("ROLLBACK");peerFraction(.75)
 // CASE forces the volatile import to execute before its failing branch.
 q:="SELECT CASE WHEN pg_restore_attribute_stats('schemaname','public','relname','ast_t','attname','a','inherited',false,'null_frac',.5::real) THEN 1/(length(current_user)-length(current_user)) ELSE 0 END"
 s.fail(q,"22012","division by zero");peerFraction(.75)
 attributeStatsBlockedCancellation(s)
 attributeStatsDisjointMerge(s)
})}
