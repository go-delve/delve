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
			want: formatConfig(0, 0, 0, false, false, "", []string{}, false, [][2]string{}, false, "", false),
		},
		{
			name: "default values",
			args: args{
				args: &defaultArgs,
			},
			want: formatConfig(50, 0, 0, false, false, "", []string{}, false, [][2]string{}, false, "", false),
		},
		{
			name: "custom values",
			args: args{
				args: &launchAttachArgs{
					StackTraceDepth:              35,
					MaxStringLen:                 1024,
					MaxArrayValues:               128,
					ShowGlobalVariables:          true,
					GoroutineFilters:             "SomeFilter",
					ShowPprofLabels:              []string{"SomeLabel"},
					substitutePathClientToServer: [][2]string{{"hello", "world"}},
					substitutePathServerToClient: [][2]string{{"world", "hello"}},
					followExec:                   true,
					followExecRegex:              "a.b?",
					ShowRawStrings:               true,
				},
			},
			want: formatConfig(35, 1024, 128, true, false, "SomeFilter", []string{"SomeLabel"}, false, [][2]string{{"hello", "world"}}, true, "a.b?", true),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listConfig(tt.args.args); got != tt.want {
				t.Errorf("listConfig() = %v, want %v", got, tt.want)
				t.Errorf("got  = %v", got)
				t.Errorf("want %v", tt.want)
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
		{
			name: "add rule from empty string",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				rest: `"" /path/to/client/dir`,
			},
			wantRules: [][2]string{{"", "/path/to/client/dir"}},
			wantErr:   false,
		},
		{
			name: "add rule to empty string",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{},
					substitutePathServerToClient: [][2]string{},
				},
				rest: `/path/to/client/dir ""`,
			},
			wantRules: [][2]string{{"/path/to/client/dir", ""}},
			wantErr:   false,
		},
		{
			name: "add rule from empty string(multiple)",
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
				rest: `"" /path/to/client/dir/c`,
			},
			wantRules: [][2]string{
				{"/path/to/client/dir/a", "/path/to/server/dir/a"},
				{"/path/to/client/dir/b", "/path/to/server/dir/b"},
				{"", "/path/to/client/dir/c"},
			},
			wantErr: false,
		},
		{
			name: "add rule to empty string(multiple)",
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
				rest: `/path/to/client/dir/c ""`,
			},
			wantRules: [][2]string{
				{"/path/to/client/dir/a", "/path/to/server/dir/a"},
				{"/path/to/client/dir/b", "/path/to/server/dir/b"},
				{"/path/to/client/dir/c", ""},
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
			name: "modify rule with from as empty string",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{{"", "/path/to/server/dir"}},
					substitutePathServerToClient: [][2]string{{"/path/to/server/dir", ""}},
				},
				rest: `"" /new/path/to/server/dir`,
			},
			wantRules: [][2]string{{"", "/new/path/to/server/dir"}},
			wantErr:   false,
		},
		{
			name: "modify rule with to as empty string",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{{"/path/to/client/dir", ""}},
					substitutePathServerToClient: [][2]string{{"", "/path/to/client/dir"}},
				},
				rest: `/path/to/client/dir ""`,
			},
			wantRules: [][2]string{{"/path/to/client/dir", ""}},
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
		{
			name: "modify rule with from as empty string(multiple)",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{
						{"/path/to/client/dir/a", "/path/to/server/dir/a"},
						{"", "/path/to/server/dir/b"},
						{"/path/to/client/dir/c", "/path/to/server/dir/b"},
					},
					substitutePathServerToClient: [][2]string{
						{"/path/to/server/dir/a", "/path/to/client/dir/a"},
						{"/path/to/server/dir/b", ""},
						{"/path/to/server/dir/b", "/path/to/client/dir/c"},
					},
				},
				rest: `"" /new/path`,
			},
			wantRules: [][2]string{
				{"/path/to/client/dir/a", "/path/to/server/dir/a"},
				{"", "/new/path"},
				{"/path/to/client/dir/c", "/path/to/server/dir/b"},
			},
			wantErr: false,
		},
		{
			name: "modify rule with to as empty string(multiple)",
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
				rest: `/path/to/client/dir/b ""`,
			},
			wantRules: [][2]string{
				{"/path/to/client/dir/a", "/path/to/server/dir/a"},
				{"/path/to/client/dir/b", ""},
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
		{
			name: "delete rule, empty string",
			args: args{
				args: &launchAttachArgs{
					substitutePathClientToServer: [][2]string{{"", "/path/to/server/dir"}},
					substitutePathServerToClient: [][2]string{{"/path/to/server/dir", ""}},
				},
				rest: `""`,
			},
			wantRules: [][2]string{},
			wantErr:   false,
		},
		// Test invalid input.
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

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name               string
		args               launchAttachArgs
		wantMaxStringLen   int
		wantMaxArrayValues int
	}{
		{
			name:               "defaults",
			args:               defaultArgs,
			wantMaxStringLen:   512,
			wantMaxArrayValues: 64,
		},
		{
			name: "custom limits",
			args: func() launchAttachArgs {
				args := defaultArgs
				args.MaxStringLen = 4096
				args.MaxArrayValues = 512
				return args
			}(),
			wantMaxStringLen:   4096,
			wantMaxArrayValues: 512,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{args: tt.args}
			got := s.loadConfig()
			if got.MaxStringLen != tt.wantMaxStringLen {
				t.Errorf("loadConfig().MaxStringLen = %d, want %d", got.MaxStringLen, tt.wantMaxStringLen)
			}
			if got.MaxArrayValues != tt.wantMaxArrayValues {
				t.Errorf("loadConfig().MaxArrayValues = %d, want %d", got.MaxArrayValues, tt.wantMaxArrayValues)
			}
			// The remaining fields always follow DefaultLoadConfig.
			if got.FollowPointers != DefaultLoadConfig.FollowPointers ||
				got.MaxVariableRecurse != DefaultLoadConfig.MaxVariableRecurse ||
				got.MaxStructFields != DefaultLoadConfig.MaxStructFields {
				t.Errorf("loadConfig() = %+v, want non-limit fields from DefaultLoadConfig %+v", got, DefaultLoadConfig)
			}
		})
	}
}

func TestConfigureSetLoadLimits(t *testing.T) {
	args := defaultArgs
	updated, _, err := configureSet(&args, "maxStringLen 2048")
	if err != nil || !updated {
		t.Fatalf("configureSet(maxStringLen) = %v, %v; want updated, nil", updated, err)
	}
	if args.MaxStringLen != 2048 {
		t.Errorf("MaxStringLen = %d, want 2048", args.MaxStringLen)
	}
	updated, _, err = configureSet(&args, "maxArrayValues 256")
	if err != nil || !updated {
		t.Fatalf("configureSet(maxArrayValues) = %v, %v; want updated, nil", updated, err)
	}
	if args.MaxArrayValues != 256 {
		t.Errorf("MaxArrayValues = %d, want 256", args.MaxArrayValues)
	}
}
