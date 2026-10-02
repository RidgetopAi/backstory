# managed by `backstory install hermes`; do not edit
"""Backstory as a Hermes Agent memory provider.

Talks to the Backstory daemon over its unix socket with the same line-JSON
protocol `backstory mcp` and `backstory hook` use: one request line
({"session","method","params"}), one response line ({"result"} or {"error"}).
Identity is never sent — the daemon derives tier from the connection
itself. The one thing the plugin does report is its chat's working folder
(`location`, on its own block and post_tool_use calls only): Hermes Desktop
runs one process for every chat, so the daemon's /proc cwd alone cannot say
which project a chat belongs to. The daemon validates it; a model's tool
arguments are never forwarded as one.
"""
import json
import os
import queue
import socket
import stat
import threading

from agent.memory_provider import MemoryProvider

PROVIDER_NAME = "backstory"

# Tunables.
SOCKET_ENV = "BACKSTORY_SOCKET"
DIAL_TIMEOUT_SECONDS = 2.0
CALL_TIMEOUT_SECONDS = 10.0
AVAILABLE_PROBE_TIMEOUT_SECONDS = 0.5
FLUSH_TIMEOUT_SECONDS = 5.0
MAX_RESPONSE_BYTES = 8 * 1024 * 1024
OUTPUT_CAP_CHARS = 65536

METHOD_BLOCK = "block"
METHOD_POST_TOOL_USE = "post_tool_use"
LOCATION_PARAM = "location"

# Hermes tool name -> the tool name Backstory's capture knows (Bash gets a
# tool.result outcome event; file tools get a path).
TOOL_NAME_ALIASES = {"terminal": "Bash"}
PATH_ARG_KEYS = ("path", "file_path")

# The five frozen v0 tools, generated from the daemon's own definitions at
# install time. Hermes tool schemas are {name, description, parameters}.
TOOL_SCHEMAS = json.loads(r'''__BACKSTORY_TOOLS_JSON__''')


def chat_location():
    """The chat's logical working folder, computed per call so a chat whose
    folder changes mid-chat moves with it. Hermes resolves it in
    agent.runtime_cwd.resolve_agent_cwd(); an older Hermes without that
    module (or a resolver that fails) falls back to the process cwd."""
    try:
        from agent.runtime_cwd import resolve_agent_cwd

        folder = resolve_agent_cwd()
        if folder:
            return str(folder)
    except Exception:  # ImportError on older Hermes; never break the agent
        pass
    try:
        return os.getcwd()
    except OSError:
        return ""


class BackstoryError(Exception):
    """A failed daemon call: a dial/IO problem or a daemon-side error."""


def socket_path():
    override = os.environ.get(SOCKET_ENV)
    if override:
        return override
    runtime = os.environ.get("XDG_RUNTIME_DIR")
    if runtime:
        return os.path.join(runtime, "backstory", "sock")
    return os.path.join(os.path.expanduser("~"), ".local", "state", "backstory", "sock")


def call_daemon(method, params=None, session=""):
    """Send one request line, return the decoded result (or raise BackstoryError)."""
    req = {"method": method}
    if session:
        req["session"] = session
    if params is not None:
        req["params"] = params
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        sock.settimeout(DIAL_TIMEOUT_SECONDS)
        sock.connect(socket_path())
        sock.settimeout(CALL_TIMEOUT_SECONDS)
        sock.sendall(json.dumps(req).encode("utf-8") + b"\n")
        buf = b""
        while b"\n" not in buf:
            chunk = sock.recv(65536)
            if not chunk:
                raise BackstoryError("daemon closed the connection before replying")
            buf += chunk
            if len(buf) > MAX_RESPONSE_BYTES:
                raise BackstoryError("daemon response too large")
        resp = json.loads(buf.split(b"\n", 1)[0])
    except (OSError, ValueError) as exc:
        raise BackstoryError(str(exc)) from exc
    finally:
        sock.close()
    err = resp.get("error")
    if err:
        raise BackstoryError(err.get("message") or err.get("code") or "daemon error")
    return resp.get("result")


def _tool_arg_keys(tool_name):
    for schema in TOOL_SCHEMAS:
        if schema["name"] == tool_name:
            return set(schema.get("parameters", {}).get("properties", {}))
    return None


def _exit_code(content):
    """The exit code the tool itself reported, or None — never invented."""
    try:
        data = json.loads(content)
    except (TypeError, ValueError):
        return None
    if isinstance(data, dict):
        for key in ("exit_code", "exit"):
            val = data.get(key)
            if isinstance(val, int) and not isinstance(val, bool):
                return val
    return None


def _content_text(content):
    if isinstance(content, str):
        return content
    if content is None:
        return ""
    try:
        return json.dumps(content)
    except (TypeError, ValueError):
        return str(content)


