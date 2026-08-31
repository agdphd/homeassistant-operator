/*
Copyright 2026.

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
	"reflect"
	"strings"
	"testing"
)

func TestReadHTTPSection(t *testing.T) {
	t.Run("absent section", func(t *testing.T) {
		data, readable, err := readHTTPSection("default_config:\n")
		if err != nil || !readable || data != nil {
			t.Fatalf("got data=%v readable=%v err=%v", data, readable, err)
		}
	})

	t.Run("empty scalar section", func(t *testing.T) {
		data, readable, err := readHTTPSection("http:\n")
		if err != nil || !readable || data != nil {
			t.Fatalf("got data=%v readable=%v err=%v", data, readable, err)
		}
	})

	t.Run("plain mapping", func(t *testing.T) {
		data, readable, err := readHTTPSection("http:\n  use_x_forwarded_for: true\n  server_port: 8443\n")
		if err != nil || !readable {
			t.Fatalf("readable=%v err=%v", readable, err)
		}
		if data["use_x_forwarded_for"] != true || data["server_port"] != 8443 {
			t.Fatalf("unexpected data: %#v", data)
		}
	})

	t.Run("unreadable include", func(t *testing.T) {
		_, readable, err := readHTTPSection("http: !include http.yaml\n")
		if err != nil || readable {
			t.Fatalf("expected unreadable, got readable=%v err=%v", readable, err)
		}
	})
}

func TestStripHTTPSection(t *testing.T) {
	in := "default_config:\nhttp:\n  use_x_forwarded_for: true\nlogger:\n  default: info\n"
	out, err := stripHTTPSection(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "http:") {
		t.Fatalf("http: not removed:\n%s", out)
	}
	if !strings.Contains(out, "default_config:") || !strings.Contains(out, "logger:") {
		t.Fatalf("other sections lost:\n%s", out)
	}

	// idempotent + safe when absent
	again, err := stripHTTPSection(out)
	if err != nil || again != out {
		t.Fatalf("strip not idempotent: %v / %q", err, again)
	}
}

func TestCanonicalizeTrustedProxies(t *testing.T) {
	cases := []struct {
		in   []string
		want []interface{}
	}{
		{[]string{"192.168.1.1"}, []interface{}{"192.168.1.1/32"}},
		{[]string{"10.0.0.0/8", "172.16.0.0/12"}, []interface{}{"10.0.0.0/8", "172.16.0.0/12"}},
		{[]string{"::1"}, []interface{}{"::1/128"}},
		{[]string{"10.0.0.5", "10.0.0.0/8"}, []interface{}{"10.0.0.5/32", "10.0.0.0/8"}}, // order preserved
	}
	for _, c := range cases {
		d := map[string]interface{}{"trusted_proxies": toIfaceList(c.in)}
		canonicalizeTrustedProxies(d)
		if !reflect.DeepEqual(d["trusted_proxies"], c.want) {
			t.Fatalf("canon(%v) = %v, want %v", c.in, d["trusted_proxies"], c.want)
		}
	}
}

func toIfaceList(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
