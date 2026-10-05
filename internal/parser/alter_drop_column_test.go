package parser

import (
 "errors"
 "testing"
 "stuntgres/internal/sqlstate"
)

func TestAlterDropColumnSyntax(t *testing.T) {
 for _, q := range []string{`ALTER TABLE t DROP b`, `ALTER TABLE t DROP COLUMN b RESTRICT`, `ALTER TABLE t* DROP COLUMN IF EXISTS b CASCADE`, `ALTER TABLE ONLY t DROP b`, `ALTER TABLE ONLY(t) DROP COLUMN b`, `ALTER TABLE "S"."T" DROP COLUMN "COLUMN"`} {
  statements, err := Parse(q)
  if err != nil || len(statements) != 1 { t.Fatal(q, statements, err) }
  s, ok := statements[0].(*AlterTableDropColumn)
  if !ok || s.Pos.Character != 1 || s.Column.Pos.Character == 0 { t.Fatal(q, s) }
 }
 for _, q := range []string{`ALTER TABLE t DROP COLUMN )`, `ALTER TABLE t DROP b, DROP c`, `ALTER TABLE t DROP b garbage`, `ALTER TABLE ONLY(t DROP b`, `ALTER TABLE t DROP COLUMN b; SELECT * FROM`} {
  if s, err := Parse(q); err == nil || s != nil { t.Fatal("accepted incomplete input", q, s, err) }
 }
}

func TestAlterDropColumnRestoreDefaultMarker(t *testing.T) {
 q := "INSERT INTO z DEFAULT VALUES"
 _, err := Parse(q)
 var got *sqlstate.Error
 want := sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"0A000", Message:"INSERT defaults are unsupported", Position:"15"}
 if !errors.As(err, &got) || *got != want { t.Fatal(err, got) }
 statements, err := ParseWithOptions(q, Options{StandardConformingStrings:true, RestoreDefaultValues:true})
 if err != nil { t.Fatal(err) }
 s := statements[0].(*Insert)
 if !s.RestoreDefaultValues || len(s.Rows) != 1 || len(s.Rows[0]) != 0 || len(s.Targets) != 0 { t.Fatal(s) }
 _, err = Parse("INSERT INTO z VALUES ()")
 want = sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:`syntax error at or near ")"`, Position:"23"}
 if !errors.As(err, &got) || *got != want { t.Fatal(err, got) }
}

func TestAlterDropColumnCapabilityBoundaries(t *testing.T) {
 for _,tc:=range []struct{q,message,position string}{
  {"ALTER TABLE t DROP b, DROP c","ALTER TABLE action lists are unsupported","21"},
  {"ALTER TABLE t ADD COLUMN c INT","ALTER TABLE action is unsupported","15"},
 } {
  _,err:=Parse(tc.q);var got *sqlstate.Error
  want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"0A000",Message:tc.message,Position:tc.position}
  if !errors.As(err,&got)||*got!=want{t.Fatal(got,err,want)}
 }
}

func TestAlterDropColumnCharacterPositions(t *testing.T) {
 _,err:=Parse("/*é*/ ALTER TABLE t DROP COLUMN )");var got *sqlstate.Error
 want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42601",Message:`syntax error at or near ")"`,Position:"33"}
 if !errors.As(err,&got)||*got!=want{t.Fatal(err,got)}
}
