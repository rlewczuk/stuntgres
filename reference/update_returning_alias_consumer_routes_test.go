package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningAliasesConsumerRoutes(t *testing.T) {
	t.Run("target alias Exec", func(t *testing.T) {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			result, err := db.Exec("CREATE TABLE t(id INT)")
			deleteReferenceCount(t, result, err, 0)
			result, err = db.Exec("INSERT INTO t VALUES(1)")
			deleteReferenceCount(t, result, err, 1)
			result, err = db.Exec("UPDATE t x SET id=2 RETURNING x.id")
			deleteReferenceCount(t, result, err, 1)
			rows, err := db.Query("SELECT id FROM t")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(2)}}})
		}
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
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			db := backend.db
			updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT NOT NULL,a INT)")
			updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,10)")
			names := []string{"a", "a"}
			types := []string{"INT4", "INT4"}
			if tc.options == "" {
				names = []string{"id", "a", "id", "a", "id", "a", "id", "a", "id", "a"}
				types = []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}
			}
			rows, err := db.Query("UPDATE t SET a=a+1 RETURNING " + tc.options + " " + tc.output)
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: names, types: types, rows: [][]any{tc.want}, bag: false})
		}
	}
}

func TestReferenceUpdateReturningExpressionsConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(a INT,v TEXT)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(-10,NULL)")
		rows, err := db.Query("UPDATE t SET a=11 RETURNING CAST(old.a AS BIGINT),new.a::BIGINT,abs(old.a),CASE WHEN old.a<0 THEN new.a ELSE old.a END,COALESCE(v,'fallback'),NULLIF(new.a,11),old.a IS DISTINCT FROM new.a,'literal',$1 AS \"Label\"", "parameter")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"a", "a", "abs", "a", "coalesce", "nullif", "?column?", "?column?", "Label"}, types: []string{"INT8", "INT8", "INT4", "INT4", "TEXT", "INT4", "BOOL", "TEXT", "TEXT"}, rows: [][]any{{int64(-10), int64(11), int64(10), int64(11), "fallback", nil, true, "literal", "parameter"}}, bag: false})
	}
}
