package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/convert/frontmatter"
	"github.com/davidemaggi/tolarix/core/internal/convert/mdscan"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

const fetchers = 8

// hashCache remembers attachment hashes across runs in this process, keyed by
// repository and git object id (which already identifies the content).
var hashCache sync.Map // string -> hashed

type hashed struct {
	hash string
	size int64
}

// wikiFiles is one listing of the wiki folder, paths relative to it.
type wikiFiles struct {
	notes  map[string]string // rel path (with .md) -> object id
	orders map[string]string // dir -> .order object id
	files  map[string]string // rel path -> object id (attachments and other files)
	dirs   map[string]bool
}

func (r *Repo) listWiki(ctx context.Context, commit, folder string, full bool) (*wikiFiles, error) {
	items, err := r.items(ctx, commit, folder, full)
	if err != nil {
		return nil, err
	}
	w := &wikiFiles{notes: map[string]string{}, orders: map[string]string{}, files: map[string]string{}, dirs: map[string]bool{}}
	prefix := repoPath(folder)
	for _, it := range items {
		rel := strings.TrimPrefix(strings.TrimPrefix(it.Path, prefix), "/")
		if rel == "" || it.Path == prefix {
			continue
		}
		base := path.Base(rel)
		switch {
		case it.IsFolder || it.GitObjectType == "tree":
			if !hidden(rel) {
				w.dirs[rel] = true
			}
		case base == ".order" && !hidden(parentDir(rel)):
			w.orders[parentDir(rel)] = it.ObjectID
		case isMD(rel) && !hidden(rel):
			w.notes[rel] = it.ObjectID
		default:
			w.files[rel] = it.ObjectID
		}
	}
	return w, nil
}

// fetchAll downloads blobs concurrently, by key.
func (r *Repo) fetchAll(ctx context.Context, ids map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fetchers)
	for key, id := range ids {
		g.Go(func() error {
			b, err := r.readBlob(gctx, id)
			if err != nil {
				return fmt.Errorf("read %s: %w", key, err)
			}
			mu.Lock()
			out[key] = string(b)
			mu.Unlock()
			return nil
		})
	}
	return out, g.Wait()
}

// Snapshot reads the wiki (the repository folder it is published from).
func (r *Repo) Snapshot(ctx context.Context, w connectors.Warner) (*model.Tree, error) {
	if err := r.resolve(ctx); err != nil {
		return nil, err
	}
	commit, err := r.head(ctx)
	if err != nil {
		return nil, err
	}
	if commit == "" {
		return nil, fmt.Errorf("the branch %s does not exist in the repository; check the address or create the branch", r.addr.Branch)
	}
	files, err := r.listWiki(ctx, commit, r.addr.Folder, true)
	if errors.Is(err, errNoFolder) {
		return nil, fmt.Errorf("the folder /%s does not exist on the branch %s; check the address", r.addr.Folder, r.addr.Branch)
	}
	if err != nil {
		return nil, err
	}
	bodies, err := r.fetchAll(ctx, files.notes)
	if err != nil {
		return nil, err
	}
	orderIDs := map[string]string{}
	for dir, id := range files.orders {
		orderIDs[dir] = id
	}
	orders, err := r.fetchAll(ctx, orderIDs)
	if err != nil {
		return nil, err
	}

	s := &snapshot{r: r, ctx: ctx, w: w, files: files, tree: model.NewTree(), raw: map[string]string{}, heads: map[string][]string{}}
	for rel, body := range bodies {
		s.raw[trimMD(rel)] = body
	}
	// Pages for notes, then placeholders for folders holding notes but no note of their own.
	for rel := range files.notes {
		id := trimMD(rel)
		s.tree.Add(&model.Page{ID: id, ParentID: parentDir(id), Name: DecodeName(path.Base(id)), Title: DecodeName(path.Base(id))})
	}
	for rel := range files.notes {
		for dir := parentDir(trimMD(rel)); dir != ""; dir = parentDir(dir) {
			if _, ok := s.tree.Pages[dir]; ok {
				break
			}
			s.tree.Add(&model.Page{ID: dir, ParentID: parentDir(dir), Name: DecodeName(path.Base(dir)), Title: DecodeName(path.Base(dir)), Placeholder: true})
		}
	}
	assignOrder(s.tree, orders)
	for id, p := range s.tree.Pages {
		if !p.Placeholder {
			s.decode(p, s.raw[id])
		}
	}
	return s.tree, nil
}

