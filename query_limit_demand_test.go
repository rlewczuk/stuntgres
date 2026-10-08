package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryLimitDemand(t *testing.T) {
	for _, query := range []string{
		"SELECT 10/v AS n FROM t LIMIT 1",
		"SELECT 10/v AS n FROM t WHERE 10/v > 0 LIMIT 1",
		"SELECT 10/t.v AS n FROM t CROSS JOIN u WHERE 10/t.v > 0 LIMIT 1",
	} {
		t.Run(query, func(t *testing.T) {
			db := queryLimitFixture(t)
			execSQL(t, db, "CREATE TABLE u(x INT)")
			execSQL(t, db, "INSERT INTO u VALUES(1)")
			queryLimitSelect(t, db, query, "n", "INT4", queryLimitIDs(5))
		})
	}
	t.Run("zero avoids predicate and key", func(t *testing.T) {
		db := queryLimitFixture(t)
		queryLimitSelect(t, db, "SELECT 10/v AS n FROM t WHERE 10/v > 0 ORDER BY 10/v LIMIT 0", "n", "INT4", queryLimitIDs())
	})
	for _, key := range []string{"id", "n"} {
		t.Run("sort evaluates cheap target/"+key, func(t *testing.T) {
			db := queryLimitFixture(t)
			for _, prepared := range []bool{false, true} {
				rows, closeQuery, err := transactionQuery(t.Context(), db, "SELECT 10/v AS n FROM t ORDER BY "+key+" LIMIT 1", prepared)
				if closeQuery != nil {
					closeQuery()
				}
				if rows != nil {
					rows.Close()
					t.Fatal("sort failure exposed rows")
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
			}
		})
	}
	for _, suffix := range []string{"LIMIT 0", "OFFSET 1 LIMIT 1", "LIMIT 1"} {
		t.Run("projection/"+suffix, func(t *testing.T) {
			db := queryLimitFixture(t)
			execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
			want := queryLimitIDs(1)
			next := int64(2)
			if suffix == "LIMIT 0" {
				want = queryLimitIDs()
				next = 1
			}
			if suffix == "OFFSET 1 LIMIT 1" {
				want = queryLimitIDs(2)
				next = 3
			}
			rows, err := db.Query("SELECT nextval('s') AS n FROM t ORDER BY id " + suffix)
			queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, want, false)
			rows, err = db.Query("SELECT nextval('s')")
			queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, queryLimitIDs(next), false)
		})
	}
	t.Run("bound once each execution offset before limit", func(t *testing.T) {
		db := queryLimitFixture(t)
		execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
		stmt, err := db.Prepare("SELECT id FROM t ORDER BY id LIMIT nextval('s') OFFSET nextval('s')")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		// Prepare draws nothing. Execution draws offset1 then count2.
		rows, err := stmt.Query()
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(2, 3), false)
		rows, err = stmt.Query()
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(4), false)
		rows, err = db.Query("SELECT nextval('s')")
		queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, queryLimitIDs(5), false)
	})
	t.Run("empty source still evaluates bound", func(t *testing.T) {
		db := queryLimitFixture(t)
		execSQL(t, db, "CREATE TABLE empty_t(id INT)")
		execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
		rows, err := db.Query("SELECT id FROM empty_t LIMIT nextval('s')")
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(), false)
		rows, err = db.Query("SELECT nextval('s')")
		queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, queryLimitIDs(2), false)
		rows, err = db.Query("SELECT id FROM empty_t LIMIT -1")
		if rows != nil {
			rows.Close()
			t.Fatal("empty source bypassed negative bound")
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "2201W", Message: "LIMIT must not be negative"})
	})
	for _, suffix := range []string{"LIMIT 0", "OFFSET 1", "LIMIT 1"} {
		t.Run("selected singleton/"+suffix, func(t *testing.T) {
			db := queryLimitFixture(t)
			execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
			want := queryLimitIDs()
			next := int64(1)
			if suffix == "LIMIT 1" {
				want = queryLimitIDs(1)
				next = 2
			}
			if suffix == "OFFSET 1" {
				next = 2
			}
			rows, err := db.Query("SELECT nextval('s') " + suffix)
			queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, want, false)
			rows, err = db.Query("SELECT nextval('s')")
			queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, queryLimitIDs(next), false)
		})
	}
}

func TestQueryLimitOrderCache(t *testing.T) {
	for _, key := range []string{"n", "1", "nextval('s')"} {
		for _, prepared := range []bool{false, true} {
			t.Run(key+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				db := queryLimitFixture(t)
				execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
				query := "SELECT nextval('s') AS n FROM t ORDER BY " + key + " DESC OFFSET 1 LIMIT 2"
				rows, closeQuery, err := transactionQuery(t.Context(), db, query, prepared)
				if closeQuery != nil {
					defer closeQuery()
				}
				queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, queryLimitIDs(3, 2), false)
				rows, err = db.Query("SELECT nextval('s')")
				queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, queryLimitIDs(5), false)
			})
		}
	}
}

func TestQueryLimitViews(t *testing.T) {
	for _, barrier := range []string{"", " WITH (security_barrier=true)"} {
		for _, suffix := range []string{"LIMIT 0", "LIMIT 1"} {
			t.Run(barrier+suffix, func(t *testing.T) {
				db := queryLimitFixture(t)
				execSQL(t, db, "CREATE SEQUENCE s START WITH 1")
				execSQL(t, db, "CREATE VIEW inner_v"+barrier+" AS SELECT id,nextval('s') AS n FROM t")
				execSQL(t, db, "CREATE VIEW outer_v AS SELECT id,n FROM inner_v")
				want := queryLimitIDs(1)
				next := int64(2)
				if suffix == "LIMIT 0" {
					want = queryLimitIDs()
					next = 1
				}
				rows, err := db.Query("SELECT n FROM outer_v " + suffix)
				queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, want, false)
				rows, err = db.Query("SELECT nextval('s')")
				queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, queryLimitIDs(next), false)
			})
		}
	}
	t.Run("persistent decorated definition", func(t *testing.T) {
		db := queryLimitFixture(t)
		execSQL(t, db, "CREATE VIEW limited AS SELECT id FROM t ORDER BY id OFFSET 1 FETCH NEXT 2 ROWS ONLY")
		queryLimitSelect(t, db, "SELECT id FROM limited ORDER BY id", "id", "INT4", queryLimitIDs(2, 3))
		execSQL(t, db, "ALTER TABLE t RENAME COLUMN id TO renamed")
		queryLimitSelect(t, db, "SELECT id FROM limited ORDER BY id", "id", "INT4", queryLimitIDs(2, 3))
	})
}

func TestQueryLimitInsert(t *testing.T) {
	db := queryLimitFixture(t)
	execSQL(t, db, "CREATE TABLE dst(id INT)")
	for _, tc := range []struct {
		query string
		count int64
	}{
		{"INSERT INTO dst SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 2", 2},
		{"INSERT INTO dst SELECT id FROM t LIMIT 0", 0},
		{"INSERT INTO dst VALUES(3),(1),(2) ORDER BY column1 OFFSET 1 FETCH NEXT 1 ROW ONLY", 1},
		{"INSERT INTO dst (VALUES(8),(9) LIMIT 1)", 1},
		{"INSERT INTO dst (SELECT 10 LIMIT 0)", 0},
	} {
		result, err := db.Exec(tc.query)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != tc.count {
			t.Fatal(tc.query, count, err)
		}
	}
	queryLimitSelect(t, db, "SELECT id FROM dst ORDER BY id", "id", "INT4", queryLimitIDs(2, 2, 3, 8))
}
