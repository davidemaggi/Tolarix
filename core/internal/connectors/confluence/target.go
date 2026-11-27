package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/convert/storage"
	"github.com/davidemaggi/tolarix/core/internal/httpc"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

const (
	maxTitle       = 255
	placeholderXML = `<ac:structured-macro ac:name="children"/>`
	// headerPrefix starts the first line of encoded content: it names the
	// parent page, so moving a page under another parent changes the content
	// hash and is written. WritePage strips it.
	headerPrefix = "tolarix-parent:"
)

// Open starts a write session under the location's parent page.
func (c *Confluence) Open(ctx context.Context, raw json.RawMessage) (connectors.Session, error) {
	if err := c.Test(ctx); err != nil {
		return nil, err
	}
	var loc Location
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &loc); err != nil {
			return nil, fmt.Errorf("invalid mirror location: %w", err)
		}
	}
	s := &session{c: c, space: c.addr.SpaceKey, byTitle: map[string]*pageRef{},
		atts: map[string]map[string]string{}, pending: map[string][]pendingAtt{}}
	parent := strings.TrimSpace(loc.Parent)
	switch {
	case parent != "":
		p, err := c.findPage(ctx, parent)
		if err != nil {
			return nil, err
		}
		s.rootID, s.rootTitle = p.ID, p.Title
	case c.addr.PageID != "":
		s.rootID, s.rootTitle = c.addr.PageID, c.addr.PageTitle
	default:
		id, title, err := c.api.defaultParent(ctx)
		if err != nil {
			return nil, c.friendly(err, "the space "+c.addr.SpaceKey)
		}
		s.rootID, s.rootTitle = id, title
	}
	return s, nil
}

// findPage resolves a page given by numeric id or by title in the space.
func (c *Confluence) findPage(ctx context.Context, ref string) (*page, error) {
	if _, err := strconv.ParseUint(ref, 10, 64); err == nil {
		p, err := c.api.page(ctx, ref, false)
		if err == nil {
			return p, nil
		}
		if httpc.StatusOf(err) != http.StatusNotFound {
			return nil, c.friendly(err, "the page "+ref)
		}
	}
	p, err := c.api.pageByTitle(ctx, ref)
	if err != nil {
		return nil, c.friendly(err, "the space "+c.addr.SpaceKey)
	}
	if p == nil {
		return nil, fmt.Errorf("the parent page %q does not exist in the space %s; create it first or fix the mirror location", ref, c.addr.SpaceKey)
	}
	return p, nil
}

type pageRef struct {
	id      string
	title   string
	version int
}

type pendingAtt struct {
	owner string
	name  string
	file  string
}

type session struct {
	c         *Confluence
	space     string
	rootID    string // "" = top of the space
	rootTitle string

	byTitle map[string]*pageRef          // lower-cased title -> page, whole space
	atts    map[string]map[string]string // page id -> filename -> attachment id
	pending map[string][]pendingAtt      // lower-cased owner title -> uploads waiting for the page
	archive *pageRef
}

var _ connectors.Session = (*session)(nil)

func (s *session) archiveTitle() string {
	return "Archived – " + firstNonEmpty(s.rootTitle, "Tolarix")
}

// Layout gives every page a title unique in the space: a duplicate gets its
// parent's title in brackets, then a number.
func (s *session) Layout(tree *model.Tree, w connectors.Warner) *connectors.Layout {
	l := &connectors.Layout{Pages: map[string]connectors.Placement{}, Attachments: map[string]string{}}
	used := map[string]bool{strings.ToLower(s.archiveTitle()): true}
	if s.rootTitle != "" {
		used[strings.ToLower(s.rootTitle)] = true
	}
	attNames := map[string]map[string]bool{} // owner -> lower names
	tree.Walk(func(p *model.Page, _ int) {
		parentKey := ""
		if pp, ok := l.Pages[p.ParentID]; ok {
			parentKey = pp.Key
		}
		base := cleanTitle(firstNonEmpty(p.Title, p.Name, p.ID))
		key := base
		if used[strings.ToLower(key)] {
			if parentKey != "" {
				key = cleanTitle(base + " (" + parentKey + ")")
			}
			for n := 2; used[strings.ToLower(key)]; n++ {
				key = cleanTitle(base + " (" + strconv.Itoa(n) + ")")
			}
			w.Warn(connectors.Warning{PageID: p.ID, Code: "title-renamed",
				Message: fmt.Sprintf("Confluence needs page titles to be unique in a space: %q is mirrored as %q", base, key)})
		}
		used[strings.ToLower(key)] = true
		l.Pages[p.ID] = connectors.Placement{Key: key, ParentKey: parentKey, Title: key}

		for _, h := range p.Attachments {
			if _, done := l.Attachments[h]; done {
				continue
			}
			a := tree.Attachments[h]
			if a == nil {
				continue
			}
			if attNames[key] == nil {
				attNames[key] = map[string]bool{}
			}
			name := uniqueName(cleanFilename(a.Name, h), attNames[key])
			l.Attachments[h] = key + "/" + name
		}
	})
	return l
}

