//go:build go1.12
// +build go1.12

package version

import (
	"bytes"
	"runtime/debug"
	"text/template"
)

func init() {
	buildInfo = moduleBuildInfo
}

var buildInfoTmpl = ` mod	{{.Main.Path}}	{{.Main.Version}}	{{.Main.Sum}}
{{range .Deps}} dep	{{.Path}}	{{.Version}}	{{.Sum}}{{if .Replace}}
	=> {{.Replace.Path}}	{{.Replace.Version}}	{{.Replace.Sum}}{{end}}
{{end}}`

func moduleBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "not built in module mode"
	}

	buf := new(bytes.Buffer)
	err := template.Must(template.New("buildinfo").Parse(buildInfoTmpl)).Execute(buf, info)
	if err != nil {
		panic(err)
	}
	return buf.String()
}
