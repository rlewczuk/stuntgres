package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceBinaryConversionConsumers(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE bc_source(id integer,s text,b bytea)", 0),
			referenceExec(`INSERT INTO bc_source VALUES(1,'é','\xe9'),(2,'',''),(3,NULL,NULL)`, 3),
			referenceExec("CREATE TABLE bc_target(id integer,b bytea,s text,CONSTRAINT bc_check CHECK(encode(b,'hex')=s))", 0),
			referenceExec("INSERT INTO bc_target SELECT id,convert_to(s,'LATIN1'),encode(convert_to(s,'LATIN1'),'hex') FROM bc_source", 3),
			referenceExec("CREATE VIEW bc_view AS SELECT id,decode(s,'hex') AS b,convert_from(b,'LATIN1') AS s FROM bc_target", 0),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		want := referenceSelect("SELECT b,s FROM bc_view ORDER BY id", []string{"b", "s"}, []string{"BYTEA", "TEXT"}, [][]any{{"\xe9", "é"}, {"", ""}, {nil, nil}})
		want.checkMetadata = binaryConversionViewMetadata(b.pg, want.columns, want.types)
		binaryConversionBoth(t, p, b, c, want)
		binaryConversionBoth(t, p, b, c, referenceSelect("SELECT encode((SELECT b FROM bc_source WHERE id=1),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"e9"}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT count(convert_to(s,'LATIN1')) AS n FROM bc_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("UPDATE bc_target SET b=decode('ff','hex'),s='ff' WHERE id=2", 1))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_bad(id integer,s text)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO bc_bad VALUES(10,'61'),(11,'0q')", 2))
		d := controlsReferenceDiagnostic("22023", `invalid hexadecimal digit: "q"`)
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic("INSERT INTO bc_target SELECT id,decode(s,'hex'),s FROM bc_bad ORDER BY id", d)
			s.errorStage = "execute"
			s.prepared = prepared
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,s FROM bc_target ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "e9"}, {int64(2), "ff"}, {int64(3), nil}}))
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO bc_bad VALUES(12,'€')", 1))
		d = controlsReferenceDiagnostic("22P05", `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`)
		s := referenceErrorDiagnostic("UPDATE bc_source SET b=convert_to(s,'LATIN1') WHERE id=1", d)
		// Row-late encoding failure, independently retaining every original row.
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO bc_source VALUES(4,'€','x')", 1))
		s.query = "UPDATE bc_source SET b=convert_to(s,'LATIN1')"
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, c, b.recorder, s)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,b FROM bc_source ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "\xe9"}, {int64(2), ""}, {int64(3), nil}, {int64(4), "x"}}))
	})
}
func TestReferenceBinaryConversionVolatility(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_generated(v bytea,s text GENERATED ALWAYS AS (encode(v,'hex')) STORED,b bytea GENERATED ALWAYS AS (decode(encode(v,'hex'),'hex')) STORED)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO bc_generated(v) VALUES('\x00ff')`, 1))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT s,b FROM bc_generated", []string{"s", "b"}, []string{"TEXT", "BYTEA"}, [][]any{{"00ff", "\x00\xff"}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE INDEX bc_immutable ON bc_generated(encode(v,'hex'))", 0))
		for _, expr := range []string{"convert(v,'LATIN1','UTF8')", "convert_to(encode(v,'hex'),'LATIN1')", "convert_from(v,'LATIN1')", "encode(convert(v,'LATIN1','UTF8'),'hex')"} {
			t.Run(expr, func(t *testing.T) {
				d := controlsReferenceDiagnostic("42P17", "generation expression is not immutable")
				referenceRunStep(t, p.ctx, c, b.recorder, referenceErrorDiagnostic("CREATE TABLE bc_stable(v bytea,s "+map[bool]string{true: "text", false: "bytea"}[expr == "convert_from(v,'LATIN1')" || expr == "encode(convert(v,'LATIN1','UTF8'),'hex')"]+" GENERATED ALWAYS AS ("+expr+") STORED)", d))
				d.Message = "functions in index expression must be marked IMMUTABLE"
				referenceRunStep(t, p.ctx, c, b.recorder, referenceErrorDiagnostic("CREATE INDEX bc_stable_index ON bc_generated(("+expr+"))", d))
			})
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_eliminated(v bytea,s text GENERATED ALWAYS AS (CASE WHEN true THEN encode(v,'hex') ELSE convert_from(v,'LATIN1') END) STORED)", 0))
	})
}
