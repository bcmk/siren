// FCS websocket wire format: parses raw frame bytes into typed application
// messages and builds outbound frames for the requests we send. The
// dispatcher in main.go does a type switch on the parsed result.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/bcmk/siren/v3/lib/cmdlib"
)

// FCS frame type constants and protocol values.
const (
	mfcFCTypeNull           = 0
	mfcFCTypeLogin          = 1
	mfcFCTypeUsernameLookup = 10
	mfcFCTypeManageList     = 14
	mfcFCTypeSessionState   = 20
	mfcFCTypeRoomData       = 44
	mfcFCTypeExtData        = 81

	mfcFCLCams         = 21
	mfcFCWOptRedisJSON = 256
	mfcFCVideoOffline  = 127

	mfcFrameLenDigits = 6 // width of FCS frame length prefix
)

// FCS handshake/keepalive constants used by the encoders below.
const (
	mfcWSHandshakeFrame = "fcsws_20180422\n\x00"
	mfcNullFrame        = "0 0 0 0 0\n\x00"

	mfcLoginVersion = "20080910"
	mfcLoginCreds   = "guest:guest"
)

// encodeLogin builds an FCTYPE.LOGIN frame for the guest credentials we
// connect with.
func encodeLogin() string {
	return fmt.Sprintf("%d 0 0 %s 0 %s\n\x00",
		mfcFCTypeLogin, mfcLoginVersion, mfcLoginCreds)
}

// encodeUsernameLookupByUID builds an FCTYPE.USERNAMELOOKUP frame whose
// payload is a numeric uid. MFC replies with a SESSIONSTATE-shaped frame
// tagged with the same qid.
func encodeUsernameLookupByUID(qid int64, uid int) string {
	return fmt.Sprintf("%d 0 0 %d %d\n\x00", mfcFCTypeUsernameLookup, qid, uid)
}

// encodeUsernameLookupByName builds an FCTYPE.USERNAMELOOKUP frame whose
// payload is a username. arg2 is a literal 0 (placeholder where the uid
// goes in the by-uid form), and the name lives in the URL-encoded payload
// slot — the format mfcauto/MFCAuto reference clients use. The qid is
// echoed back in arg1 of the reply so deliverLookup can route it.
func encodeUsernameLookupByName(qid int64, name string) string {
	return fmt.Sprintf("%d 0 0 %d 0 %s\n\x00",
		mfcFCTypeUsernameLookup, qid, url.QueryEscape(name))
}

// looksLikeJSONObject reports whether b's first non-whitespace byte is `{`.
// Used to distinguish a SESSIONSTATE payload from MFC's not-found echo
// (the raw query string) on a USERNAMELOOKUP reply.
func looksLikeJSONObject(b []byte) bool {
	for _, c := range b {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		return c == '{'
	}
	return false
}

// frame is one parsed FCS frame body, before decoding into a typed message.
// Use decodeFrame to interpret a frame's payload.
type frame struct {
	fcType, from, to, arg1, arg2 int
	payload                      []byte
}

// parseFrame parses one FCS frame body of the form
// "<type> <from> <to> <arg1> <arg2> <url-encoded-payload>".
// The leading 6-digit length prefix must already be stripped.
func parseFrame(body []byte) (frame, bool) {
	parts := bytes.SplitN(body, []byte(" "), 6)
	if len(parts) < 5 {
		return frame{}, false
	}
	nums := [5]int{}
	for i := range 5 {
		n, err := strconv.Atoi(string(parts[i]))
		if err != nil {
			return frame{}, false
		}
		nums[i] = n
	}
	f := frame{
		fcType: nums[0], from: nums[1], to: nums[2],
		arg1: nums[3], arg2: nums[4],
	}
	if len(parts) == 6 && len(parts[5]) > 0 {
		decoded, err := url.QueryUnescape(string(parts[5]))
		if err != nil {
			return frame{}, false
		}
		f.payload = []byte(decoded)
	}
	return f, true
}

