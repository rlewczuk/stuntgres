package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCoreCopy(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE tr_copy(id integer,v integer)")
		c.exec(t, "CREATE FUNCTION tr_copy_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v*2; RETURN CASE WHEN NEW.id=2 THEN NULL ELSE NEW END; END$$")
		c.exec(t, "CREATE TRIGGER a BEFORE INSERT ON tr_copy FOR EACH ROW EXECUTE FUNCTION tr_copy_fn()")
		result, err := c.from("COPY tr_copy FROM STDIN", strings.NewReader("1\t5\n2\t5\n3\t5\n"))
		copyTextSuccess(t, result, err, 2)
		c.selectRows(t, "SELECT id,v FROM tr_copy ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(10)}})
	})
}
