package sqltype

import (
 "errors"
 "stuntgres/internal/sqlstate"
 "testing"
)

func TestTimestampRangeComparisonAdmission(t *testing.T) {
 types := testTypes()
 d := types[TimestampRange]
 domain,err := Domain(20000,"span","span",d.Usage()); if err != nil { t.Fatal(err) }
 nested,err := Domain(20001,"chain","chain",domain.Usage()); if err != nil { t.Fatal(err) }
 for _, pair := range [][2]*Descriptor{{d,d},{domain,domain},{nested,nested},{domain,d},{d,nested},{domain,nested},{nested,domain}} {
  assertRejected := func(err error) {
   t.Helper()
   want := sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42883",Message:"incompatible comparison operands"}
   var got *sqlstate.Error
   if !errors.As(err,&got) || *got != want { t.Fatalf("comparison admission %s/%s got=%#v want=%#v err=%v",pair[0].Name(),pair[1].Name(),got,want,err) }
  }
  assertRejected(CheckComparisonTypes(pair[0],pair[1]))
  _,err := BindComparison(pair[0],pair[1],CollationRef{},false); assertRejected(err)
  for _, op := range []ComparisonOperator{CompareEqual,CompareNotEqual,CompareLess,CompareLessEqual,CompareGreater,CompareGreaterEqual} {
   assertRejected(CheckComparisonOperator(op,pair[0],pair[1]))
   _,err := BindComparisonPredicate(op,pair[0],pair[1],types[Bool],CollationRef{},false); assertRejected(err)
  }
 }
}
