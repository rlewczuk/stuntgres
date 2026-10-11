package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceProspectiveRetirement(t *testing.T) {
	for _, definition := range []string{"default", "check"} {
		t.Run(definition, func(t *testing.T) {
			inheritanceScenario(t, func(s *inheritanceCase) {
				s.exec("CREATE TABLE prospective_q(b INT)", 0)
				columns := "a BOOL DEFAULT ('prospective_q'::regclass IS NOT NULL),keep INT DEFAULT 7 NOT NULL,CONSTRAINT keep_positive CHECK(keep>0)"
				parentValues, childValues := "true,10", "2,true,20"
				names, types := []string{"b", "a", "keep"}, []string{"INT4", "BOOL", "INT4"}
				parentRows := [][]any{{true, int64(10)}}
				childRows := [][]any{{int64(2), true, int64(20)}}
				if definition == "check" {
					columns = "a INT DEFAULT 9,keep INT DEFAULT 7 NOT NULL,CONSTRAINT provider_positive CHECK(a>0 AND 'prospective_q'::regclass IS NOT NULL),CONSTRAINT keep_positive CHECK(keep>0)"
					parentValues, childValues = "1,10", "2,3,20"
					types[1] = "INT4"
					parentRows = [][]any{{int64(1), int64(10)}}
					childRows = [][]any{{int64(2), int64(3), int64(20)}}
				}
				s.exec("CREATE TABLE prospective_p("+columns+")", 0)
				s.exec("CREATE TABLE prospective_c() INHERITS(prospective_q,prospective_p)", 0)
				s.exec("INSERT INTO prospective_q VALUES(8)", 1)
				s.exec("INSERT INTO prospective_p VALUES("+parentValues+")", 1)
				s.exec("INSERT INTO prospective_c VALUES("+childValues+")", 1)
				if s.b.PG {
					s.values("SELECT * FROM ONLY prospective_q", []string{"b"}, []string{"INT4"}, [][]any{{int64(8)}})
					s.values("SELECT * FROM ONLY prospective_p", names[1:], types[1:], parentRows)
				}
				s.values("SELECT * FROM prospective_c", names, types, childRows)
				s.values("SELECT * FROM prospective_c WHERE false", names, types, nil)
				s.edges("prospective_c", "prospective_p", "prospective_q")
				var held *sql.Rows
				if !s.b.PG {
					var err error
					held, err = s.c.QueryContext(context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.record), "SELECT * FROM prospective_c")
					if err != nil {
						s.t.Fatal(err)
					}
					defer held.Close()
				}
				s.exec("DROP TABLE prospective_q CASCADE", 0)
				if !s.b.PG {
					fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.c, Rows: held, Query: "SELECT * FROM prospective_c"}, s.record, fixture.Select("SELECT * FROM prospective_c", names, types, childRows))
				}
				s.absent("prospective_q")
				s.absent("prospective_c")
				s.edges("prospective_q")
				s.edges("prospective_c")
				s.edges("prospective_p")
				s.values("SELECT * FROM prospective_p", names[1:], types[1:], parentRows)
				s.values("SELECT * FROM prospective_p WHERE false", names[1:], types[1:], nil)
				s.constraints("prospective_p", [][]any{{"keep_positive", "c"}, {"prospective_p_keep_not_null", "n"}})
				s.exec("INSERT INTO prospective_p DEFAULT VALUES", 1)
				if definition == "default" {
					s.values("SELECT * FROM prospective_p ORDER BY keep", names[1:], types[1:], [][]any{{nil, int64(7)}, {true, int64(10)}})
				} else {
					s.exec("INSERT INTO prospective_p VALUES(-1,30)", 1)
					s.values("SELECT * FROM prospective_p ORDER BY keep", names[1:], types[1:], [][]any{{int64(9), int64(7)}, {int64(1), int64(10)}, {int64(-1), int64(30)}})
				}
				s.exec("DROP TABLE prospective_p RESTRICT", 0)
			})
		})
	}
}
