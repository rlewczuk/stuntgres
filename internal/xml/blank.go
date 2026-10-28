package xml

// The decision belongs to the character callback, before adjacent text merges.
func (p *parser) ignorableBlank(s string,lookahead int) bool {
 if p.spaceState==1||p.spaceState==-2||p.parent==nil{return false}
 for i:=range len(s){if i&4095==0{p.err=p.ctx.Err();if p.err!=nil{return false}};if !space(s[i]){return false}}
 name:=p.parent.name
 if p.parent.uri!=""{name=p.parent.local}
 if p.dtdState!=nil {switch p.dtdState.elementTypes[name]{case "ELEMENT":return true;case "EMPTY","ANY","MIXED":return false}}
 raw:=byte(0);if lookahead<len(p.s){raw=p.s[lookahead]}
 if raw!='<'&&raw!='\r'{return false}
 children:=p.parent.children
 if len(children)==0&&raw=='<'&&lookahead+1<len(p.s)&&p.s[lookahead+1]=='/'{return false}
 if len(children)>0&&(children[0].kind==textNode||children[len(children)-1].kind==textNode){return false}
 return true
}
