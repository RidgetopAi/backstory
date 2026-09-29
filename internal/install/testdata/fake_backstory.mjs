#!/usr/bin/env node
// Fake `backstory` for the Pi extension test. FAKE_LOG is a JSONL file every
// invocation appends to; FAKE_HOOK_FAIL=1 makes every `hook` call exit 1.
import { appendFileSync } from "node:fs";

const args = process.argv.slice(2);
const log = (rec) => appendFileSync(process.env.FAKE_LOG, JSON.stringify(rec) + "\n");

const TOOLS = ["recall", "note", "timeline", "confirm", "status"].map((name) => ({
  name,
  description: `fake ${name}`,
  inputSchema: { type: "object", properties: { [`${name}_arg`]: { type: "string" } }, required: [`${name}_arg`] },
}));

if (args[0] === "mcp") {
  log({ argv: args });
  let buf = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (c) => {
    buf += c;
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl);
      buf = buf.slice(nl + 1);
      if (!line.trim()) continue;
      const req = JSON.parse(line);
      if (req.id === undefined) continue;
      let result = {};
      if (req.method === "tools/list") result = { tools: TOOLS };
      else if (req.method === "tools/call") result = { content: [{ type: "text", text: `called ${req.params.name}` }] };
      process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: req.id, result }) + "\n");
    }
  });
} else if (args[0] === "hook") {
  let stdin = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (c) => (stdin += c));
  process.stdin.on("end", () => {
    log({ argv: args, stdin });
    if (process.env.FAKE_HOOK_FAIL === "1") {
      process.stderr.write("boom\n");
      process.exit(1);
    }
    if (args[1] === "session-start") process.stdout.write("BACKSTORY WARM BLOCK\n");
  });
}
