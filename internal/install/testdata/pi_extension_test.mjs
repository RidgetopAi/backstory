// Drives the emitted Pi extension (index.ts, type-stripped by node) against a
// stub `pi` object and the fake backstory. Usage: node this.mjs <index.ts>.
// Exits non-zero (printing which clause) on the first failed assertion.
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

// Polling reads the log while the fake may be mid-append: drop the text after the
// last newline (a partial record) so JSON.parse never sees half a line.
const log = () =>
  readFileSync(process.env.FAKE_LOG, "utf8").split("\n").slice(0, -1).filter(Boolean).map((l) => JSON.parse(l));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const WAIT_DEADLINE_MS = 15000; // generous: a loaded box can take seconds to spawn node
const POLL_MS = 20;
// Negative checks ("nothing more happens") cannot poll for an event; they wait
// this short window only AFTER every positive condition has been observed.
const SETTLE_MS = 300;

// waitFor polls until pred() is truthy or the deadline passes, then returns the
// last value; callers assert on it, so a timeout surfaces as a clear failure.
async function waitFor(pred) {
  const end = Date.now() + WAIT_DEADLINE_MS;
  for (;;) {
    const v = pred();
    if (v || Date.now() >= end) return v;
    await sleep(POLL_MS);
  }
}

// A failed assertion must end the process: a live `backstory mcp` child would
// otherwise keep node running until the Go test times out.
const die = (e) => {
  console.error("FAIL:", e && e.message ? e.message : e);
  process.exit(1);
};
const crashes = [];
process.on("uncaughtException", (e) => crashes.push(e));
process.on("unhandledRejection", (e) => crashes.push(e));
process.on("exit", () => {});

function stubPi() {
  const handlers = {};
  const tools = [];
  return {
    handlers,
    tools,
    on: (name, fn) => ((handlers[name] ??= []).push(fn)),
    registerTool: (t) => tools.push(t),
    fire: async (name, event, ctx) => {
      const out = [];
      for (const fn of handlers[name] ?? []) out.push(await fn(event, ctx));
      return out;
    },
  };
}
const ctxFor = (id) => ({ sessionManager: { getSessionId: () => id } });

