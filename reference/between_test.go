package reference_test

import "testing"

var betweenReferenceForms = []string{"BETWEEN", "NOT BETWEEN", "BETWEEN SYMMETRIC", "NOT BETWEEN SYMMETRIC"}

func betweenReferenceSetup(t *testing.T) *referencePair {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INTEGER,v INTEGER)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,0),(2,1),(3,2),(4,3),(5,4),(6,NULL)", 6))
	return p
}

func betweenReferenceBoth(t *testing.T, p *referencePair, step referenceStep) {
	t.Helper()
	scalarStep(t, p, step)
	scalarStep(t, p, referencePrepared(step))
}

func TestReferenceBetweenMatrix(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, tc := range []struct {
		bounds string
		values [][]any
		ids    [][]int64
	}{
		{"1 AND 3", [][]any{{false, true, false, true}, {true, false, true, false}, {true, false, true, false}, {true, false, true, false}, {false, true, false, true}, {nil, nil, nil, nil}}, [][]int64{{2, 3, 4}, {1, 5}, {2, 3, 4}, {1, 5}}},
		{"3 AND 1", [][]any{{false, true, false, true}, {false, true, true, false}, {false, true, true, false}, {false, true, true, false}, {false, true, false, true}, {nil, nil, nil, nil}}, [][]int64{{}, {1, 2, 3, 4, 5}, {2, 3, 4}, {1, 5}}},
		{"2 AND 2", [][]any{{false, true, false, true}, {false, true, false, true}, {true, false, true, false}, {false, true, false, true}, {false, true, false, true}, {nil, nil, nil, nil}}, [][]int64{{3}, {1, 2, 4, 5}, {3}, {1, 2, 4, 5}}},
	} {
		for form, keyword := range betweenReferenceForms {
			want := [][]any{}
			for _, row := range tc.values {
				want = append(want, []any{row[form]})
			}
			expr := "v " + keyword + " " + tc.bounds
			betweenReferenceBoth(t, p, referenceSelect("SELECT "+expr+" FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, want))
			arithmeticBounds := map[string]string{"1 AND 3": "(1+0) AND (2+1)", "3 AND 1": "(2+1) AND (1+0)", "2 AND 2": "(1+1) AND (3-1)"}[tc.bounds]
			betweenReferenceBoth(t, p, referenceSelect("SELECT v "+keyword+" "+arithmeticBounds+" FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, want))
			want = [][]any{}
			for _, id := range tc.ids[form] {
				want = append(want, []any{id})
			}
			betweenReferenceBoth(t, p, referenceSelect("SELECT id FROM t WHERE "+expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, want))
			betweenReferenceBoth(t, p, referenceSelect("SELECT id FROM t WHERE v "+keyword+" "+arithmeticBounds+" ORDER BY id", []string{"id"}, []string{"INT4"}, want))
		}
	}
	scalarStep(t, p, referenceExec("CREATE TABLE empty(v INTEGER)", 0))
	for form, keyword := range betweenReferenceForms {
		betweenReferenceBoth(t, p, referenceSelect("SELECT v "+keyword+" 1 AND 3 FROM empty", []string{"?column?"}, []string{"BOOL"}, nil))
		step := referenceSelect("SELECT 2 "+keyword+" 1 AND 3 FROM t WHERE id<3", []string{"?column?"}, []string{"BOOL"}, [][]any{{form == 0 || form == 2}, {form == 0 || form == 2}})
		step.bag = true
		betweenReferenceBoth(t, p, step)
	}

}

func TestReferenceBetweenNullPositions(t *testing.T) {
	p := betweenReferenceSetup(t)
	scalarStep(t, p, referenceExec("CREATE TABLE n(id INTEGER,a INTEGER,l INTEGER,u INTEGER)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO n VALUES(1,NULL,1,3),(2,5,NULL,3),(3,5,3,NULL),(4,1,NULL,3),(5,1,3,NULL),(6,3,NULL,3),(7,3,3,NULL),(8,2,NULL,NULL),(9,NULL,NULL,NULL)", 9))
	for form, keyword := range betweenReferenceForms {
		want := [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}
		ids := [][]any{}
		if form == 0 {
			want[1][0] = false
			want[4][0] = false
		}
		if form == 1 {
			want[1][0] = true
			want[4][0] = true
			ids = [][]any{{int64(2)}, {int64(5)}}
		}
		betweenReferenceBoth(t, p, referenceSelect("SELECT a "+keyword+" l AND u FROM n ORDER BY id", []string{"?column?"}, []string{"BOOL"}, want))
		betweenReferenceBoth(t, p, referenceSelect("SELECT id FROM n WHERE a "+keyword+" l AND u ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
		for _, backend := range p.backends() {
			q := "SELECT $1 " + keyword + " $2::INTEGER AND $3::INTEGER FROM t WHERE id=1"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			for row, args := range [][]any{{nil, 1, 3}, {5, nil, 3}, {5, 3, nil}, {1, nil, 3}, {1, 3, nil}, {3, nil, 3}, {3, 3, nil}, {2, nil, nil}, {nil, nil, nil}} {
				scalarRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, q}, backend.recorder, referenceSelect(q, []string{"?column?"}, []string{"BOOL"}, [][]any{want[row]}, args...))
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		betweenReferenceBoth(t, p, referenceSelect("SELECT NULL "+keyword+" 1 AND 3 FROM t WHERE id=1", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}}))
		for row, args := range [][]any{{nil, 1, 3}, {5, nil, 3}, {5, 3, nil}, {1, nil, 3}, {1, 3, nil}, {3, nil, 3}, {3, 3, nil}, {2, nil, nil}, {nil, nil, nil}} {
			betweenReferenceBoth(t, p, referenceSelect("SELECT $1 "+keyword+" $2::INTEGER AND $3::INTEGER FROM t WHERE id=1", []string{"?column?"}, []string{"BOOL"}, [][]any{want[row]}, args...))
		}
	}
}

func TestReferenceBetweenGrammar(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, expr := range []string{"TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE", "TRUE BETWEEN FALSE AND (2 BETWEEN 1 AND 3)", "(TRUE BETWEEN (FALSE OR TRUE) AND TRUE)", "TRUE BETWEEN (1<2) AND TRUE", "TRUE BETWEEN (1<2) AND (2<3)", "TRUE BETWEEN 1<2 AND (2<3)", "2 BETWEEN 1 AND 3 = TRUE", "TRUE = 2 BETWEEN 1 AND 3", "(v IS NULL) BETWEEN FALSE AND TRUE", "(id=1) BETWEEN FALSE AND TRUE", "TRUE BETWEEN (TRUE AND TRUE) AND TRUE", "2 BeTwEeN /* lower */ (1+0) AND (2+1)"} {
		betweenReferenceBoth(t, p, referenceSelect("SELECT "+expr+" FROM t WHERE id=1", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
		betweenReferenceBoth(t, p, referenceSelect("SELECT id FROM t WHERE ("+expr+") AND id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	for _, tc := range []struct {
		expr string
		ids  [][]any
	}{
		{"NOT v BETWEEN 1 AND 3", [][]any{{int64(1)}, {int64(5)}}}, {"v NOT BETWEEN 1 AND 3 AND v=0", [][]any{{int64(1)}}},
		{"v BETWEEN 1 AND 3 OR v=4", [][]any{{int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}}, {"v BETWEEN 1 AND 3 AND v=2", [][]any{{int64(3)}}},
	} {
		betweenReferenceBoth(t, p, referenceSelect("SELECT id FROM t WHERE "+tc.expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, tc.ids))
	}
	betweenReferenceBoth(t, p, referenceSelect("SELECT v BETWEEN 1 AND 3 IS TRUE FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {true}, {true}, {true}, {false}, {false}}))
	scalarStep(t, p, referenceExec("CREATE TABLE words(between INTEGER,\"symmetric\" INTEGER)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO words VALUES(2,3)", 1))
	betweenReferenceBoth(t, p, referenceSelect("SELECT between BETWEEN 1 AND 3,\"between\" BETWEEN 2 AND \"symmetric\" FROM words", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
}

func TestReferenceBetweenProjectionEndpoints(t *testing.T) {
	p := betweenReferenceSetup(t)
	betweenReferenceBoth(t, p, referenceSelect("SELECT v BETWEEN 1 AND 3,v NOT BETWEEN 1 AND 3,v BETWEEN SYMMETRIC 3 AND 1,v NOT BETWEEN SYMMETRIC 3 AND 1 FROM t ORDER BY id", []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}, {true, false, true, false}, {true, false, true, false}, {true, false, true, false}, {false, true, false, true}, {nil, nil, nil, nil}}))
}
