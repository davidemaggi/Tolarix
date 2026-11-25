package model

import (
	"net/url"
	"strings"
)

// Canonical link schemes used inside canonical Markdown bodies.
const (
	PageLinkPrefix = "tolarix://page/"
	AttLinkPrefix  = "tolarix://att/"
)

// PageLink builds the canonical URL for a page, with an optional heading fragment.
func PageLink(id, heading string) string {
	u := PageLinkPrefix + url.PathEscape(id)
	if heading != "" {
		u += "#" + url.PathEscape(heading)
	}
	return u
}

// AttachmentLink builds the canonical URL for an attachment hash.
func AttachmentLink(hash string) string { return AttLinkPrefix + hash }

// ParsePageLink extracts id and heading from a canonical page URL.
func ParsePageLink(u string) (id, heading string, ok bool) {
	rest, found := strings.CutPrefix(u, PageLinkPrefix)
	if !found {
		return "", "", false
	}
	idPart, frag, _ := strings.Cut(rest, "#")
	id, err := url.PathUnescape(idPart)
	if err != nil || id == "" {
		return "", "", false
	}
	if frag != "" {
		if heading, err = url.PathUnescape(frag); err != nil {
			heading = frag
		}
	}
	return id, heading, true
}

// ParseAttachmentLink extracts the hash from a canonical attachment URL.
func ParseAttachmentLink(u string) (string, bool) {
	h, ok := strings.CutPrefix(u, AttLinkPrefix)
	if !ok || h == "" {
		return "", false
	}
	return h, true
}