// frameWalkError describes a parser-desync failure inside walkFrames.
// failFrame is the 0-based index of the frame that failed (equivalently,
// the count of frames that parsed cleanly before the failure). failOffset
// is the byte index in the original buffer where parsing failed. origLen
// is the total buffer size, included so log messages can report the
// failure relative to the whole batch. kind is a short description of the
// failure mode.
type frameWalkError struct {
	failFrame  int
	failOffset int
	origLen    int
	kind       string
}

func (e *frameWalkError) Error() string {
	return fmt.Sprintf("%s at byte %d of %d (frame %d)",
		e.kind, e.failOffset, e.origLen, e.failFrame)
}

// walkFrames extracts each length-prefixed FCS frame from data and invokes
// visit on it. It stops early when visit returns true. The websocket
// transport sometimes flushes a message mid-frame (MFC caps writes around
// 22 KiB regardless of FCS frame alignment), so the parser distinguishes
// two outcomes:
//
//   - Clean partial frame: a valid 6-digit prefix that promises more body
//     than is present, or fewer than 6 trailing bytes after the last
//     complete frame. Returns (consumed, nil) where consumed is the
//     offset of the incomplete frame's start. The caller should buffer
//     data[consumed:] and prepend it to the next websocket read.
//
//   - Desync: a junk prefix (non-numeric or negative) or a malformed frame
//     body. Returns a *frameWalkError; the caller should end the session
//     and reconnect for a fresh stream.
//
// On a clean visit-stop or full consumption the returned error is nil and
// consumed equals len(data) up to the last complete frame.
func walkFrames(data []byte, visit func(f frame) (stop bool)) (consumed int, err error) {
	origLen := len(data)
	framesWalked := 0
	for len(data) >= mfcFrameLenDigits {
		offset := origLen - len(data)
		bodyLen, atoErr := strconv.Atoi(string(data[:mfcFrameLenDigits]))
		if atoErr != nil || bodyLen < 0 {
			return offset, &frameWalkError{
				failFrame:  framesWalked,
				failOffset: offset,
				origLen:    origLen,
				kind:       "invalid frame prefix",
			}
		}
		if bodyLen > len(data)-mfcFrameLenDigits {
			// Valid prefix but the body extends past the buffer end:
			// the websocket message ended mid-frame. Caller buffers
			// the partial frame and reads more.
			return offset, nil
		}
		body := data[mfcFrameLenDigits : mfcFrameLenDigits+bodyLen]
		data = data[mfcFrameLenDigits+bodyLen:]
		f, ok := parseFrame(body)
		if !ok {
			return offset, &frameWalkError{
				failFrame:  framesWalked,
				failOffset: offset,
				origLen:    origLen,
				kind:       fmt.Sprintf("malformed frame body, %d bytes", len(body)),
			}
		}
		if visit(f) {
			return origLen - len(data), nil
		}
		framesWalked++
	}
	// 0 ≤ len(data) < mfcFrameLenDigits: a partial prefix at the tail.
	// Treat as an incomplete frame for the caller to buffer; if data is
	// empty, consumed == origLen and the caller has nothing to carry.
	return origLen - len(data), nil
}

// mfcMessage is one typed application-level FCS message decoded from a frame.
// Use a type switch on the concrete types below to dispatch.
type mfcMessage interface {
	isMFCMessage()
}

// bulkRefMsg is an EXTDATA pointer to a deferred MANAGELIST/CAMS bulk dump
// fetched separately over HTTP.
type bulkRefMsg struct {
	ext *mfcExtData
}

func (*bulkRefMsg) isMFCMessage() {}

// sessionStateMsg is one server-pushed SESSIONSTATE update.
type sessionStateMsg struct {
	update sessionUpdate
}

func (*sessionStateMsg) isMFCMessage() {}

