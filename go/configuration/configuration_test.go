package configuration

import "testing"

func TestParseSource(t *testing.T) {
	for _, c := range []struct {
		text, path, kind string
	}{
		{
			text: "config.prototext",
			kind: "file",
			path: "config.prototext",
		},
		{
			text: "file:config.prototext",
			kind: "file",
			path: "config.prototext",
		},
		{
			text: "colons:in:name:good:idea:config.prototext",
			kind: "file",
			path: "colons:in:name:good:idea:config.prototext",
		},
		{
			text: "etcd:/config.prototext",
			kind: "etcd",
			path: "/config.prototext",
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
