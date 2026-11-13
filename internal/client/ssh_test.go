package client

import (
	"encoding/json"
	"testing"
)

func sshMode(cmd string) AccessMode {
	return AccessMode{Name: "SSH", Type: "terminal", CopyField: cmd}
}

func TestVM_SSH(t *testing.T) {
	browser := AccessMode{Name: "vscode", Type: "browser", CopyField: "https://simplevm.example/x"}
	for _, tc := range []struct {
		name  string
		modes []AccessMode
		want  *SSHAccess
	}{
		{"as reported by SimpleVM",
			[]AccessMode{sshMode("ssh -i /path/to/your/ssh/private/key ubuntu@129.70.51.6 -p 30297"), browser},
			&SSHAccess{Host: "129.70.51.6", Port: 30297, User: "ubuntu"}},
		{"browser mode first",
			[]AccessMode{browser, sshMode("ssh ubuntu@129.70.51.6 -p 30297")},
			&SSHAccess{Host: "129.70.51.6", Port: 30297, User: "ubuntu"}},
		{"attached port and -l user",
			[]AccessMode{sshMode("ssh -p30297 -l debian gw.example.org")},
			&SSHAccess{Host: "gw.example.org", Port: 30297, User: "debian"}},
		{"no port means 22, no user means empty",
			[]AccessMode{sshMode("ssh 129.70.51.6")},
			&SSHAccess{Host: "129.70.51.6", Port: 22}},
		{"IPv6",
			[]AccessMode{sshMode("ssh ubuntu@2001:db8::1 -p 30001")},
			&SSHAccess{Host: "2001:db8::1", Port: 30001, User: "ubuntu"}},
		{"ssh command under another mode name",
			[]AccessMode{{Name: "Terminal", CopyField: "ssh ubuntu@129.70.51.6 -p 30297"}},
			&SSHAccess{Host: "129.70.51.6", Port: 30297, User: "ubuntu"}},
		{"first valid SSH mode wins",
			[]AccessMode{sshMode("ssh ubuntu@-oProxyCommand=x"), sshMode("ssh ubuntu@129.70.51.6 -p 2")},
			&SSHAccess{Host: "129.70.51.6", Port: 2, User: "ubuntu"}},
		{"no modes", nil, nil},
		{"only a browser mode", []AccessMode{browser}, nil},
		{"empty command", []AccessMode{sshMode("")}, nil},
		{"not ssh", []AccessMode{sshMode("mosh ubuntu@129.70.51.6")}, nil},
		{"no host", []AccessMode{sshMode("ssh -p 30297")}, nil},
		// Values are untrusted: they end up in ssh command lines and
		// inventories, so nothing that ssh could read as an option passes.
		{"host is an option", []AccessMode{sshMode("ssh ubuntu@-oProxyCommand=touch%20x -p 1")}, nil},
		{"host with IPv6 zone", []AccessMode{sshMode("ssh ubuntu@fe80::1%-oProxyCommand=x")}, nil},
		{"host with parseable IPv6 zone", []AccessMode{sshMode("ssh ubuntu@fe80::1%eth0")}, nil},
		{"host with shell characters", []AccessMode{sshMode("ssh ubuntu@gw;id")}, nil},
		{"user is an option", []AccessMode{sshMode("ssh -oProxyCommand=x@129.70.51.6")}, nil},
		{"user with shell characters", []AccessMode{sshMode("ssh $(id)@129.70.51.6")}, nil},
		{"port out of range", []AccessMode{sshMode("ssh ubuntu@129.70.51.6 -p 70000")}, nil},
		{"port zero", []AccessMode{sshMode("ssh ubuntu@129.70.51.6 -p 0")}, nil},
		{"port not a number", []AccessMode{sshMode("ssh ubuntu@129.70.51.6 -p x")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (&VM{Modes: tc.modes}).SSH()
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("SSH() = %+v, want nil", *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Fatalf("SSH() = %+v, want %+v", got, *tc.want)
			}
		})
	}
}

func TestVM_ModesDecode(t *testing.T) {
	var vm VM
	if err := json.Unmarshal([]byte(`{"uuid":"u","modes":[{"description":"Please use ...","name":"SSH",
		"copy_field":"ssh -i /path/to/your/ssh/private/key ubuntu@129.70.51.6 -p 30297","type":"terminal","display_name":"SSH"}]}`), &vm); err != nil {
		t.Fatal(err)
	}
	if got := vm.SSH(); got == nil || got.Port != 30297 {
		t.Fatalf("SSH() = %+v", got)
	}
}
