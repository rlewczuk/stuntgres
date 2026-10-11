package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNotNullControls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec(`CREATE TABLE t(a INT CONSTRAINT "n""é\\tail" NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
	text := "O'Brien\n\\tail"
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), text, true)))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(8,NULL,NULL,NULL)", 1))
	p.Step(t, fixture.Exec("CREATE TABLE generated(a INT NOT NULL)", 0))
	p.Step(t, fixture.Exec("COMMENT ON TABLE t IS 'persistent'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CONSTRAINT "n""é\\tail" NOT NULL`) || !strings.Contains(dump.String(), `CONSTRAINT "generated_a_not_null" NOT NULL`) {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	expected := fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}})
	p.Step(t, expected)
	p.Step(t, fixture.Prepared(expected))
	for _, b := range p.Backends() {
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, expected.Query, expected.Columns, expected.Types, []bool{false, true, true, true}, 2)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String())
	}
	p.Step(t, fixture.Prepared(fixture.Exec(`ALTER TABLE t DROP CONSTRAINT "n""é\\tail"`, 0)))
	p.Step(t, fixture.Exec("ALTER TABLE generated DROP CONSTRAINT generated_a_not_null", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(NULL,NULL,NULL,NULL)", 1))
}
