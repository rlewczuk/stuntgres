package control

import (
 "context"
 "stuntgres/internal/engine/storage"
)

func emitDumpRelationStatistics(ctx context.Context,s *dumpScript,w *dumpWriter)error {
 for _,record:=range s.RelationStatistics {
  if err:=ctx.Err();err!=nil{return err}
  line,err:=storage.EncodeRelationStatisticsSnapshot(record);if err!=nil{return err}
  if err=w.emit(line);err!=nil{return err}
 }
 return ctx.Err()
}
