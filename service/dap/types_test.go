package dap

import "testing"

func TestAttachConfigExecutable(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"omitted", `{"processId":123}`, ""},
		{"empty", `{"processId":123,"executable":""}`, ""},
		{"explicit", `{"processId":123,"executable":"./debuggee"}`, "./debuggee"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config AttachConfig
			if err := unmarshalLaunchAttachArgs([]byte(tc.args), &config); err != nil {
				t.Fatal(err)
			}
			if config.Executable != tc.want {
				t.Fatalf("executable = %q, want %q", config.Executable, tc.want)
			}
		})
	}
}
