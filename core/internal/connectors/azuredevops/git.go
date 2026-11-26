package azuredevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors/filetree"
	"github.com/davidemaggi/tolarix/core/internal/httpc"
)

// zeroID is the "no commit" object id (creating a branch).
const zeroID = "0000000000000000000000000000000000000000"

// maxPushBytes caps the file content sent in one push request. Larger runs
// are split into several pushes.
var maxPushBytes int64 = 64 << 20

// blobSHA is the git object id of content (what the items API reports).
func blobSHA(content []byte) string { return filetree.BlobID(content) }

// repoPath turns a folder-relative path into an absolute repository path.
func repoPath(parts ...string) string {
	var segs []string
	for _, p := range parts {
		if p = strings.Trim(p, "/"); p != "" {
			segs = append(segs, p)
		}
	}
	return "/" + strings.Join(segs, "/")
}

var errNoFolder = errors.New("folder not found")

// items lists the files and folders under folder at commit. A missing
// folder is errNoFolder.
func (r *Repo) items(ctx context.Context, commit, folder string, full bool) ([]gitItem, error) {
	level := "OneLevel"
	if full {
		level = "Full"
	}
	q := url.Values{
		"scopePath":                     {repoPath(folder)},
		"recursionLevel":                {level},
		"versionDescriptor.version":     {commit},
		"versionDescriptor.versionType": {"commit"},
	}
	var res list[gitItem]
	if err := r.client.Get(ctx, r.git("/items", q), &res); err != nil {
		if httpc.StatusOf(err) == http.StatusNotFound {
			return nil, errNoFolder
		}
		return nil, r.friendly(err, "the folder /"+folder)
	}
	return res.Value, nil
}

// blob downloads a file by its object id.
func (r *Repo) blob(ctx context.Context, id string) (io.ReadCloser, error) {
	resp, err := r.client.Do(ctx, httpc.Request{Method: http.MethodGet,
		Path:   r.git("/blobs/"+url.PathEscape(id), url.Values{"$format": {"octetstream"}}),
		Header: http.Header{"Accept": {"application/octet-stream"}}})
	if err != nil {
		return nil, r.friendly(err, "the file "+id)
	}
	return resp.Body, nil
}

func (r *Repo) readBlob(ctx context.Context, id string) ([]byte, error) {
	rc, err := r.blob(ctx, id)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// change is one file change of a push.
type change struct {
	Type    string // add | edit | delete | rename
	Path    string // absolute repository path
	From    string // rename source
	Size    int64
	Content func() ([]byte, error) // add and edit
}

type pushChange struct {
	ChangeType string `json:"changeType"`
	Item       struct {
		Path string `json:"path"`
	} `json:"item"`
	SourceServerItem string       `json:"sourceServerItem,omitempty"`
	NewContent       *pushContent `json:"newContent,omitempty"`
}

type pushContent struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
}

// push commits changes on top of old ("" = create the branch) and returns
// the new head. Runs bigger than maxPushBytes are split into several pushes.
func (r *Repo) push(ctx context.Context, old, message string, changes []change) (string, error) {
	if old == "" {
		old = zeroID
	}
	batches := splitChanges(changes, maxPushBytes)
	for i, b := range batches {
		msg := message
		if len(batches) > 1 {
			msg += fmt.Sprintf(" (part %d of %d)", i+1, len(batches))
		}
		head, err := r.pushOnce(ctx, old, msg, b)
		if err != nil {
			if i > 0 {
				return "", fmt.Errorf("part %d of %d: %w (the earlier parts were pushed)", i+1, len(batches), err)
			}
			return "", err
		}
		old = head
	}
	return old, nil
}

// splitChanges groups changes so each group's content stays under limit
// (a single bigger file gets a group of its own).
func splitChanges(changes []change, limit int64) [][]change {
	var out [][]change
	var cur []change
	var size int64
	for _, c := range changes {
		if len(cur) > 0 && size+c.Size > limit {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, c)
		size += c.Size
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func (r *Repo) pushOnce(ctx context.Context, old, message string, changes []change) (string, error) {
	pcs := make([]pushChange, 0, len(changes))
	for _, c := range changes {
		pc := pushChange{ChangeType: c.Type, SourceServerItem: c.From}
		pc.Item.Path = c.Path
		if c.Content != nil {
			b, err := c.Content()
			if err != nil {
				return "", fmt.Errorf("read %s: %w", c.Path, err)
			}
			pc.NewContent = &pushContent{Content: base64.StdEncoding.EncodeToString(b), ContentType: "base64encoded"}
		}
		pcs = append(pcs, pc)
	}
	body, err := json.Marshal(map[string]any{
		"refUpdates": []map[string]string{{"name": "refs/heads/" + r.addr.Branch, "oldObjectId": old}},
		"commits":    []map[string]any{{"comment": message, "changes": pcs}},
	})
	if err != nil {
		return "", err
	}
	var res struct {
		RefUpdates []struct {
			Name        string `json:"name"`
			NewObjectID string `json:"newObjectId"`
		} `json:"refUpdates"`
	}
	if err := r.client.JSON(ctx, http.MethodPost, r.git("/pushes", nil), json.RawMessage(body), &res); err != nil {
		return "", r.pushError(err)
	}
	if len(res.RefUpdates) == 0 || res.RefUpdates[0].NewObjectID == "" {
		return "", errors.New("the push was accepted by Azure DevOps, but it did not report the new commit")
	}
	return res.RefUpdates[0].NewObjectID, nil
}

func (r *Repo) pushError(err error) error {
	var he *httpc.Error
	msg := err.Error()
	if errors.As(err, &he) {
		msg = he.Message
	}
	switch {
	case strings.Contains(msg, "TF402455") || strings.Contains(msg, "TF402457"):
		return fmt.Errorf("the branch %s is protected by branch policies (pull requests required), so Tolarix cannot push to it. Use a branch without required reviews for the wiki: %s", r.addr.Branch, msg)
	case strings.Contains(msg, "TF401028") || httpc.StatusOf(err) == http.StatusConflict:
		return fmt.Errorf("the branch %s changed while Tolarix was writing (someone pushed at the same time); nothing was written, run the flow again", r.addr.Branch)
	case httpc.StatusOf(err) == http.StatusForbidden:
		return fmt.Errorf("the token cannot push to %s: give it the Code (Read & write) scope and the Contribute permission on the repository", r.addr.Branch)
	}
	return r.friendly(err, "the branch "+r.addr.Branch)
}
