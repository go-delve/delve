package terminal

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/go-delve/delve/service/api"
)

func disasmPrint(dv api.AsmInstructions, out io.Writer, showHeader bool) {
	bw := bufio.NewWriter(out)
	defer bw.Flush()
	if len(dv) > 0 && dv[0].Loc.Function != nil && showHeader {
		fmt.Fprintf(bw, "TEXT %s(SB) %s\n", dv[0].Loc.Function.Name(), dv[0].Loc.File)
	}
	tw := tabwriter.NewWriter(bw, 1, 8, 1, '\t', 0)
	defer tw.Flush()
	for _, inst := range dv {
		atbp := ""
		if inst.Breakpoint {
			atbp = "*"
		}
		atpc := ""
		if inst.AtPC {
			atpc = "=>"
		}
		fmt.Fprintf(tw, "%s\t%s:%d\t%#x%s\t%x\t%s\n", atpc, filepath.Base(inst.Loc.File), inst.Loc.Line, inst.Loc.PC, atbp, inst.Bytes, inst.Text)
	}
}
