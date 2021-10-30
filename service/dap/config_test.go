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
			want: formatConfig(0, false, false, false, [][2]string{}),
		},
		{
			name: "default values",
			args: args{
				args: &defaultArgs,
			},
			want: formatConfig(50, false, false, false, [][2]string{}),
		},
		{
			name: "custom values",
			args: args{
				args: &launchAttachArgs{
					StackTraceDepth:              35,
					ShowGlobalVariables:          true,
					substitutePathClientToServer: [][2]string{{"hello", "world"}},
					substitutePathServerToClient: [][2]string{{"world", "hello"}},
				},
			},
			want: formatConfig(35, true, false, false, [][2]string{{"hello", "world"}}),
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
