package dap

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/go-dap"
)

func (s *Server) updateConfig(expr string) (string, error) {
	// TODO(suzmue): reuse terminal package config parsing and handling.
	argv := split2PartsBySpace(expr)
	name := argv[0]
	switch name {
	case "-list":
		return listConfig([]config{{"showGlobalVariables", s.args.showGlobalVariables}, {"stackTraceDepth", s.args.stackTraceDepth}}), nil
	case "showGlobalVariables":
		if len(argv) != 2 {
			return "", fmt.Errorf("expected 1 argument to config showGlobalVariables, got %d", len(argv)-1)
		}
		showGlobal, err := strconv.ParseBool(argv[1])
		if err != nil {
			return "", err
		}
		s.args.showGlobalVariables = showGlobal
		s.send(&dap.InvalidatedEvent{
			Event: *newEvent("invalidated"),
			Body: dap.InvalidatedEventBody{
				Areas: []dap.InvalidatedAreas{"variables"},
			},
		})
	case "stackTraceDepth":
		if len(argv) != 2 {
			return "", fmt.Errorf("expected 1 argument to config stackTraceDepth, got %d", len(argv)-1)
		}
		stackDepth, err := strconv.ParseInt(argv[1], 0, 64)
		if err != nil {
			return "", err
		}
		s.args.stackTraceDepth = int(stackDepth)
	default:
		return "", fmt.Errorf("invalid config value %s", name)
	}
	return "Success", nil
}

type config struct {
	name  string
	value interface{}
}

func listConfig(configurations []config) string {
	var val string
	for _, cfg := range configurations {
		val += fmt.Sprintf("%s:\t%v\n", cfg.name, cfg.value)
	}
	return val
}

func split2PartsBySpace(s string) []string {
	v := strings.SplitN(s, " ", 2)
	for i, _ := range v {
		v[i] = strings.TrimSpace(v[i])
	}
	return v
}
