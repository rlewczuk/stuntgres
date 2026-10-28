package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// Installed ordering is a selected catalog operation, not a builtin type
// default. Keep the source capture during reference-only traversal, then map
// that exact class and authenticate its destination support in the typed pass.
func (m *ViewQueryRemapper) ordering(old plan.SortKey, target sqltype.Usage) (sqltype.Comparison,*catalog.CapturedIndexOrdering,error) {
	if old.Expression==nil || old.Expression.ResultUsage().Type==nil || target.Type==nil{return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: missing ordering operand usage")}
	source:=old.Expression.ResultUsage()
	from,to:=source.Type.BaseType(),target.Type.BaseType()
	installed:=from.IsCube()||from.IsHstore()||from.IsLtree()||to.IsCube()||to.IsHstore()||to.IsLtree()
	if !installed{
		if old.Ordering!=nil{return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: foreign ordering capture")}
		comparison,err:=sqltype.BindOrdering(target);return comparison,nil,err
	}
	if from.IsCube()!=to.IsCube()||from.IsHstore()!=to.IsHstore()||from.IsLtree()!=to.IsLtree(){return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: invalid captured installed ordering")}
	if err:=old.Ordering.Validate(source,old.Comparison);err!=nil{return sqltype.Comparison{},nil,err}
	if m.referencesOnly{return old.Comparison,old.Ordering,nil}
	if m.Catalog.Owner()==nil{return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: missing ordering destination authority")}
	mapped:=old.Ordering.References()
	for i,ref:=range mapped{
		if m.Ref!=nil{ref=m.Ref(ref)}
		if m.addressMapper!=nil{ref.Address=m.addressMapper(mapped[i].Address)}
		if ref.Domain()!=m.Catalog.Owner().Domain||ref.Epoch!=m.Catalog.Epoch()||ref.Address.ClassID!=mapped[i].Address.ClassID||ref.Address.ObjectID==0||ref.Address.SubID!=0{return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: invalid mapped ordering reference")}
		mapped[i]=ref
	}
	current,ok:=m.Catalog.LookupType(to.ID());if !ok||current!=to{return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: foreign mapped ordering type")}
	class,ok:=m.Catalog.IndexClass(mapped[0].Address.ObjectID);if !ok||class.MethodOID()!=403{return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: missing mapped ordering class")}
	comparison,capture,err:=routine.BindCapturedIndexOrdering(m.Catalog,target,class);if err!=nil{return sqltype.Comparison{},nil,err}
	if capture.References()!=mapped{return sqltype.Comparison{},nil,fmt.Errorf("stuntgres: inconsistent mapped ordering declarations")}
	return comparison.DatumComparison(),capture,nil
}
