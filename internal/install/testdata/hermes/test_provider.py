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
                reply = self.reply_for(req)
                conn.sendall(json.dumps(reply).encode() + b"\n")

    def reply_for(self, req):
        return self.replies.get(req["method"], {"result": {}})

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


def _check_backstory_off(plugin, daemon, monkeypatch, value, off):
    monkeypatch.setenv("BACKSTORY_OFF", value)
    p = make_provider(plugin, "sess-off")
    block = p.system_prompt_block()
    out = json.loads(p.handle_tool_call("status", {}))
    msgs = [
        {"role": "assistant", "content": "", "tool_calls": [
            {"id": "o1", "function": {"name": "terminal", "arguments": json.dumps({"command": "ls"})}}]},
        {"role": "tool", "tool_call_id": "o1", "content": json.dumps({"output": "x", "exit_code": 0})},
    ]
    p.sync_turn("u", "a", session_id="sess-off", messages=msgs)
    assert p.flush()
    if off:
        assert block == ""
        assert "off for this session" in out["result"]
        assert daemon.requests == []
    else:
        assert daemon.requests


def test_backstory_off_set_silences_provider(plugin, daemon, monkeypatch):
    _check_backstory_off(plugin, daemon, monkeypatch, "1", True)


def test_backstory_off_zero_or_empty_leaves_provider_on(plugin, daemon, monkeypatch):
    _check_backstory_off(plugin, daemon, monkeypatch, "0", False)


# --- post-commit ask via transform_terminal_output ---

ASK = "Backstory: you committed. If this finished something, `note` an outcome."


def _git(repo, *args):
    import subprocess

    subprocess.run(["git", "-C", str(repo), "-c", "user.email=t@t", "-c", "user.name=t", *args],
                   check=True, capture_output=True)


def _head(repo):
    import subprocess

    return subprocess.run(["git", "-C", str(repo), "rev-parse", "HEAD"], check=True,
                          capture_output=True, text=True).stdout.strip()


class CommitDaemon(FakeDaemon):
    """Mirrors the daemon's commit ask: per session, the first opted-in
    `git commit` that exits 0 and moved the location's HEAD earns one ask."""

    def __init__(self, path):
        self.heads = {}
        self.asked = set()
        super().__init__(path)

    def reply_for(self, req):
        if req["method"] != "post_tool_use":
            return super().reply_for(req)
        p, sess = req["params"], req.get("session", "")
        if not p.get("commit_ask") or p.get("tool_name") != "Bash":
            return {"result": {"events_recorded": 2}}
        head = _head(p["location"])
        last = self.heads.get(sess, "")
        self.heads[sess] = head
        commit = p.get("exit") == 0 and p["command"].startswith("git commit") and last not in ("", head)
        if commit and sess not in self.asked:
            self.asked.add(sess)
            return {"result": {"events_recorded": 2, "additional_context": ASK}}
        return {"result": {"events_recorded": 2}}


@pytest.fixture()
def commit_env(plugin, sock_path, tmp_path, monkeypatch):
    repo = tmp_path / "repo"
    repo.mkdir()
    _git(repo, "init", "-q")
    _git(repo, "commit", "-q", "--allow-empty", "-m", "seed")
    _stub_runtime_cwd(monkeypatch, lambda: str(repo))
    d = CommitDaemon(sock_path)
    yield types.SimpleNamespace(repo=repo, daemon=d, plugin=plugin)
    d.close()


def _hook(p, command, repo, rc=0, call_id="t1", out="done"):
    return p.transform_terminal_output(command=command, output=out, returncode=rc,
                                       task_id="x", env_type="local", tool_call_id=call_id)


def _prime(p, env):
    # the first observed call seeds the session's last-seen HEAD
    assert _hook(p, "ls", env.repo, call_id="seed") is None


def _commit(env, n):
    _git(env.repo, "commit", "-q", "--allow-empty", "-m", "c%d" % n)