// lookupResponseMsg is the answer to a USERNAMELOOKUP request. payload
// is the raw URL-decoded body so a caller that knows the queried name can
// strictly detect MFC's "no such user" marker (the query string echoed
// back) by comparison; update holds the parsed SESSIONSTATE when the
// payload looked like a JSON object, zero otherwise.
type lookupResponseMsg struct {
	qid     int
	payload []byte
	update  sessionUpdate
}

func (*lookupResponseMsg) isMFCMessage() {}

// roomDataMsg is a batched viewer-count update keyed by uid.
type roomDataMsg struct {
	counts map[int]int
}

func (*roomDataMsg) isMFCMessage() {}

// decodeFrame turns a raw frame into a typed message. Returns (nil, nil)
// for unknown fctypes and for extdata frames that aren't a CAMS bulk
// pointer (both are intentionally skipped). Returns a non-nil error when
// a known FCType has a malformed payload — the caller treats this as
// fatal so the session reconnects: silently dropping a bad SESSIONSTATE
// could leave a model "forever online" if its vs=127 event was the one
// we lost.
func decodeFrame(f frame) (mfcMessage, error) {
	switch f.fcType {
	case mfcFCTypeSessionState:
		u, err := parseSessionState(f.payload)
		if err != nil {
			return nil, fmt.Errorf("%w; payload head = %s", err, dumpBytes(f.payload))
		}
		return &sessionStateMsg{update: u}, nil
	case mfcFCTypeUsernameLookup:
		// MFC's "username not found" reply has the raw query string as
		// payload instead of a SESSIONSTATE JSON object. Always carry
		// the raw payload so the caller can strictly compare it to the
		// queried name; only attempt JSON parse when the shape looks
		// right, so a non-JSON echo doesn't tear down the session.
		msg := &lookupResponseMsg{qid: f.arg1, payload: f.payload}
		if looksLikeJSONObject(f.payload) {
			u, err := parseSessionState(f.payload)
			if err != nil {
				return nil, fmt.Errorf("lookup response (qid=%d), %w; payload head = %s",
					f.arg1, err, dumpBytes(f.payload))
			}
			msg.update = u
		}
		return msg, nil
	case mfcFCTypeRoomData:
		counts, err := parseRoomData(f.payload)
		if err != nil {
			return nil, fmt.Errorf("%w; payload head = %s", err, dumpBytes(f.payload))
		}
		return &roomDataMsg{counts: counts}, nil
	case mfcFCTypeExtData:
		return decodeBulkRef(f)
	}
	return nil, nil
}

// decodeBulkRef returns a bulkRefMsg if f is a MANAGELIST/CAMS bulk-dump
// pointer, (nil, nil) when f is an extdata frame we don't care about, or
// (nil, error) when the envelope JSON is malformed. Caller has already
// checked f.fcType == mfcFCTypeExtData.
func decodeBulkRef(f frame) (mfcMessage, error) {
	if f.arg2 != mfcFCWOptRedisJSON {
		return nil, nil
	}
	var ext mfcExtData
	if err := json.Unmarshal(f.payload, &ext); err != nil {
		return nil, fmt.Errorf("extdata, %w; payload head = %s", err, dumpBytes(f.payload))
	}
	if ext.Msg.Type != mfcFCTypeManageList || ext.Msg.Arg2 != mfcFCLCams {
		return nil, nil
	}
	return &bulkRefMsg{ext: &ext}, nil
}

// mfcExtDataMsg is the inner header describing the message the EXTDATA
// envelope refers to.
type mfcExtDataMsg struct {
	Type int `json:"type"`
	From int `json:"from"`
	To   int `json:"to"`
	Arg1 int `json:"arg1"`
	Arg2 int `json:"arg2"`
}

// mfcExtData is the EXTDATA envelope that points to a deferred payload to be
// fetched over HTTP.
type mfcExtData struct {
	Msg     mfcExtDataMsg `json:"msg"`
	MsgLen  int           `json:"msglen"`
	Opts    int           `json:"opts"`
	RespKey int           `json:"respkey"`
	Serv    int           `json:"serv"`
	Type    int           `json:"type"`
}

