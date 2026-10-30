package storage

import (
 "bytes"
 "context"
 "encoding/base64"
 "encoding/json"
 "fmt"
 "io"
 "math"
 "slices"
 "strings"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
)

const RelationStatisticsSnapshotMarker = "stuntgres-relation-statistics-v1"

type RelationStatisticsSnapshot struct {
 Schema string
 Relation string
 Mask uint8
 Pages int32
 TupleBits uint32
 AllVisible int32
 AllFrozen int32
}

func (r RelationStatisticsSnapshot) Validate() error {
 if r.Schema=="" || r.Relation=="" || r.Mask==0 || r.Mask & ^uint8(RelationStatisticsAll)!=0 {return fmt.Errorf("stuntgres: invalid relation statistics snapshot")}
 if r.Mask & RelationTuples != 0 {
  n:=float64(math.Float32frombits(r.TupleBits))
  if math.IsNaN(n)||math.IsInf(n,0)||n < -1 {return fmt.Errorf("stuntgres: invalid relation statistics tuples")}
 }
 return nil
}

func CaptureRelationStatisticsSnapshots(ctx context.Context, read catalog.Read, metadata map[ids.ObjectAddress]*RelationMetadata) ([]RelationStatisticsSnapshot,error) {
 var records []RelationStatisticsSnapshot
 for address,entry:=range metadata {
  if err:=ctx.Err();err!=nil{return nil,err}
  if entry==nil || entry.Imported==nil || entry.Imported.Mask==0 {continue}
  relation,ok:=read.Relation(address);if !ok {continue}
  ns:=read.MustNamespace(relation.Namespace());if ns.Temporary!=(ids.SessionToken{}){continue}
  v:=entry.Imported
  r:=RelationStatisticsSnapshot{Schema:ns.Name(),Relation:relation.Name(),Mask:v.Mask,Pages:v.Pages,TupleBits:math.Float32bits(v.Tuples),AllVisible:v.AllVisible,AllFrozen:v.AllFrozen}
  if err:=r.Validate();err!=nil{return nil,err}
  records=append(records,r)
 }
 slices.SortFunc(records,func(a,b RelationStatisticsSnapshot)int{if n:=strings.Compare(a.Schema,b.Schema);n!=0{return n};return strings.Compare(a.Relation,b.Relation)})
 return records,ctx.Err()
}

func RestoreRelationStatisticsSnapshots(ctx context.Context, read catalog.Read, records []RelationStatisticsSnapshot) (map[ids.ObjectAddress]*RelationMetadata,error) {
 metadata:=make(map[ids.ObjectAddress]*RelationMetadata,len(records))
 for _,r:=range records {
  if err:=ctx.Err();err!=nil{return nil,err};if err:=r.Validate();err!=nil{return nil,err}
  ns:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:r.Schema})
  relation,ok:=read.Relation(read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ns,Name:r.Relation}))
  if !ok{return nil,fmt.Errorf("stuntgres: missing relation statistics relation")}
  target:=relation
  if relation.SQLIndex!=nil {target,ok=read.Relation(relation.SQLIndex.Parent);if !ok{return nil,fmt.Errorf("stuntgres: missing relation statistics index parent")}}
  if !catalog.StatisticsRelationKind(target.RelationKind) && (target.RelationKind!=catalog.CatalogRelation||target.CatalogView||target.VirtualView) || catalog.SharedRelation(target) {return nil,fmt.Errorf("stuntgres: unsupported relation statistics relation")}
  if metadata[relation.Address()]!=nil{return nil,fmt.Errorf("stuntgres: duplicate relation statistics snapshot")}
  metadata[relation.Address()]=&RelationMetadata{Imported:&ImportedRelationStatistics{Mask:r.Mask,Pages:r.Pages,Tuples:math.Float32frombits(r.TupleBits),AllVisible:r.AllVisible,AllFrozen:r.AllFrozen}}
 }
 // This helper only prepares records. The caller owns the final cancellation
 // observation immediately before atomic installation, as for attribute and
 // extended statistics; an empty record set performs no preparation work.
 return metadata,nil
}

// MergeRelationStatisticsMetadata composes the imported component without
// replacing attribute/extended facts or retained maintenance metadata.
func MergeRelationStatisticsMetadata(metadata, imported map[ids.ObjectAddress]*RelationMetadata) map[ids.ObjectAddress]*RelationMetadata {
 if len(imported)==0{return metadata}
 if metadata==nil {metadata=make(map[ids.ObjectAddress]*RelationMetadata,len(imported))}
 for address,entry:=range imported {
  if previous:=metadata[address];previous!=nil {next:=*previous;next.Imported=entry.Imported;metadata[address]=&next} else {metadata[address]=entry}
 }
 return metadata
}

func EncodeRelationStatisticsSnapshot(r RelationStatisticsSnapshot)(string,error){
 if err:=r.Validate();err!=nil{return "",err};data,err:=json.Marshal(r);if err!=nil{return "",err}
 return "-- "+RelationStatisticsSnapshotMarker+" "+base64.StdEncoding.EncodeToString(data)+"\n",nil
}
func DecodeRelationStatisticsSnapshot(comment string)(RelationStatisticsSnapshot,bool,error){
 var r RelationStatisticsSnapshot
 text:=strings.TrimSpace(comment);if !strings.HasPrefix(text,"stuntgres-relation-statistics-"){return r,false,nil}
 invalid:=func()(RelationStatisticsSnapshot,bool,error){return r,true,fmt.Errorf("stuntgres: invalid relation statistics snapshot")}
 marker,payload,ok:=strings.Cut(text," ");if !ok||marker!=RelationStatisticsSnapshotMarker{return invalid()}
 data,err:=base64.StdEncoding.Strict().DecodeString(payload);if err!=nil{return invalid()}
 if err=validateSnapshotJSON(json.NewDecoder(bytes.NewReader(data)));err!=nil{return invalid()}
 decoder:=json.NewDecoder(bytes.NewReader(data));decoder.DisallowUnknownFields()
 if err=decoder.Decode(&r);err!=nil{return invalid()};if err=decoder.Decode(new(any));err!=io.EOF{return invalid()};if err=r.Validate();err!=nil{return invalid()}
 return r,true,nil
}