// assignOrder numbers siblings by the folder's .order file, then
// alphabetically.
func assignOrder(t *model.Tree, orders map[string]string) {
	byParent := map[string][]string{}
	for id, p := range t.Pages {
		byParent[p.ParentID] = append(byParent[p.ParentID], path.Base(id))
	}
	for parent, names := range byParent {
		sortByOrder(names, parseOrder(orders[parent]))
		for i, n := range names {
			t.Pages[joinRel(parent, n)].Order = i
		}
	}
}

type snapshot struct {
	r     *Repo
	ctx   context.Context //nolint:containedctx // scoped to one Snapshot call
	w     connectors.Warner
	files *wikiFiles
	tree  *model.Tree
	raw   map[string]string // page id -> source body
	heads map[string][]string

	wikis     []wikiInfo // the wikis published from this folder (lazy)
	wikisRead bool
	pageIDs   map[string]string // "<wiki>/<page id>" -> wiki page path ("" = not found)
}

func (s *snapshot) warn(p *model.Page, code, msg string) {
	s.w.Warn(connectors.Warning{PageID: p.ID, Code: code, Message: msg})
}

func (s *snapshot) decode(p *model.Page, src string) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	body := src
	if fm, rest, ok := mdscan.SplitFrontMatter(src); ok {
		if meta, err := frontmatter.Parse(fm); err != nil {
			s.warn(p, "frontmatter-invalid", fmt.Sprintf("the front matter is not valid YAML and was kept as text: %v", err))
		} else {
			p.Meta, body = meta, rest
		}
	}
	body = decodeBlocks(body, func(code, msg string) { s.warn(p, code, msg) })
	body = mdscan.ReplaceFunc(body, reSizedImage, func(m []string) (string, bool) {
		return decodeImageSizes(m[0]), true
	})
	dir := parentDir(p.ID)
	p.Body = mdscan.ReplaceFunc(body, mdscan.MarkdownLink, func(m []string) (string, bool) {
		return s.link(p, dir, m[1] == "!", m[2], m[3], m[4])
	})
}

var reScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// link turns a wiki link (absolute from the wiki root, or relative to the
// page) into a canonical page or attachment link.
func (s *snapshot) link(p *model.Page, dir string, image bool, text, rawURL, title string) (string, bool) {
	u := strings.TrimSuffix(strings.TrimPrefix(rawURL, "<"), ">")
	titlePart := ""
	if title != "" {
		titlePart = ` "` + title + `"`
	}
	// A full address of a page or file of this same wiki: point it at the
	// page, so every Mirror links to its own copy.
	if rel, frag, ok := s.ownAddress(u); ok {
		if rel == "" {
			s.warn(p, "broken-link", fmt.Sprintf("the link to %q points to this wiki, but to a page that does not exist; it is kept unchanged", u))
			return "", false
		}
		return s.resolve(p, image, text, titlePart, rel, frag, isFileLink(rel), u)
	}
	if u == "" || reScheme.MatchString(u) || strings.HasPrefix(u, "//") {
		return "", false
	}
	target, frag, _ := strings.Cut(u, "#")
	target, _, _ = strings.Cut(target, "?")
	if target == "" {
		return "[" + text + "](" + model.PageLink(p.ID, s.heading(p.ID, frag)) + titlePart + ")", true
	}
	var rel string
	if strings.HasPrefix(target, "/") {
		rel = path.Clean(strings.TrimPrefix(target, "/"))
	} else {
		rel = path.Clean(joinRel(dir, target))
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		s.warn(p, "link-outside-master", fmt.Sprintf("the link to %q points outside the wiki; it is kept unchanged", u))
		return "", false
	}
	return s.resolve(p, image, text, titlePart, rel, frag, isFileLink(target), u)
}

// resolve turns a path relative to the wiki root into a canonical page or
// attachment link; u is the link as written, for warnings.
func (s *snapshot) resolve(p *model.Page, image bool, text, titlePart, rel, frag string, file bool, u string) (string, bool) {
	for _, cand := range candidates(rel) {
		if pg, ok := s.tree.Pages[cand]; ok && !file {
			if pg.Placeholder && frag == "" {
				return "[" + text + "](" + model.PageLink(cand, "") + titlePart + ")", true
			}
			return "[" + text + "](" + model.PageLink(cand, s.heading(cand, frag)) + titlePart + ")", true
		}
		if id, ok := s.files.files[cand]; ok {
			hash, err := s.attachment(p, cand, id)
			if err != nil {
				s.warn(p, "attachment-unreadable", fmt.Sprintf("cannot read the attachment %q: %v", cand, err))
				return "", false
			}
			bang := ""
			if image {
				bang = "!"
			}
			return bang + "[" + text + "](" + model.AttachmentLink(hash) + titlePart + ")", true
		}
	}
	s.warn(p, "broken-link", fmt.Sprintf("the link to %q points to a page or file that does not exist; it is kept unchanged", u))
	return "", false
}

