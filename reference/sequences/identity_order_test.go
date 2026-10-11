package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentityLinkedSequenceSourceOrder(t *testing.T) {
	for shape, query := range []string{
		`INSERT INTO i OVERRIDING USER VALUE VALUES(nextval('i_id_seq'),10),(nextval('i_id_seq'),11)`,
		`INSERT INTO i OVERRIDING USER VALUE SELECT nextval('i_id_seq'),p FROM source`,
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("shape=%d/prepared=%t", shape, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c := fixture.IdentityConnection(t, p.Ctx, b.DB)
						var executor fixture.Executor = c
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
						run(fixture.Exec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, 0))
						run(fixture.Exec(`CREATE TABLE source(p INT)`, 0))
						run(fixture.Exec(`INSERT INTO source VALUES(10),(11)`, 2))
						if prepared {
							executor = fixture.IdentityHeld(t, p.Ctx, c, query)
						}
						run(fixture.Exec(query, 2))
						want := [][]any{{int64(2), int64(10)}, {int64(4), int64(11)}}
						if b.PG && shape == 1 {
							identityUncoupledRows(t, p.Ctx, c, b.Recorder, `SELECT * FROM i ORDER BY id`, want, 2)
						} else {
							run(fixture.Select(`SELECT * FROM i ORDER BY id`, []string{"id", "p"}, []string{"INT4", "INT4"}, want))
						}
						run(identitySequenceState("i_id_seq", 4, true))
					})
				}
			})
		}
	}
}
func TestReferenceIdentitySingleDestinationProjectionOrder(t *testing.T) {
	for _, tc := range []struct {
		name, create, insert string
		columns              []string
	}{
		{"payload_before_identity", `CREATE TABLE i(p INT,id INT GENERATED ALWAYS AS IDENTITY)`, `INSERT INTO i(p) VALUES(nextval('i_id_seq'))`, []string{"p", "id"}},
		{"reordered_targets", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, `INSERT INTO i(p,id) OVERRIDING USER VALUE VALUES(nextval('i_id_seq'),99)`, []string{"id", "p"}},
		{"identity_before_payload", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, `INSERT INTO i(p) VALUES(nextval('i_id_seq'))`, []string{"id", "p"}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec(tc.create, 0))
				s := fixture.Exec(tc.insert, 1)
				s.Prepared = prepared
				p.Step(t, s)
				p.Step(t, fixture.Select(`SELECT * FROM i`, tc.columns, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				p.Step(t, identitySequenceState("i_id_seq", 2, true))
			})
		}
	}
}
