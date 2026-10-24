package analyze

func (b *Binder) CaptureFTSParserInput() error {
	if b.Input.RegTS != nil { return nil }
	auth,err:=b.BindingAuthorization()
	if err!=nil { return err }
	return b.Host.CaptureRegTSInput(auth,&b.Input)
}