// bulkRow is one row of a MANAGELIST/CAMS bulk dump.
type bulkRow struct {
	name    string
	uid     int
	vs      int
	rc      int
	topic   string
	camserv int
}

// bulk is a freshly parsed MANAGELIST/CAMS dump. The named type lets us hang
// validation and summary methods off it.
type bulk []bulkRow

// sessionUpdateMeta is the nested "m" group of a SESSIONSTATE message.
type sessionUpdateMeta struct {
	RC    *int    `json:"rc"`
	Topic *string `json:"topic"`
}

// sessionUpdateUser is the nested "u" group of a SESSIONSTATE message.
type sessionUpdateUser struct {
	Camserv *int `json:"camserv"`
}

// sessionUpdate is one SESSIONSTATE message.
// Pointer fields distinguish "absent" from "zero" so we can merge partial
// updates.
type sessionUpdate struct {
	Name *string            `json:"nm"`
	UID  *int               `json:"uid"`
	VS   *int               `json:"vs"`
	M    *sessionUpdateMeta `json:"m"`
	U    *sessionUpdateUser `json:"u"`
}

// rc returns the inner room-count pointer, walking through M.
func (u sessionUpdate) rc() *int {
	if u.M == nil {
		return nil
	}
	return u.M.RC
}

// topic returns the inner topic pointer, walking through M.
func (u sessionUpdate) topic() *string {
	if u.M == nil {
		return nil
	}
	return u.M.Topic
}

// camserv returns the inner camserv pointer, walking through U.
func (u sessionUpdate) camserv() *int {
	if u.U == nil {
		return nil
	}
	return u.U.Camserv
}

// parseSessionState decodes a SESSIONSTATE/USERNAMELOOKUP payload.
func parseSessionState(payload []byte) (sessionUpdate, error) {
	cmdlib.Ltrace("sessionstate raw: %s", payload)
	var u sessionUpdate
	if err := json.Unmarshal(payload, &u); err != nil {
		return sessionUpdate{}, fmt.Errorf("sessionstate, %w", err)
	}
	return u, nil
}

// parseRoomData decodes a ROOMDATA payload (`{uid: rc, ...}`) into a uid → rc
// map.
func parseRoomData(payload []byte) (map[int]int, error) {
	var raw map[string]int
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("roomdata, %w", err)
	}
	parsed := make(map[int]int, len(raw))
	for k, v := range raw {
		uid, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		parsed[uid] = v
	}
	return parsed, nil
}