func cleanTitle(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		t = "Untitled"
	}
	for utf8.RuneCountInString(t) > maxTitle {
		_, size := utf8.DecodeLastRuneInString(t)
		t = t[:len(t)-size]
	}
	return t
}

func cleanFilename(name, hash string) string {
	name = strings.NewReplacer("/", "_", "\\", "_").Replace(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		name = hash[:12]
	}
	return name
}

func uniqueName(name string, used map[string]bool) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	out := name
	for n := 2; used[strings.ToLower(out)]; n++ {
		out = stem + " (" + strconv.Itoa(n) + ")" + ext
	}
	used[strings.ToLower(out)] = true
	return out
}

func splitAttKey(key string) (owner, name string) {
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return "", key
	}
	return key[:i], key[i+1:]
}

// Encode renders a page as storage format, preceded by the parent header.
func (s *session) Encode(_ *model.Tree, p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error) {
	pl := l.Pages[p.ID]
	var body string
	if p.Placeholder {
		body = placeholderXML
	} else {
		body = storage.FromMarkdown(p.Body, &pageWriter{page: p, layout: l, self: pl.Key, w: w})
	}
	return []byte(headerPrefix + pl.ParentKey + "\n" + body), nil
}

func splitContent(content []byte) (parentKey, body string) {
	s := string(content)
	if rest, ok := strings.CutPrefix(s, headerPrefix); ok {
		parentKey, body, _ = strings.Cut(rest, "\n")
		return parentKey, body
	}
	return "", s
}

type pageWriter struct {
	page   *model.Page
	layout *connectors.Layout
	self   string
	w      connectors.Warner
}

func (pw *pageWriter) SelfID() string { return pw.page.ID }

func (pw *pageWriter) PageTitle(id string) (string, bool) {
	p, ok := pw.layout.Pages[id]
	return p.Key, ok
}

func (pw *pageWriter) Attachment(hash string) (string, string, bool) {
	key, ok := pw.layout.Attachments[hash]
	if !ok {
		return "", "", false
	}
	owner, name := splitAttKey(key)
	if owner == pw.self {
		owner = ""
	}
	return name, owner, true
}

func (pw *pageWriter) Warn(code, message string) {
	pw.w.Warn(connectors.Warning{PageID: pw.page.ID, Code: code, Message: message})
}

func (s *session) ContentHash(content []byte) string { return model.HashBytes(content) }

// Inventory lists every page of the space by title: titles are unique per
// space, so a page anywhere in it blocks the title.
func (s *session) Inventory(ctx context.Context) (map[string]connectors.MirrorItem, error) {
	pages, err := s.c.api.allPages(ctx)
	if err != nil {
		return nil, s.c.friendly(err, "the space "+s.space)
	}
	out := make(map[string]connectors.MirrorItem, len(pages))
	for _, p := range pages {
		s.byTitle[strings.ToLower(p.Title)] = &pageRef{id: p.ID, title: p.Title, version: p.Version}
		out[p.Title] = connectors.MirrorItem{ID: p.ID, Key: p.Title, Version: strconv.Itoa(p.Version)}
	}
	return out, nil
}

func (s *session) parentID(parentKey string) (string, error) {
	if parentKey == "" {
		return s.rootID, nil
	}
	if p := s.byTitle[strings.ToLower(parentKey)]; p != nil {
		return p.id, nil
	}
	return "", fmt.Errorf("the parent page %q is not in the mirror yet", parentKey)
}

func (s *session) WritePage(ctx context.Context, w connectors.PageWrite) (connectors.MirrorItem, error) {
	parentKey, body := splitContent(w.Content)
	if w.Placeholder {
		body = placeholderXML
	}
	parentID, err := s.parentID(parentKey)
	if err != nil {
		return connectors.MirrorItem{}, err
	}
	title := w.Placement.Key
	hash := s.ContentHash(w.Content)
	if w.Placeholder {
		hash = ""
	}

	var res *page
	switch {
	case w.Existing == nil && w.Placeholder && s.byTitle[strings.ToLower(title)] != nil:
		// A folder-only page whose title already exists: use that page as is.
		p := s.byTitle[strings.ToLower(title)]
		return connectors.MirrorItem{ID: p.id, Key: p.title, Version: strconv.Itoa(p.version)}, s.flush(ctx, p.title)
	case w.Existing == nil:
		if res, err = s.c.api.create(ctx, title, parentID, body); err != nil {
			return connectors.MirrorItem{}, fmt.Errorf("create %q: %w", title, err)
		}
	default:
		id := w.Existing.ID
		version, err := s.currentVersion(ctx, id)
		if err != nil {
			return connectors.MirrorItem{}, err
		}
		if res, err = s.c.api.update(ctx, id, title, parentID, body, version+1); err != nil {
			return connectors.MirrorItem{}, fmt.Errorf("update %q: %w", title, err)
		}
		if old := w.Existing.Key; old != "" && !strings.EqualFold(old, title) {
			delete(s.byTitle, strings.ToLower(old))
		}
	}
	ref := &pageRef{id: res.ID, title: res.Title, version: res.Version}
	s.byTitle[strings.ToLower(res.Title)] = ref
	if err := s.flush(ctx, res.Title); err != nil {
		return connectors.MirrorItem{}, err
	}
	return connectors.MirrorItem{ID: res.ID, Key: res.Title, Hash: hash, Version: strconv.Itoa(ref.version)}, nil
}