// candidates are the spellings a link path may use for a file: as written,
// percent-decoded, without .md, and with the title encoded as a file name.
func candidates(rel string) []string {
	out := []string{rel, trimMD(rel)}
	if dec, err := url.PathUnescape(rel); err == nil && dec != rel {
		out = append(out, dec, trimMD(dec))
		segs := strings.Split(trimMD(dec), "/")
		for i, sg := range segs {
			segs[i] = EncodeName(sg)
		}
		out = append(out, strings.Join(segs, "/"))
	}
	return out
}

// isFileLink reports whether a link names a non-Markdown file.
func isFileLink(target string) bool {
	ext := path.Ext(target)
	return ext != "" && !strings.EqualFold(ext, ".md") && len(ext) <= 6 && !strings.Contains(ext, "%")
}

// heading maps an anchor back to the heading text of page id.
func (s *snapshot) heading(id, anchor string) string {
	if anchor == "" {
		return ""
	}
	if v, err := url.PathUnescape(anchor); err == nil {
		anchor = v
	}
	hs, ok := s.heads[id]
	if !ok {
		hs = headings(s.raw[id])
		s.heads[id] = hs
	}
	if h, ok := headingForSlug(hs, anchor); ok {
		return h
	}
	return anchor
}

// attachment registers a repository file as an attachment of p.
func (s *snapshot) attachment(p *model.Page, rel, objectID string) (string, error) {
	key := s.r.addr.BaseURL + "|" + s.r.repoID + "|" + objectID
	v, ok := hashCache.Load(key)
	if !ok {
		rc, err := s.r.blob(s.ctx, objectID)
		if err != nil {
			return "", err
		}
		hash, size, err := model.HashReader(rc)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		v = hashed{hash, size}
		hashCache.Store(key, v)
	}
	h := v.(hashed)
	if _, ok := s.tree.Attachments[h.hash]; !ok {
		s.tree.Attachments[h.hash] = &model.Attachment{Hash: h.hash, Name: path.Base(rel), Size: h.size,
			MediaType: mediaType(rel), SourceRef: objectID}
	}
	for _, x := range p.Attachments {
		if x == h.hash {
			return h.hash, nil
		}
	}
	p.Attachments = append(p.Attachments, h.hash)
	return h.hash, nil
}

// OpenAttachment downloads a file by its git object id.
func (r *Repo) OpenAttachment(ctx context.Context, ref string) (io.ReadCloser, error) {
	if ref == "" {
		return nil, errors.New("empty attachment reference")
	}
	if err := r.resolve(ctx); err != nil {
		return nil, err
	}
	return r.blob(ctx, ref)
}

// Browse lists the pages and folders directly under parentID ("" = the
// wiki root), in wiki order.
func (r *Repo) Browse(ctx context.Context, parentID string) ([]connectors.Node, error) {
	if err := r.resolve(ctx); err != nil {
		return nil, err
	}
	commit, err := r.head(ctx)
	if err != nil || commit == "" {
		if err == nil {
			err = fmt.Errorf("the branch %s does not exist", r.addr.Branch)
		}
		return nil, err
	}
	files, err := r.listWiki(ctx, commit, joinRel(r.addr.Folder, parentID), false)
	if errors.Is(err, errNoFolder) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	nodes := map[string]*connectors.Node{}
	add := func(name string, dir bool) {
		n := nodes[name]
		if n == nil {
			n = &connectors.Node{ID: joinRel(parentID, name), Title: DecodeName(name)}
			nodes[name] = n
		}
		n.HasChildren = n.HasChildren || dir
	}
	for rel := range files.notes {
		add(trimMD(rel), false)
	}
	for rel := range files.dirs {
		add(rel, true)
	}
	names := make([]string, 0, len(nodes))
	for n := range nodes {
		names = append(names, n)
	}
	var order []string
	if id, ok := files.orders[""]; ok {
		if b, err := r.readBlob(ctx, id); err == nil {
			order = parseOrder(string(b))
		}
	}
	sortByOrder(names, order)
	out := make([]connectors.Node, 0, len(names))
	for _, n := range names {
		out = append(out, *nodes[n])
	}
	return out, nil
}
