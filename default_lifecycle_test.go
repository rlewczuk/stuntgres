package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestDefaultExpressionsAndDescriptorLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
			for _, token := range []string{"AND", "OR"} {
				query := `CREATE TABLE bad(b BOOL DEFAULT TRUE ` + token + ` FALSE)`
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(query)
					if stmt != nil {
						stmt.Close()
					}
					err = prepareErr
				} else {
					_, err = db.Exec(query)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: "38"})
			}
			const invalidLogical = `CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT NOT NULL)`
			var syntaxErr error
			if prepared {
				stmt, err := db.Prepare(invalidLogical)
				if stmt != nil {
					stmt.Close()
				}
				syntaxErr = err
			} else {
				_, syntaxErr = db.Exec(invalidLogical)
			}
			typeCoreDiagnostic(t, syntaxErr, stuntgres.Error{Code: "42601", Message: `syntax error at or near "NOT"`, Position: "125"})
			exec(`CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT (NOT NULL))`, 0)
			exec(`INSERT INTO e(id) VALUES(1),(2)`, 2)
			wantTuples(t, db, `SELECT * FROM e ORDER BY id`, [][]any{{int64(1), true, nil, nil, nil}, {int64(2), true, nil, nil, nil}})
			exec(`CREATE TABLE source(id INT,first INT DEFAULT 7 NULL,last INT CONSTRAINT def DEFAULT 9 CONSTRAINT nn NOT NULL)`, 0)
			exec(`CREATE TABLE clone(LIKE source)`, 0)
			exec(`INSERT INTO clone(id,last) VALUES(1,3)`, 1)
			wantTuples(t, db, `SELECT * FROM clone`, [][]any{{int64(1), nil, int64(3)}})
			exec(`INSERT INTO source(id) VALUES(1)`, 1)
			exec(`COMMENT ON TABLE source IS 'preserve defaults'`, 0)
			exec(`COMMENT ON COLUMN source.last IS 'surviving attribute'`, 0)
			exec(`ALTER TABLE source ALTER COLUMN last DROP NOT NULL`, 0)
			exec(`ALTER TABLE source ALTER COLUMN last SET NOT NULL`, 0)
			exec(`CREATE TABLE IF NOT EXISTS source(i INT DEFAULT 'oops')`, 0)
			exec(`ALTER TABLE source DROP COLUMN first`, 0)
			exec(`INSERT INTO source(id) VALUES(2)`, 1)
			wantTuples(t, db, `SELECT * FROM source ORDER BY id`, [][]any{{int64(1), int64(9)}, {int64(2), int64(9)}})
			exec(`CREATE SCHEMA nested CREATE TABLE d(i INT DEFAULT 17)`, 0)
			exec(`INSERT INTO nested.d DEFAULT VALUES`, 1)
			wantTuples(t, db, `SELECT * FROM nested.d`, [][]any{{int64(17)}})
			_, err := db.Exec(`CREATE SCHEMA rejected CREATE TABLE good(i INT DEFAULT 1) CREATE TABLE bad(i INT DEFAULT 'oops')`)
			sqlState(t, err, "22P02")
			_, err = db.Query(`SELECT * FROM rejected.good`)
			sqlState(t, err, "42P01")
		})
	}
}