async function main() {
if (process.argv[3] === "missing") {
  // BACKSTORY_BIN points nowhere: every entry point must stay silent.
  const m = await import(pathToFileURL(process.argv[2]).href);
  const p = stubPi();
  m.default(p);
  await p.fire("session_start", { reason: "startup" }, ctxFor("s1"));
  assert.equal(p.tools.length, 0, "no tools without a backstory binary");
  assert.equal((await p.fire("before_agent_start", {}, ctxFor("s1"))).filter(Boolean).length, 0);
  await p.fire("tool_execution_end", { toolCallId: "t", toolName: "bash", result: {} }, ctxFor("s1"));
  // Nothing is spawned, so there is no log line to poll for: settle, then check.
  await sleep(SETTLE_MS);
  assert.equal(crashes.length, 0, "clause 3: missing binary leaked an error");
  console.log("PI EXTENSION MISSING-BINARY OK");
  process.exit(0);
}

const mod = await import(pathToFileURL(process.argv[2]).href);
const pi = stubPi();
mod.default(pi);

// (1) exactly five tools, with the fake's names and schemas.
await pi.fire("session_start", { reason: "startup" }, ctxFor("s1"));
assert.deepEqual(pi.tools.map((t) => t.name).sort(), ["confirm", "note", "recall", "status", "timeline"], "clause 1: tool names");
for (const t of pi.tools) {
  assert.deepEqual(
    t.parameters,
    { type: "object", properties: { [`${t.name}_arg`]: { type: "string" } }, required: [`${t.name}_arg`] },
    `clause 1: schema of ${t.name}`,
  );
}
const called = await pi.tools.find((t) => t.name === "recall").execute("call1", { recall_arg: "x" });
assert.equal(called.content[0].text, "called recall", "tool execute round-trips through backstory mcp");

// (2) warm block once per session; a new session injects again.
const inject = async (id) => (await pi.fire("before_agent_start", { prompt: "hi" }, ctxFor(id))).filter(Boolean);
const first = await inject("s1");
assert.equal(first.length, 1, "clause 2: first turn injects");
assert.match(first[0].message.content, /BACKSTORY WARM BLOCK/);
assert.equal((await inject("s1")).length, 0, "clause 2: second turn in same session does not inject");
assert.equal((await inject("s1")).length, 0, "clause 2: third turn neither");
await pi.fire("session_start", { reason: "new" }, ctxFor("s2"));
assert.equal(pi.tools.length, 5, "re-registering on a new session adds no duplicate tools");
assert.equal((await inject("s2")).length, 1, "clause 2: a new session injects again");

// (3) tool_execution_end forwards to the hook with the event JSON on stdin.
await pi.fire("tool_execution_start", { toolCallId: "t1", toolName: "bash", args: { command: "ls" } }, ctxFor("s2"));
await pi.fire(
  "tool_execution_end",
  { toolCallId: "t1", toolName: "bash", result: { content: [{ type: "text", text: "file.txt" }] }, isError: false },
  ctxFor("s2"),
);
await waitFor(() => log().some((r) => r.argv[0] === "hook" && r.argv[1] === "post-tool-use"));
const hooks = log().filter((r) => r.argv[0] === "hook" && r.argv[1] === "post-tool-use");
assert.equal(hooks.length, 1, "clause 3: one post-tool-use invocation");
assert.deepEqual(hooks[0].argv, ["hook", "post-tool-use", "--harness", "pi"]);
const sent = JSON.parse(hooks[0].stdin);
assert.equal(sent.session_id, "s2");
assert.equal(sent.tool_name, "Bash");
assert.equal(sent.tool_use_id, "t1");
assert.equal(sent.tool_input.command, "ls");
assert.equal(sent.tool_response.stdout, "file.txt");
assert.equal(sent.tool_response.exit_code, undefined, "never invents an exit code");

// a failing hook never throws into Pi (nor does one that cannot even spawn).
async function assertNoLeak(label) {
  const before = crashes.length;
  const evt = { toolCallId: "t2", toolName: "read", result: { content: [] }, isError: false };
  const calls = () => log().filter((r) => r.argv[0] === "hook" && r.argv[1] === "post-tool-use").length;
  const seen = calls();
  await pi.fire("tool_execution_end", evt, ctxFor("s2")); // must not reject
  await waitFor(() => calls() > seen);
  // The hook has run (the fake logs before it exits 1); give its failure a
  // moment to reach the extension's error handling before checking for leaks.
  await sleep(SETTLE_MS);
  assert.equal(crashes.length, before, `clause 3: ${label} leaked an error`);
}
process.env.FAKE_HOOK_FAIL = "1";
await assertNoLeak("failing hook");
process.env.FAKE_HOOK_FAIL = "";

// (4) bash tool_result: the hook is awaited, opted in, and its additionalContext
// is appended to the result the model sees.
const LINE = "Backstory: you committed. note an outcome";
const envelope = JSON.stringify({ hookSpecificOutput: { hookEventName: "PostToolUse", additionalContext: LINE } });
const hookCalls = () => log().filter((r) => r.argv[0] === "hook" && r.argv[1] === "post-tool-use");
const fireResult = async (id, extra = {}) => {
  const event = {
    toolCallId: id,
    toolName: "bash",
    input: { command: "git commit -m x" },
    content: [{ type: "text", text: "[main abc] x" }],
    structuredContent: { exit_code: 0, marker: "keep" },
    isError: false,
    ...extra,
  };
  const out = (await pi.fire("tool_result", event, ctxFor("s2")))[0];
  return { event, out };
};
// endFor fires tool_execution_end and returns how many hook calls it caused.
// `expected` is how many the caller asserts: when positive we poll for them;
// when 0 we can only observe absence, so we wait one settle window.
const endFor = async (id, expected = 0) => {
  const before = hookCalls().length;
  await pi.fire("tool_execution_end", { toolCallId: id, toolName: "bash", result: { content: [] }, isError: false }, ctxFor("s2"));
  if (expected > 0) await waitFor(() => hookCalls().length - before >= expected);
  else await sleep(SETTLE_MS);
  return hookCalls().length - before;
};

process.env.FAKE_HOOK_STDOUT = envelope;
{
  const before = hookCalls().length;
  const { event, out } = await fireResult("r1");
  assert.deepEqual(out.content, [...event.content, { type: "text", text: LINE }], "clause 3: original content plus one text item");
  assert.deepEqual(out.structuredContent, event.structuredContent, "clause 3: structuredContent preserved");
  const calls = hookCalls().slice(before);
  assert.equal(calls.length, 1);
  const p = JSON.parse(calls[0].stdin);
  assert.equal(p.backstory_commit_ask, true, "tool_result opts in to the ask");
  assert.equal(p.tool_response.exit_code, 0, "numeric exit_code reaches the hook");
  assert.equal(p.tool_input.command, "git commit -m x");
  assert.equal(await endFor("r1"), 0, "clause 3: bash call not forwarded again from tool_execution_end");
}
// no numeric exit_code: none is sent.
for (const sc of [undefined, {}, { exit_code: "0" }, { exit_code: null }]) {
  const before = hookCalls().length;
  await fireResult("r2", { structuredContent: sc });
  const p = JSON.parse(hookCalls().slice(before)[0].stdin);
  assert.equal("exit_code" in p.tool_response, false, `no exit_code invented for ${JSON.stringify(sc)}`);
  await endFor("r2");
}
// a non-zero numeric exit code is forwarded as is.
{
  const before = hookCalls().length;
  await fireResult("r3", { structuredContent: { exit_code: 2 } });
  assert.equal(JSON.parse(hookCalls().slice(before)[0].stdin).tool_response.exit_code, 2);
  await endFor("r3");
}
// silent hook, failing hook, timeout: the original result is returned unchanged.
process.env.FAKE_HOOK_STDOUT = "";
assert.equal((await fireResult("r4")).out, undefined, "silent hook leaves the result unchanged");
assert.equal(await endFor("r4"), 0, "silent hook: still not forwarded twice");
process.env.FAKE_HOOK_STDOUT = "not json";
assert.equal((await fireResult("r4b")).out, undefined, "non-envelope output leaves the result unchanged");
await endFor("r4b");
process.env.FAKE_HOOK_STDOUT = envelope;
process.env.FAKE_HOOK_FAIL = "1";
assert.equal((await fireResult("r5")).out, undefined, "failing hook leaves the result unchanged");
assert.equal(await endFor("r5"), 0, "failing hook: not forwarded again");
process.env.FAKE_HOOK_FAIL = "";
process.env.FAKE_HOOK_SLEEP_MS = "4000";
{
  const t0 = Date.now();
  assert.equal((await fireResult("r6")).out, undefined, "timed-out hook leaves the result unchanged");
  assert.ok(Date.now() - t0 < 3500, "the result hook is bounded");
  assert.equal(await endFor("r6"), 0, "timed-out hook: not forwarded again");
}
process.env.FAKE_HOOK_SLEEP_MS = "";
process.env.FAKE_HOOK_STDOUT = "";
// non-bash tools keep the fire-and-forget path and are not touched by tool_result.
{
  const out = (await pi.fire("tool_result", { toolCallId: "n1", toolName: "read", content: [], isError: false }, ctxFor("s2")))[0];
  assert.equal(out, undefined);
  assert.equal(await endFor("n1", 1), 1, "a call tool_result did not handle still takes the fire-and-forget path");
}
await pi.fire("session_shutdown", { reason: "quit" }, ctxFor("s2"));
console.log("PI EXTENSION OK");
}
main().then(() => process.exit(0), die);