def test_register_registers_terminal_hook(plugin):
    mem, hooks = [], {}
    ctx = types.SimpleNamespace(register_memory_provider=mem.append,
                                register_hook=lambda name, fn: hooks.setdefault(name, fn))
    plugin.register(ctx)
    assert len(mem) == 1 and "transform_terminal_output" in hooks


def test_hook_asks_once_per_session_and_again_in_a_fresh_one(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-a")
    _prime(p, env)
    _commit(env, 1)
    got = _hook(p, "git commit -m x", env.repo, call_id="k1")
    assert got.startswith("done\n") and "outcome" in got.splitlines()[-1]
    _commit(env, 2)
    assert _hook(p, "git commit -m y", env.repo, call_id="k2") is None
    q = make_provider(env.plugin, "s-b")
    _prime(q, env)
    _commit(env, 3)
    assert "outcome" in _hook(q, "git commit -m z", env.repo, call_id="k3")


def test_hooked_call_is_not_sent_again_by_sync_turn(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-c")
    _prime(p, env)
    _commit(env, 1)
    _hook(p, "git commit -m x", env.repo, call_id="k1")
    msgs = [
        {"role": "assistant", "content": "", "tool_calls": [
            {"id": "k1", "function": {"name": "terminal", "arguments": json.dumps({"command": "git commit -m x"})}},
            {"id": "w1", "function": {"name": "write_file", "arguments": json.dumps({"path": "/a"})}}]},
        {"role": "tool", "tool_call_id": "k1", "content": json.dumps({"output": "x", "exit_code": 0})},
        {"role": "tool", "tool_call_id": "w1", "content": "wrote"},
    ]
    p.sync_turn("u", "a", session_id="s-c", messages=msgs)
    assert p.flush()
    events = [r["params"] for r in env.daemon.requests if r["method"] == "post_tool_use"]
    assert [e["tool_use_id"] for e in events].count("k1") == 1
    assert [e for e in events if e["tool_use_id"] == "w1"]
    assert all("commit_ask" not in e for e in events if e["tool_use_id"] == "w1")
    assert next(e for e in events if e["tool_use_id"] == "k1")["commit_ask"] is True


def test_sync_turn_never_sends_commit_ask(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-d")
    msgs = [
        {"role": "assistant", "content": "", "tool_calls": [
            {"id": "u1", "function": {"name": "terminal", "arguments": json.dumps({"command": "git commit -m x"})}}]},
        {"role": "tool", "tool_call_id": "u1", "content": json.dumps({"output": "x", "exit_code": 0})},
    ]
    p.sync_turn("u", "a", session_id="s-d", messages=msgs)
    assert p.flush()
    events = [r["params"] for r in env.daemon.requests if r["method"] == "post_tool_use"]
    assert len(events) == 1 and "commit_ask" not in events[0]


def test_hook_returns_none_and_settles_nothing_then_real_commit_still_asks(commit_env, monkeypatch):
    env = commit_env
    p = make_provider(env.plugin, "s-e")
    _prime(p, env)
    _commit(env, 1)
    n = len(env.daemon.requests)
    assert _hook(p, "git commit -m x", env.repo, rc=None, call_id="bg") is None  # background output
    monkeypatch.setenv("BACKSTORY_OFF", "1")
    assert _hook(p, "git commit -m x", env.repo, call_id="off") is None
    monkeypatch.setenv("BACKSTORY_OFF", "0")
    assert len(env.daemon.requests) == n  # nothing reached the daemon
    # a non-commit command never asks, and does not settle the ask
    assert _hook(p, "ls", env.repo, call_id="ls1") is None
    _commit(env, 2)
    assert "outcome" in _hook(p, "git commit -m y", env.repo, call_id="real")


def test_hook_none_without_daemon_and_on_timeout(plugin, sock_path, tmp_path, monkeypatch):
    p = make_provider(plugin, "s-f")
    assert _hook(p, "git commit -m x", tmp_path) is None  # no daemon
    assert "t1" not in p._forwarded  # unhandled: sync_turn still forwards it
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(sock_path)
    srv.listen(1)  # accepts into the backlog, never replies
    saved = plugin.HOOK_TIMEOUT_SECONDS
    plugin.HOOK_TIMEOUT_SECONDS = 0.2
    try:
        assert _hook(p, "git commit -m x", tmp_path) is None
    finally:
        plugin.HOOK_TIMEOUT_SECONDS = saved
        srv.close()
    assert "t1" not in p._forwarded


def test_register_without_register_hook_or_when_it_raises(plugin):
    mem = []
    plugin.register(types.SimpleNamespace(register_memory_provider=mem.append))
    assert len(mem) == 1 and mem[0].name == "backstory"

    def boom(name, fn):
        raise RuntimeError("older hermes")

    mem2 = []
    plugin.register(types.SimpleNamespace(register_memory_provider=mem2.append, register_hook=boom))
    assert len(mem2) == 1 and mem2[0].name == "backstory"


# --- v0.21 Hermes: the hook gets no tool_call_id, so sync_turn must not double-send ---

def _term_msgs(*calls):
    """calls: (id, command, exit_code) -> one assistant message plus tool results."""
    msgs = [{"role": "assistant", "content": "", "tool_calls": [
        {"id": i, "function": {"name": "terminal", "arguments": json.dumps({"command": c})}}
        for i, c, _ in calls]}]
    for i, _, rc in calls:
        msgs.append({"role": "tool", "tool_call_id": i,
                     "content": json.dumps({"output": "wrapped", "exit_code": rc})})
    return msgs


def _post_events(env):
    return [r["params"] for r in env.daemon.requests if r["method"] == "post_tool_use"]


def _sync(p, msgs):
    p.sync_turn("u", "a", session_id=p._session_id, messages=msgs)
    assert p.flush()


def test_idless_hook_then_sync_turn_records_each_command_once(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-f")
    _hook(p, "echo a", env.repo, call_id=None)
    _hook(p, "echo b", env.repo, call_id=None)
    _sync(p, _term_msgs(("call_a", "echo a", 0), ("call_b", "echo b", 0)))
    events = _post_events(env)
    assert [e["command"] for e in events] == ["echo a", "echo b"]


def test_same_command_twice_without_ids_recorded_exactly_twice(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-g")
    _hook(p, "make", env.repo, call_id=None)
    _hook(p, "make", env.repo, call_id=None)
    _sync(p, _term_msgs(("call_a", "make", 0), ("call_b", "make", 0)))
    assert [e["command"] for e in _post_events(env)] == ["make", "make"]


def test_unhandled_terminal_calls_are_still_forwarded(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-h")
    _hook(p, "sleep 9", env.repo, rc=None, call_id=None)  # background: hook skips it
    _hook(p, "echo a", env.repo, call_id=None)
    _sync(p, _term_msgs(("call_a", "sleep 9", 0), ("call_b", "echo a", 0), ("call_c", "other", 0)))
    assert sorted(e["command"] for e in _post_events(env)) == ["echo a", "other", "sleep 9"]


def test_exit_mismatch_is_not_a_match(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-i")
    _hook(p, "echo a", env.repo, rc=0, call_id=None)
    _sync(p, _term_msgs(("call_a", "echo a", 1)))
    events = _post_events(env)
    assert len(events) == 2 and events[1]["tool_use_id"] == "call_a"


def test_tool_call_id_dedupe_unchanged(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-j")
    _hook(p, "echo a", env.repo, call_id="call_a")
    _sync(p, _term_msgs(("call_a", "echo a", 0)))
    assert len(_post_events(env)) == 1


def test_unmatched_fingerprint_does_not_suppress_a_later_turn(commit_env):
    env = commit_env
    p = make_provider(env.plugin, "s-k")
    _hook(p, "echo a", env.repo, call_id=None)  # hooked, but this turn's messages lack it
    _sync(p, [])
    _sync(p, _term_msgs(("call_a", "echo a", 0)))
    events = _post_events(env)
    assert len(events) == 2 and events[1]["tool_use_id"] == "call_a"
