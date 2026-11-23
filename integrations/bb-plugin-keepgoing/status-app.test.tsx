import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { fireEvent, cleanup } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const app = await loadPluginApp(() => import("./app.tsx"));
const loop = { loop: "example", configPath: "/private/example/keepgoing.yaml", controlState: "completed", note: null, target: "primary", supervising: true, lastState: null, lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null,
  details: { goalState: "recorded", goal: { id: "goal_0123456789abcdef0123456789abcdef", revision: 2, state: "recorded" }, readAt: "2026-01-01T00:00:00Z", completed: true, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, runtime: { state: "completed", source: "control", freshness: "stale" }, daemon: { state: "not_recorded" }, lastCheck: { at: "2025-12-31T23:00:00Z", target: "primary", action: "wake", outcome: "failed" } },
};

test("card shows completion and evidence; failed refresh labels retained data and hides actions until recovery", async () => {
  let phase = "ready";
  const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: "thr_status", projectId: "proj_example", environment: null }, { rpc: {
    forThread: () => { if (phase === "lost") throw new Error("PRIVATE RPC ERROR"); return { loops: [loop], errors: [] }; },
  } });
  try {
    const label = await ui.findByText("looptender: completed");
    const title = label.closest("[title]")?.getAttribute("title") ?? ui.container.querySelector("[title]")?.getAttribute("title") ?? "";
    assert.match(title, /Goal: goal_0123456789abcdef0123456789abcdef.*revision 2/);
    assert.match(title, /Task completion: declared; not independently verified/);
    assert.match(title, /Current target: thr_example/); assert.match(title, /Last recorded action: wake · failed/);
    assert.match(title, /running supervisor not confirmed/); assert.match(title, /Last sent: unavailable/);
    // A held loop says where a person resumes it; the plugin offers no Resume.
    assert.ok(ui.getByText(/A person resumes this from an ordinary terminal/)); assert.equal(ui.queryByRole("button", { name: "Resume" }), null);
    assert.ok(ui.getByText('keepgoing resume -c "/private/example/keepgoing.yaml"'));
    phase = "lost"; await ui.behavior.emitRealtime("changed", {});
    await ui.findByRole("alert"); assert.ok(ui.getByText(/previous snapshot/));
    assert.equal(ui.queryByText(/A person resumes this from an ordinary terminal/), null);
    assert.equal(ui.queryByRole("button", { name: "Add to Looptender" }), null);
    assert.ok(!ui.container.textContent?.includes("PRIVATE"));
    phase = "ready"; fireEvent.click(ui.getByRole("button", { name: "Refresh status" }));
    await ui.findByText(/A person resumes this from an ordinary terminal/); assert.equal(ui.queryByRole("alert"), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("returned lookup errors do not offer setup as though the thread had no supervision", async () => {
  const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: "thr_unknown_status", projectId: "proj_example", environment: null }, { rpc: {
    forThread: () => ({ loops: [], errors: ["PRIVATE PATH"] }),
  } });
  try {
    await ui.findByRole("alert");
    assert.equal(ui.queryByRole("button", { name: "Add to Looptender" }), null);
    assert.ok(!ui.container.textContent?.includes("PRIVATE"));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("banner names a different target and expands historical evidence with explicit scheduler limits", async () => {
  const segment = app.threadContextSegments[0]!;
  const props = { threadId: "thr_banner_status", projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} };
  const rpc = { forThread: () => ({ loops: [{ ...loop, controlState: "enabled", supervising: false }], errors: [] }) };
  const summary = renderSlot({ component: segment.summary }, props, { rpc });
  const details = renderSlot({ component: segment.details! }, props, { rpc });
  try {
    await summary.findByRole("button", { name: "looptender: watching thr_example" });
    await details.findByText(/Current target: thr_example.*not this thread/);
    assert.ok(details.getByText(/Task completion: declared; not independently verified/));
    assert.ok(details.getByText(/Goal: goal_0123456789abcdef0123456789abcdef.*revision 2/));
    assert.ok(details.getByText(/Last recorded action: wake · failed/));
    assert.ok(details.getByText(/External scheduler health: not inspected/));
    assert.ok(details.getByText(/Status read:.*snapshot/));
  } finally { summary.lifecycle.unmount(); details.lifecycle.unmount(); cleanup(); }
});

test("an imported loop with a responsive daemon and failed checks is visibly failed, not operational",async()=>{
  const failed={...loop,imported:true,controlState:"enabled",details:{...loop.details,completed:false,failedChecks:3,daemon:{state:"responsive"},lastCheck:{at:"2030-01-01T00:00:00Z",target:"primary",action:"none",outcome:"failed"}}};
  const ui=renderSlot(app.threadContextCardRows[0]!,{threadId:"thr_imported_failure",projectId:"proj_example",environment:null},{rpc:{forThread:()=>({loops:[failed],errors:[]})}});
  try{const label=await ui.findByText("looptender: checks failing");const text=label.closest("[title]")?.getAttribute("title")??"";assert.match(text,/Checks failing: 3/);assert.match(text,/responsive daemon does not establish operational supervision/);}finally{ui.lifecycle.unmount();cleanup();}
});

test("thread setup exposes registry attachment through the typed RPC without creating a setup",async()=>{
  const calls:string[]=[];
  const ui=renderSlot(app.threadContextCardRows[0]!,{threadId:"thr_attach_flow",projectId:"proj_example",environment:null},{rpc:{
    forThread:()=>({loops:[],errors:[]}),setup:()=>({ok:true,message:"No saved setup"}),transfer:()=>({ok:true,message:"No moves",choices:[]}),
    attachment:(raw:unknown)=>{const input=raw as {action:string;threadId:string}; calls.push(input.action);assert.equal(input.threadId,"thr_attach_flow");return {ok:true,candidates:[],message:"Registry read"};},
  }});
  try{fireEvent.click(await ui.findByRole("button",{name:"Add to Looptender"}));fireEvent.click(await ui.findByRole("button",{name:"Find registered loops"}));await ui.findByText("Registry read");assert.deepEqual(calls,["list"]);}finally{ui.lifecycle.unmount();cleanup();}
});

test("responsive imported startup with no recorded first check stays unverified",async()=>{
  const waiting={...loop,imported:true,controlState:"enabled",details:{...loop.details,completed:false,failedChecks:0,daemon:{state:"responsive"},lastCheck:null}};
  const ui=renderSlot(app.threadContextCardRows[0]!,{threadId:"thr_imported_first_check",projectId:"proj_example",environment:null},{rpc:{forThread:()=>({loops:[waiting],errors:[]})}});
  try{await ui.findByText("looptender: no check recorded");assert.equal(ui.queryByText("looptender: enabled"),null);}finally{ui.lifecycle.unmount();cleanup();}
});

test("reference provenance does not imply CLI coverage or provider health", async () => {
  const segment = app.threadContextSegments[0]!;
  for (const referenceSource of ["plugin_setting", "plugin_setup", "registry_attachment"] as const) {
    const props = { threadId: `thr_provenance_${referenceSource}`, projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} };
    const data = { ...loop, referenceSource, ...(referenceSource === "plugin_setup" ? { publication: { state: "repair_needed", source: "cli_registry" } } : {}) };
    const ui = renderSlot({ component: segment.details! }, props, { rpc: { forThread: () => ({ loops: [data], errors: [] }) } });
    try {
      await ui.findByText(referenceSource === "plugin_setting" ? /Reference: plugin setting; CLI menu registration checked separately/ : referenceSource === "plugin_setup" ? /Reference: saved BB setup/ : /Reference: explicitly attached from CLI registry/);
      if (referenceSource === "plugin_setup") assert.ok(ui.getByText(/CLI menu publication: unverified; use explicit registry reconcile/));
      assert.ok(ui.getByText(/running supervisor not confirmed/));
      assert.equal(ui.queryByRole("button", { name: "Resume" }), null);
    } finally { ui.lifecycle.unmount(); cleanup(); }
  }
});

test("the status label keeps the state word first and adds the running build, and says when the installed build differs", async () => {
  const withBuild = (runtime: object) => ({ ...loop, controlState: "enabled", supervising: true, details: { ...loop.details, completed: false, failedChecks: 0, runtime: { state: "recent", source: "state", freshness: "recent", ...runtime } } });
  const render = (l: object, id: string) => renderSlot(app.threadContextCardRows[0]!, { threadId: id, projectId: "proj_example", environment: null }, { rpc: { forThread: () => ({ loops: [l], errors: [] }) } });
  const a = render(withBuild({ recordedBuild: "v0.2.0-dev.ccd2310", installedBuild: "v0.2.0-dev.ccd2310", binaryComparison: "match" }), "thr_build_match");
  try { assert.ok(await a.findByText("looptender: enabled v0.2.0-dev.ccd2310")); } finally { a.lifecycle.unmount(); cleanup(); }
  const b = render(withBuild({ recordedBuild: "v0.1.0", installedBuild: "v0.2.0", binaryComparison: "different" }), "thr_build_differs");
  try {
    const label = await b.findByText("looptender: enabled v0.1.0 (installed v0.2.0)");
    assert.match(label.closest("[title]")?.getAttribute("title") ?? "", /Build: last check ran v0\.1\.0 · installed v0\.2\.0/);
  } finally { b.lifecycle.unmount(); cleanup(); }
  // An older looptender, or a build with no version, keeps the plain label.
  const c = render(withBuild({}), "thr_build_unknown");
  try { assert.ok(await c.findByText("looptender: enabled")); } finally { c.lifecycle.unmount(); cleanup(); }
});

test("a held loop that was moved reads differently from an ordinary awaiting-user hold", async () => {
  const held = (moved?: object) => ({ ...loop, controlState: "awaiting user", supervising: true, details: { ...loop.details, completed: false, failedChecks: 0, targetProvider: "claude-code", targetModel: "example-model", ...(moved ? { moved } : {}) } });
  const render = (l: object, id: string) => renderSlot(app.threadContextCardRows[0]!, { threadId: id, projectId: "proj_example", environment: null }, { rpc: { forThread: () => ({ loops: [l], errors: [] }) } });
  const a = render(held({ held: true, text: "Moved to its new thread. Resuming it is the one action needed." }), "thr_moved_hold");
  try {
    const label = await a.findByText("looptender: moved, resume to start");
    const title = label.closest("[title]")?.getAttribute("title") ?? "";
    assert.match(title, /Moved: Moved to its new thread/); assert.match(title, /Provider: claude-code · model example-model/);
  } finally { a.lifecycle.unmount(); cleanup(); }
  const b = render(held(), "thr_plain_hold");
  try { assert.ok(await b.findByText("looptender: awaiting user")); assert.equal(b.queryByText(/moved, resume/), null); } finally { b.lifecycle.unmount(); cleanup(); }
});


test("Preview setup reaches the plugin through BB's RPC layer, which refuses undefined values in an input",async()=>{
  const seen:{action:string;ticket?:unknown}[]=[];
  const plan={loop:"example-loop",project:"/projects/example",configPath:"/private/example/looptender.yaml",stateDir:"/private/example/state",threadId:"thr_preview_rpc",provider:"codex",limits:{interval:"5m",wakeEvery:"1h",stallAfter:"30m",backoff:"10m",maxNudges:3,maxReprompts:3}};
  const ui=renderSlot(app.threadContextCardRows[0]!,{threadId:"thr_preview_rpc",projectId:"proj_example",environment:null},{rpc:{
    forThread:()=>({loops:[],errors:[]}),transfer:()=>({ok:true,message:"No moves",choices:[]}),attachment:()=>({ok:true,candidates:[],message:"Registry read"}),
    setup:(raw:unknown)=>{const i=raw as {action:string;ticket?:unknown}; seen.push(i); if(i.action==="inspect")return{ok:true,message:"Nothing set up yet"}; if(i.action==="preview")return{ok:true,message:"Preview only",ticket:"t",plan}; return{ok:false,message:"unexpected"};},
  }});
  try{
    fireEvent.click(await ui.findByRole("button",{name:"Add to Looptender"}));
    await ui.findByText("Nothing set up yet");
    fireEvent.click(ui.getByRole("button",{name:"Preview setup"}));
    await ui.findByRole("button",{name:"Create disabled setup"});
    assert.equal(ui.container.querySelector("[role=alert]"),null,"the request was not refused before it left the browser");
    assert.deepEqual(seen.map(s=>s.action),["inspect","preview"]);
    assert.ok(!("ticket" in seen[1]!),"no ticket key on the first preview");
  }finally{ui.lifecycle.unmount();cleanup();}
});
