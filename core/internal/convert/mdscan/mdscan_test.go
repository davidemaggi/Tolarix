package mdscan

import (
	"strings"
	"testing"
)

func TestReplaceSkipsCode(t *testing.T) {
	src := strings.Join([]string{
		"See [[A]] and `[[B]]` here.",
		"```",
		"[[C]]",
		"```",
		"~~~~md",
		"[[D]]",
		"```",
		"[[E]]",
		"~~~~",
		"<!-- [[F]] --> %% [[G]] %% ``x `[[H]]` y`` [[I]]",
		"\\`[[J]]",
		"",
	}, "\n")
	var seen []string
	ReplaceFunc(src, WikiLink, func(m []string) (string, bool) {
		seen = append(seen, m[2])
		return "", false
	})
	if got := strings.Join(seen, ","); got != "A,I,J" {
		t.Fatalf("matched %q", got)
	}
}

func TestUnclosedFenceProtectsRest(t *testing.T) {
	src := "[[A]]\n```\n[[B]]\n"
	var seen []string
	ReplaceFunc(src, WikiLink, func(m []string) (string, bool) { seen = append(seen, m[2]); return "", false })
	if strings.Join(seen, ",") != "A" {
		t.Fatalf("%v", seen)
	}
}

func TestReplaceKeepsUnmatchedTextIdentical(t *testing.T) {
	src := "a [x](y) b ![i](p.png \"t\") c"
	out := ReplaceFunc(src, MarkdownLink, func(m []string) (string, bool) {
		if m[1] == "!" {
			return "IMG(" + m[3] + "|" + m[4] + ")", true
		}
		return "", false
	})
	if out != "a [x](y) b IMG(p.png|t) c" {
		t.Fatalf("%q", out)
	}
}

func TestMarkdownLinkVariants(t *testing.T) {
	cases := map[string]string{
		"[a](b.md)":            "b.md",
		"[a [nested]](b.md)":   "b.md",
		"[a](<with space.md>)": "<with space.md>",
		"[a]( b.md )":          "b.md",
	}
	for in, want := range cases {
		m := MarkdownLink.FindStringSubmatch(in)
		if m == nil || m[3] != want {
			t.Errorf("%s: %v", in, m)
		}
	}
}

func TestSplitFrontMatter(t *testing.T) {
	y, b, ok := SplitFrontMatter("---\ntags: [a]\n---\n# T\n")
	if !ok || y != "tags: [a]\n" || b != "# T\n" {
		t.Fatalf("%q %q %v", y, b, ok)
	}
	if _, b, ok := SplitFrontMatter("# no fm\n---\n"); ok || b != "# no fm\n---\n" {
		t.Fatal("false positive")
	}
	if _, _, ok := SplitFrontMatter("---\nunterminated\n"); ok {
		t.Fatal("unterminated accepted")
	}
	if y, b, ok := SplitFrontMatter("---\r\na: 1\r\n---\r\nx"); !ok || y != "a: 1\r\n" || b != "x" {
		t.Fatalf("crlf: %q %q", y, b)
	}
}

func TestReplaceHTMLLinks(t *testing.T) {
	src := "<table>\n<tr><td><a href=\"tolarix://page/1#A&amp;B\">x</a> <a href=\"tolarix://page/2\">y</a></td>" +
		"<td><img src=\"tolarix://att/abc\" alt=\"a\"></td></tr>\n</table>\n\n`<a href=\"tolarix://page/1\">`\n"
	got := ReplaceHTMLLinks(src, func(attr, u string) (string, bool) {
		switch u {
		case "tolarix://page/1#A&B":
			return "One.md#A&B", true
		case "tolarix://att/abc":
			return attr + ".png", true
		}
		return "", false
	})
	want := "<table>\n<tr><td><a href=\"One.md#A&amp;B\">x</a> <a>y</a></td>" +
		"<td><img src=\"src.png\" alt=\"a\"></td></tr>\n</table>\n\n`<a href=\"tolarix://page/1\">`\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestReplaceFuncRewritesImagesInsideLinks(t *testing.T) {
	src := "[![alt](tolarix://att/png)](tolarix://att/src) and [plain](x)"
	got := ReplaceFunc(src, MarkdownLink, func(m []string) (string, bool) {
		if !strings.HasPrefix(m[3], "tolarix://att/") {
			return "", false
		}
		return m[1] + "[" + m[2] + "](/" + strings.TrimPrefix(m[3], "tolarix://att/") + ")", true
	})
	if want := "[![alt](/png)](/src) and [plain](x)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The outer link is left alone, the image inside it is still rewritten.
	got = ReplaceFunc("[![alt](tolarix://att/png)](https://example.com)", MarkdownLink, func(m []string) (string, bool) {
		if m[1] != "!" {
			return "", false
		}
		return "![" + m[2] + "](/png)", true
	})
	if want := "[![alt](/png)](https://example.com)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
