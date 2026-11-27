package render

import (
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/convert/mdscan"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// wrapRendered rewrites links/images to the source attachment into a
// rendered image that links to the source.
func wrapRendered(body, srcHash, derivedHash, derivedName string) string {
	srcURL := model.AttachmentLink(srcHash)
	imgURL := model.AttachmentLink(derivedHash)
	return mdscan.ReplaceFunc(body, mdscan.MarkdownLink, func(m []string) (string, bool) {
		u := strings.TrimSuffix(strings.TrimPrefix(m[3], "<"), ">")
		if u != srcURL {
			return "", false
		}
		alt := m[2]
		if alt == "" {
			alt = derivedName
		}
		return "[![" + alt + "](" + imgURL + ")](" + srcURL + ")", true
	})
}
