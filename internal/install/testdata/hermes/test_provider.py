"""Tests for the installed Backstory Hermes memory provider.

BACKSTORY_PLUGIN_DIR points at a plugin directory written by the real
installer (hermes_pytest_test.go). MemoryProvider is a stub of Hermes'
agent/memory_provider.py base (0.19.0).
"""
import abc
import importlib.util
import json
import os
import socket
import sys
import threading
import types

import pytest


class StubMemoryProvider(abc.ABC):
    @property
    @abc.abstractmethod
    def name(self): ...

    @abc.abstractmethod
    def is_available(self): ...

    @abc.abstractmethod
    def initialize(self, session_id, **kwargs): ...

    @abc.abstractmethod
    def get_tool_schemas(self): ...

    def system_prompt_block(self):
        return ""

    def prefetch(self, query, *, session_id=""):
        return ""

    def sync_turn(self, user_content, assistant_content, *, session_id="", messages=None):
        pass

    def handle_tool_call(self, tool_name, args, **kwargs):
        raise NotImplementedError

    def on_session_end(self, messages=None):
        pass

    def shutdown(self):
        pass


@pytest.fixture()
def plugin(monkeypatch):
    pkg = types.ModuleType("agent")
    mod = types.ModuleType("agent.memory_provider")
    mod.MemoryProvider = StubMemoryProvider
    monkeypatch.setitem(sys.modules, "agent", pkg)
    monkeypatch.setitem(sys.modules, "agent.memory_provider", mod)
    path = os.path.join(os.environ["BACKSTORY_PLUGIN_DIR"], "__init__.py")
    spec = importlib.util.spec_from_file_location("backstory_plugin_under_test", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeDaemon:
    """A unix-socket daemon speaking the one-line-in, one-line-out protocol."""

    def __init__(self, path):
        self.path = path
        self.requests = []
        self.replies = {}
        self.srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.srv.bind(path)
        self.srv.listen(8)
        self.thread = threading.Thread(target=self._serve, daemon=True)
        self.thread.start()

    def _serve(self):
        while True:
            try:
                conn, _ = self.srv.accept()
            except OSError:
                return
            with conn:
                buf = b""
                while b"\n" not in buf:
                    chunk = conn.recv(65536)
                    if not chunk:
                        break
                    buf += chunk
                if not buf:
                    continue  # availability probe: connect and close
                req = json.loads(buf.split(b"\n", 1)[0])
                self.requests.append(req)
                reply = self.replies.get(req["method"], {"result": {}})
                conn.sendall(json.dumps(reply).encode() + b"\n")

    def close(self):
        self.srv.close()
        if os.path.exists(self.path):
            os.unlink(self.path)


@pytest.fixture()
def sock_path(tmp_path, monkeypatch):
    p = str(tmp_path / "sock")
    monkeypatch.setenv("BACKSTORY_SOCKET", p)
    return p


@pytest.fixture()
def daemon(sock_path):
    d = FakeDaemon(sock_path)
    yield d
    d.close()


def make_provider(plugin, session="sess-1"):
    p = plugin.BackstoryProvider()
    p.initialize(session)
    return p


def test_name_and_exactly_five_tool_schemas(plugin):
    p = make_provider(plugin)
    assert p.name == "backstory"
    assert isinstance(p, StubMemoryProvider)
    schemas = p.get_tool_schemas()
    assert [s["name"] for s in schemas] == ["recall", "note", "timeline", "confirm", "status"]
    for s in schemas:
        assert s["description"]
        assert s["parameters"]["type"] == "object"


def test_register_hands_provider_to_ctx(plugin):
    got = []
    ctx = types.SimpleNamespace(register_memory_provider=got.append)
    plugin.register(ctx)
    assert len(got) == 1 and got[0].name == "backstory"


def test_is_available_false_without_socket(plugin, sock_path):
    assert not os.path.exists(sock_path)
    assert make_provider(plugin).is_available() is False


def test_is_available_false_for_stale_socket_file(plugin, sock_path):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.bind(sock_path)
    s.close()  # file remains, nobody listening
    assert make_provider(plugin).is_available() is False


def test_is_available_true_with_listening_socket(plugin, daemon):
    assert make_provider(plugin).is_available() is True


def test_system_prompt_block_and_prefetch_return_served_block(plugin, daemon):
    daemon.replies["block"] = {"result": {"block": "WARM BLOCK TEXT"}}
    p = make_provider(plugin, "sess-9")
    assert p.system_prompt_block() == "WARM BLOCK TEXT"
    assert p.prefetch("anything") == "WARM BLOCK TEXT"
    blocks = [r for r in daemon.requests if r["method"] == "block"]
    assert len(blocks) == 1  # cached for the session
    assert blocks[0]["session"] == "sess-9"


def test_block_is_empty_string_when_daemon_absent(plugin, sock_path):
    assert make_provider(plugin).system_prompt_block() == ""


def test_handle_tool_call_round_trips_through_socket(plugin, daemon):
    daemon.replies["note"] = {"result": {"id": "rec-1", "tier": "B"}}
    p = make_provider(plugin, "sess-2")
    out = json.loads(p.handle_tool_call("note", {"kind": "note", "text": "hi", "tier": "A", "session": "forged"}))
    assert out == {"id": "rec-1", "tier": "B"}
    req = daemon.requests[-1]
    assert req["method"] == "note" and req["session"] == "sess-2"
    assert req["params"] == {"kind": "note", "text": "hi"}  # never-list: no declared tier/session


def test_handle_tool_call_daemon_error_and_unknown_tool(plugin, daemon):
    daemon.replies["confirm"] = {"error": {"code": "invalid-params", "message": "bad record"}}
    p = make_provider(plugin)
    assert json.loads(p.handle_tool_call("confirm", {"record_id": "x", "action": "promote"})) == {"error": "bad record"}
    assert "error" in json.loads(p.handle_tool_call("discover", {}))


def test_handle_tool_call_without_daemon_returns_error_json(plugin, sock_path):
    assert "error" in json.loads(make_provider(plugin).handle_tool_call("status", {}))


def test_sync_turn_forwards_new_tool_results_once(plugin, daemon):
    p = make_provider(plugin, "sess-3")
    msgs = [
        {"role": "user", "content": "run it"},
        {"role": "assistant", "content": "", "tool_calls": [
            {"id": "c1", "function": {"name": "terminal", "arguments": json.dumps({"command": "make check"})}},
            {"id": "c2", "function": {"name": "write_file", "arguments": json.dumps({"path": "/x/y.go"})}},
        ]},
        {"role": "tool", "tool_call_id": "c1", "content": json.dumps({"output": "ok", "exit_code": 0})},
        {"role": "tool", "tool_call_id": "c2", "content": "wrote"},
    ]
    p.sync_turn("run it", "done", session_id="sess-3", messages=msgs)
    p.sync_turn("again", "done", session_id="sess-3", messages=msgs)  # replay: no duplicates
    assert p.flush()
    events = [r for r in daemon.requests if r["method"] == "post_tool_use"]
    assert len(events) == 2
    by_id = {e["params"]["tool_use_id"]: e for e in events}
    assert by_id["c1"]["session"] == "sess-3"
    assert by_id["c1"]["params"]["tool_name"] == "Bash"
    assert by_id["c1"]["params"]["command"] == "make check"
    assert by_id["c1"]["params"]["exit"] == 0
    assert by_id["c2"]["params"]["path"] == "/x/y.go"
    assert "exit" not in by_id["c2"]["params"]  # never invented


def test_sync_turn_never_raises_without_daemon(plugin, sock_path):
    p = make_provider(plugin)
    p.sync_turn("u", "a", messages=[{"role": "tool", "tool_call_id": "c", "name": "terminal", "content": "x"}])
    assert p.flush()
    p.on_session_end([])
    p.shutdown()


def _stub_runtime_cwd(monkeypatch, fn):
    pkg = sys.modules.get("agent") or types.ModuleType("agent")
    mod = types.ModuleType("agent.runtime_cwd")
    mod.resolve_agent_cwd = fn
    monkeypatch.setitem(sys.modules, "agent", pkg)
    monkeypatch.setitem(sys.modules, "agent.runtime_cwd", mod)


def _sync_one_tool_event(p):
    msgs = [
        {"role": "assistant", "content": "", "tool_calls": [
            {"id": "c1", "function": {"name": "write_file", "arguments": json.dumps({"path": "/x/y.go"})}},
        ]},
        {"role": "tool", "tool_call_id": "c1", "content": "wrote"},
    ]
    p.sync_turn("u", "a", messages=msgs)
    assert p.flush()


def test_block_and_post_tool_use_carry_resolved_chat_location(plugin, daemon, monkeypatch):
    folder = ["/work/wobble-party"]
    _stub_runtime_cwd(monkeypatch, lambda: folder[0])
    p = make_provider(plugin, "sess-loc")
    p.system_prompt_block()
    _sync_one_tool_event(p)
    folder[0] = "/work/other"  # the chat moves mid-chat: computed per call
    msgs = [
        {"role": "assistant", "content": "", "tool_calls": [
            {"id": "c2", "function": {"name": "write_file", "arguments": json.dumps({"path": "/x/z.go"})}},
        ]},
        {"role": "tool", "tool_call_id": "c2", "content": "wrote"},
    ]
    p.sync_turn("u", "a", messages=msgs)
    assert p.flush()
    block = [r for r in daemon.requests if r["method"] == "block"][0]
    assert block["params"] == {"location": "/work/wobble-party"}
    events = [r for r in daemon.requests if r["method"] == "post_tool_use"]
    assert [e["params"]["location"] for e in events] == ["/work/wobble-party", "/work/other"]


def test_location_falls_back_to_getcwd_without_runtime_cwd_module(plugin, daemon, monkeypatch):
    monkeypatch.setitem(sys.modules, "agent.runtime_cwd", None)  # import raises ImportError
    p = make_provider(plugin)
    p.system_prompt_block()
    _sync_one_tool_event(p)
    for r in daemon.requests:
        assert r["params"]["location"] == os.getcwd()


def test_handle_tool_call_never_carries_or_forwards_location(plugin, daemon, monkeypatch):
    _stub_runtime_cwd(monkeypatch, lambda: "/work/wobble-party")
    daemon.replies["note"] = {"result": {"id": "rec-1"}}
    p = make_provider(plugin)
    p.handle_tool_call("note", {"kind": "note", "text": "hi", "location": "/etc", "cwd": "/etc", "project": "/etc"})
    p.handle_tool_call("recall", {"query": "q", "location": "/etc", "cwd": "/etc"})
    calls = [r for r in daemon.requests if r["method"] in ("note", "recall")]
    assert len(calls) == 2
    for r in calls:
        assert not ({"location", "cwd", "project"} & set(r["params"]))
