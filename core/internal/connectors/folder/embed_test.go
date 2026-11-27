package folder

import (
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

func TestEncodePageEmbedAndHTMLTable(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	p := &model.Page{ID: "1", Body: "![Intro](tolarix://page/2)\n\n<table>\n" +
		`<tr><td><a href="tolarix://page/2#Set%20up">set up</a> <a href="tolarix://page/9">gone</a></td></tr>` + "\n" +
		`<tr><td><img src="tolarix://att/` + hash + `" alt="a.png"> <img src="tolarix://att/` + strings.Repeat("cd", 32) + `" alt="b.png"></td></tr>` + "\n</table>\n"}
	l := &connectors.Layout{
		Pages:       map[string]connectors.Placement{"1": {Key: "Home.md"}, "2": {Key: "Guide/Intro.md"}},
		Attachments: map[string]string{hash: "files/a.png"},
	}
	var codes []string
	got := encodeBody(p, l, connectors.WarnFunc(func(w connectors.Warning) { codes = append(codes, w.Code) }),
		func(k string) string { return "./" + k }, func(k string) string { return "./" + k })
	for _, want := range []string{
		"[Intro](./Guide/Intro.md)",
		`<a href="./Guide/Intro.md#set-up">set up</a> <a>gone</a>`,
		`<img src="./files/a.png" alt="a.png"> <img alt="b.png">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Join(codes, ",") != "page-embed,link-out-of-scope,attachment-not-mirrored" {
		t.Errorf("warnings = %v", codes)
	}
}
