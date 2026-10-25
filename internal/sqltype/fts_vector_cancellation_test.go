package sqltype

import (
 "context"
 "errors"
 "fmt"
 "strings"
 "testing"
)
type vectorFunctionCancelContext struct {context.Context;remaining int}
func(c *vectorFunctionCancelContext)Err()error{c.remaining--;if c.remaining<=0{return context.Canceled};return nil}
func TestFTSVectorFunctionsExtractionCancellation(t *testing.T){
 types:=testTypes();var input strings.Builder
 for i:=range 2048{fmt.Fprintf(&input,"w%04d:1B ",i)}
 source,err:=types[TSVector].Input(context.Background(),input.String(),-1);if err!=nil{t.Fatal(err)}
 p,err:=BindFTSVector(FTSVectorToArray,types[1009].Usage(),[3]Usage{types[TSVector].Usage()});if err!=nil{t.Fatal(err)}
 c:=&vectorFunctionCancelContext{Context:context.Background(),remaining:4}
 if v,err:=p.Evaluate(c,[3]Value{source});!errors.Is(err,context.Canceled)||v.Type()!=nil||c.remaining!=0{t.Fatal("interior extraction cancellation",v,err)}
 empty,err:=types[TSVector].Input(context.Background(),"",-1);if err!=nil{t.Fatal(err)}
 v,err:=p.Evaluate(context.Background(),[3]Value{empty});if err!=nil||v.IsNull()||v.ArrayRank()!=0||v.ArrayLen()!=0{t.Fatal("empty owned array",err)}
}
