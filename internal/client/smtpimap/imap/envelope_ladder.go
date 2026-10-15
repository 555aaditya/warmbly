package imap

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/rs/zerolog/log"
	"github.com/warmbly/warmbly/internal/config"
)

// envelopeRung is how one message's envelope is asked for. A message steps
// down a rung each time the server's answer to its current form cannot be
// read, which closes the session: go-imap reads message/global in a
// BODYSTRUCTURE as message/rfc822, so one RFC 6533 bounce failed its whole
// batch on every pass.
type envelopeRung uint8

const (
	// envelopeFull asks for the body structure, in a batch.
	envelopeFull envelopeRung = iota
	// envelopeBare leaves the structure out; the body is parsed locally.
	envelopeBare
	// envelopeAlone is bare, one message per FETCH, so one unreadable
	// message cannot hold back its neighbours.
	envelopeAlone
	// envelopeSkip is a message no form could read; it is left unsynced.
	envelopeSkip
)

// maxEnvelopeRungs bounds the messages a client remembers a rung for.
const maxEnvelopeRungs = 4096

// envelopeRungs remembers the rung of every message that stepped down, per
// folder generation. It lives on the Client, so it outlives a reconnect.
type envelopeRungs struct {
	mu    sync.Mutex
	n     int
	views map[string]map[imap.UID]envelopeRung
}

func (r *envelopeRungs) get(view string, uid imap.UID) envelopeRung {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.views[view][uid]
}

// stepDown moves uids one rung below from and returns the rung they reached.
func (r *envelopeRungs) stepDown(view string, uids []imap.UID, from envelopeRung) envelopeRung {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.views == nil || r.n+len(uids) > maxEnvelopeRungs {
		r.views, r.n = map[string]map[imap.UID]envelopeRung{}, 0
	}
	rungs := r.views[view]
	if rungs == nil {
		rungs = map[imap.UID]envelopeRung{}
		r.views[view] = rungs
	}
	next := from + 1
	for _, uid := range uids {
		if _, ok := rungs[uid]; !ok {
			r.n++
		}
		rungs[uid] = next
	}
	return next
}

// envelopeRequest is one FETCH of the plan for a batch.
type envelopeRequest struct {
	uids []imap.UID
	rung envelopeRung
}

// planEnvelopes splits a batch by rung, keeping each rung's order: the full
// and bare messages as one FETCH each, every alone message on its own, and
// the skipped ones left out.
func (c *Client) planEnvelopes(view string, uids []imap.UID) []envelopeRequest {
	var full, bare []imap.UID
	var plan []envelopeRequest
	for _, uid := range uids {
		switch c.envelopes.get(view, uid) {
		case envelopeFull:
			full = append(full, uid)
		case envelopeBare:
			bare = append(bare, uid)
		case envelopeAlone:
			plan = append(plan, envelopeRequest{uids: []imap.UID{uid}, rung: envelopeAlone})
		}
	}
	if len(bare) > 0 {
		plan = append([]envelopeRequest{{uids: bare, rung: envelopeBare}}, plan...)
	}
	if len(full) > 0 {
		plan = append([]envelopeRequest{{uids: full, rung: envelopeFull}}, plan...)
	}
	return plan
}

// envelopeView names the selected folder generation, which is what a UID
// means anything inside.
func (c *Client) envelopeView() string {
	sel := c.selection.Load()
	if sel == nil {
		return ""
	}
	return sel.name + "\x00" + strconv.FormatUint(uint64(sel.uidValidity), 10)
}

// stepDownEnvelopes records that req's answer could not be read, so the next
// pass asks for those messages the next way down.
func (c *Client) stepDownEnvelopes(view string, req envelopeRequest) {
	next := c.envelopes.stepDown(view, req.uids, req.rung)
	ev := log.Warn().Str("host", c.host()).Int("messages", len(req.uids))
	if sel := c.selection.Load(); sel != nil {
		ev = ev.Str("folder", sel.name)
	}
	switch next {
	case envelopeBare:
		ev.Msg("imap: server sent a body structure that could not be read; asking for these messages without one")
	case envelopeAlone:
		ev.Msg("imap: server sent an envelope batch that could not be read; asking for these messages one at a time")
	default:
		ev.Uint32("uid", uint32(req.uids[0])).Msg("imap: server sent a message that could not be read in any form; leaving it unsynced")
	}
}

// fetchMIMEText reads a message whole, up to the body size cap, and takes its
// text and report parts by parsing the MIME locally, for a message fetched
// without its body structure.
func fetchMIMEText(c *imapclient.Client, uid imap.UID) (plain, html string, err error) {
	raw, err := fetchSections(c, uid, []*imap.FetchItemBodySection{{
		Peek:    true,
		Partial: &imap.SectionPartial{Offset: 0, Size: int64(config.MaxEmailBodySize)},
	}})
	if err != nil {
		return "", "", err
	}
	body, ok := raw[""]
	if !ok {
		return "", "", errors.New("imap: required body section missing")
	}
	plain, html = mimeTextParts(body)
	return plain, html, nil
}

// mimeTextParts applies selectTextParts' rules to a raw message: the first
// part of each type inside an alternative, sibling parts joined elsewhere,
// attachments skipped and report parts kept as plain text. A message cut off
// by the size cap yields the parts read before the cut.
func mimeTextParts(raw []byte) (plain, html string) {
	ent, err := message.Read(bytes.NewReader(raw))
	if ent == nil || err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return "", ""
	}
	alternativeAt := map[string]bool{}
	claimed := map[string]bool{}
	var plainParts, htmlParts []string
	taken := 0
	_ = ent.Walk(func(path []int, part *message.Entity, _ error) error {
		if part == nil {
			return nil
		}
		mediaType, _, _ := part.Header.ContentType()
		mediaType = strings.ToLower(mediaType)
		if strings.HasPrefix(mediaType, "multipart/") {
			alternativeAt[partKey(path)] = mediaType == "multipart/alternative"
			return nil
		}
		if path == nil && mediaType == "" {
			mediaType = "text/plain"
		}
		isReport := reportPartTypes[mediaType]
		if mediaType != "text/plain" && mediaType != "text/html" && !isReport {
			return nil
		}
		if disp, _, _ := part.Header.ContentDisposition(); !isReport && strings.EqualFold(disp, "attachment") {
			return nil
		}
		if taken >= maxTextParts {
			return io.EOF
		}
		if len(path) > 0 {
			if parent := partKey(path[:len(path)-1]); alternativeAt[parent] {
				if claimed[parent+"/"+mediaType] {
					return nil
				}
				claimed[parent+"/"+mediaType] = true
			}
		}
		body, _ := io.ReadAll(io.LimitReader(part.Body, int64(config.MaxEmailBodySize)))
		text := string(body)
		taken++
		if mediaType == "text/html" {
			htmlParts = append(htmlParts, text)
		} else {
			plainParts = append(plainParts, text)
		}
		return nil
	})
	return strings.Join(plainParts, "\n\n"), strings.Join(htmlParts, "")
}
