package azuredevops

import (
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

func TestEncodePageEmbedAndHTMLTable(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	p := &model.Page{ID: "1", Body: "![Intro](tolarix://page/2)\n\n<table>\n" +
		`<tr><td colspan="2"><a href="tolarix://page/2#Set%20up">set up</a> <a href="tolarix://page/9">gone</a></td></tr>` + "\n" +
		`<tr><td><img src="tolarix://att/` + hash + `" alt="a.png"></td><td>x</td></tr>` + "\n</table>\n"}
	l := &connectors.Layout{
		Pages:       map[string]connectors.Placement{"1": {Key: "Home.md"}, "2": {Key: "Guide/Getting-Started.md"}},
		Attachments: map[string]string{hash: ".attachments/a.png"},
	}
	var codes []string
	out, err := (&Format{}).Encode(p, l, connectors.WarnFunc(func(w connectors.Warning) { codes = append(codes, w.Code) }))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"[Intro](/Guide/Getting-Started)",
		`<a href="/Guide/Getting-Started#set-up">set up</a> <a>gone</a>`,
		`<img src="/.attachments/a.png" alt="a.png">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Join(codes, ",") != "page-embed,link-out-of-scope" {
		t.Errorf("warnings = %v", codes)
	}
}
