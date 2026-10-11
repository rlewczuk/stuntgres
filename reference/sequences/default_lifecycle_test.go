package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDefaultExpressionsAndDescriptorLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) {
				s.Prepared = prepared
				if s.State == "42601" {
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
				}
				p.Step(t, s)
			}
			fixture.SchemaReferenceCleanup(t, "nested")
			for _, token := range []string{"AND", "OR"} {
				run(fixture.ImplicitError(`CREATE TABLE bad(b BOOL DEFAULT TRUE `+token+` FALSE)`, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: "38"}))
			}
			run(fixture.ImplicitError(`CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT NOT NULL)`, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "NOT"`, Position: "125"}))
			run(fixture.Exec(`CREATE TABLE e(id INT,b BOOL DEFAULT (1 < 2 AND NOT FALSE),n INT DEFAULT NULL,u BOOL DEFAULT (FALSE OR NULL),v BOOL DEFAULT (NOT NULL))`, 0))
			run(fixture.Exec(`INSERT INTO e(id) VALUES(1),(2)`, 2))
			run(fixture.Select(`SELECT * FROM e ORDER BY id`, []string{"id", "b", "n", "u", "v"}, []string{"INT4", "BOOL", "INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, nil, nil, nil}, {int64(2), true, nil, nil, nil}}))
			run(fixture.Exec(`CREATE TABLE source(id INT,first INT DEFAULT 7 NULL,last INT CONSTRAINT def DEFAULT 9 CONSTRAINT nn NOT NULL)`, 0))
			run(fixture.Exec(`CREATE TABLE clone(LIKE source)`, 0))
			run(fixture.Exec(`INSERT INTO clone(id,last) VALUES(1,3)`, 1))
			run(fixture.Select(`SELECT * FROM clone`, []string{"id", "first", "last"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, int64(3)}}))
			run(fixture.Exec(`INSERT INTO source(id) VALUES(1)`, 1))
			run(fixture.Exec(`COMMENT ON TABLE source IS 'preserve defaults'`, 0))
			run(fixture.Exec(`COMMENT ON COLUMN source.last IS 'surviving attribute'`, 0))
			run(fixture.Exec(`ALTER TABLE source ALTER COLUMN last DROP NOT NULL`, 0))
			run(fixture.Exec(`ALTER TABLE source ALTER COLUMN last SET NOT NULL`, 0))
			run(fixture.Exec(`CREATE TABLE IF NOT EXISTS source(i INT DEFAULT 'oops')`, 0))
			run(fixture.Exec(`ALTER TABLE source DROP COLUMN first`, 0))
			run(fixture.Exec(`INSERT INTO source(id) VALUES(2)`, 1))
			run(fixture.Select(`SELECT * FROM source ORDER BY id`, []string{"id", "last"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(9)}}))
			run(fixture.Exec(`CREATE SCHEMA nested CREATE TABLE d(i INT DEFAULT 17)`, 0))
			run(fixture.Exec(`INSERT INTO nested.d DEFAULT VALUES`, 1))
			run(fixture.Select(`SELECT * FROM nested.d`, []string{"i"}, []string{"INT4"}, [][]any{{int64(17)}}))
		})
	}
}

func TestReferenceDefaultSchemaAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "rejected")
			for _, backend := range p.Backends() {
				_, err := backend.DB.ExecContext(p.Ctx, `CREATE SCHEMA rejected CREATE TABLE good(i INT DEFAULT 1) CREATE TABLE bad(i INT DEFAULT 'oops')`)
				if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "22P02" {
					t.Fatalf("compound schema failure: %T %v", err, err)
				}
				rows, err := backend.DB.QueryContext(p.Ctx, `SELECT * FROM rejected.good`)
				if rows != nil {
					rows.Close()
					t.Fatal("failed schema published a cursor")
				}
				if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "42P01" {
					t.Fatalf("failed schema table absence: %T %v", err, err)
				}
			}
		})
	}
}
