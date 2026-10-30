package control

import (
 "context"
 "stuntgres/internal/engine/storage"
)
func emitDumpAttributeStatistics(ctx context.Context,s *dumpScript,w *dumpWriter)error{
 for _,record:=range s.AttributeStatistics{if err:=ctx.Err();err!=nil{return err};line,err:=storage.EncodeAttributeStatisticsSnapshot(record);if err!=nil{return err};if err=w.emit(line);err!=nil{return err}}
 return ctx.Err()
}
