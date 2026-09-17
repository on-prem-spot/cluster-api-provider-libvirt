/*
Copyright 2026 Anthony Green.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package libvirt

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestShellQuoteEscapesMetacharacters(t *testing.T) {
	got := shellJoin("virsh", "vol-info", "root.qcow2", "--pool", "default; printf INJECTED")
	want := "'virsh' 'vol-info' 'root.qcow2' '--pool' 'default; printf INJECTED'"
	if got != want {
		t.Fatalf("shellJoin() = %q, want %q", got, want)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := shellQuote("pool'$(id)")
	want := `'pool'\''$(id)'`
	if got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestLocalConnectionURI(t *testing.T) {
	tests := []struct {
		uri     string
		want    string
		wantErr bool
	}{
		{uri: "qemu+ssh://caplv@host.example.com/system", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com:2222/system", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com/session", want: ConnURISession},
		{uri: "qemu+ssh://caplv@host.example.com", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com/", want: ConnURISystem},
		{uri: "qemu+ssh://caplv@host.example.com/sessions", wantErr: true},
		{uri: "qemu+ssh://caplv@host.example.com/foo", wantErr: true},
	}
	for _, tt := range tests {
		got, err := LocalConnectionURI(tt.uri)
		if tt.wantErr {
			if err == nil {
				t.Errorf("LocalConnectionURI(%q) expected error, got %q", tt.uri, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("LocalConnectionURI(%q) unexpected error: %v", tt.uri, err)
			continue
		}
		if got != tt.want {
			t.Errorf("LocalConnectionURI(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestNewVirshClientDefaultsToSystem(t *testing.T) {
	c := NewVirshClient(nil, "")
	if c.connURI != ConnURISystem {
		t.Fatalf("connURI = %q, want %q", c.connURI, ConnURISystem)
	}
	c = NewVirshClient(nil, ConnURISession)
	if c.connURI != ConnURISession {
		t.Fatalf("connURI = %q, want %q", c.connURI, ConnURISession)
	}
}

// --- privileged host operations ---
//
// Every privileged operation goes through /usr/local/sbin/caplv-hostctl, which
// canonicalises its path argument and confines it to /run/caplv. The previous
// per-command sudoers rules could not do that: in a sudoers command *argument*
// a '*' matches '/' too, so "tee /run/caplv/*" also permitted
// "tee /run/caplv/../../etc/sudoers.d/x".

func TestHostCtlCmdRoutesThroughTheHelper(t *testing.T) {
	got := hostCtlCmd("write", "/run/caplv/ns-cl-m1/ignition.json")
	want := "sudo /usr/local/sbin/caplv-hostctl write '/run/caplv/ns-cl-m1/ignition.json'"
	if got != want {
		t.Errorf("hostCtlCmd() = %q, want %q", got, want)
	}
}

func TestHostCtlCmdQuotesEveryArgument(t *testing.T) {
	got := hostCtlCmd("mount-tmpfs", "/run/caplv/a b; rm -rf /", "80%")
	if !strings.Contains(got, `'/run/caplv/a b; rm -rf /'`) {
		t.Errorf("path argument was not shell-quoted: %q", got)
	}
	if !strings.Contains(got, `'80%'`) {
		t.Errorf("size argument was not shell-quoted: %q", got)
	}
}

// Regression guard against reintroducing M4: every privileged operation must
// build its command through hostCtlCmd, so the literal "sudo " may appear in
// exactly one place in the source. A direct `sudo tee`/`sudo rm`/`sudo mount`
// would again depend on wildcard sudoers rules that cannot confine a path.
func TestSudoIsOnlyInvokedThroughTheHelper(t *testing.T) {
	src, err := os.ReadFile("virsh_client.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	const literal = `"sudo `
	if n := strings.Count(string(src), literal); n != 1 {
		t.Errorf("found %d occurrences of %s in virsh_client.go, want exactly 1 (in hostCtlCmd); "+
			"privileged commands must go through %s", n, literal, hostCtl)
	}
}

// WriteRemoteFile derives the parent directory by string index; a path with no
// directory component used to slice with a negative index and panic.
func TestWriteRemoteFileRejectsPathWithoutDirectory(t *testing.T) {
	c := &VirshClient{}
	for _, path := range []string{"ignition.json", "/ignition.json", ""} {
		if err := c.WriteRemoteFile(context.Background(), path, []byte("x")); err == nil {
			t.Errorf("expected path %q to be rejected", path)
		}
	}
}
