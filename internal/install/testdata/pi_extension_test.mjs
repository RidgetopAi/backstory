// Drives the emitted Pi extension (index.ts, type-stripped by node) against a
// stub `pi` object and the fake backstory. Usage: node this.mjs <index.ts>.
// Exits non-zero (printing which clause) on the first failed assertion.
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

const log = () =>
  readFileSync(process.env.FAKE_LOG, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

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
  await sleep(400);
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
await sleep(400);
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
  await pi.fire("tool_execution_end", evt, ctxFor("s2")); // must not reject
  await sleep(400);
  assert.equal(crashes.length, before, `clause 3: ${label} leaked an error`);
}
process.env.FAKE_HOOK_FAIL = "1";
await assertNoLeak("failing hook");
await pi.fire("session_shutdown", { reason: "quit" }, ctxFor("s2"));
console.log("PI EXTENSION OK");
}
main().then(() => process.exit(0), die);
