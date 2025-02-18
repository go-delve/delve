package version

import (
	"bytes"
	"fmt"
	"runtime/debug"
)

func init() {
	buildInfo = moduleBuildInfo
}

func moduleBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "not built in module mode"
	}

	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, " mod\t%s\t%s\t%s\n", info.Main.Path, info.Main.Version, info.Main.Sum)
	for _, dep := range info.Deps {
		fmt.Fprintf(buf, " dep\t%s\t%s\t%s", dep.Path, dep.Version, dep.Sum)
		if dep.Replace != nil {
			fmt.Fprintf(buf, "\t=> %s\t%s\t%s", dep.Replace.Path, dep.Replace.Version, dep.Replace.Sum)
		}
		fmt.Fprintf(buf, "\n")
	}
	return buf.String()
}
