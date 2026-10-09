package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceIdentityLinkedSequenceSourceOrder(t *testing.T) {
	for shape, query := range []string{
		`INSERT INTO i OVERRIDING USER VALUE VALUES(nextval('i_id_seq'),10),(nextval('i_id_seq'),11)`,
		`INSERT INTO i OVERRIDING USER VALUE SELECT nextval('i_id_seq'),p FROM source`,
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("shape=%d/prepared=%t", shape, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						c := identityConnection(t, p.ctx, b.db)
						var executor referenceExecutor = c
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, executor, b.recorder, s) }
						run(referenceExec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, 0))
						run(referenceExec(`CREATE TABLE source(p INT)`, 0))
						run(referenceExec(`INSERT INTO source VALUES(10),(11)`, 2))
						if prepared {
							executor = identityHeld(t, p.ctx, c, query)
						}
						run(referenceExec(query, 2))
						want := [][]any{{int64(2), int64(10)}, {int64(4), int64(11)}}
						if b.pg && shape == 1 {
							identityUncoupledRows(t, p.ctx, c, b.recorder, `SELECT * FROM i ORDER BY id`, want, 2)
						} else {
							run(referenceSelect(`SELECT * FROM i ORDER BY id`, []string{"id", "p"}, []string{"INT4", "INT4"}, want))
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
				p := newReferenceScenario(t)
				p.step(t, referenceExec(tc.create, 0))
				s := referenceExec(tc.insert, 1)
				s.prepared = prepared
				p.step(t, s)
				p.step(t, referenceSelect(`SELECT * FROM i`, tc.columns, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				p.step(t, identitySequenceState("i_id_seq", 2, true))
			})
		}
	}
}
