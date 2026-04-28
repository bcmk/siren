"""mitmproxy addon that captures MyFreeCams FCS WebSocket frames.

Usage:
    mitmdump -s scripts/mfc_mitmproxy_addon.py
    # or, to redirect output to a file:
    mitmdump -s scripts/mfc_mitmproxy_addon.py 2> /tmp/mfc-frames.jsonl

Each parsed FCS frame is emitted as one JSON line on stderr with these fields:
    ts:      unix timestamp (float)
    dir:     "client->server" or "server->client"
    type:    FCType integer
    from, to, arg1, arg2: header integers
    payload: decoded payload — either parsed JSON when it looks like JSON,
             or the raw decoded string otherwise

The handshake/login/keepalive frames (which lack the 6-digit length prefix on
the client side) are emitted with type=null and the raw text in payload.

To intercept MFC traffic, configure your client (browser or the daemon) to
use mitmproxy as the HTTPS proxy and trust mitmproxy's CA cert.

Browser (throwaway Chrome profile, no system-proxy changes):

    # macOS
    /Applications/Google\\ Chrome.app/Contents/MacOS/Google\\ Chrome \\
        --user-data-dir=/tmp/chrome-mitm \\
        --proxy-server=http://127.0.0.1:8080 \\
        https://www.myfreecams.com &

    # Linux
    google-chrome \\
        --user-data-dir=/tmp/chrome-mitm \\
        --proxy-server=http://127.0.0.1:8080 \\
        https://www.myfreecams.com &

Daemon:

    HTTPS_PROXY=http://127.0.0.1:8080 \\
    SSL_CERT_FILE=~/.mitmproxy/mitmproxy-ca-cert.pem \\
        ./mfc-checker-daemon --daemon
"""

import json
import sys
import time
from urllib.parse import unquote

from mitmproxy import http

FRAME_LEN_DIGITS = 6


def _emit(record: dict) -> None:
    print(json.dumps(record, ensure_ascii=False), file=sys.stderr, flush=True)


def _try_decode_payload(raw: str):
    if not raw:
        return ""
    try:
        decoded = unquote(raw)
    except Exception:  # noqa: BLE001
        decoded = raw
    try:
        return json.loads(decoded)
    except Exception:  # noqa: BLE001
        return decoded


def _parse_frame_body(body: str):
    parts = body.split(" ", 5)
    if len(parts) < 5:
        return None
    try:
        ftype, ffrom, fto, farg1, farg2 = (int(p) for p in parts[:5])
    except ValueError:
        return None
    payload_raw = parts[5] if len(parts) == 6 else ""
    return {
        "type": ftype,
        "from": ffrom,
        "to": fto,
        "arg1": farg1,
        "arg2": farg2,
        "payload": _try_decode_payload(payload_raw),
    }


def _walk_frames(text: str):
    """Yield parsed frames from a length-prefixed FCS stream."""
    i = 0
    while i + FRAME_LEN_DIGITS <= len(text):
        try:
            body_len = int(text[i : i + FRAME_LEN_DIGITS])
        except ValueError:
            return
        i += FRAME_LEN_DIGITS
        if body_len < 0 or i + body_len > len(text):
            return
        body = text[i : i + body_len]
        i += body_len
        parsed = _parse_frame_body(body)
        if parsed is not None:
            yield parsed


class MFCWSTap:
    def websocket_message(self, flow: http.HTTPFlow) -> None:  # noqa: D401
        host = (flow.request.pretty_host or "").lower()
        if "myfreecams.com" not in host:
            return
        if not flow.websocket or not flow.websocket.messages:
            return
        msg = flow.websocket.messages[-1]
        direction = "client->server" if msg.from_client else "server->client"
        ts = time.time()

        # Server-bound frames are length-prefixed by MFC; client-bound writes
        # (handshake, login, keepalive, lookup) are raw lines without the
        # 6-digit prefix.
        text = (
            msg.content
            if isinstance(msg.content, str)
            else msg.content.decode("utf-8", errors="replace")
        )

        if msg.from_client:
            _emit(
                {
                    "ts": ts,
                    "dir": direction,
                    "host": host,
                    "type": None,
                    "raw": text,
                }
            )
            return

        emitted = False
        for frame in _walk_frames(text):
            emitted = True
            _emit({"ts": ts, "dir": direction, "host": host, **frame})
        if not emitted:
            _emit(
                {
                    "ts": ts,
                    "dir": direction,
                    "host": host,
                    "type": None,
                    "raw": text,
                }
            )

    def response(self, flow: http.HTTPFlow) -> None:  # noqa: D401
        # Capture the deferred bulk MANAGELIST/CAMS payload that the websocket
        # EXTDATA pointer refers to. Emit the parsed JSON inline so the bulk
        # appears in the same JSONL stream as the websocket frames.
        host = (flow.request.pretty_host or "").lower()
        if "myfreecams.com" not in host:
            return
        if "FcwExtResp.php" not in flow.request.path:
            return
        if not flow.response or flow.response.content is None:
            return
        body = flow.response.content.decode("utf-8", errors="replace")
        try:
            payload = json.loads(body)
        except Exception:  # noqa: BLE001
            payload = body
        _emit(
            {
                "ts": time.time(),
                "dir": "http response",
                "host": host,
                "kind": "bulk",
                "url": flow.request.pretty_url,
                "payload": payload,
            }
        )


addons = [MFCWSTap()]
