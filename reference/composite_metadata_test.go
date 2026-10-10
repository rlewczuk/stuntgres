package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceCompositeMetadata(t *testing.T) {
	for _, rowType := range []bool{false, true} {
		name := "named"
		if rowType {
			name = "table_row"
		}
		t.Run(name, func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				if rowType {
					compositeReferenceSetup(t, p, b, c, `CREATE TABLE tested(i integer,t text)`)
				} else {
					compositeReferenceSetup(t, p, b, c, `CREATE TYPE tested AS (i integer,t text)`)
				}
				compositeReferenceSetup(t, p, b, c, `CREATE TABLE holder(k integer,v tested)`)
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES (1,'(7,x)'),(2,NULL),(3,'(,)')`, 3))
				for _, prepared := range []bool{false, true} {
					compositeReferenceRows(t, p, b, c, `SELECT v AS v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {nil}, {"(,)"}}, prepared)
					compositeReferenceRows(t, p, b, c, `SELECT v FROM holder WHERE k=0`, [][]any{}, prepared)
					s := referenceSelect(`SELECT $1::tested AS v`, []string{"v"}, []string{""}, [][]any{{nil}}, nil)
					s.prepared = prepared
					s.checkMetadata = enumReferenceMetadata(b.pg, false)
					enumReferenceRunStep(t, p.ctx, c, b.recorder, s)
				}
			})
		})
	}
}

func TestReferenceCompositeChildEnvironment(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TYPE mood AS ENUM ('ok','')`, `CREATE DOMAIN limited AS varchar(3)`, `CREATE TABLE referenced(i integer)`, `CREATE TYPE environmental AS (m mood,d limited,a integer[],r regclass,t regtype,dt date,tm timestamp(2),bytes bytea)`, `CREATE TABLE holder(v environmental)`)
		input := `(ok,abc,"{1,2}",referenced,environmental,2020-01-02,"2020-01-02 03:04:05.126","\\x0041")`
		referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(referenceExec(`INSERT INTO holder VALUES ($1)`, 1, input)))
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{`(ok,abc,"{1,2}",referenced,environmental,2020-01-02,"2020-01-02 03:04:05.13","\\x0041")`}}, true)
		compositeReferenceSetup(t, p, b, c, `ALTER TABLE referenced RENAME TO renamed`)
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{`(ok,abc,"{1,2}",renamed,environmental,2020-01-02,"2020-01-02 03:04:05.13","\\x0041")`}}, false)
	})
}
