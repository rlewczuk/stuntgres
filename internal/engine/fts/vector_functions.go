package fts

import (
 "context"
 "fmt"
 "slices"
 "sort"
)

// ParseWeight consumes an already converted PostgreSQL internal char.
func ParseWeight(b byte) (byte, error) {
 switch b { case 'a','A': return 3,nil; case 'b','B': return 2,nil; case 'c','C': return 1,nil; case 'd','D': return 0,nil }
 display := string([]byte{b})
 if b < 32 || b > 126 { display = fmt.Sprintf("\\%03o", b) }
 return 0, failure("22023", `unrecognized weight: "`+display+`"`)
}

func (v *Vector) LexemeCount() int { return len(v.lexemes) }

// Selection is exact: selector text is never parsed or normalized. The callback
// reads an immutable typed array without making a second selector buffer.
func (v *Vector) selection(ctx context.Context, count int, at func(int) (string,bool)) ([]bool,error) {
 var selected []bool
 for i:=range count {
  if i&255==0 { if err:=ctx.Err();err!=nil{return nil,err} }
  word,ok:=at(i); if !ok {continue}
  j:=sort.Search(len(v.lexemes),func(j int)bool{return v.lexemes[j].word>=word})
  if j<len(v.lexemes)&&v.lexemes[j].word==word {
   if selected==nil {selected=make([]bool,len(v.lexemes))}; selected[j]=true
  }
 }
 return selected,ctx.Err()
}

func (v *Vector) SetWeight(ctx context.Context, weight byte) (*Vector,error) { return v.setWeight(ctx,weight,nil,true) }
func (v *Vector) SetSelectedWeight(ctx context.Context, weight byte, count int, at func(int)(string,bool)) (*Vector,error) {
 selected,err:=v.selection(ctx,count,at);if err!=nil{return nil,err}
 return v.setWeight(ctx,weight,selected,false)
}
func (v *Vector) setWeight(ctx context.Context, weight byte, selected []bool, all bool) (*Vector,error) {
 if err:=ctx.Err();err!=nil{return nil,err}
 var out []lexeme
 for i,entry:=range v.lexemes {
  if err:=ctx.Err();err!=nil{return nil,err}
  if !all&&(selected==nil||!selected[i]) {continue}
  changed:=false
  for _,p:=range entry.positions {if p.weight!=weight {changed=true;break}}
  if !changed {continue}
  if out==nil {out=slices.Clone(v.lexemes)}
  positions:=slices.Clone(entry.positions)
  for j:=range positions {positions[j].weight=weight}
  out[i].positions=positions
 }
 if out==nil {return v,ctx.Err()};return renderVector(ctx,out)
}
func (v *Vector) Strip(ctx context.Context) (*Vector,error) {
 var out []lexeme
 for i,entry:=range v.lexemes {
  if err:=ctx.Err();err!=nil{return nil,err}
  if len(entry.positions)==0 {continue}
  if out==nil {out=slices.Clone(v.lexemes)};out[i].positions=nil
 }
 if out==nil {return v,ctx.Err()};return renderVector(ctx,out)
}
func (v *Vector) Delete(ctx context.Context,count int,at func(int)(string,bool)) (*Vector,error) {
 selected,err:=v.selection(ctx,count,at);if err!=nil{return nil,err}
 if selected==nil{return v,ctx.Err()}
 n:=0;for i,yes:=range selected {if i&255==0{if err:=ctx.Err();err!=nil{return nil,err}};if yes {n++}}
 out:=make([]lexeme,0,len(v.lexemes)-n)
 for i,entry:=range v.lexemes {
  if i&255==0 {if err:=ctx.Err();err!=nil{return nil,err}}
  if !selected[i] {out=append(out,entry)}
 }
 return renderVector(ctx,out)
}
func (v *Vector) Filter(ctx context.Context,mask byte) (*Vector,error) {
 if err:=ctx.Err();err!=nil{return nil,err}
 var out []lexeme
 for i,entry:=range v.lexemes {
  if err:=ctx.Err();err!=nil{return nil,err}
  var scratch [256]position
  n:=0;for _,p:=range entry.positions {if mask&(1<<p.weight)!=0 {scratch[n]=p;n++}}
  if n==len(entry.positions)&&n>0 {if out!=nil {out=append(out,entry)};continue}
  if out==nil {out=make([]lexeme,i,len(v.lexemes));copy(out,v.lexemes[:i])}
  if n>0 {entry.positions=append([]position(nil),scratch[:n]...);out=append(out,entry)}
 }
 if out==nil {return v,ctx.Err()};return renderVector(ctx,out)
}
func (v *Vector) LexemeAt(i int) string {return v.lexemes[i].word}
