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

package controller

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// authEntry is one .dockerconfigjson "auths" record. user/pass are kept free of
// ':' so the base64 "user:pass" encoding round-trips unambiguously.
type authEntry struct{ host, user, pass string }

// dockerConfigJSON builds a .dockerconfigjson payload from the given entries.
func dockerConfigJSON(t *testing.T, entries ...authEntry) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"auths":{`)
	for i, e := range entries {
		if i > 0 {
			b.WriteString(",")
		}
		auth := base64.StdEncoding.EncodeToString([]byte(e.user + ":" + e.pass))
		fmt.Fprintf(&b, "%q:{\"auth\":%q}", e.host, auth)
	}
	b.WriteString(`}}`)
	return []byte(b.String())
}

func TestLookupDockerConfigAuth_ExactHostMatch(t *testing.T) {
	raw := dockerConfigJSON(t, authEntry{"quay.io", "robot", "s3cr3t"})
	user, pass, err := lookupDockerConfigAuth(raw, "quay.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "robot" || pass != "s3cr3t" {
		t.Errorf("got %q/%q, want the quay.io entry", user, pass)
	}
}

// A credential issued for one registry must never be handed to a host the
// spec author picked. These three shapes all leaked it before: a host that
// merely contains the registry name, an unrelated host falling through to
// "any entry", and the empty host the HTTPS base-image path used to pass.
func TestLookupDockerConfigAuth_RejectsNonMatchingHosts(t *testing.T) {
	raw := dockerConfigJSON(t, authEntry{"quay.io", "robot", "s3cr3t"})
	for _, host := range []string{
		"quay.io.attacker.example",     // substring of the key
		"registry.attacker.example",    // unrelated
		"attacker.example",             // registry name only in the path
		"quay.io.attacker.example:443", // substring plus port
		"",                             // no host supplied
	} {
		if user, _, err := lookupDockerConfigAuth(raw, host); err == nil {
			t.Errorf("host %q: expected no credential, got user %q", host, user)
		}
	}
}

func TestLookupDockerConfigAuth_MatchesDockerHubAliases(t *testing.T) {
	// docker login writes the hub entry as a URL; a bare "docker.io" reference
	// must still resolve to it.
	raw := dockerConfigJSON(t, authEntry{"https://index.docker.io/v1/", "hubuser", "hubpass"})
	user, _, err := lookupDockerConfigAuth(raw, "docker.io")
	if err != nil {
		t.Fatalf("expected the Docker Hub alias to resolve: %v", err)
	}
	if user != "hubuser" {
		t.Errorf("unexpected user %q", user)
	}
}

func TestLookupDockerConfigAuth_HostMatchIsCaseInsensitive(t *testing.T) {
	raw := dockerConfigJSON(t, authEntry{"Quay.IO", "robot", "s3cr3t"})
	if _, _, err := lookupDockerConfigAuth(raw, "quay.io"); err != nil {
		t.Fatalf("expected case-insensitive host match: %v", err)
	}
}

func TestRegistryHostFromReference(t *testing.T) {
	cases := map[string]string{
		"quay.io/openshift/rhcos:4.18": "quay.io",
		"localhost:5000/img:1":         "localhost:5000",
		"localhost/img:1":              "localhost",
		"alpine:3":                     "docker.io", // short name -> Docker Hub
		"library/alpine:3":             "docker.io", // unqualified path -> Docker Hub
		"quay.io.attacker.example/x:1": "quay.io.attacker.example",
		"attacker.example/quay.io/x:1": "attacker.example",
		"registry.example.com:443/x:1": "registry.example.com:443",
	}
	for ref, want := range cases {
		if got := registryHostFromReference(ref); got != want {
			t.Errorf("registryHostFromReference(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://mirror.example.com/img.qcow2":      "mirror.example.com",
		"https://mirror.example.com:8443/img.qcow2": "mirror.example.com:8443",
		"http://10.0.0.5/img.qcow2":                 "10.0.0.5",
		"not a url":                                 "", // parses, but carries no host
		"://x":                                      "", // fails to parse
	}
	for raw, want := range cases {
		if got := hostFromURL(raw); got != want {
			t.Errorf("hostFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
