package dap

import (
	"testing"
)

func TestListConfig(t *testing.T) {
	type args struct {
		args *launchAttachArgs
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "empty",
			args: args{
				args: &launchAttachArgs{},
			},
			want: `stopOnEntry	false
stackTraceDepth	0
showGlobalVariables	false
showRegisters	false
substitutePath	[]
substitutePathReverse	[] (read only)
`,
		},
		{
			name: "default values",
			args: args{
				args: &defaultArgs,
			},
			want: `stopOnEntry	false
stackTraceDepth	50
showGlobalVariables	false
showRegisters	false
substitutePath	[]
substitutePathReverse	[] (read only)
`,
		},
		{
			name: "custom values",
			args: args{
				args: &launchAttachArgs{
					stopOnEntry:                  false,
					StackTraceDepth:              35,
					ShowGlobalVariables:          true,
					substitutePathClientToServer: [][2]string{{"hello", "world"}},
					substitutePathServerToClient: [][2]string{{"world", "hello"}},
				},
			},
			want: `stopOnEntry	false
stackTraceDepth	35
showGlobalVariables	true
showRegisters	false
substitutePath	[[hello world]]
substitutePathReverse	[[world hello]] (read only)
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listConfig(tt.args.args); got != tt.want {
				t.Errorf("listConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetConfigureString(t *testing.T) {
	type args struct {
		sargs    *launchAttachArgs
		cfgname  string
		readonly []string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "basic bool",
			args: args{
				sargs: &launchAttachArgs{
					stopOnEntry:                  false,
					StackTraceDepth:              0,
					ShowGlobalVariables:          true,
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				cfgname:  "showGlobalVariables",
				readonly: []string{},
			},
			want: "showGlobalVariables	true\n",
		},
		{
			name: "basic readonly",
			args: args{
				sargs: &launchAttachArgs{
					stopOnEntry:                  false,
					StackTraceDepth:              34,
					ShowGlobalVariables:          true,
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				cfgname:  "stackTraceDepth",
				readonly: []string{"stackTraceDepth"},
			},
			want: "stackTraceDepth	34 (read only)\n",
		},
		{
			name: "substitute path print both",
			args: args{
				sargs: &launchAttachArgs{
					stopOnEntry:                  false,
					StackTraceDepth:              34,
					ShowGlobalVariables:          true,
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				cfgname:  "substitutePath",
				readonly: []string{"substitutePathReverse"},
			},
			want: "substitutePath	[]\nsubstitutePathReverse	[] (read only)\n",
		},
		{
			name: "substitute path reverse print both",
			args: args{
				sargs: &launchAttachArgs{
					stopOnEntry:                  false,
					StackTraceDepth:              34,
					ShowGlobalVariables:          true,
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				cfgname:  "substitutePathReverse",
				readonly: []string{"substitutePathReverse"},
			},
			want: "substitutePath	[]\nsubstitutePathReverse	[] (read only)\n",
		},
		{
			name: "empty",
			args: args{
				sargs:    &launchAttachArgs{},
				cfgname:  "",
				readonly: []string{},
			},
			want: "",
		},
		{
			name: "invalid",
			args: args{
				sargs: &launchAttachArgs{
					stopOnEntry:                  false,
					StackTraceDepth:              0,
					ShowGlobalVariables:          false,
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				cfgname:  "nonexistent",
				readonly: []string{},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConfigureString(tt.args.sargs, tt.args.cfgname, tt.args.readonly); got != tt.want {
				t.Errorf("getConfigureString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigureSetSubstitutePath(t *testing.T) {
	type args struct {
		args *launchAttachArgs
		rest string
	}
	tests := []struct {
		name      string
		args      args
		wantRules [][2]string
		wantErr   bool
	}{
		// Test add rule.
		{
			name: "add rule",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				rest: "/path/to/client/dir /path/to/server/dir",
			},
			wantRules: [][2]string{{"/path/to/client/dir", "/path/to/server/dir"}},
			wantErr:   false,
		},
		{
			name: "add rule (multiple)",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{
						{"/path/to/client/dir/a", "/path/to/server/dir/a"},
						{"/path/to/client/dir/b", "/path/to/server/dir/b"},
					},
					substitutePathServerToClient: [][2]string{
						{"/path/to/server/dir/a", "/path/to/client/dir/a"},
						{"/path/to/server/dir/b", "/path/to/client/dir/b"},
					},
				},
				rest: "/path/to/client/dir/c /path/to/server/dir/b",
			},
			wantRules: [][2]string{
				{"/path/to/client/dir/a", "/path/to/server/dir/a"},
				{"/path/to/client/dir/b", "/path/to/server/dir/b"},
				{"/path/to/client/dir/c", "/path/to/server/dir/b"},
			},
			wantErr: false,
		},
		// Test modify rule.
		{
			name: "modify rule",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{{"/path/to/client/dir", "/path/to/server/dir"}},
					substitutePathServerToClient: [][2]string{{"/path/to/server/dir", "/path/to/client/dir"}},
				},
				rest: "/path/to/client/dir /new/path/to/server/dir",
			},
			wantRules: [][2]string{{"/path/to/client/dir", "/new/path/to/server/dir"}},
			wantErr:   false,
		},
		{
			name: "modify rule (multiple)",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{
						{"/path/to/client/dir/a", "/path/to/server/dir/a"},
						{"/path/to/client/dir/b", "/path/to/server/dir/b"},
						{"/path/to/client/dir/c", "/path/to/server/dir/b"},
					},
					substitutePathServerToClient: [][2]string{
						{"/path/to/server/dir/a", "/path/to/client/dir/a"},
						{"/path/to/server/dir/b", "/path/to/client/dir/b"},
						{"/path/to/server/dir/b", "/path/to/client/dir/c"},
					},
				},
				rest: "/path/to/client/dir/b /new/path",
			},
			wantRules: [][2]string{
				{"/path/to/client/dir/a", "/path/to/server/dir/a"},
				{"/path/to/client/dir/b", "/new/path"},
				{"/path/to/client/dir/c", "/path/to/server/dir/b"},
			},
			wantErr: false,
		},
		// Test delete rule.
		{
			name: "delete rule",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{{"/path/to/client/dir", "/path/to/server/dir"}},
					substitutePathServerToClient: [][2]string{{"/path/to/server/dir", "/path/to/client/dir"}},
				},
				rest: "/path/to/client/dir",
			},
			wantRules: [][2]string{},
			wantErr:   false,
		},
		// Test invalid input.
		{
			name: "error on empty args",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				rest: "  \n\r   ",
			},
			wantErr: true,
		},
		{
			name: "error on delete nonexistent rule",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{{"/path/to/client/dir", "/path/to/server/dir"}},
					substitutePathServerToClient: [][2]string{{"/path/to/server/dir", "/path/to/client/dir"}},
				},
				rest: "/path/to/server/dir",
			},
			wantRules: [][2]string{{"/path/to/client/dir", "/path/to/server/dir"}},
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := configureSetSubstitutePath(tt.args.args, tt.args.rest)
			if (err != nil) != tt.wantErr {
				t.Errorf("configureSetSubstitutePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(tt.args.args.substitutePathClientToServer) != len(tt.wantRules) {
				t.Errorf("configureSetSubstitutePath() got substitutePathClientToServer=%v, want %d rules", tt.args.args.substitutePathClientToServer, len(tt.wantRules))
				return
			}
			gotClient2Server := tt.args.args.substitutePathClientToServer
			gotServer2Client := tt.args.args.substitutePathServerToClient
			for i, rule := range tt.wantRules {
				if gotClient2Server[i][0] != rule[0] || gotClient2Server[i][1] != rule[1] {
					t.Errorf("configureSetSubstitutePath() got substitutePathClientToServer[%d]=%#v,\n want %#v rules", i, gotClient2Server[i], rule)
				}
				if gotServer2Client[i][1] != rule[0] || gotServer2Client[i][0] != rule[1] {
					reverseRule := [2]string{rule[1], rule[0]}
					t.Errorf("configureSetSubstitutePath() got substitutePathServerToClient[%d]=%#v,\n want %#v rules", i, gotClient2Server[i], reverseRule)
				}
			}
		})
	}
}
