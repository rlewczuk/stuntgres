package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitwiseTypedNullBothSides(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE one(v INTEGER)", 0))
	p.Step(t, fixture.Exec("INSERT INTO one VALUES(1)", 1))
	for _, tc := range []struct{ q, typ string }{
		{"SELECT NULL::smallint & 1::smallint,1::smallint & NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint | 1::smallint,1::smallint | NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint # 1::smallint,1::smallint # NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint << 1::integer,1::smallint << NULL::integer FROM one", "INT2"},
		{"SELECT NULL::smallint >> 1::integer,1::smallint >> NULL::integer FROM one", "INT2"},
		{"SELECT NULL::integer & 1::integer,1::integer & NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer | 1::integer,1::integer | NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer # 1::integer,1::integer # NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer << 1::integer,1::integer << NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer >> 1::integer,1::integer >> NULL::integer FROM one", "INT4"},
		{"SELECT NULL::bigint & 1::bigint,1::bigint & NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint | 1::bigint,1::bigint | NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint # 1::bigint,1::bigint # NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint << 1::integer,1::bigint << NULL::integer FROM one", "INT8"},
		{"SELECT NULL::bigint >> 1::integer,1::bigint >> NULL::integer FROM one", "INT8"},
	} {
		s := fixture.Select(tc.q, []string{"?column?", "?column?"}, []string{tc.typ, tc.typ}, [][]any{{nil, nil}})
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}

func TestReferenceBitwiseConstantNullRowConversion(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE null_input(id INTEGER,x TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO null_input VALUES(1,'bad'),(2,'7')", 2))
	for _, width := range []struct{ sql, typ, cast string }{
		{"smallint", "INT2", "CAST(CAST(x AS integer) AS smallint)"},
		{"integer", "INT4", "CAST(x AS integer)"},
		{"bigint", "INT8", "CAST(x AS bigint)"},
	} {
		for _, op := range []string{"&", "|", "#", "<<", ">>"} {
			leftPeer, rightNull := width.cast, "NULL::"+width.sql
			if op == "<<" || op == ">>" {
				leftPeer, rightNull = "CAST(x AS integer)", "NULL::integer"
			}
			q := "SELECT NULL::" + width.sql + " " + op + " " + leftPeer + "," + width.cast + " " + op + " " + rightNull + " FROM null_input ORDER BY id"
			step := fixture.Select(q, []string{"?column?", "?column?"}, []string{width.typ, width.typ}, [][]any{{nil, nil}, {nil, nil}})
			t.Run(width.sql+"/"+op, func(t *testing.T) { referenceArithmeticMetadata(t, p, step) })
			empty := fixture.Select(strings.Replace(q, " ORDER BY id", " WHERE FALSE ORDER BY id", 1), []string{"?column?", "?column?"}, []string{width.typ, width.typ}, [][]any{})
			t.Run(width.sql+"/"+op+"/empty", func(t *testing.T) {
				referenceArithmeticMetadata(t, p, empty)
			})
		}
	}
}

func TestReferenceBitwiseConstantNullConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE null_source(id INTEGER,x TEXT,i INTEGER,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO null_source VALUES(1,'bad',9,9),(2,'7',9,9)", 2))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT COALESCE(NULL::integer & CAST(x AS integer),5) AS fallback,CASE WHEN id=1 THEN CAST(x AS bigint) >> NULL::integer ELSE NULL::bigint END AS mask FROM null_source ORDER BY id", []string{"fallback", "mask"}, []string{"INT4", "INT8"}, [][]any{{int64(5), nil}, {int64(5), nil}}),
		fixture.Select("SELECT COALESCE(NULL::integer & CAST(x AS integer),5),CASE WHEN id=1 THEN CAST(x AS bigint) >> NULL::integer ELSE NULL::bigint END FROM null_source ORDER BY id", []string{"coalesce", "case"}, []string{"INT4", "INT8"}, [][]any{{int64(5), nil}, {int64(5), nil}}),
		fixture.Select("SELECT id FROM null_source WHERE (CAST(x AS integer) # NULL::integer) IS NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE VIEW null_view AS SELECT id,NULL::integer | CAST(x AS integer) AS mask FROM null_source", 0))
	step := fixture.Select("SELECT mask FROM null_view ORDER BY id", []string{"mask"}, []string{"INT4"}, [][]any{{nil}, {nil}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	p.Step(t, fixture.Exec("CREATE TABLE null_dest(id INTEGER,s SMALLINT,b BIGINT)", 0))
	for _, prepared := range []bool{false, true} {
		for _, step := range []fixture.Step{
			fixture.Exec("INSERT INTO null_dest SELECT id,NULL::smallint << CAST(x AS integer),CAST(x AS bigint) & NULL::bigint FROM null_source ORDER BY id", 2),
			fixture.Exec("UPDATE null_source SET i=CAST(x AS integer) | NULL::integer,b=NULL::bigint >> CAST(x AS integer)", 2),
		} {
			if prepared {
				step = fixture.Prepared(step)
			}
			p.Step(t, step)
		}
		p.Step(t, fixture.Select("SELECT i,b FROM null_source ORDER BY id", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{nil, nil}, {nil, nil}}))
		p.Step(t, fixture.Prepared(fixture.Select("SELECT i,b FROM null_source ORDER BY id", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{nil, nil}, {nil, nil}})))
	}
	p.Step(t, fixture.Select("SELECT s,b FROM null_dest ORDER BY id", []string{"s", "b"}, []string{"INT2", "INT8"}, [][]any{{nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT s,b FROM null_dest ORDER BY id", []string{"s", "b"}, []string{"INT2", "INT8"}, [][]any{{nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}})))
	dependency := fixture.ImplicitError("ALTER TABLE null_source DROP COLUMN x", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column x of table null_source because other objects depend on it", Detail: "view null_view depends on column x of table null_source", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	dependency.ErrorStage = "execute"
	referenceArithmeticFailure(t, p, dependency)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(step))
	fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{p.Memory, t}, p.MemoryRecord, dependency)
}
