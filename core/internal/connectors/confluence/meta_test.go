package confluence

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

const propsBody = `<ac:structured-macro ac:name="details"><ac:rich-text-body><table><tbody>` +
	`<tr><th>Owner</th><td><ac:link><ri:user ri:userkey="ukey-ada"/></ac:link></td></tr>` +
	`<tr><th>Status</th><td><ac:structured-macro ac:name="status"><ac:parameter ac:name="title">Done</ac:parameter></ac:structured-macro></td></tr>` +
	`<tr><th>Tags</th><td>clashes with the labels</td></tr>` +
	`</tbody></table></ac:rich-text-body></ac:structured-macro>` +
	`<p>Ask <ac:link><ri:user ri:account-id="557058:alan"/></ac:link> or <ac:link><ri:user ri:userkey="ukey-alan"/></ac:link>, ` +
	`see <a href="%s/x/%s">the short link</a>.</p>`

func metaOf(p *model.Page) string {
	var b strings.Builder
	for _, m := range p.Meta {
		fmt.Fprintf(&b, "%s=%v\n", m.Key, m.Value)
	}
	return b.String()
}

// tinyCode is the short-link code of a page id.
func tinyCode(id int) string {
	var b []byte
	for ; id > 0; id >>= 8 {
		b = append(b, byte(id))
	}
	for len(b) < 8 {
		b = append(b, 0)
	}
	s := strings.TrimRight(base64.StdEncoding.EncodeToString(b), "=A")
	return strings.NewReplacer("/", "-", "+", "_").Replace(s)
}

func TestSnapshotFrontMatterOptions(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(map[bool]string{false: "dc", true: "cloud"}[cloud], func(t *testing.T) {
			var f *fakeConfluence
			if cloud {
				f = newCloudFake(t)
			} else {
				f = newFake(t)
			}
			target := f.addPage("Target", "", "<p>t</p>")
			var targetID int
			_, _ = fmt.Sscan(target, &targetID)
			id := f.addPage("Spec", "", fmt.Sprintf(propsBody, f.srv.URL+f.ctx, tinyCode(targetID)))
			f.pages[id].Labels = []string{"api", "draft"}

			cfg := Config{URL: f.url("/spaces/DOC/overview"), Properties: true, PageDetails: true}
			var c *Confluence
			var err error
			if cloud {
				cfg.Email = f.email
				c, err = NewCloud(cfg, secrets.New(f.token))
			} else {
				c, err = New(cfg, secrets.New(f.token))
			}
			if err != nil {
				t.Fatal(err)
			}
			var ws connectors.Warnings
			tree, err := c.Snapshot(context.Background(), &ws)
			if err != nil {
				t.Fatal(err)
			}
			if len(ws.List) != 0 {
				t.Errorf("warnings = %+v", ws.List)
			}
			p := tree.Pages[id]
			url, author, created, updated := f.url("/pages/viewpage.action?pageId="+id), "Ada Lovelace", "2024-01-02T10:00:00.000+01:00", "2024-03-04T12:00:00.000+01:00"
			if cloud {
				url, created, updated = f.url("/spaces/DOC/pages/"+id), "2024-01-02T09:00:00.000Z", "2024-03-04T11:00:00.000Z"
			}
			want := "tags=[api draft]\nconfluence_id=" + id + "\nconfluence_url=" + url + "\nauthor=" + author +
				"\ncreated=" + created + "\nupdated=" + updated + "\nupdated_by=Alan Turing\nversion=1\n" +
				"Owner=Ada Lovelace\nStatus=Done\n"
			if got := metaOf(p); got != want {
				t.Errorf("meta:\n%s\nwant:\n%s", got, want)
			}
			// Mentions show names (an unknown kind of reference keeps its id);
			// the short link points to the page.
			mentions := "Ask @Alan Turing or @Alan Turing"
			if !strings.Contains(p.Body, mentions) {
				t.Errorf("body = %q, want %q", p.Body, mentions)
			}
			if !strings.Contains(p.Body, "[the short link](tolarix://page/"+target+")") {
				t.Errorf("body = %q, short link not resolved", p.Body)
			}

			// Options off: no front matter.
			cfg.Properties, cfg.PageDetails = false, false
			if cloud {
				c, _ = NewCloud(cfg, secrets.New(f.token))
			} else {
				c, _ = New(cfg, secrets.New(f.token))
			}
			tree, err = c.Snapshot(context.Background(), connectors.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if got := metaOf(tree.Pages[id]); got != "" {
				t.Errorf("meta with options off = %q", got)
			}
		})
	}
}
