package client

import (
	"net/netip"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// SSHAccess is how to reach a VM over SSH. SimpleVM VMs are usually reached
// through an SSH gateway on a port of their own, not on their fixed IP.
type SSHAccess struct {
	Host string
	Port int64
	// User is empty when SimpleVM does not name one.
	User string
}

// SSH returns the VM's SSH access, parsed from its "SSH" access mode (for
// example "ssh -i /path/to/your/ssh/private/key ubuntu@129.70.51.6 -p 30297"),
// or nil when SimpleVM reports none that is valid.
//
// The values are untrusted: project members choose parts of the VM record,
// and the result ends up in ssh command lines and inventories. A host must
// be an IP address without a zone or a DNS name, and a user a plain user
// name, so neither can be read by ssh as an option.
func (v *VM) SSH() *SSHAccess {
	for _, m := range v.Modes {
		if !strings.EqualFold(m.Name, "SSH") && !strings.HasPrefix(m.CopyField, "ssh ") {
			continue
		}
		if a := parseSSHCommand(m.CopyField); a != nil {
			return a
		}
	}
	return nil
}

// sshOptionsWithValue are the ssh options that take an argument.
const sshOptionsWithValue = "BbcDEeFIiJLlmOoPpQRSWw"

var (
	hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	userRE     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,31}$`)
)

func parseSSHCommand(cmd string) *SSHAccess {
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 || path.Base(tokens[0]) != "ssh" {
		return nil
	}
	a := SSHAccess{Port: 22}
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "-") {
			if len(t) < 2 || !strings.ContainsRune(sshOptionsWithValue, rune(t[1])) {
				continue // a flag without value, such as -v
			}
			value := t[2:]
			if value == "" && i+1 < len(tokens) {
				i++
				value = tokens[i]
			}
			switch t[1] {
			case 'p':
				port, err := strconv.ParseInt(value, 10, 64)
				if err != nil || port < 1 || port > 65535 {
					return nil
				}
				a.Port = port
			case 'l':
				a.User = value
			}
			continue
		}
		if a.Host != "" {
			continue // the remote command
		}
		if at := strings.LastIndex(t, "@"); at >= 0 {
			a.User, a.Host = t[:at], t[at+1:]
		} else {
			a.Host = t
		}
	}
	if !validHost(a.Host) || (a.User != "" && !userRE.MatchString(a.User)) {
		return nil
	}
	return &a
}

func validHost(h string) bool {
	if len(h) > 253 {
		return false
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		return addr.Zone() == ""
	}
	return hostnameRE.MatchString(h)
}
