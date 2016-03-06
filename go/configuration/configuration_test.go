package configuration

import "testing"

func TestParseSource(t *testing.T) {
	for _, c := range []struct {
		text, path, kind string
	}{
		{
			text: "config.yml",
			kind: "file",
			path: "config.yml",
		},
		{
			text: "file:config.yml",
			kind: "file",
			path: "config.yml",
		},
		{
			text: "colons:in:name:good:idea:config.yml",
			kind: "file",
			path: "colons:in:name:good:idea:config.yml",
		},
		{
			text: "etcd:/config.yml",
			kind: "etcd",
			path: "/config.yml",
		},
	} {
		kind, path := ParseSource(c.text)
		if kind != c.kind {
			t.Errorf("parseSource(%q): kind = %q; want %q", c.text, kind, c.kind)
		}
		if path != c.path {
			t.Errorf("parseSource(%q): kind = %q; want %q", c.text, path, c.path)
		}
	}
}
