package plan

import (
 "context"
 "math"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqltype"
)

// Imported facts have probability mass, never synthetic integer sample counts.
// Wholly measured facts deliberately retain the exact-count path.
func estimateImportedComparison(ctx context.Context, s *storage.ColumnStatistics, rows float64, value sqltype.Value, op sqltype.ComparisonOperator) (selectivityTruth, error) {
 nonnull := selectivityUnit(1-s.NullFraction())
 var yes, mass float64
 if op == sqltype.CompareEqual || op == sqltype.CompareNotEqual {
  grouping, err := sqltype.BindGrouping(s.Usage); if err != nil { return selectivityTruth{}, err }
  key, err := grouping.PrepareKey(ctx, value); if err != nil { return selectivityTruth{}, err }
  hit := false
  least := 1.0
  for i:=range s.MCVLen() {
   v, frequency := s.MCVProbability(i)
   equal, err := grouping.Equal(ctx,v,key); if err != nil { return selectivityTruth{},err }
   mass += frequency; least = min(least, frequency)
   if equal { yes,hit=frequency,true }
  }
  if !hit {
   distinct, known := s.EncodedDistinct()
   if !known || distinct == 0 { distinct = min(200, max(1, rows)) } else if distinct < 0 { distinct = -distinct*rows }
   distinct = math.RoundToEven(min(max(1,distinct),1e100))
   yes = max(0,nonnull-mass)/max(1,distinct-float64(s.MCVLen()))
   if s.MCVLen()!=0 { yes=min(yes,least) }
  }
  if op==sqltype.CompareNotEqual { yes=max(0,nonnull-yes) }
 } else {
  ordering,err:=sqltype.BindOrdering(s.Usage); if err!=nil { return selectivityTruth{},nil }
  qualifies:=func(n int) bool { switch op { case sqltype.CompareLess: return n<0; case sqltype.CompareLessEqual:return n<=0;case sqltype.CompareGreater:return n>0;default:return n>=0 } }
  for i:=range s.MCVLen() {
   v,frequency:=s.MCVProbability(i); n,err:=ordering.Compare(ctx,v,value,sqltype.InputEnvironment{});if err!=nil{return selectivityTruth{},err};mass+=frequency;if qualifies(n){yes+=frequency}
  }
  residual:=max(0,nonnull-mass)
  if residual>0 {
   if s.HistogramLen()==0{return selectivityTruth{},nil}
   matching:=0
   for i:=range s.HistogramLen() {n,err:=ordering.Compare(ctx,s.HistogramValue(i),value,sqltype.InputEnvironment{});if err!=nil{return selectivityTruth{},err};if qualifies(n){matching++}}
   yes+=residual*float64(matching)/float64(s.HistogramLen())
  }
 }
 yes=min(max(yes,0),nonnull)
 return selectivityTruth{yes:yes,no:nonnull-yes,known:true},ctx.Err()
}
