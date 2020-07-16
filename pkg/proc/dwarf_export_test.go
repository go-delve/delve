package proc

// PackageVars returns bi.packageVars (for tests)
func (bi *BinaryInfo) PackageVars() []packageVar {
	return bi.packageVars
}