// currentVersion is the page's version now (it may have been edited since
// the mapping was saved).
func (s *session) currentVersion(ctx context.Context, id string) (int, error) {
	for _, p := range s.byTitle {
		if p.id == id {
			return p.version, nil
		}
	}
	p, err := s.c.api.page(ctx, id, false)
	if err != nil {
		return 0, err
	}
	return p.Version, nil
}

func (s *session) DeletePage(ctx context.Context, item connectors.MirrorItem) error {
	err := s.c.api.deletePage(ctx, item.ID)
	if err != nil && httpc.StatusOf(err) != http.StatusNotFound {
		return err
	}
	delete(s.byTitle, strings.ToLower(item.Key))
	return nil
}

// ArchivePage moves a page under the archive page and renames it
// "<title> (archived)".
func (s *session) ArchivePage(ctx context.Context, item connectors.MirrorItem) (connectors.MirrorItem, error) {
	arch, err := s.archivePage(ctx)
	if err != nil {
		return connectors.MirrorItem{}, err
	}
	cur, err := s.c.api.page(ctx, item.ID, true)
	if err != nil {
		return connectors.MirrorItem{}, err
	}
	base := cleanTitle(strings.TrimSuffix(cur.Title, " (archived)") + " (archived)")
	title := base
	for n := 2; ; n++ {
		p := s.byTitle[strings.ToLower(title)]
		if p == nil || p.id == item.ID {
			break
		}
		title = cleanTitle(strings.TrimSuffix(base, " (archived)") + " (archived " + strconv.Itoa(n) + ")")
	}
	res, err := s.c.api.update(ctx, item.ID, title, arch.id, cur.Body, cur.Version+1)
	if err != nil {
		return connectors.MirrorItem{}, fmt.Errorf("archive %q: %w", cur.Title, err)
	}
	delete(s.byTitle, strings.ToLower(item.Key))
	s.byTitle[strings.ToLower(res.Title)] = &pageRef{id: res.ID, title: res.Title, version: res.Version}
	return connectors.MirrorItem{ID: res.ID, Key: res.Title, Hash: item.Hash, Version: strconv.Itoa(res.Version)}, nil
}

func (s *session) archivePage(ctx context.Context) (*pageRef, error) {
	if s.archive != nil {
		return s.archive, nil
	}
	title := s.archiveTitle()
	if p := s.byTitle[strings.ToLower(title)]; p != nil {
		s.archive = p
		return p, nil
	}
	if p, err := s.c.api.pageByTitle(ctx, title); err != nil {
		return nil, err
	} else if p != nil {
		s.archive = &pageRef{id: p.ID, title: p.Title, version: p.Version}
		return s.archive, nil
	}
	body := `<p>Pages removed from the Master are moved here by Tolarix. You can delete them when they are no longer needed.</p>`
	res, err := s.c.api.create(ctx, title, s.rootID, body)
	if err != nil {
		return nil, fmt.Errorf("create the archive page %q: %w", title, err)
	}
	s.archive = &pageRef{id: res.ID, title: res.Title, version: res.Version}
	s.byTitle[strings.ToLower(res.Title)] = s.archive
	return s.archive, nil
}

// ---- attachments ----

// ownerAttachments lists the attachments of a page (filename -> id).
func (s *session) ownerAttachments(ctx context.Context, pageID string) (map[string]string, error) {
	if m, ok := s.atts[pageID]; ok {
		return m, nil
	}
	list, err := s.c.api.attachments(ctx, pageID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(list))
	for _, a := range list {
		m[a.Title] = a.ID
	}
	s.atts[pageID] = m
	return m, nil
}

func (s *session) HasAttachment(ctx context.Context, key string) (bool, error) {
	owner, name := splitAttKey(key)
	p := s.byTitle[strings.ToLower(owner)]
	if p == nil {
		return false, nil
	}
	m, err := s.ownerAttachments(ctx, p.id)
	if err != nil {
		return false, err
	}
	_, ok := m[name]
	return ok, nil
}