class BackstoryProvider(MemoryProvider):
    def __init__(self):
        self._session_id = ""
        self._block = None
        self._forwarded = set()
        self._queue = queue.Queue()
        self._worker = None
        self._lock = threading.Lock()

    @property
    def name(self):
        return PROVIDER_NAME

    def is_available(self):
        """True only when the daemon socket exists and accepts a connection."""
        path = socket_path()
        try:
            if not stat.S_ISSOCK(os.stat(path).st_mode):
                return False
            probe = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            try:
                probe.settimeout(AVAILABLE_PROBE_TIMEOUT_SECONDS)
                probe.connect(path)
            finally:
                probe.close()
        except OSError:
            return False
        return True

    def initialize(self, session_id, **kwargs):
        self._session_id = session_id or ""
        self._block = None
        self._forwarded = set()

    def get_tool_schemas(self):
        return [dict(s) for s in TOOL_SCHEMAS]

    def _warm_block(self):
        if self._block is None:
            try:
                result = call_daemon(METHOD_BLOCK, {LOCATION_PARAM: chat_location()}, self._session_id)
            except BackstoryError:
                return ""
            self._block = (result or {}).get("block", "")
        return self._block

    def system_prompt_block(self):
        return self._warm_block()

    def prefetch(self, query, *, session_id=""):
        return self._warm_block()

    def handle_tool_call(self, tool_name, args, **kwargs):
        keys = _tool_arg_keys(tool_name)
        if keys is None:
            return json.dumps({"error": "unknown tool " + str(tool_name)})
        # Forward only the schema's own fields: never a caller-declared
        # tier, session, project or location (AGENT-CONTRACT.md never-list).
        params = {k: v for k, v in (args or {}).items() if k in keys}
        try:
            result = call_daemon(tool_name, params, self._session_id)
        except BackstoryError as exc:
            return json.dumps({"error": str(exc)})
        return json.dumps(result)

    # --- turn sync: tool results as observed events ---

    def sync_turn(self, user_content, assistant_content, *, session_id="", messages=None):
        for event in self._new_tool_events(messages or []):
            self._enqueue(event, session_id or self._session_id)

    def _new_tool_events(self, messages):
        calls = {}
        events = []
        for msg in messages:
            if not isinstance(msg, dict):
                continue
            for call in msg.get("tool_calls") or []:
                fn = call.get("function") or {}
                calls[call.get("id")] = (fn.get("name"), fn.get("arguments"))
            if msg.get("role") != "tool":
                continue
            call_id = msg.get("tool_call_id")
            if call_id in self._forwarded:
                continue
            self._forwarded.add(call_id)
            name, raw_args = calls.get(call_id, (msg.get("name"), None))
            if not name:
                continue
            try:
                args = json.loads(raw_args) if isinstance(raw_args, str) else (raw_args or {})
            except ValueError:
                args = {}
            if not isinstance(args, dict):
                args = {}
            content = _content_text(msg.get("content"))
            params = {"tool_name": TOOL_NAME_ALIASES.get(name, name), "output": content[:OUTPUT_CAP_CHARS]}
            if call_id:
                params["tool_use_id"] = str(call_id)
            for key in PATH_ARG_KEYS:
                if isinstance(args.get(key), str):
                    params["path"] = args[key]
                    break
            if isinstance(args.get("command"), str):
                params["command"] = args["command"]
            code = _exit_code(content)
            if code is not None:
                params["exit"] = code
            events.append(params)
        return events

    def _enqueue(self, params, session):
        with self._lock:
            if self._worker is None or not self._worker.is_alive():
                self._worker = threading.Thread(target=self._drain, name="backstory-sync", daemon=True)
                self._worker.start()
        location = chat_location()
        if location:
            params = dict(params, **{LOCATION_PARAM: location})
        self._queue.put((params, session))

    def _drain(self):
        while True:
            item = self._queue.get()
            try:
                if item is None:
                    return
                try:
                    call_daemon(METHOD_POST_TOOL_USE, item[0], item[1])
                except BackstoryError:
                    pass  # capture is best effort; never break the agent's turn
            finally:
                self._queue.task_done()

    def flush(self, timeout=FLUSH_TIMEOUT_SECONDS):
        """Wait for queued events to be delivered (bounded)."""
        done = threading.Event()

        def wait():
            self._queue.join()
            done.set()

        threading.Thread(target=wait, daemon=True).start()
        return done.wait(timeout)

    def on_session_end(self, messages=None):
        self.flush()

    def shutdown(self):
        self.flush()
        with self._lock:
            worker = self._worker
        if worker is not None and worker.is_alive():
            self._queue.put(None)


def register(ctx):
    ctx.register_memory_provider(BackstoryProvider())
