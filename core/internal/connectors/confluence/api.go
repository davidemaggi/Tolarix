package confluence

import "context"

// api is the part of the Confluence REST API the connector uses. Data Center
// / Server speaks REST v1 (v1.go); Cloud speaks REST v2 (v2.go). Attachment
// uploads and downloads go through the same v1 endpoints on both.
type api interface {
	// page reads a page (with its storage body when body is true).
	page(ctx context.Context, id string, body bool) (*page, error)
	// pageByTitle finds a page of the space by title; nil when there is none.
	pageByTitle(ctx context.Context, title string) (*page, error)
	// space checks that the space exists and is visible.
	space(ctx context.Context) error
	// tree lists rootID (or the whole space when "") and everything below it
	// with bodies: parents before children, children in Confluence's order.
	tree(ctx context.Context, rootID string) ([]fetched, error)
	// children lists what is directly below parentID ("" = the top of the
	// space), for browsing.
	children(ctx context.Context, parentID string) ([]page, error)
	// allPages lists every page of the space with its version.
	allPages(ctx context.Context) ([]page, error)
	// attachments lists the attachments of a page.
	attachments(ctx context.Context, pageID string) ([]attachment, error)
	// create adds a page under parentID ("" = the top of the space).
	create(ctx context.Context, title, parentID, body string) (*page, error)
	// update rewrites a page, moving it under parentID when that is set;
	// version is the new version number.
	update(ctx context.Context, id, title, parentID, body string, version int) (*page, error)
	deletePage(ctx context.Context, id string) error
	deleteAttachment(ctx context.Context, id string) error
	// defaultParent is where mirrors go when no parent is configured and the
	// connection names no page: "" (the top of the space) on Data Center, the
	// space homepage on Cloud.
	defaultParent(ctx context.Context) (id, title string, err error)
}

// page is a page whatever the API version.
type page struct {
	ID       string
	Title    string
	Body     string
	Version  int
	SpaceKey string
	// Folder is a Confluence Cloud folder: it has a title and children but
	// no content.
	Folder bool
	// HasChildren is set when browsing; Cloud cannot tell cheaply and says
	// true.
	HasChildren bool
}

// attachment is an attachment whatever the API version.
type attachment struct {
	ID        string
	Title     string
	MediaType string
	Version   int
	Download  string // path relative to the base URL
}

type fetched struct {
	page   page
	parent string
	order  int
}
