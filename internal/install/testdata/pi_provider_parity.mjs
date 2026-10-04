// Drives the emitted Pi extension through one session whose model provider is
// argv[3] (e.g. "local-qwen" / "anthropic"), against the fake backstory, and
// prints one JSON summary line: the registered tool list, the number of
// warm-block injections, and the post-tool-use hook invocations.
// Usage: node this.mjs <index.ts> <provider> <api>
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const [, , indexTS, provider, api] = process.argv;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const log = () => readFileSync(process.env.FAKE_LOG, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));

const handlers = {};
const tools = [];
const pi = {
  on: (name, fn) => (handlers[name] ??= []).push(fn),
  registerTool: (t) => tools.push(t),
};
const fire = async (name, event, ctx) => {
  const out = [];
  for (const fn of handlers[name] ?? []) out.push(await fn(event, ctx));
  return out;
};
// The provider is visible everywhere Pi exposes it, so an extension that keys
// on it anywhere would diverge between the two runs.
const model = { provider, api, id: `${provider}-model` };
const ctx = { model, sessionManager: { getSessionId: () => "sess-1" } };
const withModel = (e) => ({ ...e, model, provider });

async function main() {
  const mod = await import(pathToFileURL(indexTS).href);
  mod.default(pi);
  await fire("session_start", withModel({ reason: "startup" }), ctx);
  const toolNames = tools.map((t) => t.name).sort();

  let injections = 0;
  for (let turn = 0; turn < 3; turn++) {
    const out = (await fire("before_agent_start", withModel({ prompt: "hi" }), ctx)).filter(Boolean);
    injections += out.length;
  }

  for (const [id, name, args] of [
    ["t1", "bash", { command: "ls" }],
    ["t2", "read", { path: "a.txt" }],
  ]) {
    await fire("tool_execution_start", withModel({ toolCallId: id, toolName: name, args }), ctx);
    await fire("tool_execution_end", withModel({ toolCallId: id, toolName: name, result: { content: [{ type: "text", text: "ok" }] }, isError: false }), ctx);
  }
  await sleep(500);
  const hooks = log()
    .filter((r) => r.argv[0] === "hook" && r.argv[1] === "post-tool-use")
    .map((r) => ({ argv: r.argv, stdin: JSON.parse(r.stdin) }))
    .sort((a, b) => a.stdin.tool_use_id.localeCompare(b.stdin.tool_use_id));
  await fire("session_shutdown", withModel({ reason: "quit" }), ctx);
  console.log("SUMMARY " + JSON.stringify({ tools: toolNames, injections, hooks }));
}
main().then(() => process.exit(0), (e) => {
  console.error("FAIL:", e && e.message ? e.message : e);
  process.exit(1);
});
