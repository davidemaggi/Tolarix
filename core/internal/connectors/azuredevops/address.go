package azuredevops

import (
	"net/url"
	"path"
	"strconv"
	"strings"
)

// ownAddress recognises a link written as the Azure DevOps address of a page
// or file of the wiki being read, as copied from the browser (full, or
// starting at the project):
//
//	…/_wiki/wikis/<wiki>/<page id>/<Page-Title>
//	…/_wiki/wikis/<wiki>?pagePath=/Folder/Page Title
//	…/_git/<repo>?path=/<wiki folder>/Folder/Page.md
//
// ok reports whether u points into this wiki; rel is the path relative to the
// wiki root ("" when it names a page that cannot be found) and frag the
// heading anchor. Addresses of anything else are not this wiki's.
func (s *snapshot) ownAddress(u string) (rel, frag string, ok bool) {
	if !strings.Contains(u, "/_wiki/") && !strings.Contains(u, "/_git/") {
		return "", "", false
	}
	lu, err := url.Parse(u)
	if err != nil {
		return "", "", false
	}
	if lu.Host == "" { // server-relative: /<project>/_wiki/…
		if !strings.HasPrefix(lu.Path, "/") {
			return "", "", false
		}
		base, err := url.Parse(s.r.addr.BaseURL)
		if err != nil {
			return "", "", false
		}
		lu.Scheme, lu.Host = base.Scheme, base.Host
		if lu, err = url.Parse(lu.String()); err != nil {
			return "", "", false
		}
	}
	a, err := parseURL(lu.String(), false)
	if err != nil || orgKey(a.BaseURL) != orgKey(s.r.addr.BaseURL) || !strings.EqualFold(a.Project, s.r.addr.Project) {
		return "", "", false
	}
	q := lu.Query()
	frag = lu.Fragment
	if frag == "" {
		frag = q.Get("anchor")
	}

	if a.Wiki == "" { // a repository address
		if !strings.EqualFold(a.Repo, s.r.addr.Repo) && !strings.EqualFold(a.Repo, s.r.repoID) {
			return "", "", false
		}
		if a.Branch != "" && a.Branch != s.r.addr.Branch {
			return "", "", false // another branch is another version of the wiki
		}
		file := cleanFolder(q.Get("path"))
		folder := s.r.addr.Folder
		switch {
		case folder == "":
			rel = file
		case file == folder:
			rel = ""
		case strings.HasPrefix(file, folder+"/"):
			rel = strings.TrimPrefix(file, folder+"/")
		default:
			return "", "", false // elsewhere in the repository
		}
		if rel == "" {
			return "", "", false // the wiki folder itself: keep the address
		}
		return rel, frag, true
	}

	if !s.isThisWiki(a.Wiki) {
		return "", "", false
	}
	if pp := q.Get("pagePath"); pp != "" {
		return pagePathRel(pp), frag, true
	}
	rest := wikiRest(lu, a.Wiki)
	if len(rest) == 0 {
		return "", "", false // the wiki home: keep the address
	}
	if _, err := strconv.Atoi(rest[0]); err != nil {
		return "", "", false
	}
	if pp := s.pagePath(a.Wiki, rest[0]); pp != "" {
		return pagePathRel(pp), frag, true
	}
	if len(rest) > 1 { // the page id is unknown: try the title in the address
		return s.pageByURLTitle(rest[1]), frag, true
	}
	return "", frag, true
}

// orgKey identifies an organisation whatever the host spelling:
// dev.azure.com/org and org.visualstudio.com are the same.
func orgKey(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return strings.ToLower(base)
	}
	host := strings.ToLower(u.Hostname())
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case host == "dev.azure.com" && segs[0] != "":
		return "ado:" + strings.ToLower(segs[0])
	case strings.HasSuffix(host, ".visualstudio.com"):
		return "ado:" + strings.TrimSuffix(host, ".visualstudio.com")
	}
	return strings.ToLower(u.Host + strings.TrimRight(u.Path, "/"))
}

// wikiRest returns the path segments after …/_wiki/wikis/<wiki>/.
func wikiRest(u *url.URL, wiki string) []string {
	var segs []string
	for _, sg := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		if v, err := url.PathUnescape(sg); err == nil {
			sg = v
		}
		segs = append(segs, sg)
	}
	for i := 0; i+2 < len(segs); i++ {
		if segs[i] == "_wiki" && segs[i+1] == "wikis" && strings.EqualFold(segs[i+2], wiki) {
			return segs[i+3:]
		}
	}
	return nil
}

// pagePathRel turns a wiki page path (/Folder/Page Title) into the page's
// file path relative to the wiki root, without .md.
func pagePathRel(pp string) string {
	var out []string
	for _, sg := range strings.Split(strings.Trim(pp, "/"), "/") {
		if sg != "" {
			out = append(out, EncodeName(sg))
		}
	}
	return path.Join(out...)
}

// isThisWiki reports whether name (or id) is a wiki published from the
// folder being read.
func (s *snapshot) isThisWiki(name string) bool {
	if strings.EqualFold(name, s.r.addr.Wiki) {
		return true
	}
	if !s.wikisRead {
		s.wikisRead = true
		var all list[wikiInfo]
		if err := s.r.client.Get(s.ctx, s.r.api("wiki/wikis", nil), &all); err == nil {
			for _, w := range all.Value {
				if strings.EqualFold(w.RepositoryID, s.r.repoID) && cleanFolder(w.MappedPath) == s.r.addr.Folder {
					s.wikis = append(s.wikis, w)
				}
			}
		}
	}
	for _, w := range s.wikis {
		if strings.EqualFold(name, w.Name) || strings.EqualFold(name, w.ID) {
			return true
		}
	}
	return false
}

// pagePath asks Azure DevOps for the path of a wiki page by its id ("" when
// it cannot be read).
func (s *snapshot) pagePath(wiki, id string) string {
	key := strings.ToLower(wiki) + "/" + id
	if v, ok := s.pageIDs[key]; ok {
		return v
	}
	var pg struct {
		Path string `json:"path"`
	}
	if err := s.r.client.Get(s.ctx, s.r.api("wiki/wikis/"+url.PathEscape(wiki)+"/pages/"+id, nil), &pg); err != nil {
		pg.Path = ""
	}
	if s.pageIDs == nil {
		s.pageIDs = map[string]string{}
	}
	s.pageIDs[key] = pg.Path
	return pg.Path
}

// pageByURLTitle finds the only page whose name matches the title part of a
// wiki address (spaces written as "-"); "" when none or several match.
func (s *snapshot) pageByURLTitle(title string) string {
	want := strings.ToLower(strings.ReplaceAll(title, "-", " "))
	found := ""
	for id, pg := range s.tree.Pages {
		if strings.ToLower(pg.Title) == want {
			if found != "" {
				return ""
			}
			found = id
		}
	}
	return found
}
