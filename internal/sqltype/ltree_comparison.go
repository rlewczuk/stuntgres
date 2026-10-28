package sqltype

import (
 "context"
 "fmt"
)

// compareLtree walks immutable label spans; separators are not label bytes.
func compareLtree(ctx context.Context,a,b *ltreePayload)(int,error){
 for i:=0;i<len(a.labels)&&i<len(b.labels);i++ {
  if err:=ctx.Err();err!=nil{return 0,err}
  x,y:=a.labels[i],b.labels[i]
  left,right:=a.text[x.start:x.end],b.text[y.start:y.end]
  for j:=0;j<len(left)&&j<len(right);j++ {
   if j&255==0 {if err:=ctx.Err();err!=nil{return 0,err}}
   if left[j]<right[j]{return -1,nil};if left[j]>right[j]{return 1,nil}
  }
  if len(left)<len(right){return -1,nil};if len(left)>len(right){return 1,nil}
 }
 if len(a.labels)<len(b.labels){return -1,ctx.Err()};if len(a.labels)>len(b.labels){return 1,ctx.Err()}
 return 0,ctx.Err()
}

// LtreeHash implements the release's per-label uint32 fold without flattening.
func LtreeHash(ctx context.Context,v Value)(uint32,error){
 if err:=ctx.Err();err!=nil{return 0,err}
 if !v.typ.IsLtree()||v.null||!v.validLtree(){return 0,fmt.Errorf("invalid ltree hash value")}
 result:=uint32(1)
 for _,span:=range v.ltree.labels {
  if err:=ctx.Err();err!=nil{return 0,err}
  h,err:=partitionHashBytes(ctx,v.ltree.text[span.start:span.end],0);if err!=nil{return 0,err}
  result=result*31+uint32(h)
 }
 return result,ctx.Err()
}

// LtreeHashExtended preserves the seed bit pattern and uint64 wraparound.
func LtreeHashExtended(ctx context.Context,v Value,seed uint64)(uint64,error){
 if err:=ctx.Err();err!=nil{return 0,err}
 if !v.typ.IsLtree()||v.null||!v.validLtree(){return 0,fmt.Errorf("invalid ltree hash value")}
 if len(v.ltree.labels)==0{return 1+seed,ctx.Err()}
 result:=uint64(1)
 for _,span:=range v.ltree.labels {
  if err:=ctx.Err();err!=nil{return 0,err}
  h,err:=partitionHashBytes(ctx,v.ltree.text[span.start:span.end],seed);if err!=nil{return 0,err}
  result=result*31+h
 }
 return result,ctx.Err()
}