// PutAttachment uploads now when the owner page exists, otherwise right
// after WritePage creates it (attachments are uploaded before pages).
func (s *session) PutAttachment(ctx context.Context, key string, r io.Reader) error {
	owner, name := splitAttKey(key)
	f, err := os.CreateTemp("", "tolarix-att-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	lo := strings.ToLower(owner)
	s.pending[lo] = append(s.pending[lo], pendingAtt{owner: owner, name: name, file: f.Name()})
	if s.byTitle[lo] != nil {
		return s.flush(ctx, owner)
	}
	return nil
}

// flush uploads the attachments waiting for the page titled owner.
func (s *session) flush(ctx context.Context, owner string) error {
	lo := strings.ToLower(owner)
	queue := s.pending[lo]
	if len(queue) == 0 {
		return nil
	}
	p := s.byTitle[lo]
	if p == nil {
		return fmt.Errorf("the page %q for its attachments does not exist", owner)
	}
	var errs []error
	var left []pendingAtt
	for _, a := range queue {
		if err := s.upload(ctx, p.id, a); err != nil {
			errs = append(errs, fmt.Errorf("upload %q to %q: %w", a.name, owner, err))
			left = append(left, a)
			continue
		}
		_ = os.Remove(a.file)
	}
	s.pending[lo] = left
	if len(left) == 0 {
		delete(s.pending, lo)
	}
	return errors.Join(errs...)
}

func (s *session) upload(ctx context.Context, pageID string, a pendingAtt) error {
	m, err := s.ownerAttachments(ctx, pageID)
	if err != nil {
		return err
	}
	endpoint := "/rest/api/content/" + url.PathEscape(pageID) + "/child/attachment"
	if id, ok := m[a.name]; ok {
		endpoint += "/" + url.PathEscape(id) + "/data"
	}
	var head, tail bytes.Buffer
	mw := multipart.NewWriter(&head)
	_ = mw.WriteField("minorEdit", "true")
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, strings.NewReplacer(`"`, "%22", "\r", "", "\n", "").Replace(a.name)))
	hdr.Set("Content-Type", "application/octet-stream")
	if _, err := mw.CreatePart(hdr); err != nil {
		return err
	}
	headBytes := append([]byte(nil), head.Bytes()...)
	head.Reset()
	_ = mw.Close()
	tail.Write(head.Bytes())
	tailBytes := tail.Bytes()

	resp, err := s.c.client.Do(ctx, httpc.Request{
		Method: http.MethodPost, Path: endpoint,
		Header: http.Header{"Content-Type": {mw.FormDataContentType()}, "X-Atlassian-Token": {"no-check"}},
		Body: func() (io.Reader, error) {
			f, err := os.Open(a.file)
			if err != nil {
				return nil, err
			}
			return io.MultiReader(bytes.NewReader(headBytes), &closingReader{f}, bytes.NewReader(tailBytes)), nil
		},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var res results[content]
	var one content // the data endpoint may answer with the attachment itself
	switch {
	case json.Unmarshal(raw, &res) == nil && len(res.Results) > 0:
		m[a.name] = res.Results[0].ID
	case json.Unmarshal(raw, &one) == nil && one.ID != "":
		m[a.name] = one.ID
	}
	return nil
}

// closingReader closes the file once it has been read to the end.
type closingReader struct{ f *os.File }

func (r *closingReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	if err == io.EOF {
		_ = r.f.Close()
	}
	return n, err
}

func (s *session) DeleteAttachment(ctx context.Context, key string) error {
	owner, name := splitAttKey(key)
	p := s.byTitle[strings.ToLower(owner)]
	if p == nil {
		return nil // the page is gone, and its attachments with it
	}
	m, err := s.ownerAttachments(ctx, p.id)
	if err != nil {
		return err
	}
	id, ok := m[name]
	if !ok {
		return nil
	}
	if err := s.c.api.deleteAttachment(ctx, id); err != nil && httpc.StatusOf(err) != http.StatusNotFound {
		return err
	}
	delete(m, name)
	return nil
}

// Finish uploads attachments still waiting for their page and removes
// temporary files.
func (s *session) Finish(ctx context.Context, commit bool) error {
	var errs []error
	for lo, queue := range s.pending {
		if commit && len(queue) > 0 {
			if s.byTitle[lo] != nil {
				if err := s.flush(ctx, s.byTitle[lo].title); err != nil {
					errs = append(errs, err)
				}
			} else {
				errs = append(errs, fmt.Errorf("%d attachment(s) were not uploaded because the page %q was not written", len(queue), queue[0].owner))
			}
		}
	}
	for _, queue := range s.pending {
		for _, a := range queue {
			_ = os.Remove(a.file)
		}
	}
	s.pending = map[string][]pendingAtt{}
	return errors.Join(errs...)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