// parseBulkRData decodes the positional [schema, row, row, ...] format that
// MFC uses for bulk MANAGELIST payloads.
//
// rdata[0] is a schema, e.g. ["uid", "nm", {"m": ["rc", "topic"]}].
// Each subsequent element is a row with values in the same positional order.
func parseBulkRData(rdata []any) (bulk, error) {
	// expectedFields lists every dotted path the daemon reads from a row.
	// We log an error for any missing field so an upstream schema change is
	// visible immediately.
	expectedFields := []string{"uid", "nm", "vs", "m.rc", "m.topic", "u.camserv"}
	if len(rdata) == 0 {
		return nil, nil
	}
	schema, ok := rdata[0].([]any)
	if !ok {
		return nil, errors.New("missing schema")
	}
	paths := make([]string, 0, len(schema))
	for _, item := range schema {
		switch v := item.(type) {
		case string:
			paths = append(paths, v)
		case map[string]any:
			// json.Unmarshal into map[string]any loses object-key order,
			// so multi-key items would silently misalign positions. MFC
			// only ever ships single-key items in practice; reject the
			// rest so a schema change ends the session and we reconnect.
			// If MFC ever does start shipping multi-key items, swap this
			// in for json.Decoder.Token() to walk the schema in source
			// order — straightforward, just not needed today.
			if len(v) != 1 {
				return nil, fmt.Errorf("bulk schema: nested item has %d keys, expected 1", len(v))
			}
			for k, sub := range v {
				subs, ok := sub.([]any)
				if !ok {
					continue
				}
				for _, s := range subs {
					if name, ok := s.(string); ok {
						paths = append(paths, k+"."+name)
					}
				}
			}
		}
	}
	idx := map[string]int{}
	for i, p := range paths {
		idx[p] = i
	}
	// uid is the row's primary key; without it every row would collapse to
	// uid=0 and overwrite each other in the snapshot. Bail so the session
	// ends and we reconnect for a fresh bulk.
	if _, ok := idx["uid"]; !ok {
		return nil, fmt.Errorf("bulk schema missing uid (have %v)", paths)
	}
	var missing []string
	for _, exp := range expectedFields {
		if _, ok := idx[exp]; !ok {
			missing = append(missing, exp)
		}
	}
	if len(missing) > 0 {
		cmdlib.Lerr("bulk schema missing expected fields %v (have %v)", missing, paths)
	}
	get := func(row []any, key string) any {
		i, ok := idx[key]
		if !ok || i >= len(row) {
			return nil
		}
		return row[i]
	}
	toInt := func(v any) int {
		if n, ok := v.(float64); ok {
			return int(n)
		}
		return 0
	}
	toStr := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	out := make(bulk, 0, len(rdata)-1)
	for _, rec := range rdata[1:] {
		row, ok := rec.([]any)
		if !ok {
			continue
		}
		out = append(out, bulkRow{
			name:    toStr(get(row, "nm")),
			uid:     toInt(get(row, "uid")),
			vs:      toInt(get(row, "vs")),
			rc:      toInt(get(row, "m.rc")),
			topic:   toStr(get(row, "m.topic")),
			camserv: toInt(get(row, "u.camserv")),
		})
	}
	return out, nil
}

// decodeMFCTopic URL-decodes an MFC topic string. MFC stores topics inside
// SESSIONSTATE/bulk payloads url-encoded (e.g. "%20" for spaces, "%E2%99%A5"
// for hearts), even though the surrounding frame payload was already
// url-decoded once. Decoding once more yields plain text. On failure (rare;
// only on malformed encoding) we return the raw string.
func decodeMFCTopic(s string) string {
	if s == "" {
		return s
	}
	if decoded, err := url.QueryUnescape(s); err == nil {
		return decoded
	}
	return s
}

// mfcStateName returns a fine-grained human-readable name for an MFC video
// state. Unlike showName, it distinguishes vs values that the bot's ShowKind
// lumps together (e.g., 0 "free" vs 90 "idle"); intended for diagnostic logs.
func mfcStateName(vs int) string {
	switch vs {
	case 0:
		return "free"
	case 2:
		return "away"
	case 12:
		return "private (tx)"
	case 13:
		return "group (tx)"
	case 14:
		return "club (tx)"
	case 90:
		return "idle"
	case 91:
		return "private (rx)"
	case 93:
		return "group (rx)"
	case 94:
		return "club (rx)"
	case mfcFCVideoOffline:
		return "offline"
	}
	return fmt.Sprintf("vs=%d", vs)
}

/*
mfcShowKind maps an MFC video state to a cmdlib ShowKind.

Video state values:

	0  — free chat (public)
	2  — away
	12 — private show (TX)
	13 — group show (TX)
	14 — club show (TX)
	90 — idle (RX)
	91 — private show (RX)
	93 — group show (RX)
	94 — club show (RX)
*/
func mfcShowKind(vs int) cmdlib.ShowKind {
	switch vs {
	case 0, 90:
		return cmdlib.ShowPublic
	case 12, 91:
		return cmdlib.ShowPrivate
	case 13, 93:
		return cmdlib.ShowGroup
	case 14, 94:
		return cmdlib.ShowTicket
	case 2:
		return cmdlib.ShowAway
	}
	return cmdlib.ShowUnknown
}
