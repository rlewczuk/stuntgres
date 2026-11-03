package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
)

// enterExpressionEnvironment borrows the actual expression actor for generated
// scope SELECTs. This is not a catalog routine or a settings/security boundary.
// Its lifetime encloses producer close and nested SQL scratch restoration.
func (s *StatementScope) enterExpressionEnvironment(actor authorization.PrincipalRef)(leave func(error)error,err error){
	op:=s.Op;previous,foreign:=op.EffectiveUser,s.ForeignActionAuth
	identity,inquiry,authority:=s.Identity,s.Inquiry,s.RoutineAuth
	input,output,presentation,rowSecurity:=s.Input,s.Output,s.presentationSlot,s.RowSecurity
	op.EffectiveUser=&session.EffectiveUserInvocation{Owner:actor,Parent:previous,Restricted:op.Restricted}
	s.ForeignActionAuth=nil
	leave=func(primary error)error{
		op.EffectiveUser=previous;s.ForeignActionAuth=foreign
		cleanup:=op.Context().Err()
		if cleanup==nil{s.presentationSlot=nil;cleanup=s.refreshRestrictedPresentation();if cleanup==nil{cleanup=op.Context().Err()}}
		if cleanup!=nil{
			s.Identity,s.Inquiry,s.RoutineAuth=identity,inquiry,authority
			s.Input,s.Output,s.presentationSlot,s.RowSecurity=input,output,presentation,rowSecurity
		}
		if primary!=nil{return primary};return cleanup
	}
	s.presentationSlot=nil
	if err:=s.refreshRestrictedPresentation();err!=nil{return nil,leave(err)}
	if err:=op.Context().Err();err!=nil{return nil,leave(err)}
	return leave,nil
}
