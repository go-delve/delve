//+build arm64

package gdbserial

const (
	regnamePC = "pc"
	regnameCX = "x0"
	regnameSP = "sp"
	regnameBP = "fp"

	// not needed but needs to be declared
	regnameFsBase = ""
	regnameDX     = ""

	breakpointKind = 4
)
