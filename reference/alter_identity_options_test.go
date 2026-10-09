package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterIdentityOptions(t *testing.T) {
	alterIdentityScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(START 7 INCREMENT 3 CACHE 1))`, 0))
		draw := func(want int64) {
			run(referenceExec(`INSERT INTO i DEFAULT VALUES`, 1))
			run(referenceSelect(`SELECT last_value,is_called FROM i_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{want, true}}))
		}
		draw(7)
		draw(10)
		run(referenceExec(`ALTER TABLE i ALTER id SET START 20`, 0))
		draw(13)
		for _, tc := range []struct {
			suffix string
			want   int64
		}{{"RESTART", 20}, {"RESTART 30", 30}, {"RESTART WITH 40", 40}} {
			run(referencePrepared(referenceExec(`ALTER TABLE i ALTER id `+tc.suffix, 0)))
			draw(tc.want)
		}
		run(referenceExec(`ALTER TABLE i ALTER id SET INCREMENT 2 SET MINVALUE 1 SET MAXVALUE 3 SET START 1 RESTART 1 SET CACHE 1 SET CYCLE`, 0))
		draw(1)
		draw(3)
		draw(1)
		run(referenceExec(`ALTER TABLE i ALTER id SET NO CYCLE`, 0))
		draw(3)
		run(referenceImplicitError(`INSERT INTO i DEFAULT VALUES`, referenceDiagnostic{Code: "2200H", Message: `nextval: reached maximum value of sequence "i_id_seq" (3)`}))
		run(referenceExec(`ALTER TABLE i ALTER id SET NO MINVALUE SET NO MAXVALUE SET INCREMENT 1 RESTART 4`, 0))
		draw(4)
		draw(5)
		run(referenceSelect(`SELECT * FROM i ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(3)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(7)}, {int64(10)}, {int64(13)}, {int64(20)}, {int64(30)}, {int64(40)}}))
		for _, tc := range []struct{ suffix, code, message, position string }{
			{"SET INCREMENT 0", "22023", "INCREMENT must not be zero", ""},
			{"SET CACHE 0", "22023", "CACHE (0) must be greater than zero", ""},
			{"SET CACHE 2 SET CACHE 3", "42601", "conflicting or redundant options", "40"},
			{"SET MINVALUE 9 SET MAXVALUE 8", "22023", "MINVALUE (9) must be less than MAXVALUE (8)", ""},
			{"RESTART WITH 2147483648", "22023", "RESTART value (2147483648) cannot be greater than MAXVALUE (2147483647)", ""},
			{"SET AS bigint", "42601", `sequence option "as" not supported here`, "28"},
			{"SET OWNED BY NONE", "42601", `sequence option "owned_by" not supported here`, "28"},
			{"SET RESTART 1", "42601", `sequence option "restart" not supported here`, "28"},
		} {
			t.Run(tc.suffix, func(t *testing.T) {
				before := ""
				if !b.pg {
					before = referenceDDLNativeDump(t, p)
				}
				s := referenceImplicitError(`ALTER TABLE i ALTER id `+tc.suffix, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.errorStage = "execute"
				run(s)
				if !b.pg && before != referenceDDLNativeDump(t, p) {
					t.Fatal("invalid option published")
				}
				run(referenceSelect(`SELECT last_value,is_called FROM i_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(5), true}}))
			})
		}
		draw(6)
	})
}
