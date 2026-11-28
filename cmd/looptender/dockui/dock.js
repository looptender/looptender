"use strict";
// looptender panel page. Reads bounded versioned JSON relative to this
// page and renders it with DOM text nodes only; no data is parsed as HTML.
// Ages come from the report's read time, never this browser's clock, so the
// page reads the same wherever it is opened. Every sentence about a loop or a
// route is sent by the report, which the Go tests check word for word; the
// page only shows its times in this browser's time zone.
(function () {
  // The header buttons and the reading lines are shown only once this runs;
  // without a script the page says so instead (index.html, noscript).
  document.documentElement.classList.add("has-script");
  var SCHEMA = 1;
  // How long reads may fail before the page asks a person to look (G4).
  var FAIL_LONG_MS = 120000;
  var API = "api/v1/dock";
  var SERVER = "api/v1/server";
  var SAVED_ACCOUNTS = "api/v1/saved-accounts";
  var savedAccounts = { reference: "", status: "", data: null, serial: 0, abort: null };
  // The end of the route brief that says a missing standby does not touch the
  // route in use; the line is then a plain notice, not a warning. Kept in step
  // with dockCurrentUnaffected in dock_view.go (a test checks).
  var CURRENT_UNAFFECTED = "its current route is unaffected";
  // What looptender did: worded by the same read as API, served on its own
  // for its size, and read only for the Usage page and a loop's page.
  var USAGE = "api/v1/usage";
  var state = { data: null, identifiers: false, paused: false, timer: 0, busy: false, again: false, missed: false, lastOk: null, failed: false, failedSince: 0, failedLong: false, notice: "", pending: false, said: "", server: null, treeOpen: Object.create(null), accountOpen: Object.create(null), ruleOpen: Object.create(null), advRoutesOpen: Object.create(null), usage: null, usageText: "", usageFailed: false, usageBusy: false, accountRemove: null, mapMode: "map", mapQuery: "", mapFilter: "all", mapShowRetired: false, mapSelected: null, activeDrawer: null, issueStaleNotice: "", changeSerial: 0, supersededChangeResult: null, retainedNeeds: [], retainedLoops: Object.create(null), acctFilter: "all", acctDensity: "detailed" };

  var TONES = {
    supervising: "good", waiting_for_you: "attn", waiting_on_limit: "limit", waiting_on_network: "limit", checks_failing: "warn",
    not_checked_recently: "warn", outside_hours: "calm", recovering: "calm", paused: "calm", stopped: "calm", finished: "calm", off: "calm", unknown: "unknown"
  };
  // Icons: one 20px grid, one stroke weight.
  var ICONS = {
    good: '<circle cx="10" cy="10" r="3.2" fill="currentColor" stroke="none"/><circle cx="10" cy="10" r="6.5" opacity=".35"/>',
    attn: '<circle cx="10" cy="10" r="7"/><path d="M10 6.2v4.6"/><path d="M10 13.6v.1"/>',
    limit: '<circle cx="10" cy="10" r="7"/><path d="M10 6v4l2.6 1.6"/>',
    warn: '<path d="M10 3.2 17.3 16H2.7z"/><path d="M10 8.2v3.6"/><path d="M10 13.9v.1"/>',
    paused: '<path d="M7.5 5.5v9"/><path d="M12.5 5.5v9"/>',
    stopped: '<rect x="5.5" y="5.5" width="9" height="9" rx="1.5"/>',
    finished: '<path d="m4.5 10.5 3.6 3.5 7.4-8"/>',
    off: '<path d="M10 3.5v6"/><path d="M6.1 5.8a6 6 0 1 0 7.8 0"/>',
    unknown: '<circle cx="10" cy="10" r="7" stroke-dasharray="2.4 2.4"/>',
    check: '<path d="m4.5 10.5 3.6 3.5 7.4-8"/>',
    chevron: '<path d="m7.5 5 5 5-5 5"/>'
  };
  var STATE_ICON = {
    supervising: "good", waiting_for_you: "attn", waiting_on_limit: "limit", waiting_on_network: "limit", checks_failing: "warn",
    not_checked_recently: "warn", outside_hours: "limit", recovering: "limit", paused: "paused", stopped: "stopped", finished: "finished", off: "off", unknown: "unknown"
  };

  function $(id) { return document.getElementById(id); }
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }
  function icon(name) {
    var s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    s.setAttribute("viewBox", "0 0 20 20");
    s.setAttribute("aria-hidden", "true");
    s.innerHTML = ICONS[name] || ICONS.unknown; // fixed markup above, never data
    return s;
  }
  function tag(kind, text) { return el("span", "tag tag-" + kind, text); }
  function link(href, text, cls) {
    var a = el("a", cls || null, text);
    a.href = href;
    return a;
  }
  function list(v) { return Array.isArray(v) ? v : []; }

  // Time. Every sentence comes from the report as parts: literal text, or a
  // time shown here in this browser's own time zone. Ages and countdowns are
  // generally text, computed against the report's read time, so the page reads
  // the same wherever it is opened. A time that does not parse is said to be
  // unknown; it never throws. Accounts clocks separately advance below.
  function readTime() { var t = state.data ? Date.parse(state.data.readAt) : NaN; return isNaN(t) ? Date.now() : t; }
  function span(seconds) {
    var s = Math.abs(seconds), m = Math.round(s / 60);
    if (s < 45) return "under a minute";
    if (m < 60) return m + " min";
    var h = Math.floor(m / 60), mm = m % 60;
    if (h < 48) return h + " h" + (mm && h < 6 ? " " + mm + " min" : "");
    var d = Math.floor(h / 24), hh = h % 24;
    return d + " days" + (hh ? " " + hh + " h" : "");
  }
  var timeFmt = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
  var dayTimeFmt = new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
  var dayTimeYearFmt = new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric", year: "numeric", hour: "numeric", minute: "2-digit" });
  var dateFmt = new Intl.DateTimeFormat(undefined, { weekday: "short", day: "numeric", month: "short" });
  var dateYearFmt = new Intl.DateTimeFormat(undefined, { weekday: "short", day: "numeric", month: "short", year: "numeric" });
  var secFmt = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit", second: "2-digit" });
  var YEAR = 365 * 24 * 3600 * 1000, CLOCK = 18 * 3600 * 1000;
  // date: "Thu 12 Nov"; datetime: "Wed 18 Nov, 19:12"; clock: a time of day
  // within 18 hours of the read, otherwise date and time. The year is added
  // when the time is more than a year from the read.
  function when(p) {
    var t = Date.parse(p.at);
    if (isNaN(t)) return "an unknown time";
    var d = new Date(t), far = Math.abs(t - readTime()) > YEAR;
    if (p.f === "date") return (far ? dateYearFmt : dateFmt).format(d);
    if (p.f === "clock" && !far && Math.abs(t - readTime()) < CLOCK) return timeFmt.format(d);
    return (far ? dayTimeYearFmt : dayTimeFmt).format(d);
  }
  function fmt(f, iso) { var t = Date.parse(iso); return isNaN(t) ? "an unknown time" : f.format(new Date(t)); }
  function say(parts) {
    if (typeof parts === "string") return parts;
    return list(parts).map(function (p) { return p && p.at ? when(p) : (p && p.t) || ""; }).join("");
  }

  function fact(label, value) {
    var row = el("div", "fact");
    row.appendChild(el("dt", null, label));
    var dd = el("dd");
    if (typeof value === "string") dd.textContent = value; else dd.appendChild(value);
    row.appendChild(dd);
    return row;
  }
  function frag() { return document.createDocumentFragment(); }
  function unknown(text) { return el("span", "unknown", text || "Unknown"); }
  function joinParts(parts) {
    var f = frag();
    parts.filter(Boolean).forEach(function (p, i) {
      if (i) f.appendChild(el("span", "sep", "·"));
      f.appendChild(typeof p === "string" ? document.createTextNode(p) : p);
    });
    return f;
  }

  // Routing: "#/" is the overview, "#/loop/<key>" one loop, "#/routes" every
  // route and "#/routes/<key>" every route, at that one; "#/usage" is what
  // looptender did.
  function route() {
    var h = location.hash.replace(/^#\/?/, "");
    var m = /^routes(?:\/(r[0-9a-f]+))?$/.exec(h);
    if (m) return { view: "routes", key: m[1] || "" };
    m = /^accounts(?:\/([a-f0-9]{32}))?$/.exec(h);
    if (m) return { view: "accounts", key: m[1] || "" };
    if (h === "versions") return { view: "versions" };
    if (h === "usage") return { view: "usage" };
    m = /^loop\/([a-z0-9]+)$/.exec(h);
    if (m) return { view: "loop", key: m[1] };
    return { view: "home" };
  }
  function scrubNeedForHiddenIds(n) {
    if (!n) return n;
    var out = Object.assign({}, n);
    if (out.maskedNote) {
      out.note = out.maskedNote;
      delete out.maskedNote;
    } else if (out.noteMasked && state.data && state.data.identifiers) {
      out.note = "[hidden]";
    }
    return out;
  }
  function scrubLoopForHiddenIds(l) {
    if (!l) return l;
    var out = Object.assign({}, l);
    delete out.identifiers;
    if (out.currentRoute) {
      out.currentRoute = Object.assign({}, out.currentRoute);
      delete out.currentRoute.name;
    }
    if (out.device) {
      out.device = Object.assign({}, out.device);
      delete out.device.hostname;
      delete out.device.hostText;
    }
    if (out.archived) {
      out.archived = Object.assign({}, out.archived);
      delete out.archived.note;
    }
    if (out.archiveLapsed) {
      out.archiveLapsed = Object.assign({}, out.archiveLapsed);
      delete out.archiveLapsed.note;
    }
    if (out.chain && Array.isArray(out.chain.entries)) {
      out.chain = Object.assign({}, out.chain, {
        entries: out.chain.entries.map(function (ent) {
          var eCopy = Object.assign({}, ent);
          delete eCopy.id;
          return eCopy;
        })
      });
    }
    if (out.lanes && Array.isArray(out.lanes.lanes)) {
      out.lanes = Object.assign({}, out.lanes, {
        lanes: out.lanes.lanes.map(function (ln) {
          var lnCopy = Object.assign({}, ln);
          if (lnCopy.title || lnCopy.titleEvidence === "recorded" || lnCopy.titleEvidence === "withheld") {
            lnCopy.titleEvidence = "withheld";
          }
          delete lnCopy.title;
          delete lnCopy.key;
          return lnCopy;
        })
      });
    }
    return out;
  }
  function scrubRetainedForHiddenIds() {
    if (state.retainedNeeds && state.retainedNeeds.length) {
      state.retainedNeeds = state.retainedNeeds.map(scrubNeedForHiddenIds);
    }
    if (state.retainedLoops) {
      var nextLoops = Object.create(null);
      Object.keys(state.retainedLoops).forEach(function (k) {
        nextLoops[k] = scrubLoopForHiddenIds(state.retainedLoops[k]);
      });
      state.retainedLoops = nextLoops;
    }
  }
  function supersededOutcomeBanner(d) {
    var bg = state.supersededChangeResult;
    if (!bg || !bg.result) return null;
    var bgLoop = effectiveLoopByKey(d, bg.key);
    var bnr = el("div", "issue-stale-banner");
    bnr.setAttribute("role", "status");
    bnr.dataset.bgOutcome = "1";
    var label = "Earlier action (" + bg.action + (bgLoop ? " on " + bgLoop.name : "") + ") finished (" + bg.result.outcome + "): " + bg.result.text;
    bnr.appendChild(el("span", null, label));
    bnr.appendChild(button("Dismiss", "dismiss-bg-outcome", "", function () { state.supersededChangeResult = null; render(false, true); }));
    return bnr;
  }
  function effectiveNeeds(d) {
    var cur = list(d && d.needsYou);
    if (!state.retainedNeeds || !state.retainedNeeds.length) return cur;
    var seen = Object.create(null);
    cur.forEach(function (n) { seen[n.key] = true; });
    var out = cur.slice();
    state.retainedNeeds.forEach(function (n) { if (!seen[n.key]) out.push(n); });
    return out;
  }
  function effectiveLoopByKey(d, key) {
    return list(d && d.loops).filter(function (l) { return l.key === key; })[0] || (state.retainedLoops && state.retainedLoops[key]) || null;
  }
  function loopByKey(key) { return list(state.data.loops).filter(function (l) { return l.key === key; })[0]; }
  function needFor(key) { return effectiveNeeds(state.data).filter(function (n) { return n.key === key; })[0]; }

  // ---- Small pieces shared by the views ----

  function lastCheck(c) {
    if (c.availability === "never" || c.availability === "unknown" || !c.text) return unknown(c.text);
    var f = frag();
    // The age is said in plain words, with no badge: a loop that is on and
    // not checked for longer than it should be has its own state, "Not
    // checked recently"; for a paused or held loop an old check is expected.
    f.appendChild(document.createTextNode(c.text));
    if (c.failed) f.appendChild(el("div", "sub", c.failed));
    return f;
  }
  function nextCheck(n) {
    if (n.availability === "known") return document.createTextNode(say(n.text));
    if (n.availability === "in_progress") return document.createTextNode(n.phrase);
    return joinParts([unknown("Unknown"), n.detail ? el("span", "sub", n.detail) : null]);
  }
  function limits(items, wait) {
    var f = frag();
    if (wait) {
      var w = el("div", null, say(wait.text));
      if (!wait.passed) w.appendChild(tag("reached", "In effect"));
      f.appendChild(w);
    }
    list(items).forEach(function (l) {
      var row = el("div");
      if (l.label) row.appendChild(el("span", "sub", l.label + ": "));
      row.appendChild(l.unknown ? unknown(say(l.text)) : document.createTextNode(say(l.text)));
      if (l.reached) row.appendChild(tag("reached", "Reached"));
      f.appendChild(row);
      if (l.reached && l.effect) f.appendChild(el("div", "sub", "Reached: " + l.effect));
    });
    if (!wait && !list(items).length) f.appendChild(unknown());
    return f;
  }

  // Usage windows as meters. A stale window is drawn empty and hatched: it
  // says nothing about how much is used now.
  function meters(t) {
    var f = frag();
    list(t.windows).forEach(function (w) {
      var row = el("div", "meter-row");
      row.appendChild(el("span", "w", w.name));
      var m = el("div", "meter" + (w.stale ? " is-stale" : w.usedPercent >= 100 ? " full" : w.usedPercent >= 70 ? " hi" : ""));
      m.setAttribute("role", "img");
      m.setAttribute("aria-label", w.name + " window " + (w.stale ? "has ended since this reading" : w.usedPercent + " percent used"));
      var bar = el("i");
      bar.style.width = (w.stale ? 0 : Math.max(2, w.usedPercent)) + "%";
      m.appendChild(bar);
      row.appendChild(m);
      row.appendChild(el("span", "pct", w.stale ? "—" : w.usedPercent + "%"));
      if (list(w.reset).length) row.appendChild(el("span", "meter-reset", say(w.reset)));
      if (list(w.projection).length) row.appendChild(el("span", "meter-projection", say(w.projection)));
      f.appendChild(row);
    });
    if (t.omitted) f.appendChild(el("p", "more", t.omitted + (t.omitted === 1 ? " more window is" : " more windows are") + " not shown."));
    return f;
  }
  function usageBlock(u) {
    u = u || { availability: "unavailable" };
    var box = el("div", "route-usage");
    if (u.availability === "unavailable") {
      box.appendChild(el("div", "sub usage-none", (u.readLine || "No recent reading") + (u.reason ? " · " + u.reason : "")));
      return box;
    }
    if (u.availability === "stale") {
      box.appendChild(el("div", "sub usage-none", "Window ended" + (u.readLine ? " · " + u.readLine : "")));
      return box;
    }
    var uh = el("div", "sub usage-read", u.readLine || "Usage reading");
    box.appendChild(uh);
    if (u.needsPerson && u.ageNote) box.appendChild(el("p", "route-note is-attn usage-age", u.ageNote));
    box.appendChild(meters(u));
    if (u.readingsNote) box.appendChild(el("p", "sub usage-note", u.readingsNote));
    return box;
  }
  function notesOf(notes) { return list(notes).map(function (n) { return { tone: n.tone, text: say(n.text) }; }); }

  // The route component: one route (a model on an account at an effort
  // level). It leads with what is recorded against it, then readiness tied to
  // its test, then what the route is. A loop's routes and the view across
  // loops both render it. Its data-route is the opaque route key.
  var READY_TONE = { ready: "good", ready_overdue: "calm", not_ready: "warn", not_probed: "unknown", disabled: "calm", unknown: "unknown" };
  function routeItem(o) {
    var li = el("li", "route" + (o.active ? " is-active" : "") + (o.off ? " is-off" : ""));
    if (o.anchor) { li.id = o.anchor; li.tabIndex = -1; }
    if (o.route) li.dataset.route = o.route;
    if (o.position != null) li.dataset.position = String(o.position);
    if (o.badge) {
      var pos = el("span", "pos", o.badge);
      pos.setAttribute("aria-hidden", "true");
      li.appendChild(pos);
    }
    var body = el("div", "route-body");
    var top = el("div", "route-top");
    var name = el("h3", "route-name", o.title);
    if (o.titleAccount) name.appendChild(el("span", "route-account", " on " + o.titleAccount));
    top.appendChild(name);
    if (o.use) top.appendChild(el("span", o.active ? "in-use" : "in-use is-recorded", o.use));
    body.appendChild(top);
    list(o.notes).forEach(function (n) { body.appendChild(el("p", "route-note is-" + (n.tone || "plain"), n.text)); });
    if (o.readiness) {
      var st = el("div", "route-status tone-" + (READY_TONE[o.readiness] || "unknown"));
      st.appendChild(el("span", "dot"));
      st.appendChild(el("span", null, o.phrase));
      body.appendChild(st);
    }
    list(o.after).forEach(function (t) { body.appendChild(el("p", "route-note is-plain", t)); });
    var metaItems = list(o.meta).concat(state.identifiers && o.named ? [o.named] : []);
    if (o.compact) {
      if (o.usage && o.usage.availability !== "unavailable") body.appendChild(usageBlock(o.usage));
      list(o.boxes).forEach(function (b) { body.appendChild(b); });
      var extraNotes = list(o.extraNotes);
      if (metaItems.length || extraNotes.length || (o.usage && o.usage.availability === "unavailable")) {
        var more = el("details", "route-more");
        more.appendChild(el("summary", null, "Details"));
        var mb = el("div", "route-more-body");
        if (metaItems.length) {
          var m = el("div", "sub route-meta");
          m.appendChild(joinParts(metaItems));
          mb.appendChild(m);
        }
        extraNotes.forEach(function (n) { mb.appendChild(el("p", "route-note is-" + (n.tone || "plain"), n.text)); });
        if (o.usage && o.usage.availability === "unavailable") mb.appendChild(usageBlock(o.usage));
        more.appendChild(mb);
        body.appendChild(more);
      }
    } else {
      var meta = el("div", "sub route-meta");
      meta.appendChild(joinParts(metaItems));
      body.appendChild(meta);
      body.appendChild(usageBlock(o.usage));
      list(o.boxes).forEach(function (b) { body.appendChild(b); });
    }
    li.appendChild(body);
    return li;
  }
  function box(label, content) {
    var b = el("div", "lanes");
    b.appendChild(el("span", "lanes-label", label));
    b.appendChild(content);
    return b;
  }

  // A hold's note is always shown. Without identifiers, parts shaped like an
  // identifier read "[hidden]" and the card offers to show them.
  function showNotesButton() {
    var b = el("button", "linkish", "Show identifiers");
    b.setAttribute("data-focus", "show-notes");
    b.type = "button";
    b.addEventListener("click", function () { if (!state.identifiers) $("identifiers").click(); });
    return b;
  }
  // What a loop waits for and who set it, in words, from the report's kind and
  // by. by is a role the journal records, not proof of who: it is never said as
  // "you".
  var WAIT_WORDS = {
    asked: { owner_thread: "Its own thread asked for a person", check: "A check found it waiting on a question", other_thread: "Another BB thread asked for a person", terminal: "A question was left from a terminal", client: "A question was left from a local client", unknown: "Waiting on a question" },
    held: { terminal: "Held from a terminal", client: "Held from a local client, such as the menu", other_thread: "Held by another BB thread", owner_thread: "Held by its own thread", unknown: "Held; who set it is not recorded" },
    limit: { check: "Waiting on a limit" },
    checks: { check: "Its checks have stopped" },
    unreadable: { unknown: "Its records cannot be read" },
    unknown: { unknown: "What it waits for is not recorded" }
  };
  // Own keys only: a kind or by that is the name of something every object has
  // ("constructor", "toString", "__proto__") must not find it.
  function own(o, key) {
    return Object.prototype.hasOwnProperty.call(o, key) ? o[key] : undefined;
  }
  function waitWords(kind, by) {
    var k = own(WAIT_WORDS, kind);
    if (!k) return "";
    return own(k, by) || own(k, "unknown") || own(k, "check") || "";
  }
  // The page's own span words, as the server says them (dockSpan).
  function spanWords(ms) {
    var s = Math.floor(Math.abs(ms) / 1000), m = Math.floor((Math.abs(ms) + 30000) / 60000);
    if (s < 45) return "under a minute";
    if (m < 60) return m + " min";
    var h = Math.floor(m / 60), mm = m % 60;
    if (h < 48) return mm > 0 && h < 6 ? h + " h " + mm + " min" : h + " h";
    var days = Math.floor(h / 24), hh = h % 24;
    return hh > 0 ? days + " days " + hh + " h" : days + " days";
  }
  // "for 3 days 18 h", or "for at least ..." when only the last control change
  // is known. Empty when the report gives no start.
  function waitingFor(w, readAt) {
    if (!w || !w.since || !readAt) return "";
    var ms = Date.parse(readAt) - Date.parse(w.since);
    if (!isFinite(ms) || ms < 0) return "";
    var exact = w.sinceEvidence === "hold_recorded" || w.sinceEvidence === "limit_detected";
    return "for " + (exact ? "" : "at least ") + spanWords(ms);
  }
  function waitsLine(w, readAt, withTime) {
    var words = w ? waitWords(w.kind, w.by) : "";
    if (!words) return null;
    var when = withTime ? waitingFor(w, readAt) : "";
    return el("span", "waits", when ? words + " \u00B7 " + when : words);
  }
  // Questions an agent asked first, then deliberate holds, then limits and
  // checks that stopped; a server that sends no kind keeps the flat list.
  var NEED_GROUPS = [
    { label: "Questions an agent asked", kinds: ["asked"] },
    { label: "Deliberate holds", kinds: ["held"] },
    { label: "Limits and checks that stopped", kinds: ["limit", "checks", "unreadable", "unknown"] }
  ];
  function needGroups(needs) {
    if (!needs.some(function (n) { return !!n.waiting; })) return [{ label: "", needs: needs }];
    var out = NEED_GROUPS.map(function (g) { return { label: g.label, needs: [] }; });
    needs.forEach(function (n) {
      var at = NEED_GROUPS.length - 1;
      NEED_GROUPS.forEach(function (g, i) { if (g.kinds.indexOf(n.waiting) >= 0) at = i; });
      out[at].needs.push(n);
    });
    return out.filter(function (g) { return g.needs.length; });
  }
  // A loop whose recorded supervisor did not answer says how old its last check
  // is, since that check is all that is known.
  function supervisorAge(l) {
    if (l.supervisor !== "not_answering" || !l.lastCheck || l.lastCheck.ageSeconds == null) return "";
    return "\u00B7 last check " + spanWords(l.lastCheck.ageSeconds * 1000) + " ago";
  }
  function uiBadge(toneClass, textValue) { return el("span", "ui-badge" + (toneClass ? " ui-badge-" + toneClass : ""), textValue); }
  function buildRevisionNote(verStr) {
    if (!verStr || verStr === "dev" || verStr === "unavailable") return "source revision unknown (dev)";
    if (/-dev\.[0-9a-f]+/i.test(verStr)) return "embedded revision";
    return "release label · commit revision not embedded";
  }
  function countVersionDiffs(d) {
    var vs = (d && d.versions) || {}, rows = list(vs.rows), bf = (d && d.buildFreshness) || {};
    var behindCount = typeof vs.behind === "number" ? vs.behind : 0;
    var differCount = 0;
    rows.forEach(function (rowItem) {
      if (rowItem.availability === "different" || rowItem.availability === "different_file") differCount++;
    });
    if (!behindCount && !differCount && typeof bf.notOnIt === "number") differCount = bf.notOnIt;
    return behindCount + differCount;
  }
  function renderBuildPill(d) {
    var pillEl = $("build-pill");
    if (!pillEl) return;
    var verStr = state.server && typeof state.server.version === "string" ? state.server.version : "";
    if (!verStr && !d) { pillEl.hidden = true; return; }
    var shownVer = (!verStr || verStr === "dev") ? "Build unknown (dev)" : verStr;
    var diffs = countVersionDiffs(d);
    pillEl.hidden = false;
    pillEl.classList.toggle("is-diff", diffs > 0);
    pillEl.textContent = diffs > 0 ? shownVer + " \u00B7 " + diffs + " differ" : shownVer;
    pillEl.title = shownVer + " (" + buildRevisionNote(verStr) + "). Inspect Versions.";
  }

  function isLoopSuperseded(loopObj) {
    return !!(loopObj && ((loopObj.hold && loopObj.hold.state === "superseded") || loopObj.phrase === "Superseded" || (loopObj.archived && loopObj.archived.continuedIn)));
  }
  function isLoopRetired(loopObj) {
    return !!(loopObj && (loopObj.inactive || loopObj.state === "archived" || isLoopSuperseded(loopObj)));
  }
  function countAccountDrift() {
    var src = (state.data && state.data.accounts && list(state.data.accounts.cards)) || (savedAccounts.status === "ready" && savedAccounts.data && list(savedAccounts.data.cards)) || [];
    return src.filter(function (cItem) { return cItem.attention === "context_changed" || cItem.reason === "context_changed"; });
  }
  function categorizeNeeds(d) {
    var items = effectiveNeeds(d), dec = [], chk = [], unr = [];
    items.forEach(function (nItem) {
      var kindStr = nItem.waiting || "";
      if (kindStr === "checks" || (!kindStr && (nItem.kind === "overdue" || nItem.kind === "build_replaced"))) chk.push(nItem);
      else if (kindStr === "unreadable" || kindStr === "unknown" || (!kindStr && nItem.kind === "unreadable")) unr.push(nItem);
      else dec.push(nItem);
    });
    var loopsArr = list(d && d.loops);
    var activeLoops = [], retiredLoops = [], atLimitLoops = [];
    loopsArr.forEach(function (loopObj) {
      if (isLoopRetired(loopObj)) retiredLoops.push(loopObj);
      else activeLoops.push(loopObj);
      if (!isLoopRetired(loopObj) && loopObj.state === "waiting_on_limit") atLimitLoops.push(loopObj);
    });
    return {
      decisions: dec,
      checks: chk,
      unreadable: unr,
      all: items,
      activeLoops: activeLoops,
      retiredLoops: retiredLoops,
      atLimit: atLimitLoops,
      driftCards: countAccountDrift()
    };
  }
  function waitingLanesOf(loopObj) {
    return list(loopObj && loopObj.lanes && loopObj.lanes.lanes).filter(function (laneRow) { return laneRow.state === "waiting"; });
  }
  function currentChainRouteKey(loopObj) {
    var entries = list(loopObj && loopObj.chain && loopObj.chain.entries);
    var cur = entries.filter(function (e) { return !!e.current; })[0] || entries[0] || null;
    return cur && cur.route ? cur.route : "";
  }
  function issueFingerprint(nItem, loopObj, includeIdentifiers) {
    if (!nItem) return "";
    var includeIds = includeIdentifiers !== false;
    var cr = loopObj && loopObj.currentRoute;
    var crParts = cr ? [cr.availability || "", cr.role || "", includeIds ? (cr.name || "") : "", cr.tool || "", cr.provider || "", cr.model || ""].join(":") : "";
    var ids = loopObj && loopObj.identifiers;
    var idParts = includeIds && ids ? [ids.registrationId || "", ids.thread || ""].join(":") : "";
    var laneIds = waitingLanesOf(loopObj).map(function (ln) {
      var laneTitleEv = includeIds ? (ln.titleEvidence || "") : ((ln.titleEvidence === "recorded" || ln.titleEvidence === "withheld") ? "has_title" : (ln.titleEvidence || ""));
      return [ln.number || "", ln.loopKey || "", ln.state || "", ln.phrase || "", laneTitleEv, includeIds ? (ln.key || "") : "", includeIds ? (ln.title || "") : ""].join(":");
    }).sort().join(",");
    var limIds = list(loopObj && loopObj.limits).filter(function (lm) { return !!lm.reached; }).map(function (lm) {
      return [lm.label || "", lm.phrase || "", lm.effect || ""].join(":");
    }).sort().join(",");
    var canonMaskedNote = nItem.maskedNote || nItem.note || "";
    return [
      nItem.key || "",
      nItem.kind || "",
      nItem.waiting || "",
      nItem.by || "",
      nItem.since || "",
      nItem.sinceEvidence || "",
      nItem.reason || "",
      canonMaskedNote,
      includeIds ? (nItem.note || "") : "",
      loopObj ? (loopObj.state || "") : "",
      loopObj ? (loopObj.evidence || "") : "",
      loopObj && loopObj.hold ? [loopObj.hold.state || "", loopObj.hold.since || "", loopObj.hold.sinceEvidence || ""].join(":") : "",
      loopObj && loopObj.waiting ? [loopObj.waiting.kind || "", loopObj.waiting.by || "", loopObj.waiting.since || ""].join(":") : "",
      crParts,
      currentChainRouteKey(loopObj),
      loopObj ? (loopObj.provider || "") : "",
      loopObj ? (loopObj.model || "") : "",
      loopObj && loopObj.moved ? String(!!loopObj.moved.held) : "",
      loopObj && loopObj.archived ? (loopObj.archived.at || "") : "",
      loopObj && loopObj.lineage ? [loopObj.lineage.count || 0, loopObj.lineage.from || ""].join(":") : "",
      loopObj && loopObj.lanes ? (loopObj.lanes.availability || "") : "",
      idParts,
      laneIds,
      limIds
    ].join("|");
  }
  function taskUnconfirmedText(d, nItem, loopObj) {
    if (state.failed) return "Unconfirmed \u2014 last read failed; showing retained snapshot";
    if (nItem && nItem._retainedIncomplete) return "Unconfirmed \u2014 omitted from incomplete read; not confirmed resolved";
    if (d && d.complete === false) return "Unconfirmed \u2014 read was incomplete";
    if (loopObj && loopObj.evidence === "partial") return "Unconfirmed \u2014 loop evidence is partial";
    if (loopObj && loopObj.evidence === "unavailable") return "Unconfirmed \u2014 loop evidence is unavailable";
    return "";
  }
  function reconcileActiveIssue(d) {
    if (!d || state.failed) return;
    var curIdsMode = !!d.identifiers;
    if (state.activeDrawer) {
      var drKey = state.activeDrawer.key;
      var curNeed = effectiveNeeds(d).filter(function (x) { return x.key === drKey; })[0] || null;
      var curLoop = effectiveLoopByKey(d, drKey);
      if (!curNeed) {
        if (curLoop && isLoopSuperseded(curLoop)) state.issueStaleNotice = curLoop.name + " is now superseded; previous task flow was cleared.";
        else state.issueStaleNotice = "";
        state.activeDrawer = null;
      } else if (!curNeed._retainedIncomplete) {
        var nextFullFp = issueFingerprint(curNeed, curLoop, true);
        var nextBaseFp = issueFingerprint(curNeed, curLoop, false);
        var fpMismatch = curIdsMode === state.activeDrawer.idsMode ? nextFullFp !== state.activeDrawer.fp : nextBaseFp !== state.activeDrawer.baseFp;
        if (fpMismatch) {
          state.activeDrawer = null;
          state.issueStaleNotice = "Issue details updated for " + curNeed.loop + " (target, route, thread, or hold changed). Review current state before acting.";
        } else if (curIdsMode !== state.activeDrawer.idsMode) {
          state.activeDrawer.idsMode = curIdsMode;
          state.activeDrawer.fp = nextFullFp;
          state.activeDrawer.baseFp = nextBaseFp;
        }
      }
    }
    if (state.change && state.change.issueFp) {
      var chKey = state.change.key;
      var chNeed = effectiveNeeds(d).filter(function (x) { return x.key === chKey; })[0] || null;
      var chLoop = effectiveLoopByKey(d, chKey);
      if (d.complete === false && (!chNeed || chNeed._retainedIncomplete || (chLoop && chLoop.evidence !== "available"))) {
        state.changeSerial = (state.changeSerial || 0) + 1;
        state.change = null;
        state.issueStaleNotice = "Read was incomplete; previous pending review was cancelled until a complete read confirms current state.";
      } else {
        var chNextFp = chNeed ? issueFingerprint(chNeed, chLoop, true) : "";
        var chNextBaseFp = chNeed ? issueFingerprint(chNeed, chLoop, false) : "";
        var chFpMismatch = chNeed && (curIdsMode === state.change.issueIdsMode ? chNextFp !== state.change.issueFp : chNextBaseFp !== state.change.issueBaseFp);
        var changedOut = !chNeed || chNeed._retainedIncomplete || (chLoop && (isLoopRetired(chLoop) || isLoopSuperseded(chLoop))) || chFpMismatch;
        if (changedOut) {
          state.changeSerial = (state.changeSerial || 0) + 1;
          state.issueStaleNotice = chLoop && isLoopSuperseded(chLoop)
            ? chLoop.name + " was superseded on the latest read. Previous pending review was cancelled."
            : "Issue, target, or hold state updated before confirming" + (chNeed ? " for " + chNeed.loop : "") + ". Previous pending review was cancelled.";
          state.change = null;
        } else if (curIdsMode !== state.change.issueIdsMode) {
          state.change.issueIdsMode = curIdsMode;
          state.change.issueFp = chNextFp;
          state.change.issueBaseFp = chNextBaseFp;
        }
      }
    }
  }
  function verifiedStopCause(nItem, loopObj) {
    var issuesArr = list(loopObj && loopObj.issues);
    var reasonStr = (nItem && nItem.reason) || "";
    if (loopObj && loopObj.supervisor === "not_answering") return { label: "Supervisor did not answer", detail: "A recorded daemon did not answer status checks just now." };
    if (/cannot load the configuration file/i.test(reasonStr) || issuesArr.some(function (x) { return /config/i.test(x); })) return { label: "Configuration unreadable", detail: "The daemon could not load the loop's configuration file." };
    if (nItem && (nItem.kind === "build_replaced" || /replaced from this panel/i.test(reasonStr))) return { label: "Check file replaced before checks stopped", detail: "A panel build update changed the check file before checks stopped." };
    if (/Nothing is supervising/i.test(reasonStr) || (loopObj && loopObj.checkedBy && loopObj.checkedBy.kind === "none")) return { label: "No scheduler on record", detail: "Nothing has checked this loop since it was enabled, resumed, or moved." };
    if (issuesArr.length) return { label: issuesArr[0], detail: "Reported issue in loop records." };
    return { label: "Cause unknown from saved records", detail: "Saved records show overdue checks without an identified scheduler failure cause." };
  }
  function currentDeadlineLimit(loopObj) {
    return list(loopObj && loopObj.limits).filter(function (x) { return /deadline/i.test(x.label || ""); })[0] || null;
  }
  function isDeadlineNeed(nItem, loopObj) {
    return !!((nItem && /deadline/i.test(nItem.reason || "")) || (currentDeadlineLimit(loopObj) && currentDeadlineLimit(loopObj).reached));
  }
  function isSendAllowanceNeed(nItem, loopObj) {
    var hasSendLimit = list(loopObj && loopObj.limits).some(function (x) { return x.reached && /send|message/i.test(x.label || ""); });
    return !!((nItem && /limit on messages sent|send allowance/i.test(nItem.reason || "")) || hasSendLimit);
  }
  function renderResumeFallback(n, isQuestion) {
    if (n.resume && !changesOn()) {
      var fold = el("details", "cli-fold");
      fold.appendChild(el("summary", null, isQuestion ? "Terminal fallback (only if parent hold remains)" : "Terminal resume command (read-only panel)"));
      var body = el("div", "cli-fold-body");
      body.appendChild(el("p", "sub resume-where", isQuestion ? "If answering outside the panel leaves a separate parent hold, run:" : "This page cannot resume a loop. In a terminal, run:"));
      var cmdEl = copyCommand("p", n.resume, "copy-resume-" + n.key, "resume-where-" + n.key);
      cmdEl.classList.add("resume-command");
      body.appendChild(cmdEl);
      var where = el("p", "sub resume-where");
      where.id = "resume-where-" + n.key;
      put(where, ["CONFIG is the path of " + n.loop + "'s configuration file; ", el("code", null, "looptender registry list --private"), " lists it."]);
      body.appendChild(where);
      fold.appendChild(body);
      return fold;
    }
    return null;
  }
  function taskDrawerBox(nItem, loopObj) {
    var dr = state.activeDrawer;
    if (!dr || dr.key !== nItem.key) return null;
    var boxEl = el("div", "task-drawer");
    boxEl.dataset.drawerKey = nItem.key;
    boxEl.dataset.drawerMode = dr.mode;
    var head = el("div", "task-drawer-head");
    var uncText = taskUnconfirmedText(state.data, nItem, loopObj);
    var readPaused = !!(state.failed || (nItem && nItem._retainedIncomplete));
    var fpFull = issueFingerprint(nItem, loopObj, true);
    var fpBase = issueFingerprint(nItem, loopObj, false);
    var idsMode = !!(state.data && state.data.identifiers);
    if (dr.mode === "question") {
      head.appendChild(el("span", null, "Question details"));
      head.appendChild(uiBadge("attn", "Wait: resolves externally when answered"));
      boxEl.appendChild(head);
      if (uncText) boxEl.appendChild(el("span", "unconfirmed-badge", uncText));
      var kv = el("dl", "drawer-kv");
      var waitLanes = waitingLanesOf(loopObj);
      kv.appendChild(el("dt", null, "Waiting lanes"));
      var lanesCell = el("dd");
      if (waitLanes.length) {
        waitLanes.forEach(function (ln, idx) {
          if (idx) lanesCell.appendChild(document.createTextNode(", "));
          var hasPrivTitle = !!(ln.title || ln.titleEvidence === "recorded" || ln.titleEvidence === "withheld");
          var lbl = (state.identifiers && ln.title) ? ln.title : ("Lane " + ln.number + (!state.identifiers && hasPrivTitle ? " (title withheld)" : ""));
          if (ln.loopKey) lanesCell.appendChild(link("#/loop/" + ln.loopKey, lbl));
          else lanesCell.appendChild(document.createTextNode(lbl));
        });
      } else {
        lanesCell.textContent = "Not enumerated separately in this snapshot";
      }
      kv.appendChild(lanesCell);
      var firstLaneLink = waitLanes.filter(function (ln) { return !!ln.loopKey; })[0];
      var thId = loopObj && loopObj.identifiers && loopObj.identifiers.thread;
      kv.appendChild(el("dt", null, "Target thread"));
      var tgtCell = el("dd");
      if (firstLaneLink) tgtCell.appendChild(link("#/loop/" + firstLaneLink.loopKey, "Open " + (firstLaneLink.loop || (state.identifiers && firstLaneLink.title) || "lane loop")));
      else if (state.identifiers && thId) tgtCell.appendChild(copyCommand("div", "bb thread open " + thId, "copy-qthread-" + nItem.key));
      else {
        tgtCell.appendChild(document.createTextNode("Thread identifier hidden or unavailable. "));
        if (!state.identifiers) tgtCell.appendChild(showNotesButton());
      }
      kv.appendChild(tgtCell);
      boxEl.appendChild(kv);
      if (changesOn() && loopObj && list(loopObj.can).indexOf("resume") >= 0 && loopObj.hold && loopObj.hold.state === "held") {
        var optRow = el("div", "task-actions");
        optRow.appendChild(button("Review parent resume…", "act-resume-" + nItem.key, "", function () {
          prepareChange(nItem.key, "resume", undefined, fpFull, fpBase, idsMode);
        }, readPaused));
        optRow.appendChild(el("span", "sub", readPaused ? "Actions paused until a complete read confirms current state." : "Only if a separate parent hold remains after answering."));
        boxEl.appendChild(optRow);
      }
      boxEl.appendChild(el("p", "gap-note", "Direct conversation URLs and backend question, target, or owner revisions are not yet emitted by the panel feed (contract gap G9). This panel conservatively guards against observed route, thread, hold, and lane changes in saved snapshots and relies on backend prepare/confirm checks; same-snapshot replacements without a changed field are not verified separately here. Answer in BB/IDE above; supervision updates on the next saved check."));
    } else if (dr.mode === "deadline") {
      var dlLim = currentDeadlineLimit(loopObj);
      head.appendChild(el("span", null, "Configured deadline details"));
      head.appendChild(uiBadge("attn", dlLim && dlLim.reached ? "Deadline reached" : "Bound reached"));
      boxEl.appendChild(head);
      if (uncText) boxEl.appendChild(el("span", "unconfirmed-badge", uncText));
      var dkv = el("dl", "drawer-kv");
      dkv.appendChild(el("dt", null, "Deadline"));
      dkv.appendChild(el("dd", null, dlLim ? say(dlLim.text) : "Configured deadline reached"));
      dkv.appendChild(el("dt", null, "Impact"));
      dkv.appendChild(el("dd", null, (dlLim && dlLim.effect) || (loopObj && loopObj.limitWait && loopObj.limitWait.afterDeadline ? "Limit reset falls after deadline; held until deadline is extended or marked finished." : "Loop held until finished or deadline extended.")));
      boxEl.appendChild(dkv);
      if (changesOn() && loopObj) {
        var actRow = el("div", "task-actions");
        if (list(loopObj.can).indexOf("archive") >= 0) actRow.appendChild(button("Archive loop…", "act-archive-" + nItem.key, "", function () { prepareChange(nItem.key, "archive", undefined, fpFull, fpBase, idsMode); }, readPaused));
        if (list(loopObj.can).indexOf("stop") >= 0) actRow.appendChild(button("Stop…", "act-stop-" + nItem.key, "", function () { prepareChange(nItem.key, "stop", undefined, fpFull, fpBase, idsMode); }, readPaused));
        if (readPaused && actRow.childNodes.length) actRow.appendChild(el("span", "sub", "Actions paused until a complete read confirms current state."));
        if (actRow.childNodes.length) boxEl.appendChild(actRow);
      }
      boxEl.appendChild(el("p", "gap-note", "Extending loop.limits.deadline or marking finished directly from the panel is not supported yet (contract gap G11). Archive above if done, update the loop's configuration, or run looptender complete in Terminal."));
    } else if (dr.mode === "supervision") {
      var causeInfo = verifiedStopCause(nItem, loopObj);
      head.appendChild(el("span", null, "Supervision diagnostics"));
      head.appendChild(uiBadge("plain", causeInfo.label));
      boxEl.appendChild(head);
      if (uncText) boxEl.appendChild(el("span", "unconfirmed-badge", uncText));
      var skv = el("dl", "drawer-kv");
      skv.appendChild(el("dt", null, "Verified cause"));
      skv.appendChild(el("dd", null, causeInfo.detail));
      skv.appendChild(el("dt", null, "Scheduler / check"));
      skv.appendChild(el("dd", null, (loopObj && loopObj.checkedBy && loopObj.checkedBy.text) || "No check source recorded"));
      skv.appendChild(el("dt", null, "Last check"));
      skv.appendChild(el("dd", null, ((loopObj && loopObj.lastCheck && loopObj.lastCheck.text) || "Never checked") + (nItem.reason ? " \u00B7 " + nItem.reason : "")));
      if (loopObj && loopObj.buildFreshness && loopObj.buildFreshness.text) {
        skv.appendChild(el("dt", null, "Build check"));
        skv.appendChild(el("dd", null, loopObj.buildFreshness.text + (loopObj.buildFreshness.detail ? " " + loopObj.buildFreshness.detail : "")));
      }
      boxEl.appendChild(skv);
      var sAct = el("div", "task-actions");
      if (changesOn() && loopObj && loopObj.buildChange && loopObj.buildChange.action) {
        sAct.appendChild(button(loopObj.buildChange.label + "\u2026", "act-" + loopObj.buildChange.action + "-" + nItem.key, "", function () {
          prepareChange(nItem.key, loopObj.buildChange.action, undefined, fpFull, fpBase, idsMode);
        }, readPaused));
        if (readPaused) sAct.appendChild(el("span", "sub", "Actions paused until a complete read confirms current state."));
      }
      sAct.appendChild(link("#/versions", "Inspect Versions \u2192", "overview-jump"));
      boxEl.appendChild(sAct);
      boxEl.appendChild(el("p", "gap-note", "Starting or restarting a daemon or host launchd/scheduler is outside panel permissions, and structured overdueCause / Recovering states are tracked under contract gap G12."));
    }
    var closeBtn = el("div", "task-actions");
    closeBtn.appendChild(button("Close details", "drawer-close-" + nItem.key, "", function () { state.activeDrawer = null; render(false, true); }));
    boxEl.appendChild(closeBtn);
    return boxEl;
  }
  function taskCard(nItem, onOverview, loopObj) {
    var cat = (nItem.waiting === "checks" || (!nItem.waiting && (nItem.kind === "overdue" || nItem.kind === "build_replaced"))) ? "checks"
      : (nItem.waiting === "unreadable" || nItem.waiting === "unknown" || (!nItem.waiting && nItem.kind === "unreadable")) ? "unreadable" : "decisions";
    var li = el("li", "need task-card " + (cat === "checks" ? "is-stale" : cat === "decisions" ? "is-person" : "is-unreadable"));
    li.dataset.issueKey = nItem.key;
    var head = el("div", "need-head task-top");
    if (onOverview) {
      var hEl = el("h3", "need-name task-name-head");
      hEl.appendChild(link("#/loop/" + nItem.key, nItem.loop));
      head.appendChild(hEl);
    }
    head.appendChild(el("span", "waited", nItem.waited || "Waiting time unknown"));
    li.appendChild(head);
    var uncText = taskUnconfirmedText(state.data, nItem, loopObj);
    if (uncText) {
      li.dataset.unconfirmed = "1";
      li.appendChild(el("span", "unconfirmed-badge", uncText));
    }
    if (loopObj && loopObj.lineage && loopObj.lineage.from) {
      var lin = el("p", "sub");
      lin.appendChild(uiBadge("plain", "Continues earlier loop (" + loopObj.lineage.count + " prior)"));
      li.appendChild(lin);
    }
    li.appendChild(el("p", "reason", nItem.reason));
    var hideLiveIds = !state.identifiers && !!(state.data && state.data.identifiers);
    var textVal = !state.identifiers && nItem.maskedNote ? nItem.maskedNote : (hideLiveIds && nItem.noteMasked ? "[hidden]" : nItem.note);
    if (textVal) {
      var noteEl = el("div", "note");
      noteEl.appendChild(el("span", "note-label", "Note left with the hold"));
      noteEl.appendChild(document.createTextNode("\u201C" + textVal + "\u201D"));
      li.appendChild(noteEl);
      if (nItem.noteMasked && !state.identifiers) {
        var maskP = el("p", "sub", "Some identifiers are hidden. ");
        maskP.appendChild(showNotesButton());
        li.appendChild(maskP);
        var rule = el("details", "mask-rule");
        rule.dataset.rule = nItem.key;
        rule.open = !!state.ruleOpen[nItem.key];
        rule.addEventListener("toggle", function () { state.ruleOpen[nItem.key] = rule.open; });
        rule.appendChild(el("summary", null, "What is hidden"));
        rule.appendChild(el("p", "sub", "Parts that look like an ID, an absolute or home path (a /, ~/, ~name, C:\\ or C:/ or \\ path, with its spaces when each word after the first starts with a capital letter, a digit or a bracket, also one that contains .. or starts with Users, home or root, with / or \\ between its parts, a \\\\server\\share path, and a path with %2F or %5C in it), a web address, a user name and password before a host, an email address, an IPv4 or IPv6 address or a long code are hidden; another relative path such as docs/plan.md or docs\\plan.md is shown. "));
        li.appendChild(rule);
      }
    }
    var todoEl = el("p", "todo");
    todoEl.appendChild(el("span", "todo-label", "Next step"));
    todoEl.appendChild(document.createTextNode(nItem.action));
    li.appendChild(todoEl);

    var actRow = el("div", "task-actions");
    var fpFull = issueFingerprint(nItem, loopObj, true);
    var fpBase = issueFingerprint(nItem, loopObj, false);
    var idsMode = !!(state.data && state.data.identifiers);
    var readPaused = !!(state.failed || (nItem && nItem._retainedIncomplete));
    var isQ = nItem.waiting === "asked";
    var isDl = !isQ && isDeadlineNeed(nItem, loopObj);
    var isAllow = !isQ && !isDl && isSendAllowanceNeed(nItem, loopObj);
    function toggleDrawer(modeName) {
      if (state.activeDrawer && state.activeDrawer.key === nItem.key && state.activeDrawer.mode === modeName) state.activeDrawer = null;
      else state.activeDrawer = { key: nItem.key, mode: modeName, fp: fpFull, baseFp: fpBase, idsMode: idsMode };
      render(false, true);
    }
    if (isQ) {
      var laneTargets = waitingLanesOf(loopObj).filter(function (ln) { return !!ln.loopKey; });
      if (laneTargets.length === 1) {
        var qLane = link("#/loop/" + laneTargets[0].loopKey, "Open question", "btn is-primary");
        qLane.dataset.focus = "open-question-" + nItem.key;
        actRow.appendChild(qLane);
      } else {
        var qBtn = button("Open question", "open-question-" + nItem.key, "is-primary", function () { toggleDrawer("question"); });
        qBtn.setAttribute("aria-pressed", String(!!(state.activeDrawer && state.activeDrawer.key === nItem.key && state.activeDrawer.mode === "question")));
        actRow.appendChild(qBtn);
      }
    } else if (isDl) {
      var dlBtn = button("Review deadline", "review-deadline-" + nItem.key, "is-primary", function () { toggleDrawer("deadline"); });
      dlBtn.setAttribute("aria-pressed", String(!!(state.activeDrawer && state.activeDrawer.key === nItem.key && state.activeDrawer.mode === "deadline")));
      actRow.appendChild(dlBtn);
    } else if (isAllow) {
      var canAllow = !readPaused && changesOn() && !!(state.server && state.server.sendAllowanceEdit === true);
      var allowBtn = button("Review send allowance\u2026", "act-set_send_allowance-" + nItem.key, "is-primary", function () {
        if (onOverview) window.location.hash = "#/loop/" + nItem.key;
        editAllowance(nItem.key, fpFull, fpBase, idsMode);
      }, !canAllow);
      if (!canAllow) allowBtn.title = readPaused ? "Paused until a complete read confirms current state." : "Requires panel started with --allow-changes outside a supervised thread.";
      actRow.appendChild(allowBtn);
      if (!canAllow) actRow.appendChild(el("span", "sub", readPaused ? "Actions paused until a complete read confirms current state." : "Read-only or in-thread: editAllowance unavailable here."));
    } else if (cat === "checks") {
      var supBtn = button("Inspect supervision", "inspect-supervision-" + nItem.key, "", function () { toggleDrawer("supervision"); });
      supBtn.setAttribute("aria-pressed", String(!!(state.activeDrawer && state.activeDrawer.key === nItem.key && state.activeDrawer.mode === "supervision")));
      actRow.appendChild(supBtn);
    } else if (cat === "decisions" && changesOn() && loopObj && list(loopObj.can).indexOf("resume") >= 0) {
      actRow.appendChild(button("Review resume\u2026", "act-resume-" + nItem.key, "", function () {
        if (onOverview) window.location.hash = "#/loop/" + nItem.key;
        prepareChange(nItem.key, "resume", undefined, fpFull, fpBase, idsMode);
      }, readPaused));
    }
    if (onOverview) actRow.appendChild(link("#/loop/" + nItem.key, cat === "checks" ? "Loop diagnostics \u2192" : "Inspect loop \u2192", "overview-jump"));
    li.appendChild(actRow);
    var drBox = taskDrawerBox(nItem, loopObj);
    if (drBox) li.appendChild(drBox);
    if (onOverview && loopObj && state.change && state.change.key === loopObj.key) {
      var inlineCh = changeBox(loopObj, false, true);
      if (inlineCh) li.appendChild(inlineCh);
    }
    var cliFall = renderResumeFallback(nItem, isQ);
    if (cliFall) li.appendChild(cliFall);
    return li;
  }
  function needCard(n, onOverview) {
    return taskCard(n, onOverview, state.data ? effectiveLoopByKey(state.data, n.key) : null);
  }

  // ---- Level 1: the picker ----
  // The needs-you line and the Routes view stay put; only the loop list
  // scrolls, so neither can scroll out of reach.
  function pickRow(href, dotClass, name, sub, current) {
    var a = el("a", "pick");
    a.href = href;
    if (current) a.setAttribute("aria-current", "page");
    a.appendChild(el("span", "dot " + dotClass));
    // The full text stays in the link, so it is the accessible name and what
    // find and copy see; the ellipsis is only how it is drawn. A cut name is
    // read in full by focusing or hovering the row (see dock.css), and by
    // opening it: the page heading shows it whole.
    var n = el("span", "pick-name");
    n.appendChild(el("span", null, name));
    a.appendChild(n);
    var s = el("span", "pick-state");
    s.appendChild(el("span", null, sub));
    a.appendChild(s);
    var go = icon("chevron");
    go.setAttribute("class", "go");
    a.appendChild(go);
    return a;
  }
  function renderPicker(d, r) {
    var nav = $("picker");
    var old = nav.querySelector(".pick-scroll");
    var scrollTop = old ? old.scrollTop : 0;
    nav.replaceChildren();

    var fixed = el("div", "pick-fixed");
    var cats = categorizeNeeds(d);
    var decCount = cats.decisions.length, chkCount = cats.checks.length, unrCount = cats.unreadable.length;
    var urgentNeeds = decCount + unrCount;
    var totalNeeds = list(d.needsYou).length;
    var top = el("a", "pick pick-needs" + (urgentNeeds ? "" : " is-clear"));
    top.href = "#/";
    if (r.view === "home") top.setAttribute("aria-current", "page");
    if (urgentNeeds) top.appendChild(el("span", "count", String(urgentNeeds)));
    else if (chkCount) top.appendChild(el("span", "dot dot-warn"));
    else top.appendChild(icon(d.allClear ? "check" : "unknown"));
    var line = d.needsLine || "Overview status unknown";
    var topTitle = urgentNeeds ? "Decisions & bounds" : chkCount ? "Checks stopped" : (line || "Overview");
    var subParts = [];
    if (decCount) subParts.push(decCount + (decCount === 1 ? " decision" : " decisions"));
    if (chkCount) subParts.push(chkCount + " checks stopped");
    if (cats.atLimit.length) subParts.push(cats.atLimit.length + " at limit");
    if (unrCount) subParts.push(unrCount + " unreadable");
    top.appendChild(el("span", "pick-name", topTitle));
    top.appendChild(el("span", "pick-state", subParts.length ? subParts.join(" \u00B7 ") : (d.allClear ? "All clear" : line)));
    top.setAttribute("aria-label", topTitle + (subParts.length ? ": " + subParts.join(", ") : ""));
    top.appendChild(icon("chevron"));
    fixed.appendChild(top);
    var rs = d.routesSummary || {};
    fixed.appendChild(pickRow("#/routes", rs.trouble ? "dot-limit" : "dot-calm", "Routes", rs.picker || "", r.view === "routes"));
    var ac = d.accounts || {};
    var driftLen = cats.driftCards.length;
    var acctSub = ac.complete ? ((d.accountsReference ? d.selectedAccounts || 0 : list(ac.cards).length) + " selected" + (driftLen ? " \u00B7 " + driftLen + " review needed" : "")) : "Unavailable";
    fixed.appendChild(pickRow("#/accounts", driftLen ? "dot-attn" : "dot-calm", "Accounts", acctSub, r.view === "accounts"));
    nav.appendChild(fixed);

    var label = el("p", "pick-label", "Loops");
    label.id = "pick-loops";
    nav.appendChild(label);
    var scroll = el("div", "pick-scroll");
    var ul = el("ul", "pick-list");
    ul.setAttribute("aria-labelledby", "pick-loops");
    list(d.loops).forEach(function (l) {
      var li = el("li");
      var dotTone = (l.alsoWaiting && !l.inactive) ? "attn" : l.inactive ? "calm" : TONES[l.state] || "unknown";
      var row = pickRow("#/loop/" + l.key, "dot-" + dotTone, l.name, l.pickerState, r.view === "loop" && r.key === l.key);
      if (l.inactive || l.state === "archived" || (l.state === "paused" && !l.alsoWaiting) || l.state === "stopped" || l.state === "finished" || l.state === "off") row.classList.add("is-quiet");
      li.appendChild(row);
      ul.appendChild(li);
    });
    if (!list(d.loops).length) ul.appendChild(el("li", "sub pick-empty", d.registry.availability === "available" ? "No loops registered yet" : "Unknown"));
    scroll.appendChild(ul);
    if (d.registry.omitted) scroll.appendChild(el("p", "sub pick-label", d.registry.omitted + " more not shown"));
    nav.appendChild(scroll);
    scroll.scrollTop = scrollTop;

    var foot = el("div", "pick-foot");
    var vs = d.versions || {};
    foot.appendChild(pickRow("#/versions", vs.behind ? "dot-limit" : "dot-calm", "Versions", vs.summary || "", r.view === "versions"));
    foot.appendChild(pickRow("#/usage", "dot-calm", "Activity", d.usagePicker || "", r.view === "usage"));
    nav.appendChild(foot);
    fitPickerRows();
  }
  // The loop list scrolls inside the sidebar, and its edge used to cut a row in
  // half. When it overflows, size it to a whole number of rows; the leftover
  // space (less than one row) stays empty below it. Scrolling, wheel, keyboard
  // and focus are the browser's own.
  function fitPickerRows() {
    var box = document.querySelector("#picker .pick-scroll");
    if (!box) return;
    box.style.flex = "";
    box.style.height = "";
    var rows = box.querySelectorAll(".pick-list > li");
    if (rows.length < 2 || box.scrollHeight <= box.clientHeight) return;
    var pitch = rows[1].offsetTop - rows[0].offsetTop;
    var gap = pitch - rows[0].offsetHeight;
    var cs = window.getComputedStyle(box);
    var inner = box.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
    var fit = Math.max(1, Math.floor((inner + gap) / pitch));
    if (fit >= rows.length) return;
    box.style.flex = "0 0 auto";
    box.style.height = (fit * pitch - gap + parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom) + parseFloat(cs.borderTopWidth) + parseFloat(cs.borderBottomWidth)) + "px";
  }
  var fitPending = false;
  window.addEventListener("resize", function () {
    if (fitPending) return;
    fitPending = true;
    window.requestAnimationFrame(function () { fitPending = false; fitPickerRows(); });
  });

  function paneHead(title, pill) {
    var head = el("div", "pane-head");
    var h = el("h1", null, title);
    h.id = "pane-title";
    // Focus lands here after a change of view or the skip link.
    h.tabIndex = -1;
    head.appendChild(h);
    if (pill) head.appendChild(pill);
    return head;
  }
  // The view's heading takes focus; the pane only when there is none.
  function focusPaneTitle(scroll) {
    ($("pane-title") || $("pane")).focus({ preventScroll: !scroll });
  }
  function back() {
    var a = link("#/", "", "back");
    a.appendChild(icon("chevron"));
    a.appendChild(document.createTextNode("All loops"));
    return a;
  }
  function panel(title, extra) {
    var p = el("section", "panel");
    var h = el("h2", null, title);
    list(extra).forEach(function (x) { if (x) h.appendChild(x); });
    p.appendChild(h);
    return p;
  }
  function emptyBox(title, body) {
    var b = el("div", "empty");
    b.appendChild(el("p", null, title));
    if (body) b.appendChild(el("p", "sub", body));
    return b;
  }

  // ---- Overview: what needs you, then what else is not routine ----
  // Pause all and stop all: each offered while any loop can have it; the
  // server reviews every loop and words the review, its options and the
  // outcome.
  function groupBox(d) {
    var can = ["pause", "stop"].filter(function (a) {
      return list(d.loops).some(function (l) { return list(l.can).indexOf(a) >= 0; });
    });
    return changeBox({ key: "all", can: can }, true);
  }
  // How fresh a supervisor's build is, in the server's words (it comes from
  // one place in Go, which the menu reads too). Visible text, never a title only.
  function freshnessChip(l) {
    var f = l.buildFreshness;
    if (!f || !f.text) return null;
    var chip = el("span", "fresh fresh-" + String(f.state).replace(/[^a-z]/g, ""), f.text);
    chip.setAttribute("data-fresh", String(f.state));
    return chip;
  }

  // ---- Notifications: the one list of events the server derives (`notices`),
  // the same one the macOS menu reads. The page keeps the ids it has seen: its
  // first read is a baseline and says nothing, a later read adds each new id
  // once, and an id that goes away from a complete read has ended. Many that
  // begin together and share a group are one line. Nothing leaves this
  // browser; browser notifications only after the person clicks "Notify me".
  var NOTICE_KINDS = [
    ["needs_you", "A loop needs you"], ["limit", "A usage limit is hit"], ["supervisor_gone", "A supervisor stops answering"],
    ["lane_failed", "A lane fails"], ["hold", "A hold is set or lifted"], ["install", "An install is adopted or refused"],
    ["disk", "The disk is low"], ["bound", "A deadline or send limit is reached"], ["host", "This computer's session or load"], ["model_step", "A conversation is stepped to another model"]
  ];
  var NOTICE_DEFAULT_ON = { needs_you: true, limit: true, supervisor_gone: true, host: true };
  var NOTICE_RECENT = 10;
  state.notices = { seen: Object.create(null), baselined: false, log: [], unread: 0, open: false };

  // The choices are the shared ones the server keeps in one place (the macOS
  // menu reads them too): they arrive with each read as `notify`, and a change
  // is saved to the server, so they survive a reload. The page itself stores
  // nothing in the browser (a test holds it to that).
  function noticeSetting(kind) {
    var n = state.data && state.data.notify;
    if (n && n.kinds && typeof n.kinds[kind] === "boolean") return n.kinds[kind];
    return NOTICE_DEFAULT_ON[kind] === true;
  }
  function menuNotifying() { var n = state.data && state.data.notify; return !!(n && n.menu && n.menu.notifying); }
  function browserNoticesOn() {
    var n = state.data && state.data.notify;
    return !!(n && n.browser === true) && typeof Notification !== "undefined" && Notification.permission === "granted";
  }
  // Saves one choice: {kind, on} or {browser}. The reply is the new shared state.
  function saveNotify(body) {
    var form = state.server && state.server.notifyForm;
    if (!form) return Promise.resolve(null);
    var ctl = new AbortController(), timer = setTimeout(function () { ctl.abort(); }, 15000);
    return fetch("api/v1/notify", { method: "POST", cache: "no-store", credentials: "same-origin", signal: ctl.signal,
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Keepgoing-Form": form }, body: JSON.stringify(body) })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.json(); })
      .then(function (shared) { if (state.data) state.data.notify = shared; return shared; })
      .catch(function () { notice("notify", "That choice could not be saved. It stays as it was.", false); return null; })
      .finally(function () { clearTimeout(timer); });
  }
  function noticeEntry(text, at) {
    state.notices.log.unshift({ text: String(text), at: at, unread: true });
    if (state.notices.log.length > NOTICE_RECENT) state.notices.log.length = NOTICE_RECENT;
    state.notices.unread = state.notices.log.filter(function (x) { return x.unread; }).length;
  }

  // Takes one read's notices; returns what to push as browser notifications:
  // {text, loop}. A group's count is its real total in this read.
  function noticeStep(d) {
    var st = state.notices, ns = d && Array.isArray(d.notices) ? d.notices : null;
    if (!ns) return [];
    var cur = Object.create(null), fresh = [], push = [], now = Date.now(), total = Object.create(null);
    ns.forEach(function (n) { if (n && typeof n.id === "string") { cur[n.id] = n; total[n.group] = (total[n.group] || 0) + 1; if (!st.seen[n.id]) fresh.push(n); } });
    var complete = d.complete === true, before = st.unread;
    if (st.baselined) {
      if (complete) Object.keys(st.seen).forEach(function (id) { if (!cur[id] && st.seen[id].endText) noticeEntry(st.seen[id].endText, now); });
      fresh.forEach(function (n) { noticeEntry(n.text, now); });
      var groups = Object.create(null), order = [];
      fresh.forEach(function (n) {
        if (!noticeSetting(n.kind)) return;
        if (!groups[n.group]) { groups[n.group] = []; order.push(n.group); }
        groups[n.group].push(n);
      });
      order.forEach(function (g) {
        var m = groups[g];
        push.push(m.length === 1 && total[g] === 1 ? { text: m[0].text, loop: m[0].loop } : { text: String(m[0].groupText).replace("{n}", String(Math.max(m.length, total[g] || 0))), loop: "" });
      });
      // The list opens when something new arrives, and is never closed for the person.
      if (st.unread > before) st.open = true;
    }
    if (complete) st.seen = cur; else Object.keys(cur).forEach(function (id) { st.seen[id] = cur[id]; });
    st.baselined = true;
    return push;
  }

  // Browser notifications: only after the person's click (saved in the shared
  // settings), never while the menu on this computer is already notifying (it
  // pushes every kind that is on, so a second banner would repeat it), and a
  // click on one focuses this page on the loop.
  function postBrowserNotices(items) {
    if (!browserNoticesOn() || menuNotifying()) return;
    items.forEach(function (it) {
      try {
        var n = new Notification("looptender", { body: it.text });
        n.onclick = function () { try { window.focus(); } catch (e) { /* nothing to focus */ } if (it.loop) window.location.hash = "#/loop/" + it.loop; if (n.close) n.close(); };
      } catch (e) { /* the browser refused it */ }
    });
  }

  function noticeArea() {
    var st = state.notices;
    var box = el("details", "notices");
    box.open = st.open;
    box.addEventListener("toggle", function () { st.open = box.open; });
    var sum = el("summary", null, st.unread ? "Notifications · " + st.unread + " new" : "Notifications");
    box.appendChild(sum);
    if (!st.log.length) box.appendChild(el("p", "sub", "Nothing new since this page was opened. Events are listed here when they begin: a loop needing you, a usage limit, a supervisor that stopped answering."));
    else {
      var ul = el("ul", "notice-list");
      st.log.forEach(function (x) {
        var li = el("li", x.unread ? "is-unread" : null);
        if (x.unread) li.appendChild(el("span", "tag", "New"));
        li.appendChild(document.createTextNode(" " + x.text + " "));
        li.appendChild(el("span", "sub", dockAgoText(x.at)));
        ul.appendChild(li);
      });
      box.appendChild(ul);
      if (st.unread) {
        var mark = el("button", "btn", "Mark all read");
        mark.type = "button";
        mark.addEventListener("click", function () { st.log.forEach(function (x) { x.unread = false; }); st.unread = 0; updateTitle(); render(false, true); });
        box.appendChild(mark);
      }
    }
    var set = el("fieldset", "notice-settings");
    set.appendChild(el("legend", "sub", "Notify me when"));
    NOTICE_KINDS.forEach(function (k) {
      var lab = el("label", "notice-kind"), cb = document.createElement("input");
      cb.type = "checkbox"; cb.checked = noticeSetting(k[0]);
      cb.addEventListener("change", function () { saveNotify({ kind: k[0], on: cb.checked }).then(function () { render(false, true); }); });
      lab.appendChild(cb); lab.appendChild(document.createTextNode(" " + k[1]));
      set.appendChild(lab);
    });
    box.appendChild(set);
    if (menuNotifying()) box.appendChild(el("p", "sub", "The looptender menu on this computer is already notifying for these kinds, so the browser stays quiet and nothing is shown twice. This list still fills."));
    if (typeof Notification !== "undefined") {
      var granted = browserNoticesOn();
      var ask = el("button", "btn", granted ? "Browser notifications are on" : "Notify me");
      ask.type = "button";
      ask.disabled = granted || Notification.permission === "denied";
      ask.title = Notification.permission === "denied" ? "This browser has blocked notifications for this page." : "Shows a browser notification for each new event in the kinds above. Nothing leaves this computer.";
      ask.addEventListener("click", function () {
        Promise.resolve(Notification.requestPermission()).then(function (p) { return saveNotify({ browser: p === "granted" }); }).then(function () { render(false, true); });
      });
      box.appendChild(ask);
    }
    return box;
  }

  function dockAgoText(at) {
    var sec = Math.max(0, Math.round((Date.now() - at) / 1000));
    if (sec < 60) return "just now";
    if (sec < 3600) return Math.round(sec / 60) + " min ago";
    return Math.round(sec / 3600) + " h ago";
  }

  function updateTitle() {
    var needs = state.data ? list(state.data.needsYou).length : 0, unread = state.notices.unread;
    document.title = (needs ? "(" + needs + ") " : "") + "looptender" + (unread ? " · " + unread + " new" : "");
  }

  function routerServiceText(svc) {
    switch (svc) {
      case "loaded": return "service loaded";
      case "not_installed": return "service not installed";
      case "absent": return "service not loaded";
      case "partial": return "service partially loaded";
      case "unknown": return "service status unknown";
      default: return "service unavailable";
    }
  }

  function routerBox(r) {
    if (!r || typeof r !== "object") return null;
    var isEnabled = !!r.enabled;
    var isAnswering = !!r.answering;
    var pointed = typeof r.pointedCount === "number" && r.pointedCount >= 0 ? r.pointedCount : 0;
    var needsAttention = (isEnabled || pointed > 0) && !isAnswering;
    var tone = needsAttention ? "attn" : (isAnswering && (isEnabled || pointed > 0) ? "good" : "calm");
    var stateLabel = isAnswering ? "Answering" : "Not answering";
    var wrap = el("section", "panel router-card" + (needsAttention ? " is-attn" : ""));
    wrap.setAttribute("aria-label", "Account Router");
    var head = el("div", "router-head");
    head.appendChild(el("h2", "router-title", "Account Router"));
    var pill = el("span", "state tone-" + tone);
    pill.appendChild(el("span", "dot dot-" + tone));
    pill.appendChild(el("span", null, stateLabel));
    head.appendChild(pill);
    wrap.appendChild(head);
    var meta = [
      isEnabled ? "Enabled" : "Off",
      pointed + " pointed",
      routerServiceText(r.service)
    ];
    if (r.directory && r.directory !== "private") meta.push("directory " + r.directory);
    wrap.appendChild(el("p", "sub router-meta", meta.join(" \u00B7 ")));
    if (needsAttention) {
      wrap.appendChild(el("p", "router-warning", "Router is enabled or pointed while its socket is not answering. Check looptender router status, start the router, or run looptender router off."));
    }
    return wrap;
  }

  function overviewHealthBar(d, cats) {
    var wrap = el("section", "overview-health");
    wrap.setAttribute("aria-label", "Build status and categorized summary");
    var left = el("div", "overview-build-meta");
    var verStr = state.server && typeof state.server.version === "string" ? state.server.version : "";
    var verShown = (!verStr || verStr === "dev") ? "Build unknown (dev)" : verStr;
    var panelLbl = el("span", null);
    panelLbl.appendChild(el("span", "sub", "Panel running: "));
    panelLbl.appendChild(el("code", null, verShown));
    left.appendChild(panelLbl);
    if (verStr || (state.server && state.server.servedBy)) left.appendChild(el("span", "build-rev-note", buildRevisionNote(verStr)));
    if (d.buildFreshness && d.buildFreshness.text && list(d.loops).length) {
      var freshSpan = el("span", "sub fresh-summary", d.buildFreshness.text);
      left.appendChild(freshSpan);
    } else if (d.versions && d.versions.summary) {
      left.appendChild(el("span", "sub", d.versions.summary));
    }
    left.appendChild(link("#/versions", "Versions \u2192", "overview-jump"));
    wrap.appendChild(left);

    var countsDiv = el("div", "cat-counts");
    function addCatPill(filterId, clsName, numVal, labelStr) {
      var btnEl = el("button", "cat-pill " + clsName);
      btnEl.type = "button";
      btnEl.setAttribute("aria-pressed", String(state.mapFilter === filterId));
      btnEl.dataset.focus = "health-" + filterId;
      btnEl.appendChild(el("span", "count-num", String(numVal)));
      btnEl.appendChild(el("span", null, labelStr));
      btnEl.addEventListener("click", function () {
        if (state.mapFilter === filterId) {
          var targetSec = $("attention-section");
          if (targetSec && targetSec.scrollIntoView) targetSec.scrollIntoView({ block: "start" });
          return;
        }
        state.mapFilter = filterId;
        render(false, true);
      });
      countsDiv.appendChild(btnEl);
    }
    if (cats.decisions.length) addCatPill("decisions", "is-attn", cats.decisions.length, cats.decisions.length === 1 ? "Decision or bound" : "Decisions & bounds");
    if (cats.atLimit.length) addCatPill("at_limit", "is-limit", cats.atLimit.length, "At limit");
    if (cats.checks.length) addCatPill("checks", "", cats.checks.length, "Checks stopped");
    if (cats.unreadable.length) addCatPill("all", "is-warn", cats.unreadable.length, "Unreadable");
    if (cats.driftCards.length) {
      var driftLink = link("#/accounts", "", "cat-pill is-attn");
      driftLink.appendChild(el("span", "count-num", String(cats.driftCards.length)));
      driftLink.appendChild(el("span", null, "Account review"));
      countsDiv.appendChild(driftLink);
    }
    if (!cats.all.length && !cats.atLimit.length && !cats.driftCards.length && d.allClear) {
      countsDiv.appendChild(uiBadge("good", "All active loops clear"));
    } else if (cats.all.length || cats.driftCards.length) {
      var jump = link("#/", "Jump to actions \u2193", "overview-jump");
      jump.addEventListener("click", function (ev) {
        ev.preventDefault();
        var targetSec = $("attention-section");
        if (targetSec && targetSec.scrollIntoView) targetSec.scrollIntoView({ block: "start" });
      });
      countsDiv.appendChild(jump);
    }
    wrap.appendChild(countsDiv);
    return wrap;
  }

  function buildSetupTopology(d, cats) {
    var needByKey = Object.create(null);
    list(d.needsYou).forEach(function (nItem) { needByKey[nItem.key] = nItem; });
    var decKeys = Object.create(null), chkKeys = Object.create(null);
    cats.decisions.forEach(function (nItem) { decKeys[nItem.key] = true; });
    cats.checks.forEach(function (nItem) { chkKeys[nItem.key] = true; });

    var routeCatalogMap = Object.create(null);
    function ensureRouteObj(rKey, rawTitle, rawMeta, extraSrc) {
      if (!rKey) return null;
      if (!routeCatalogMap[rKey]) {
        routeCatalogMap[rKey] = {
          key: rKey, title: rawTitle || rKey, meta: list(rawMeta), readiness: "", phrase: "",
          attention: "", inUseCount: 0, standbyCount: 0, accountId: "acct:unbound",
          bindingKind: "none", bindingNote: "Account not assigned in catalog", groupName: "",
          rawRoute: null
        };
      }
      var rec = routeCatalogMap[rKey];
      if (extraSrc) {
        if (extraSrc.title && rec.title === rKey) rec.title = extraSrc.title;
        if (extraSrc.account && !rec.groupName) rec.groupName = extraSrc.account;
      }
      return rec;
    }
    list(d.routes).forEach(function (rt) {
      var rec = ensureRouteObj(rt.key, routeDisplayTitle(rt), rt.meta, rt);
      rec.rawRoute = rt;
      rec.readiness = rt.readiness || "";
      rec.phrase = rt.phrase || "";
      rec.attention = rt.attention || "";
      rec.inUseCount = list(rt.runningNow).length + list(rt.recordedFor).length;
      var stTotal = 0;
      list(rt.standby).forEach(function (sg) { stTotal += list(sg.loops).length; });
      rec.standbyCount = stTotal;
    });

    var cardList = (d.accounts && d.accounts.complete && list(d.accounts.cards)) || (savedAccounts.status === "ready" && savedAccounts.data && list(savedAccounts.data.cards)) || [];
    var cardById = Object.create(null), acctMap = Object.create(null), accts = [];
    cardList.forEach(function (cItem) { cardById[cItem.id] = cItem; });
    var usesArr = list(d.accountUses);
    usesArr.forEach(function (uItem) {
      if (uItem.card && uItem.routes) {
        list(uItem.routes).forEach(function (ur) {
          var rec = ensureRouteObj(ur.route, ur.title, [], null);
          if (!rec) return;
          if (ur.checkText) {
            rec.accountId = "acct:mismatch:" + uItem.card;
            rec.bindingKind = "mismatch";
            rec.bindingNote = ur.checkText;
          } else {
            rec.accountId = "acct:" + uItem.card;
            rec.bindingKind = ur.bbThread ? "not_verified" : "consistent";
            rec.bindingNote = ur.bbThread ? "Assigned by you (BB thread unverified)" : "Assigned by you";
          }
        });
      }
    });

    var q = state.mapQuery.trim().toLowerCase();
    var baseLoops = state.mapShowRetired ? list(d.loops) : cats.activeLoops;
    var visibleLoops = [];
    baseLoops.forEach(function (loopObj) {
      if (state.mapFilter === "decisions" && !decKeys[loopObj.key]) return;
      if (state.mapFilter === "at_limit" && loopObj.state !== "waiting_on_limit") return;
      if (state.mapFilter === "checks" && !chkKeys[loopObj.key]) return;
      var chainEntries = list(loopObj.chain && loopObj.chain.entries);
      var edges = [];
      chainEntries.forEach(function (entryItem, idx) {
        var rKey = entryItem.route;
        var rRec = ensureRouteObj(rKey, entryItem.title, entryItem.meta, entryItem);
        var ca = entryItem.catalogAccount;
        if (ca && ca.assigned === "by_you") {
          var acctKey = ca.card || (ca.label ? "label:" + ca.label : "");
          if (ca.check === "consistent" || ca.check === "not_verified") {
            rRec.accountId = acctKey ? "acct:" + acctKey : "acct:unbound";
            rRec.bindingKind = ca.check === "not_verified" ? "not_verified" : "consistent";
            rRec.bindingNote = ca.check === "not_verified" ? "Assigned by you (BB thread unverified)" : "Assigned by you";
          } else if (ca.check) {
            rRec.accountId = acctKey ? "acct:" + acctKey : "acct:unbound";
            rRec.bindingKind = "mismatch";
            rRec.bindingNote = ca.check.replace(/_/g, " ");
          }
        }
        var departedLimit = list(loopObj.away && loopObj.away.departures).some(function (dep) {
          return dep.route === rKey && dep.how === "usage_limit" && !dep.resetPassed;
        });
        var edgeObj = {
          loopKey: loopObj.key, routeKey: rKey, pos: (typeof entryItem.position === "number" ? entryItem.position : idx) + 1,
          inUse: !!entryItem.inUse, current: !!entryItem.current, departedLimit: departedLimit,
          handoffAuto: list(loopObj.chain && loopObj.chain.handoffOn).length > 0,
          readiness: entryItem.readiness || "", title: entryItem.title || rRec.title
        };
        edges.push(edgeObj);
      });
      if (!edges.length && loopObj.currentRoute && loopObj.currentRoute.availability === "known") {
        var cr = loopObj.currentRoute;
        var synKey = "current:" + loopObj.key;
        var synRec = ensureRouteObj(synKey, (cr.model || cr.tool || "Configured route"), [], cr);
        edges.push({ loopKey: loopObj.key, routeKey: synKey, pos: 1, inUse: loopObj.state === "supervising", current: true, departedLimit: false, handoffAuto: false, readiness: "", title: synRec.title });
      }
      var hay = [loopObj.name, loopObj.phrase, (loopObj.device && loopObj.device.text) || ""].concat(edges.map(function (ed) {
        var rRec = routeCatalogMap[ed.routeKey];
        var caCard = rRec && cardById[rRec.accountId.replace(/^acct:/, "")];
        return (ed.title || "") + " " + (rRec ? rRec.meta.join(" ") : "") + " " + (caCard ? caCard.label : "");
      })).join(" ").toLowerCase();
      if (q && hay.indexOf(q) < 0) return;
      visibleLoops.push({ loop: loopObj, edges: edges, need: needByKey[loopObj.key] || null });
    });

    var routeKeysSeen = Object.create(null), visibleRoutes = [];
    visibleLoops.forEach(function (vItem) {
      vItem.edges.forEach(function (ed) {
        if (!routeKeysSeen[ed.routeKey]) { routeKeysSeen[ed.routeKey] = true; visibleRoutes.push(routeCatalogMap[ed.routeKey]); }
      });
    });
    if (state.mapFilter === "all" && !q) {
      list(d.routes).forEach(function (rt) {
        if (!routeKeysSeen[rt.key] && routeCatalogMap[rt.key]) { routeKeysSeen[rt.key] = true; visibleRoutes.push(routeCatalogMap[rt.key]); }
      });
    }

    function ensureAcctNode(aid, labelStr, providerStr, unboundFlag, cardData) {
      if (!acctMap[aid]) {
        acctMap[aid] = { id: aid, label: labelStr, provider: providerStr, unbound: !!unboundFlag, card: cardData || null, uses: null };
        accts.push(acctMap[aid]);
      }
      return acctMap[aid];
    }
    var hasUnboundRoute = false;
    visibleRoutes.forEach(function (rRec) {
      if (rRec.accountId === "acct:unbound" || rRec.bindingKind === "none") {
        hasUnboundRoute = true;
        return;
      }
      var cid = rRec.accountId.replace(/^acct:mismatch:/, "").replace(/^acct:/, "");
      var cObj = cardById[cid];
      var lbl = cObj ? cObj.label : (rRec.groupName || "Assigned account");
      var prv = cObj ? cObj.provider : "";
      var acNode = ensureAcctNode("acct:" + cid, lbl, prv, false, cObj);
      if (rRec.bindingKind === "mismatch") rRec.accountId = "acct:" + cid;
      acNode.hasRoutes = true;
    });
    if (state.mapFilter === "all" && !q) {
      cardList.forEach(function (cItem) { ensureAcctNode("acct:" + cItem.id, cItem.label, cItem.provider, false, cItem); });
    }
    usesArr.forEach(function (uItem) {
      if (uItem.card && acctMap["acct:" + uItem.card]) acctMap["acct:" + uItem.card].uses = uItem;
      if (uItem.context) hasUnboundRoute = true;
    });
    if (hasUnboundRoute) {
      ensureAcctNode("acct:unbound", "Unbound route contexts", "Account unknown", true, null);
    }

    var sel = state.mapSelected;
    var selLoops = Object.create(null), selRoutes = Object.create(null), selAccts = Object.create(null);
    if (sel) {
      if (sel.kind === "loop") {
        selLoops[sel.id] = true;
        visibleLoops.forEach(function (vItem) {
          if (vItem.loop.key !== sel.id) return;
          vItem.edges.forEach(function (ed) {
            selRoutes[ed.routeKey] = true;
            var rRec = routeCatalogMap[ed.routeKey];
            if (rRec) selAccts[rRec.accountId] = true;
          });
        });
      } else if (sel.kind === "route") {
        selRoutes[sel.id] = true;
        var rPicked = routeCatalogMap[sel.id];
        if (rPicked) selAccts[rPicked.accountId] = true;
        visibleLoops.forEach(function (vItem) {
          if (vItem.edges.some(function (ed) { return ed.routeKey === sel.id; })) selLoops[vItem.loop.key] = true;
        });
      } else if (sel.kind === "acct") {
        selAccts[sel.id] = true;
        visibleRoutes.forEach(function (rRec) {
          if (rRec.accountId !== sel.id) return;
          selRoutes[rRec.key] = true;
          visibleLoops.forEach(function (vItem) {
            if (vItem.edges.some(function (ed) { return ed.routeKey === rRec.key; })) selLoops[vItem.loop.key] = true;
          });
        });
      }
    }
    return {
      loops: visibleLoops, routes: visibleRoutes, accounts: accts,
      routeMap: routeCatalogMap, acctMap: acctMap,
      sel: sel, selLoops: selLoops, selRoutes: selRoutes, selAccts: selAccts
    };
  }

  function renderPathInspector(topo) {
    var insp = el("div", "path-inspector" + (topo.sel ? " is-active" : ""));
    insp.id = "map-inspector";
    var desc = el("div");
    var linksDiv = el("div", "path-inspector-links");
    if (!topo.sel) {
      desc.appendChild(el("span", "sub", "Minimal view active: showing active current routes only. Select any loop, configured route, or account card to trace full #1 \u2192 #2 fallback chains."));
      insp.appendChild(desc);
      return insp;
    }
    if (topo.sel.kind === "loop") {
      var vMatch = topo.loops.filter(function (v) { return v.loop.key === topo.sel.id; })[0];
      if (!vMatch) {
        desc.textContent = "Selected loop is hidden by current filter.";
      } else {
        desc.appendChild(el("strong", null, "Selected loop: "));
        desc.appendChild(document.createTextNode(vMatch.loop.name + " "));
        vMatch.edges.forEach(function (ed) {
          var rRec = topo.routeMap[ed.routeKey];
          var acNode = rRec && topo.acctMap[rRec.accountId];
          desc.appendChild(document.createTextNode(" \u2192 #" + ed.pos + " " + ed.title + " (" + (ed.inUse ? "in use" : ed.departedLimit ? "hit limit" : ed.current ? "current \u00B7 held" : (ed.handoffAuto ? "standby \u00B7 auto" : "standby \u00B7 manual")) + (acNode ? " \u2192 " + acNode.label + (rRec.bindingKind === "not_verified" ? " [BB unverified]" : rRec.bindingKind === "none" ? " [unbound]" : "") : "") + ")"));
        });
        linksDiv.appendChild(link("#/loop/" + vMatch.loop.key, "Inspect loop \u2192", "overview-jump"));
      }
    } else if (topo.sel.kind === "route") {
      var rMatch = topo.routeMap[topo.sel.id];
      if (rMatch) {
        var acTarget = topo.acctMap[rMatch.accountId];
        desc.appendChild(el("strong", null, "Selected route: "));
        desc.appendChild(document.createTextNode(rMatch.title + (rMatch.groupName ? " \u00B7 group " + rMatch.groupName : "") + (acTarget ? " \u2192 " + acTarget.label + " (" + rMatch.bindingNote + ")" : "")));
        if (!/^current:/.test(rMatch.key)) linksDiv.appendChild(link("#/routes/" + rMatch.key, "View route \u2192", "overview-jump"));
      }
    } else if (topo.sel.kind === "acct") {
      var aMatch = topo.acctMap[topo.sel.id];
      if (aMatch) {
        desc.appendChild(el("strong", null, aMatch.unbound ? "Unbound contexts: " : "Selected account: "));
        desc.appendChild(document.createTextNode(aMatch.label + (aMatch.unbound ? " \u2014 separate routes do not prove shared or separate subscriptions." : "")));
        if (!aMatch.unbound && aMatch.card && aMatch.card.id) linksDiv.appendChild(link("#/accounts/" + aMatch.card.id, "Open account \u2192", "overview-jump"));
        else linksDiv.appendChild(link("#/accounts", "View Accounts \u2192", "overview-jump"));
      }
    }
    linksDiv.appendChild(button("Clear selection", "map-clear-selection", "", function () { state.mapSelected = null; render(false, true); }));
    insp.appendChild(desc);
    insp.appendChild(linksDiv);
    return insp;
  }

  function setupListFallback(topo) {
    var wrap = el("div", "list-fallback");
    wrap.id = "setup-list";
    if (!topo.loops.length) {
      wrap.appendChild(el("p", "sub", "No loops match the current Setup Map filter."));
      return wrap;
    }
    topo.loops.forEach(function (vItem) {
      var lObj = vItem.loop, selectedHere = topo.sel && topo.sel.kind === "loop" && topo.sel.id === lObj.key;
      var cardDiv = el("div", "list-loop-card" + (selectedHere ? " is-selected" : ""));
      var topRow = el("div", "map-card-top");
      var titleSpan = el("span", "map-card-title");
      var lTone = lObj.alsoWaiting ? "attn" : (TONES[lObj.state] || "unknown");
      titleSpan.appendChild(el("span", "dot dot-" + lTone));
      titleSpan.appendChild(link("#/loop/" + lObj.key, lObj.name));
      topRow.appendChild(titleSpan);
      topRow.appendChild(uiBadge(lTone === "unknown" ? "plain" : lTone, lObj.phrase));
      cardDiv.appendChild(topRow);
      var metaParts = [];
      if (lObj.device && lObj.device.text) metaParts.push(lObj.device.text);
      if (lObj.lastCheck && lObj.lastCheck.text) metaParts.push(lObj.lastCheck.text);
      if (metaParts.length) cardDiv.appendChild(el("div", "map-card-meta", metaParts.join(" \u00B7 ")));
      if (lObj.lanes && lObj.lanes.summary) cardDiv.appendChild(el("div", "map-card-meta", "Lanes: " + lObj.lanes.summary));
      if (vItem.edges.length) {
        var ol = el("ol", "list-route-chain");
        vItem.edges.forEach(function (ed) {
          var rRec = topo.routeMap[ed.routeKey];
          var acTarget = rRec && topo.acctMap[rRec.accountId];
          var rowLi = el("li", "list-route-row");
          var leftTxt = el("span");
          leftTxt.appendChild(el("strong", null, "#" + ed.pos + " "));
          leftTxt.appendChild(document.createTextNode(ed.title + " (" + (ed.inUse ? "in use" : ed.departedLimit ? "hit limit" : ed.current ? "current \u00B7 held" : (ed.handoffAuto ? "standby \u00B7 auto" : "standby \u00B7 manual")) + ")"));
          rowLi.appendChild(leftTxt);
          if (acTarget) rowLi.appendChild(uiBadge(acTarget.unbound ? "plain" : (rRec.bindingKind === "mismatch" ? "attn" : "good"), acTarget.label + (rRec.bindingKind === "not_verified" ? " (BB unverified)" : rRec.bindingKind === "none" ? " (unknown)" : "")));
          ol.appendChild(rowLi);
        });
        cardDiv.appendChild(ol);
      }
      wrap.appendChild(cardDiv);
    });
    if (topo.accounts.length) {
      var acHeading = el("h3", "attn-group-title", "Selected accounts & unbound contexts (" + topo.accounts.length + ")");
      wrap.appendChild(acHeading);
      topo.accounts.forEach(function (ac) {
        var acCard = el("div", "list-loop-card" + (ac.unbound ? " is-unbound" : ""));
        var topA = el("div", "map-card-top");
        topA.appendChild(el("span", "map-card-title", ac.label));
        topA.appendChild(uiBadge(ac.unbound ? "plain" : "good", ac.provider || "Account"));
        acCard.appendChild(topA);
        if (ac.unbound) acCard.appendChild(el("div", "map-card-meta", "No assigned catalog account. Separate routes do not prove separate subscriptions."));
        wrap.appendChild(acCard);
      });
    }
    return wrap;
  }

  function drawSetupEdges(topo, canvasEl, svgEl) {
    if (!canvasEl || !svgEl) return;
    if (window.getComputedStyle && window.getComputedStyle(canvasEl).display === "none") { svgEl.replaceChildren(); return; }
    var cBox = canvasEl.getBoundingClientRect();
    if (!cBox || cBox.width < 40 || cBox.height < 20) { svgEl.replaceChildren(); return; }
    svgEl.setAttribute("viewBox", "0 0 " + cBox.width.toFixed(1) + " " + cBox.height.toFixed(1));
    svgEl.replaceChildren();

    var ns = "http://www.w3.org/2000/svg";
    var markerColors = {
      "arr-good": "var(--good)",
      "arr-neutral": "var(--ink-3)",
      "arr-limit": "var(--limit)",
      "arr-bound": "var(--border-ui)",
      "arr-attn": "var(--attn)"
    };

    function anchorPt(cardId, side, dy) {
      var domEl = canvasEl.querySelector('[data-card-id="' + cardId + '"]');
      if (!domEl) return null;
      var rBox = domEl.getBoundingClientRect();
      return { x: (side === "right" ? rBox.right : rBox.left) - cBox.left, y: (rBox.top + rBox.height / 2 + (dy || 0)) - cBox.top };
    }
    var placedBadges = [], badgeNodes = [], suppressedBadges = 0;
    var maxBadgeY = Math.max(9, cBox.height - 9);
    function badgeCollides(cx, cy, badgeW) {
      return placedBadges.some(function (b) {
        return Math.abs(b.x - cx) < (b.w + badgeW) / 2 + 2 && Math.abs(b.y - cy) < 18;
      });
    }
    function ortho(p1, p2, channelX, clsStr, markerId, badgeLabel, portCls, isLimitBadge, badgeSide) {
      if (!p1 || !p2) return;
      var x1 = p1.x, y1 = p1.y, x2 = p2.x - 1, y2 = p2.y;
      var endX = x2 - 6;
      var pathD;
      if (Math.abs(y1 - y2) < 2) {
        pathD = "M" + x1.toFixed(1) + "," + y1.toFixed(1) + " L" + endX.toFixed(1) + "," + y2.toFixed(1);
      } else {
        var rVal = Math.min(6, Math.abs(y2 - y1) / 2, Math.abs(channelX - x1) / 2, Math.abs(endX - channelX) / 2);
        var sy = y2 > y1 ? 1 : -1;
        pathD = "M" + x1.toFixed(1) + "," + y1.toFixed(1)
          + " H" + (channelX - rVal).toFixed(1)
          + " Q" + channelX.toFixed(1) + "," + y1.toFixed(1) + " " + channelX.toFixed(1) + "," + (y1 + sy * rVal).toFixed(1)
          + " V" + (y2 - sy * rVal).toFixed(1)
          + " Q" + channelX.toFixed(1) + "," + y2.toFixed(1) + " " + (channelX + rVal).toFixed(1) + "," + y2.toFixed(1)
          + " H" + endX.toFixed(1);
      }
      var pNode = document.createElementNS(ns, "path");
      pNode.setAttribute("d", pathD); pNode.setAttribute("class", "edge " + clsStr);
      svgEl.appendChild(pNode);

      if (markerId && markerColors[markerId]) {
        var arrowEl = document.createElementNS(ns, "polygon");
        arrowEl.setAttribute("points", (endX - 1).toFixed(1) + "," + (y2 - 3.5).toFixed(1) + " " + x2.toFixed(1) + "," + y2.toFixed(1) + " " + (endX - 1).toFixed(1) + "," + (y2 + 3.5).toFixed(1));
        arrowEl.setAttribute("fill", markerColors[markerId]);
        arrowEl.setAttribute("class", "edge-arrow" + (markerId === "arr-good" || markerId === "arr-limit" ? " is-accent" : ""));
        svgEl.appendChild(arrowEl);
      }

      var dotEl = document.createElementNS(ns, "circle");
      dotEl.setAttribute("cx", x1.toFixed(1)); dotEl.setAttribute("cy", y1.toFixed(1)); dotEl.setAttribute("r", "3");
      dotEl.setAttribute("class", "edge-port " + (portCls || ""));
      svgEl.appendChild(dotEl);

      if (badgeLabel) {
        var bw = Math.max(22, Math.ceil(badgeLabel.length * 6.5 + 8));
        var bx = badgeSide === "start"
          ? Math.max(x1 + bw / 2 + 6, Math.min(channelX - bw / 2 - 4, x1 + bw / 2 + 7))
          : Math.max(channelX + bw / 2 + 3, endX - bw / 2 - 7);
        var baseBy = badgeSide === "start" ? y1 : y2;
        var by = Math.min(maxBadgeY, Math.max(9, baseBy));
        var placed = !badgeCollides(bx, by, bw);
        if (!placed) {
          for (var step = 1; step <= 12; step++) {
            var upY = baseBy - step * 18;
            if (upY >= 9 && upY <= maxBadgeY && !badgeCollides(bx, upY, bw)) { by = upY; placed = true; break; }
            var dnY = baseBy + step * 18;
            if (dnY >= 9 && dnY <= maxBadgeY && !badgeCollides(bx, dnY, bw)) { by = dnY; placed = true; break; }
          }
        }
        if (placed) {
          placedBadges.push({ x: bx, y: by, w: bw });
          var bg = document.createElementNS(ns, "rect");
          bg.setAttribute("x", (bx - bw / 2).toFixed(1)); bg.setAttribute("y", (by - 8).toFixed(1));
          bg.setAttribute("width", String(bw)); bg.setAttribute("height", "16"); bg.setAttribute("rx", "3");
          bg.setAttribute("class", "edge-badge-bg" + (isLimitBadge ? " is-limit" : ""));
          var txt = document.createElementNS(ns, "text");
          txt.setAttribute("x", bx.toFixed(1)); txt.setAttribute("y", by.toFixed(1));
          txt.setAttribute("class", "edge-badge-text"); txt.textContent = badgeLabel;
          badgeNodes.push(bg, txt);
        } else {
          suppressedBadges += 1;
        }
      }
    }

    var col1 = canvasEl.querySelector("#col-loops"), col2 = canvasEl.querySelector("#col-routes"), col3 = canvasEl.querySelector("#col-accounts");
    if (!col1 || !col2 || !col3) return;
    var c1R = col1.getBoundingClientRect().right - cBox.left, c2L = col2.getBoundingClientRect().left - cBox.left;
    var c2R = col2.getBoundingClientRect().right - cBox.left, c3L = col3.getBoundingClientRect().left - cBox.left;

    var lrList = [], inCountByRoute = Object.create(null), outCountByLoop = Object.create(null);
    topo.loops.forEach(function (vItem) {
      var lKey = vItem.loop.key;
      vItem.edges.forEach(function (ed, idx) {
        if (topo.sel) {
          if (!(topo.selLoops[lKey] && topo.selRoutes[ed.routeKey])) return;
        } else {
          if (!ed.current && !(idx === 0 && !vItem.edges.some(function (e2) { return e2.current; }))) return;
        }
        inCountByRoute[ed.routeKey] = (inCountByRoute[ed.routeKey] || 0) + 1;
        outCountByLoop[lKey] = (outCountByLoop[lKey] || 0) + 1;
        lrList.push({ loopKey: lKey, edge: ed, seq: inCountByRoute[ed.routeKey], outSeq: outCountByLoop[lKey] });
      });
    });
    var maxInPerRoute = 0, maxOutPerLoop = 0, maxBadgeW = 22;
    Object.keys(inCountByRoute).forEach(function (k) { if (inCountByRoute[k] > maxInPerRoute) maxInPerRoute = inCountByRoute[k]; });
    Object.keys(outCountByLoop).forEach(function (k) { if (outCountByLoop[k] > maxOutPerLoop) maxOutPerLoop = outCountByLoop[k]; });
    if (topo.sel) {
      lrList.forEach(function (it) {
        var lblW = Math.max(22, Math.ceil(("#" + it.edge.pos).length * 6.5 + 8));
        if (lblW > maxBadgeW) maxBadgeW = lblW;
      });
    }
    var useStartBadges = Boolean(topo.sel && topo.sel.kind !== "loop" && maxInPerRoute > 1 && maxOutPerLoop < maxInPerRoute);
    lrList.forEach(function (item, i) {
      var ed = item.edge, totalIn = inCountByRoute[ed.routeKey] || 1, totalOut = outCountByLoop[item.loopKey] || 1;
      var tgtDy = totalIn > 1 ? (item.seq - (totalIn + 1) / 2) * 8 : 0;
      var srcStep = useStartBadges ? Math.min(19, 54 / Math.max(1, totalOut - 1)) : Math.min(8, 44 / Math.max(1, totalOut - 1));
      var srcDy = totalOut > 1 ? (item.outSeq - (totalOut + 1) / 2) * srcStep : 0;
      var p1 = anchorPt("loop-" + item.loopKey, "right", srcDy), p2 = anchorPt("route-" + ed.routeKey, "left", tgtDy);
      var chLeft = useStartBadges ? Math.min(c1R + maxBadgeW + 16, c2L - 20) : (c1R + 12);
      var chRight = Math.max(chLeft + 4, useStartBadges ? (c2L - 16) : (c2L - (maxBadgeW + 24)));
      var spanW = Math.max(4, chRight - chLeft);
      var chX = chLeft + (lrList.length <= 1 ? spanW / 2 : (i / (lrList.length - 1)) * spanW);
      var eCls = ed.departedLimit ? "edge-limit" : ed.inUse ? "edge-inuse" : ed.current ? "edge-current" : "edge-fallback";
      var mkId = ed.departedLimit ? "arr-limit" : (ed.inUse || !ed.current) ? "arr-good" : "arr-neutral";
      var pCls = ed.departedLimit ? "is-limit" : ed.current && !ed.inUse ? "is-neutral" : "";
      ortho(p1, p2, chX, eCls, mkId, topo.sel ? "#" + ed.pos : "", pCls, ed.departedLimit, useStartBadges ? "start" : "end");
    });

    var raList = [], inCountByAcct = Object.create(null);
    topo.routes.forEach(function (rRec) {
      if (topo.sel && (!topo.selRoutes[rRec.key] || !topo.selAccts[rRec.accountId])) return;
      if (!topo.sel && !lrList.some(function (it) { return it.edge.routeKey === rRec.key; })) return;
      inCountByAcct[rRec.accountId] = (inCountByAcct[rRec.accountId] || 0) + 1;
      raList.push({ route: rRec, seq: inCountByAcct[rRec.accountId] });
    });
    raList.forEach(function (item, i) {
      var rRec = item.route, totalIn = inCountByAcct[rRec.accountId] || 1;
      var tgtDy = totalIn > 1 ? (item.seq - (totalIn + 1) / 2) * 8 : 0;
      var p1 = anchorPt("route-" + rRec.key, "right", 0), p2 = anchorPt("acct-" + rRec.accountId, "left", tgtDy);
      var chLeft = c2R + 12, chRight = Math.max(chLeft + 4, c3L - 18);
      var spanW = Math.max(4, chRight - chLeft);
      var chX = chLeft + (raList.length <= 1 ? spanW / 2 : (i / (raList.length - 1)) * spanW);
      var eCls = rRec.bindingKind === "none" ? "edge-unbound" : rRec.bindingKind === "mismatch" ? "edge-mismatch" : "edge-bound";
      var mkId = rRec.bindingKind === "none" ? "arr-neutral" : rRec.bindingKind === "mismatch" ? "arr-attn" : "arr-bound";
      ortho(p1, p2, chX, eCls, mkId, "", rRec.bindingKind === "mismatch" ? "is-attn" : "is-neutral", false);
    });
    badgeNodes.forEach(function (badgeNode) { svgEl.appendChild(badgeNode); });

    if (suppressedBadges > 0) svgEl.setAttribute("data-suppressed-badges", String(suppressedBadges));
    else if (typeof svgEl.removeAttribute === "function") svgEl.removeAttribute("data-suppressed-badges");
    var fbEl = canvasEl.parentElement && typeof canvasEl.parentElement.querySelector === "function"
      ? canvasEl.parentElement.querySelector("#map-badge-fallback")
      : null;
    if (fbEl) {
      fbEl.replaceChildren();
      if (suppressedBadges > 0) {
        fbEl.hidden = false;
        fbEl.appendChild(el("span", null, suppressedBadges + " route-order badge" + (suppressedBadges === 1 ? "" : "s") + " omitted to avoid overlap in this dense view. Full #1 \u2192 #2 chains remain in List view or by selecting a single loop."));
        var fbBtn = el("button", "linkish", "Open List view");
        fbBtn.type = "button";
        fbBtn.dataset.focus = "map-fallback-list";
        fbBtn.addEventListener("click", function () { state.mapMode = "list"; render(false, true); });
        fbEl.appendChild(fbBtn);
      } else {
        fbEl.hidden = true;
      }
    }
  }
  function vEdgesCount(topo, lKey) {
    var hit = topo.loops.filter(function (v) { return v.loop.key === lKey; })[0];
    return hit ? hit.edges.length : 0;
  }

  function setupMapPanel(d, cats) {
    var topo = buildSetupTopology(d, cats);
    var sec = el("section", "panel setup-map-panel");
    sec.id = "setup-map";
    sec.dataset.mapMode = state.mapMode;
    sec.setAttribute("aria-labelledby", "setup-map-title");

    var head = el("div", "setup-map-head");
    var headLeft = el("div");
    var h2El = el("h2", null, "Setup map");
    h2El.id = "setup-map-title";
    headLeft.appendChild(h2El);
    headLeft.appendChild(el("p", "sub setup-map-sub", topo.sel ? "Tracing full configured chain and account bindings for the selected item. Press Esc to clear." : "Loops, direct lanes, configured routes, and selected accounts. Only current routes are connected until you select an item."));
    head.appendChild(headLeft);

    var modeSwitch = el("div", "view-switch");
    modeSwitch.setAttribute("role", "group");
    modeSwitch.setAttribute("aria-label", "Setup layout mode");
    var mapBtn = el("button", null, "Map");
    mapBtn.type = "button"; mapBtn.dataset.focus = "mode-map"; mapBtn.setAttribute("aria-pressed", String(state.mapMode !== "list"));
    mapBtn.addEventListener("click", function () { state.mapMode = "map"; render(false, true); });
    var listBtn = el("button", null, "List");
    listBtn.type = "button"; listBtn.dataset.focus = "mode-list"; listBtn.setAttribute("aria-pressed", String(state.mapMode === "list"));
    listBtn.addEventListener("click", function () { state.mapMode = "list"; render(false, true); });
    modeSwitch.appendChild(mapBtn); modeSwitch.appendChild(listBtn);
    head.appendChild(modeSwitch);
    sec.appendChild(head);

    var toolbar = el("div", "map-toolbar");
    var filters = el("search", "map-filters");
    var searchIn = document.createElement("input");
    searchIn.type = "search"; searchIn.className = "map-search"; searchIn.dataset.focus = "map-search";
    searchIn.placeholder = "Filter loops, routes, accounts\u2026";
    searchIn.setAttribute("aria-label", "Filter setup map");
    searchIn.value = state.mapQuery;
    searchIn.addEventListener("input", function () { state.mapQuery = searchIn.value; render(false, true); });
    filters.appendChild(searchIn);

    [
      ["all", "All active (" + cats.activeLoops.length + ")"],
      ["decisions", "Decisions & bounds (" + cats.decisions.length + ")"],
      ["at_limit", "At limit (" + cats.atLimit.length + ")"],
      ["checks", "Checks stopped (" + cats.checks.length + ")"]
    ].forEach(function (pair) {
      var chip = el("button", "chip-btn", pair[1]);
      chip.type = "button"; chip.dataset.focus = "filter-" + pair[0];
      chip.setAttribute("aria-pressed", String(state.mapFilter === pair[0]));
      chip.addEventListener("click", function () { state.mapFilter = pair[0]; render(false, true); });
      filters.appendChild(chip);
    });
    if (cats.retiredLoops.length) {
      var retChip = el("button", "chip-btn", "Show retired (" + cats.retiredLoops.length + ")");
      retChip.type = "button"; retChip.dataset.focus = "filter-retired";
      retChip.setAttribute("aria-pressed", String(state.mapShowRetired));
      retChip.addEventListener("click", function () { state.mapShowRetired = !state.mapShowRetired; render(false, true); });
      filters.appendChild(retChip);
    }
    toolbar.appendChild(filters);
    sec.appendChild(toolbar);

    sec.appendChild(renderPathInspector(topo));
    var badgeFallback = el("div", "map-badge-fallback");
    badgeFallback.id = "map-badge-fallback";
    badgeFallback.setAttribute("role", "status");
    badgeFallback.hidden = true;
    sec.appendChild(badgeFallback);

    var canvas = el("div", "graph-canvas");
    canvas.id = "graph-canvas";
    var svgEl = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svgEl.setAttribute("class", "graph-svg");
    svgEl.setAttribute("id", "graph-svg");
    svgEl.setAttribute("aria-hidden", "true");
    canvas.appendChild(svgEl);

    var cols = el("div", "graph-cols");
    function mkCol(idStr, headingTxt, countTxt) {
      var cDiv = el("div", "graph-col");
      cDiv.id = idStr;
      var ch = el("div", "col-head");
      ch.appendChild(el("h3", null, headingTxt));
      ch.appendChild(el("span", "col-count", countTxt));
      cDiv.appendChild(ch);
      return cDiv;
    }
    var colLoops = mkCol("col-loops", "Loops & direct lanes", topo.loops.length + (cats.retiredLoops.length ? " \u00B7 " + cats.retiredLoops.length + " retired" : ""));
    var colRoutes = mkCol("col-routes", "Configured routes", topo.routes.length + " shown");
    var colAccts = mkCol("col-accounts", "Selected accounts & unbound", topo.accounts.length + " items");

    function selectCard(kindStr, idStr) {
      if (state.mapSelected && state.mapSelected.kind === kindStr && state.mapSelected.id === idStr) state.mapSelected = null;
      else state.mapSelected = { kind: kindStr, id: idStr };
      render(false, true);
    }

    if (!topo.loops.length) {
      colLoops.appendChild(el("p", "sub", "No loops match this filter."));
    }
    topo.loops.forEach(function (vItem) {
      var loopObj = vItem.loop;
      var isSel = topo.sel && topo.sel.kind === "loop" && topo.sel.id === loopObj.key;
      var isRel = !!topo.selLoops[loopObj.key];
      var lTone = loopObj.alsoWaiting ? "attn" : (TONES[loopObj.state] || "unknown");
      var cardBtn = el("button", "map-card" + (isSel ? " is-selected" : topo.sel ? (isRel ? " is-related" : " is-dimmed") : "") + (lTone === "attn" ? " is-attn" : "") + (isLoopRetired(loopObj) ? " is-retired" : ""));
      cardBtn.type = "button";
      cardBtn.dataset.cardId = "loop-" + loopObj.key;
      cardBtn.dataset.focus = "map-loop-" + loopObj.key;
      cardBtn.setAttribute("aria-pressed", String(!!isSel));
      cardBtn.addEventListener("click", function () { selectCard("loop", loopObj.key); });

      var cTop = el("div", "map-card-top");
      var cTit = el("span", "map-card-title");
      cTit.appendChild(el("span", "dot dot-" + lTone));
      cTit.appendChild(document.createTextNode(loopObj.name));
      cTop.appendChild(cTit);
      cTop.appendChild(uiBadge(lTone === "unknown" ? "plain" : lTone, loopObj.phrase));
      cardBtn.appendChild(cTop);

      var m1 = [];
      if (loopObj.device && loopObj.device.text) m1.push(loopObj.device.text);
      if (vItem.need && vItem.need.waited) m1.push(vItem.need.waited);
      else if (loopObj.lastCheck && loopObj.lastCheck.text) m1.push(loopObj.lastCheck.text);
      if (m1.length) cardBtn.appendChild(el("div", "map-card-meta", m1.join(" \u00B7 ")));
      if (loopObj.lanes && loopObj.lanes.summary) cardBtn.appendChild(el("div", "map-card-meta", "Lanes: " + loopObj.lanes.summary));
      if (isLoopSuperseded(loopObj)) cardBtn.appendChild(el("div", "map-card-meta", "Superseded \u2014 do not resume"));
      else if (loopObj.lineage && loopObj.lineage.from) cardBtn.appendChild(el("div", "map-card-meta", "Continues earlier loop (" + loopObj.lineage.count + " prior)"));
      if (loopObj.state === "recovering" || loopObj.recovering) cardBtn.appendChild(el("div", "map-card-meta", "Recovering"));
      colLoops.appendChild(cardBtn);
    });

    topo.routes.forEach(function (rRec) {
      var isSel = topo.sel && topo.sel.kind === "route" && topo.sel.id === rRec.key;
      var isRel = !!topo.selRoutes[rRec.key];
      var rTone = rRec.attention === "limit" ? "limit" : rRec.attention === "not_ready" ? "warn" : rRec.inUseCount > 0 ? "good" : "calm";
      var cardBtn = el("button", "map-card" + (isSel ? " is-selected" : topo.sel ? (isRel ? " is-related" : " is-dimmed") : ""));
      cardBtn.type = "button";
      cardBtn.dataset.cardId = "route-" + rRec.key;
      cardBtn.dataset.focus = "map-route-" + rRec.key;
      cardBtn.setAttribute("aria-pressed", String(!!isSel));
      cardBtn.addEventListener("click", function () { selectCard("route", rRec.key); });

      var cTop = el("div", "map-card-top");
      var cTit = el("span", "map-card-title");
      cTit.appendChild(el("span", "dot dot-" + rTone));
      cTit.appendChild(document.createTextNode(rRec.title));
      cTop.appendChild(cTit);
      if (rRec.bindingKind === "not_verified") cTop.appendChild(uiBadge("plain", "BB unverified"));
      else if (rRec.bindingKind === "none") cTop.appendChild(uiBadge("plain", "Unassigned"));
      else if (rRec.bindingKind === "mismatch") cTop.appendChild(uiBadge("attn", "Check mismatch"));
      cardBtn.appendChild(cTop);

      if (rRec.meta.length) cardBtn.appendChild(el("div", "map-card-meta", rRec.meta.slice(0, 3).join(" \u00B7 ")));
      var rStatusTxt = (rRec.inUseCount ? rRec.inUseCount + " active" : "0 active") + (rRec.standbyCount ? " \u00B7 " + rRec.standbyCount + " standby" : "");
      if (rRec.phrase) rStatusTxt += " \u00B7 " + rRec.phrase;
      cardBtn.appendChild(el("div", "map-card-meta", rStatusTxt));
      colRoutes.appendChild(cardBtn);
    });

    topo.accounts.forEach(function (ac) {
      var isSel = topo.sel && topo.sel.kind === "acct" && topo.sel.id === ac.id;
      var isRel = !!topo.selAccts[ac.id];
      var cardData = ac.card;
      var isDrift = !!(cardData && (cardData.attention === "context_changed" || cardData.reason === "context_changed"));
      var acTone = ac.unbound ? "calm" : isDrift ? "attn" : "good";
      var cardBtn = el("button", "map-card" + (ac.unbound ? " is-unbound" : "") + (isSel ? " is-selected" : topo.sel ? (isRel ? " is-related" : " is-dimmed") : "") + (isDrift ? " is-attn" : ""));
      cardBtn.type = "button";
      cardBtn.dataset.cardId = "acct-" + ac.id;
      cardBtn.dataset.focus = "map-acct-" + ac.id;
      cardBtn.setAttribute("aria-pressed", String(!!isSel));
      cardBtn.addEventListener("click", function () { selectCard("acct", ac.id); });

      var cTop = el("div", "map-card-top");
      var cTit = el("span", "map-card-title");
      cTit.appendChild(el("span", "dot dot-" + acTone));
      cTit.appendChild(document.createTextNode(ac.label));
      cTop.appendChild(cTit);
      cTop.appendChild(uiBadge(ac.unbound ? "plain" : isDrift ? "attn" : "good", ac.provider || "Account"));
      cardBtn.appendChild(cTop);

      if (ac.unbound) {
        cardBtn.appendChild(el("div", "map-card-meta", "No account assigned \u2014 separate routes do not prove separate subscriptions."));
      } else if (cardData) {
        var qs = accountQuotaSummary(cardData);
        var uUpd = cardData.usageUpdating;
        if (isDrift) {
          cardBtn.appendChild(el("div", "map-card-meta", "Context review needed \u2014 not a quota limit. Prior quota withheld."));
        } else if (accountQuotaMalformedProvenance(cardData)) {
          cardBtn.appendChild(el("div", "map-card-meta", "Quota rejected \u00B7 invalid provenance metadata"));
        } else if (qs && qs.state === "reported") {
          var rowsQ = list(qs.rows), nowMs = readTime();
          var allEnded = rowsQ.length > 0 && rowsQ.every(function (w) { return accountWindowEnded(w.resetsAt, w.ended, nowMs); });
          var firstW = rowsQ.filter(function (w) { return typeof w.usedPercent === "number"; })[0];
          var qsAtMs = Date.parse(accountQuotaRetrievedAt(qs));
          var isOlder = qs.freshness === "stale" || !isFinite(qsAtMs) || qsAtMs > nowMs || (nowMs - qsAtMs) > 1800000;
          var freshStr = (qs.provenance || qs.retrievedAt)
            ? (isOlder ? "Retrieved (older \u00B7 provider freshness unknown)" : "Retrieved (provider freshness unknown)")
            : (isOlder ? "Older local read (provider freshness unknown)" : "Local read (provider freshness unknown)");
          cardBtn.appendChild(el("div", "map-card-meta", allEnded ? freshStr + " \u00B7 Window ended \u00B7 current unknown" : freshStr + (firstW ? " \u00B7 " + firstW.usedPercent + "% used" : "")));
          if (!allEnded && firstW) {
            var mDiv = el("div", "map-mini-meter" + (firstW.usedPercent >= 100 ? " is-limit" : ""));
            var mBar = el("i");
            mBar.style.width = Math.min(100, Math.max(2, firstW.usedPercent)) + "%";
            mDiv.appendChild(mBar);
            cardBtn.appendChild(mDiv);
          }
        } else if (cardData.quota && cardData.quota.summary && cardData.quota.summary.state === "reported") {
          cardBtn.appendChild(el("div", "map-card-meta", "Quota reading unavailable"));
        } else {
          cardBtn.appendChild(el("div", "map-card-meta", "No saved quota reading"));
        }
        cardBtn.appendChild(el("div", "map-card-meta", (uUpd && uUpd.text) || "Automatic updates: not reported"));
      } else {
        var useSub = ac.uses && list(ac.uses.reading).length ? say(ac.uses.reading) : "Selected account (snapshot loading or read-only summary)";
        cardBtn.appendChild(el("div", "map-card-meta", useSub));
      }
      colAccts.appendChild(cardBtn);
    });

    cols.appendChild(colLoops); cols.appendChild(colRoutes); cols.appendChild(colAccts);
    canvas.appendChild(cols);
    sec.appendChild(canvas);
    sec.appendChild(setupListFallback(topo));

    var leg = el("details", "map-legend-fold");
    leg.appendChild(el("summary", null, "Connection legend (orthogonal ports & edge meanings)"));
    var legRow = el("div", "map-legend-row");
    [
      ["edge-inuse", "Current in use"],
      ["edge-current", "Current (held / stale)"],
      ["edge-fallback", "Configured fallback (on select)"],
      ["edge-limit", "Left on usage limit"],
      ["edge-bound", "Assigned account (by you)"],
      ["edge-unbound", "Unbound (account unknown)"]
    ].forEach(function (pair) {
      var itemSpan = el("span", "legend-item");
      var swSvg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      swSvg.setAttribute("class", "legend-swatch"); swSvg.setAttribute("viewBox", "0 0 24 12"); swSvg.setAttribute("aria-hidden", "true");
      var swLine = document.createElementNS("http://www.w3.org/2000/svg", "line");
      swLine.setAttribute("x1", "1"); swLine.setAttribute("y1", "6"); swLine.setAttribute("x2", "23"); swLine.setAttribute("y2", "6");
      swLine.setAttribute("class", pair[0]);
      swSvg.appendChild(swLine);
      itemSpan.appendChild(swSvg);
      itemSpan.appendChild(document.createTextNode(pair[1]));
      legRow.appendChild(itemSpan);
    });
    leg.appendChild(legRow);
    sec.appendChild(leg);

    var drawSoon = function () { drawSetupEdges(topo, canvas, svgEl); };
    if (typeof window.requestAnimationFrame === "function") window.requestAnimationFrame(drawSoon);
    else drawSoon();
    if (typeof ResizeObserver === "function") {
      state.mapObserver = new ResizeObserver(function () { drawSetupEdges(topo, canvas, svgEl); });
      state.mapObserver.observe(canvas);
    }
    return sec;
  }

  function attentionPanel(d, cats) {
    var sec = el("section", "panel attn-section");
    sec.id = "attention-section";
    sec.setAttribute("aria-labelledby", "attn-section-title");
    var hd = el("h2", null, "Needs attention");
    hd.id = "attn-section-title";
    hd.appendChild(el("span", "sub", "Clears on the next complete saved read once resolved externally; failed or incomplete reads retain items as Unconfirmed."));
    sec.appendChild(hd);

    if (state.issueStaleNotice) {
      var bnr = el("div", "issue-stale-banner");
      bnr.setAttribute("role", "status");
      bnr.appendChild(el("span", null, state.issueStaleNotice));
      bnr.appendChild(button("Dismiss", "dismiss-stale-notice", "", function () { state.issueStaleNotice = ""; render(false, true); }));
      sec.appendChild(bnr);
    }
    var bgBanner = supersededOutcomeBanner(d);
    if (bgBanner) sec.appendChild(bgBanner);

    var loopLookup = Object.create(null);
    if (state.retainedLoops) Object.keys(state.retainedLoops).forEach(function (k) { loopLookup[k] = state.retainedLoops[k]; });
    list(d.loops).forEach(function (loopObj) { loopLookup[loopObj.key] = loopObj; });
    var hasAny = cats.decisions.length || cats.checks.length || cats.unreadable.length || cats.driftCards.length;
    if (!hasAny) {
      if (!d.allClear) {
        sec.appendChild(emptyBox("Not every loop was read.", "So this cannot confirm every loop is clear. It reads again shortly."));
      } else {
        var clearP = el("p", "clear");
        clearP.appendChild(icon("check"));
        clearP.appendChild(document.createTextNode("Nothing needs you: every registered loop was read and none is held."));
        sec.appendChild(clearP);
      }
      return sec;
    }

    function addGroup(titleStr, items, clsOverride) {
      if (!items.length) return;
      var boxDiv = el("div");
      boxDiv.appendChild(el("h3", "attn-group-title", titleStr));
      var ul = el("ul", "attn-grid need-list");
      items.forEach(function (nItem) { ul.appendChild(taskCard(nItem, true, loopLookup[nItem.key] || null)); });
      boxDiv.appendChild(ul);
      sec.appendChild(boxDiv);
    }
    addGroup("Decisions & bounds waiting for you (" + cats.decisions.length + ")", cats.decisions);
    addGroup("Checks stopped \u2014 supervision stale (" + cats.checks.length + ", not a person hold)", cats.checks);
    addGroup("Records unreadable (" + cats.unreadable.length + ")", cats.unreadable);

    if (cats.driftCards.length) {
      var drDiv = el("div");
      drDiv.appendChild(el("h3", "attn-group-title", "Account context review needed (" + cats.driftCards.length + ", not a quota limit)"));
      var drList = el("ul", "attn-grid");
      cats.driftCards.forEach(function (cItem) {
        var li = el("li", "need task-card is-person");
        var topA = el("div", "task-top");
        topA.appendChild(link("#/accounts/" + cItem.id, cItem.label, "task-name"));
        topA.appendChild(uiBadge("attn", "Context review needed \u2014 not a quota limit"));
        li.appendChild(topA);
        li.appendChild(el("p", "reason", "Selection saved under a different shell, context, or CLI path. Prior quota stays withheld until reviewed."));
        var acts = el("div", "task-actions");
        if (changesOn() && state.server && state.server.accountSelectionReview) {
          acts.appendChild(button("Review selection\u2026", "drift-review-" + cItem.id, "is-primary", function () {
            window.location.hash = "#/accounts/" + cItem.id;
            accountQuotaRequest(cItem, false, true);
          }));
        } else {
          acts.appendChild(el("span", "sub", "Requires panel with --allow-changes to review selection."));
        }
        acts.appendChild(link("#/accounts/" + cItem.id, "Open account \u2192", "overview-jump"));
        li.appendChild(acts);
        drList.appendChild(li);
      });
      drDiv.appendChild(drList);
      sec.appendChild(drDiv);
    }
    return sec;
  }

  function renderHome(d, pane) {
    pane.appendChild(paneHead("Overview"));
    if (d.registry.availability !== "available") {
      var groupBad = groupBox(d);
      if (groupBad) pane.appendChild(groupBad);
      var routerBad = routerBox(d.router);
      if (routerBad) pane.appendChild(routerBad);
      pane.appendChild(el("p", "lede", "Unknown."));
      pane.appendChild(emptyBox("The registry could not be read.", "Nothing is shown rather than guessing. Check it with looptender registry list."));
      return;
    }
    if (d.lede) pane.appendChild(el("p", "lede", d.lede));
    var cats = categorizeNeeds(d);
    if (!list(d.loops).length && !cats.all.length) {
      var gEmpty = groupBox(d);
      if (gEmpty) pane.appendChild(gEmpty);
      var rEmpty = routerBox(d.router);
      if (rEmpty) pane.appendChild(rEmpty);
      pane.appendChild(noticeArea());
      pane.appendChild(emptyBox("No loops are registered yet.", "Register one with looptender registry add -c path/to/looptender.yaml (or keepgoing.yaml), and it appears here on the next read."));
      return;
    }
    pane.appendChild(overviewHealthBar(d, cats));
    pane.appendChild(setupMapPanel(d, cats));
    pane.appendChild(attentionPanel(d, cats));
    var group = groupBox(d);
    if (group) pane.appendChild(group);
    var router = routerBox(d.router);
    if (router) pane.appendChild(router);
    pane.appendChild(noticeArea());
    pane.appendChild(workTree(d));
    pane.appendChild(recentAcrossLoops(d));
  }

  // The existing feed verifies direct lanes at a check, not an entire live
  // descendant tree. Keep each registered loop visible independently, even
  // when a lane links to it: its own control state may differ from the
  // parent's observation. No relationship is inferred from names or order.
  function workBranch(l, d, retainedOpen, folded) {
    var quiet = folded || l.inactive || l.state === "archived" || (l.state === "paused" && !l.alsoWaiting) || l.state === "stopped" || l.state === "finished" || l.state === "off";
    var inlineActive = folded && state.change && state.change.key === l.key;
    var li = el("li", "work-branch" + (quiet ? " is-quiet" : "")), details = el("details");
    details.dataset.loop = l.key;
    details.open = inlineActive || (Object.prototype.hasOwnProperty.call(state.treeOpen, l.key) ? state.treeOpen[l.key] : (!folded && !!needFor(l.key)));
    retainedOpen[l.key] = details.open;
    var summary = el("summary", "work-summary");
    summary.setAttribute("data-focus", "tree-" + l.key);
    var name = el("span", "work-name", l.name);
    // The glossary's role: its last verified listing has lanes.
    if (l.lanes && l.lanes.role === "coordinator") name.appendChild(el("span", "work-role", "coordinator"));
    if (l.provider) { var chip = el("span", "work-role", l.provider + (l.model ? " \u00B7 " + l.model : "")); chip.title = "Configured provider and model"; name.appendChild(chip); }
    if (l.observed) name.appendChild(el("span", "work-role work-observed", observedLabel(l)));
    if (l.inactive) name.appendChild(el("span", "work-role work-inactive", "inactive 3d+"));
    if (isLoopSuperseded(l)) name.appendChild(el("span", "work-role", "superseded"));
    if (l.lineage && l.lineage.from) name.appendChild(el("span", "work-role", "continues prior loop"));
    if (l.state === "recovering" || l.recovering) name.appendChild(el("span", "work-role", "recovering"));
    summary.appendChild(name);
    var status = el("span", "work-status");
    var tone = l.inactive ? "calm" : (TONES[l.state] || "unknown");
    if (l.alsoWaiting && !l.inactive) tone = "attn";
    if (l.lanesFailed && tone === "good") tone = "warn";
    status.appendChild(el("span", "dot dot-" + tone));
    status.appendChild(el("span", null, l.phrase));
    var silent = supervisorAge(l);
    if (silent) status.appendChild(el("span", "sub work-silent", silent));
    var freshChip = freshnessChip(l);
    if (freshChip) status.appendChild(freshChip);
    if (l.state === "finished") status.appendChild(tag("calm", "Unverified"));
    if (l.lanesFailed) status.appendChild(tag("needs", l.lanesFailed));
    var rowWaits = waitsLine(l.waiting, d.readAt, true);
    if (rowWaits) { rowWaits.classList.add("sub", "work-waits"); status.appendChild(rowWaits); }
    summary.appendChild(status);
    if (l.lanes && l.lanes.summary) summary.appendChild(el("span", "sub work-observation", "Lanes: " + l.lanes.summary));
    // Age in plain words, as for usage readings; see lastCheck().
    summary.appendChild(el("span", "sub work-age", (l.lastCheck && l.lastCheck.text) || "Last check unknown"));
    var mr = l.migrationReview;
    if (mr && mr.availability === "available" && mr.counts && mr.counts.unfinished > 0) {
      summary.appendChild(el("span", "sub work-migration", "Migration review: " + mr.text));
    }
    details.appendChild(summary);
    var body = el("div", "work-body");
    body.appendChild(el("p", "detail", l.detail));
    if (l.observed && l.observedNote) body.appendChild(el("p", "sub observed-note", l.observedNote));
    if (l.sessionpipeWake && l.sessionpipeWake.text) body.appendChild(el("p", "sub sessionpipe-wake", "Wake: " + l.sessionpipeWake.text));
    var facts = el("dl", "work-facts");
    facts.appendChild(fact("Last decision", l.decision ? say(l.decision.phrase) : "Unknown"));
    facts.appendChild(fact("Next check", nextCheck(l.nextCheck || { availability: "unknown" })));
    if (l.checkedBy && l.checkedBy.text) facts.appendChild(fact("Checked", l.checkedBy.text));
    body.appendChild(facts);
    body.appendChild(laneBox(l, true));
    var more = body.querySelector("details.lane-more"), moreKey = l.key + "/lanes";
    if (more) {
      more.open = !!state.treeOpen[moreKey];
      retainedOpen[moreKey] = more.open;
      more.addEventListener("toggle", function () {
        if (more.isConnected) state.treeOpen[moreKey] = more.open;
      });
    }
    var foot = el("div", "work-quick-actions");
    foot.appendChild(link("#/loop/" + l.key, "Inspect " + l.name, "work-inspect"));
    if (folded && changesOn()) {
      var can = list(l.can);
      ["unarchive", "archive", "resume"].forEach(function (act) {
        if (can.indexOf(act) >= 0) {
          var label = l.observed && OBSERVED_ACTION_LABEL[act] ? OBSERVED_ACTION_LABEL[act] : ACTION_LABEL[act];
          if (label) foot.appendChild(button(label + "\u2026", "act-" + act, "", function () { prepareChange(l.key, act); }));
        }
      });
    }
    body.appendChild(foot);
    if (inlineActive) {
      var inlineChange = changeBox(l, false, true);
      if (inlineChange) body.appendChild(inlineChange);
    }
    details.appendChild(body);
    details.addEventListener("toggle", function () {
      if (details.isConnected) state.treeOpen[l.key] = details.open;
    });
    li.appendChild(details);
    return li;
  }

  function workTree(d) {
    var p = panel("Loops");
    var ul = el("ul", "work-tree");
    var retainedOpen = Object.create(null);
    var active = [], folded = [], inactiveCount = 0, archivedCount = 0;
    list(d.loops).forEach(function (l) {
      if (l.state === "archived") { archivedCount++; folded.push(l); }
      else if (l.inactive) { inactiveCount++; folded.push(l); }
      else { active.push(l); }
    });
    active.forEach(function (l) { ul.appendChild(workBranch(l, d, retainedOpen, false)); });
    if (folded.length) {
      var fold = el("details", "work-older");
      fold.open = !!state.treeOpen["older-folded"] || folded.some(function (x) { return state.change && state.change.key === x.key; });
      retainedOpen["older-folded"] = fold.open;
      var parts = [];
      if (inactiveCount) parts.push("Inactive 3d+ (" + inactiveCount + ")");
      if (archivedCount) parts.push((inactiveCount ? "archived" : "Archived") + " (" + archivedCount + ")");
      fold.appendChild(el("summary", "work-older-summary", parts.join(" and ")));
      var olderList = el("ul", "work-tree work-tree-older");
      folded.forEach(function (l) { olderList.appendChild(workBranch(l, d, retainedOpen, true)); });
      fold.appendChild(olderList);
      fold.addEventListener("toggle", function () {
        if (fold.isConnected) state.treeOpen["older-folded"] = fold.open;
      });
      p.appendChild(ul);
      if (!active.length) p.appendChild(el("p", "sub work-older-empty", "No active loops right now. Expand older or archived loops below if needed."));
      p.appendChild(fold);
    } else {
      p.appendChild(ul);
    }
    state.treeOpen = retainedOpen;
    if (!d.complete || d.registry.omitted) p.appendChild(el("p", "more", "This read does not include every registered loop. Missing loops are not assumed stopped or clear."));
    return p;
  }

  // Merge only the bounded, already-projected events. This is a recent
  // sample, not a complete chronology or a persisted "while away" cursor.
  function recentAcrossLoops(d) {
    var p = panel("Recent activity");
    var intro = el("p", "sub", "The newest few events from each loop.");
    p.appendChild(intro);
    var rows = [], missing = false, pruned = false;
    list(d.loops).forEach(function (l) {
      if (!l.events || l.events.availability === "unknown") missing = true;
      if (l.events && l.events.pruned) pruned = true;
      list(l.events && l.events.events).forEach(function (e) {
        var at = Date.parse(e.at);
        if (!isNaN(at)) rows.push({ loop: l, event: e, at: at });
      });
    });
    rows.sort(function (a, b) { return b.at - a.at || a.loop.key.localeCompare(b.loop.key); });
    var ol = el("ol", "events overview-events");
    rows.slice(0, 8).forEach(function (r) {
      var x = r.event, li = el("li", "event is-" + x.tone);
      var t = el("time", null, say(x.when));
      t.dateTime = x.at;
      li.appendChild(t);
      li.appendChild(el("span", "mark"));
      var body = el("div");
      body.appendChild(link("#/loop/" + r.loop.key, r.loop.name));
      body.appendChild(el("div", null, x.phrase));
      if (x.detail) body.appendChild(el("span", "sub", x.detail));
      if (list(x.foldedText).length) body.appendChild(el("span", "sub", say(x.foldedText)));
      li.appendChild(body);
      ol.appendChild(li);
    });
    if (rows.length) p.appendChild(ol);
    else p.appendChild(el("p", "sub", missing ? "Recent activity is unavailable for one or more loops." : "No recent activity is recorded in this read."));
    if (rows.length > 8) p.appendChild(el("p", "more", "Showing the newest 8 of " + rows.length + ". Open a loop for more."));
    if (pruned) intro.textContent = "The newest few events from each loop; older ones are no longer kept.";
    if ((rows.length && missing) || !d.complete || d.registry.omitted) p.appendChild(el("p", "more", "Some loops or their history could not be read, so this list is missing events."));
    return p;
  }

  // Last decision, hold and route: short answers under the loop's name.
  function nowRow(l) {
    var dl = el("dl", "now");
    function card(label, value, sub) {
      var d = el("div");
      d.appendChild(el("dt", null, label));
      var dd = el("dd");
      if (typeof value === "string") dd.textContent = value; else dd.appendChild(value);
      if (sub) dd.appendChild(el("span", "sub", sub));
      d.appendChild(dd);
      dl.appendChild(d);
    }
    var dec = l.decision;
    if (dec.availability !== "known") card("Last decision", unknown(dec.phrase));
    else card("Last decision", dec.phrase, say(dec.sub));
    var h = l.hold;
    if (h.state === "unknown") card("Hold", unknown());
    else if (h.state === "none") card("Hold", "None");
    else card("Hold", h.phrase, h.sub);
    if (l.away) card("Route", say(l.away.short), say(l.away.why));
    // The no-progress flag informs; it is never a need.
    if (l.noProgress && l.noProgress.availability === "flagged") card("Progress", "No progress", l.noProgress.text.replace(/^No progress: /, ""));
    return dl;
  }

  // Each lane of the last verified listing: its state, then what it is (its
  // title when one is recorded, otherwise its number).
  function laneBox(l, overview) {
    var c = l.children, lanes = l.lanes, rows = list(lanes && lanes.lanes);
    var b = el("div", "lanes is-wide");
    b.appendChild(el("span", "lanes-label", (lanes && lanes.heading) || "This loop's lanes"));
    if (!rows.length) {
      // An unknown answer carries its reason in the phrase: "Unknown: …".
      b.appendChild(!c || !c.phrase ? unknown() : c.availability === "unknown" ? unknown(c.phrase) : el("span", "sub", c.phrase));
      return b;
    }
    function row(x) {
      var li = el("li", "lane");
      li.appendChild(el("span", "dot " + (x.state === "working" ? "dot-good" : x.state === "waiting" ? "dot-attn" : x.state === "failed" ? "dot-warn" : "dot-unknown")));
      li.appendChild(el("span", "lane-state", x.phrase));
      var what = el("span", "lane-what");
      var laneTitle = state.identifiers ? x.title : "";
      what.appendChild(el("span", "lane-title", laneTitle || "Lane " + x.number));
      // A recorded title is the person's own text: shown with identifiers only.
      if (!laneTitle && (x.titleEvidence === "withheld" || x.titleEvidence === "recorded" || (!state.identifiers && x.title))) what.appendChild(el("span", "sub", " \u00B7 title withheld"));
      if (x.loop) {
        what.appendChild(document.createTextNode(" · also a registered loop: "));
        what.appendChild(link("#/loop/" + x.loopKey, x.loop));
      }
      if (x.key && state.identifiers) {
        what.appendChild(document.createTextNode(" · "));
        what.appendChild(el("code", null, x.key));
      }
      li.appendChild(what);
      return li;
    }
    function rowList(xs) {
      var ul = el("ul", "lane-list");
      xs.forEach(function (x) { ul.appendChild(row(x)); });
      return ul;
    }
    // On the overview, a long listing shows the lanes waiting for an answer
    // and folds the rest into one details element.
    var shown = rows, rest = [];
    if (overview && rows.length > 8) {
      shown = rows.filter(function (x) { return x.state === "waiting"; });
      rest = rows.filter(function (x) { return x.state !== "waiting"; });
    }
    if (shown.length) b.appendChild(rowList(shown));
    if (rest.length) {
      var fold = el("details", "lane-more");
      fold.appendChild(el("summary", null, (shown.length ? "Show the other " + rest.length + " of " : "Show all ") + rows.length + (lanes.omitted ? " listed" : "") + " lanes"));
      fold.appendChild(rowList(rest));
      b.appendChild(fold);
    }
    if (overview) {
      if (lanes.omitted) b.appendChild(el("p", "more", lanes.omitted + " more lanes are not listed."));
      b.appendChild(el("p", "more", "Lane titles appear only with identifiers. Routes for individual lanes are not recorded."));
    } else if (lanes && lanes.footer) b.appendChild(el("p", "more", lanes.footer));
    return b;
  }

  // The newest retained history entries, newest first.
  function eventsPanel(ev) {
    var p = panel("Recent activity", [el("span", "sub", "from the retained history; newest first")]);
    if (ev.availability === "unknown") { p.appendChild(unknown("Unknown: the history could not be read.")); return p; }
    var events = list(ev.events);
    if (!events.length) { p.appendChild(el("p", "sub chain-how", "Nothing is recorded yet.")); return p; }
    var ol = el("ol", "events");
    events.forEach(function (x) {
      var li = el("li", "event is-" + x.tone);
      var t = el("time", null, say(x.when));
      t.dateTime = x.at;
      li.appendChild(t);
      li.appendChild(el("span", "mark"));
      var body = el("div", null, x.phrase);
      if (x.detail) body.appendChild(el("span", "sub", x.detail));
      if (list(x.foldedText).length) body.appendChild(el("span", "sub", say(x.foldedText)));
      li.appendChild(body);
      ol.appendChild(li);
    });
    p.appendChild(ol);
    if (ev.footer) p.appendChild(el("p", "more", ev.footer));
    return p;
  }

  // ---- Level 2: one loop, shown through its routes ----
  function observedLabel(l) {
    if (!l || !l.observed) return "";
    return l.observedWakes ? "Observed, woken only when idle" : "Observed, never woken";
  }
  function renderLoop(d, pane, key) {
    pane.appendChild(back());
    var l = loopByKey(key);
    if (!l) {
      pane.appendChild(paneHead("Loop not found"));
      pane.appendChild(emptyBox("This loop is no longer registered.", "Pick another from the list."));
      return;
    }
    // A paused loop that is still waiting for a person reads as one that needs
    // a person: the tone and icon follow what is waited for, not only the word.
    var stState = l.alsoWaiting ? "waiting_for_you" : l.state;
    var st = el("span", "state tone-" + (TONES[stState] || "unknown"));
    st.appendChild(icon(STATE_ICON[stState] || "unknown"));
    st.appendChild(document.createTextNode(l.phrase));
    var pills = st;
    // A failed lane or observed target is said beside the supervision state, in words.
    if (l.lanesFailed || l.observed) {
      pills = el("span", "pane-pills");
      pills.appendChild(st);
      if (l.observed) pills.appendChild(el("span", "work-role work-observed", observedLabel(l)));
      if (l.lanesFailed) pills.appendChild(tag("needs", l.lanesFailed));
    }
    pane.appendChild(paneHead(l.name, pills));
    // The provider and model the loop now targets, from its configuration, so
    // its name is not the only cue. Nothing when an older server sends none.
    if (l.provider) pane.appendChild(el("p", "sub build-line", "Configured provider: " + l.provider + " \u00B7 " + (l.model ? "model " + l.model : "model not configured")));
    // How the loop is checked (daemon, service, ticks, nothing), whatever its
    // holds; nothing from an older server that sends none.
    if (l.checkedBy && l.checkedBy.text) pane.appendChild(el("p", "sub build-line checked-by", l.checkedBy.text));
    // The build its last check ran; "version unknown" when none is recorded.
    // An older server sends no line, and then nothing is shown.
    if (l.build && l.build.line) pane.appendChild(el("p", "sub build-line", l.build.line));
    // The supervisor's build against the installed one, with its detail and,
    // for an older daemon, the command a person would run. Nothing here acts.
    if (l.buildFreshness && l.buildFreshness.text) {
      var fresh = el("p", "sub build-line fresh-line");
      fresh.appendChild(freshnessChip(l));
      if (l.buildFreshness.detail) fresh.appendChild(document.createTextNode(" " + l.buildFreshness.detail));
      pane.appendChild(fresh);
      if (l.buildFreshness.action) pane.appendChild(el("p", "sub build-line fresh-action", l.buildFreshness.action));
      if (l.buildFreshness.command) {
        var run = el("p", "sub build-line fresh-command");
        run.appendChild(document.createTextNode("Command: "));
        run.appendChild(el("code", null, l.buildFreshness.command));
        pane.appendChild(run);
      }
    }
    var need = needFor(key);
    if (!need) pane.appendChild(el("p", "detail", l.detail));
    if (l.inactive) pane.appendChild(el("p", "sub build-line work-inactive-note", "Auto-folded from the active list after 3+ days without check activity. Archive moves it out of the way without deleting files."));
    if (l.lineage && l.lineage.from) {
      var linLine = el("p", "sub build-line");
      linLine.appendChild(document.createTextNode("Continues earlier loop (" + l.lineage.count + " prior): "));
      linLine.appendChild(link("#/loop/" + l.lineage.from, (loopByKey(l.lineage.from) && loopByKey(l.lineage.from).name) || l.lineage.from));
      pane.appendChild(linLine);
    }
    if (l.archived && l.archived.continuedIn) {
      var ci = l.archived.continuedIn;
      var ciLine = el("p", "sub build-line");
      ciLine.appendChild(document.createTextNode("Retired / archived \u2014 continued in "));
      if (ci.key) ciLine.appendChild(link("#/loop/" + ci.key, ci.loop || ci.key));
      else ciLine.appendChild(document.createTextNode(ci.loop || "successor loop"));
      ciLine.appendChild(document.createTextNode(". Do not resume this retired loop."));
      pane.appendChild(ciLine);
    } else if (isLoopSuperseded(l)) {
      pane.appendChild(el("p", "sub build-line", "Superseded \u2014 retired loop; do not resume."));
    }
    if (l.state === "recovering" || l.recovering) pane.appendChild(el("p", "sub build-line", "Recovering: scheduled checks are evaluating recovery according to policy."));
    var pageWaits = need ? null : waitsLine(l.waiting, d.readAt, true);
    if (pageWaits) { pageWaits.classList.add("sub"); pane.appendChild(el("p", "waits-line", null)).appendChild(pageWaits); }
    if (need) {
      var nb = el("div", "panel is-need");
      var nl = el("ol", "need-list");
      nl.appendChild(needCard(need, false));
      nb.appendChild(nl);
      pane.appendChild(nb);
    }
    pane.appendChild(nowRow(l));
    // A limit wait, with what status says about it (the reset falls after the
    // loop's deadline; a held loop's wait is from before the hold), shown with
    // the state and not only in the diagnostics. A passed wait is not news.
    if (l.limitWait && !l.limitWait.passed && (list(l.limitWait.note).length || l.state === "paused" || l.state === "stopped" || l.state === "waiting_for_you")) {
      var lwBox = el("p", "detail limit-wait", say(l.limitWait.text));
      if (list(l.limitWait.note).length) lwBox.appendChild(document.createTextNode(" " + say(l.limitWait.note)));
      pane.appendChild(lwBox);
    }
    // The standing wake policy, when status warns about it. Names no path.
    if (list(l.wakePolicy).length) pane.appendChild(el("p", "detail wake-policy", say(l.wakePolicy)));
    if (l.sessionpipeWake && l.sessionpipeWake.text) pane.appendChild(el("p", "detail sessionpipe-wake", "Wake: " + l.sessionpipeWake.text));
    var bgOutcome = supersededOutcomeBanner(d);
    if (bgOutcome) pane.appendChild(bgOutcome);
    var change = changeBox(l);
    if (change) pane.appendChild(change);
    if (l.migrationReview && l.migrationReview.availability === "available" && l.migrationReview.counts && l.migrationReview.counts.unfinished > 0) {
      pane.appendChild(migrationReviewPanel(l.migrationReview));
    }
    if (l.assignments && l.assignments.availability === "available" && l.assignments.counts && (l.assignments.counts.unfinished > 0 || l.assignments.counts.unknown > 0 || l.assignments.counts.unacknowledged > 0 || l.assignments.counts.personBlocked > 0)) {
      pane.appendChild(assignmentPanel(l.assignments));
    }

    // The ordered preferred routes are the main structure.
    var c = l.chain, entries = list(c.entries);
    var brief = !c.brief ? null : c.availability !== "none_configured" && c.brief.indexOf("unknown") >= 0 ? unknown(c.brief) :
      el("span", c.availability === "none_configured" ? "sub" : "chain-ready" + (c.brief.indexOf(CURRENT_UNAFFECTED) >= 0 ? " is-calm" : c.short ? " is-short" : ""), c.brief);
    var routing = panel("Preferred routes", [brief]);
    routing.appendChild(el("p", "sub chain-how", c.how || "Unknown."));
    if (l.observed && l.observedNote) routing.appendChild(el("p", "sub observed-note", l.observedNote));
    var ol = el("ol", "route-list is-ordered");
    entries.forEach(function (e) {
      var bound = boundAccount(e.catalogAccount);
      var item = routeItem({
        route: e.route, position: e.position, badge: String(e.position + 1), title: e.title, titleAccount: bound || e.titleAccount, active: e.inUse, use: e.use, off: e.disabled,
        readiness: e.position > 0 && e.readiness !== "current" ? e.readiness : "", phrase: e.phrase, notes: notesOf(e.notes),
        after: list(e.after),
        meta: e.meta, named: e.named, usage: e.usage, boxes: e.current ? [laneBox(l)] : [], compact: true
      });
      item.setAttribute("aria-label", "Choice " + (e.position + 1) + ": " + e.title + (e.use ? ", " + e.use : ""));
      ol.appendChild(item);
    });
    routing.appendChild(ol);
    if (c.more) routing.appendChild(el("p", "sub", c.more));
    if (!entries.some(function (e) { return e.current; })) routing.appendChild(laneBox(l));
    // Per-loop preferred routes controls/editor, plus collapsed advanced Terminal commands.
    if (c.availability !== "unknown") {
      routing.appendChild(loopRouteFlowBox(l));
      var adv = el("details", "how-advanced");
      adv.open = !!state.advRoutesOpen[l.key];
      adv.appendChild(el("summary", "how-advanced-summary", "Terminal commands (advanced)"));
      adv.addEventListener("toggle", function () { state.advRoutesOpen[l.key] = adv.open; });
      var how = el("div", "how-group how-advanced-body");
      how.appendChild(addRouteBox(l));
      var order = reorderBox(l);
      if (order) how.appendChild(order);
      adv.appendChild(how);
      routing.appendChild(adv);
    }
    pane.appendChild(routing);

    pane.appendChild(eventsPanel(l.events));

    var checks = el("details", "panel panel-fold");
    if (list(l.issues).length || state.identifiers) checks.open = true;
    var checksSum = el("summary", "panel-fold-summary", "Checks and diagnostics");
    checks.appendChild(checksSum);
    var checksBody = el("div", "panel-fold-body");
    var dl = el("dl", "facts"), dev = l.device || {};
    dl.appendChild(fact("Last check", lastCheck(l.lastCheck)));
    dl.appendChild(fact("Next check", nextCheck(l.nextCheck)));
    dl.appendChild(fact("Limits", limits(l.limits, l.limitWait)));
    if (l.build && l.build.text) dl.appendChild(fact("Build", l.build.availability === "unknown" || l.build.availability === "not_recorded" ? unknown(l.build.text) : el("span", null, l.build.text)));
    if (l.buildChange && l.buildChange.text) dl.appendChild(fact("Its file", el("span", l.buildChange.pending ? "c-warn" : null, l.buildChange.text)));
    if (l.noProgress) {
      var np = l.noProgress, pf = frag();
      pf.appendChild(np.availability === "not_available" ? unknown(np.text) : el("span", null, np.text));
      list(np.lines).forEach(function (s) { pf.appendChild(el("div", "sub", s)); });
      // A reached no-progress limit beside a clear flag: the loop is held, so
      // the flag is not judged and the limit is the last check's.
      var npReached = list(l.limits).some(function (x) { return x.reached && /progress/i.test(x.label || ""); });
      if (npReached && np.availability !== "flagged") pf.appendChild(el("div", "sub", "The no-progress limit is reached" + (l.hold && l.hold.state !== "none" ? "; the loop is held, so the flag is not judged now." : ".")));
      dl.appendChild(fact("Progress", pf));
    }
    if (l.refusals && (l.refusals.text || l.refusals.question)) {
      var rf = frag();
      if (l.refusals.text) rf.appendChild(el("span", null, l.refusals.text));
      if (l.refusals.question) rf.appendChild(el("div", "sub", "Question: " + l.refusals.question));
      dl.appendChild(fact("Refusals", rf));
    }
    dl.appendChild(fact("Device", (state.identifiers && dev.hostText) || dev.text || unknown()));
    checksBody.appendChild(dl);
    if (list(l.issues).length) {
      var ul = el("ul", "issues");
      ul.setAttribute("aria-label", "Missing evidence");
      l.issues.forEach(function (i) { ul.appendChild(el("li", null, i)); });
      checksBody.appendChild(ul);
    }
    if (state.identifiers && l.identifiers) {
      var ids = el("dl", "ids");
      ids.appendChild(el("dt", null, "Registration"));
      ids.appendChild(el("dd", null, l.identifiers.registrationId));
      if (l.identifiers.thread) {
        ids.appendChild(el("dt", null, "Configured BB thread"));
        ids.appendChild(el("dd", null, l.identifiers.thread));
        var open = threadCommand(l.identifiers.thread);
        if (open) {
          ids.appendChild(el("dt", null, "Open it in BB"));
          ids.appendChild(open);
        }
      }
      checksBody.appendChild(ids);
    }
    checks.appendChild(checksBody);
    pane.appendChild(checks);
    pane.appendChild(usageBrief(key));
  }

  function assignmentPanel(review) {
    var p = panel("Assignment handoffs");
    p.classList.add("assignment-review");
    p.appendChild(el("p", null, review ? review.text : "Assignment evidence unavailable. Outcomes are unknown."));
    if (review && review.availability === "available" && review.counts) {
      var c = review.counts, dl = el("dl", "facts");
      dl.appendChild(fact("Evidence read", fmt(dayTimeYearFmt, review.observedAt)));
      dl.appendChild(fact("Review recorded", fmt(dayTimeYearFmt, review.recordedAt)));
      dl.appendChild(fact("Coverage", review.coverage === "observed_direct_children" ? "Observed direct children" : "Partial — coverage unknown"));
      dl.appendChild(fact("Unknown / unacknowledged", c.unknown + " / " + c.unacknowledged));
      dl.appendChild(fact("Unfinished assignments", String(c.unfinished)));
      dl.appendChild(fact("Person-blocked / dependency-waiting", c.personBlocked + " / " + c.dependencyWaiting));
      p.appendChild(dl);
    }
    p.appendChild(el("p", "sub", "Dated direct-lane evidence; descendants are excluded. BB running or idle is separate from declared outcome and availability. Missing handoffs need explicit coordinator reconciliation. This view does not dispatch work, grant permission or clear holds."));
    return p;
  }

  function migrationReviewPanel(review) {
    var p = panel("Migration review");
    p.classList.add("migration-review");
    if (!review) {
      p.appendChild(el("p", null, "Migration review unavailable in this read. Unfinished work is unknown."));
      return p;
    }
    p.appendChild(el("p", null, review.text));
    if (review.availability === "available" && review.counts) {
      var c = review.counts, dl = el("dl", "facts");
      dl.appendChild(fact("Evidence read", fmt(dayTimeYearFmt, review.observedAt)));
      dl.appendChild(fact("Declared assignments", String(c.assignments)));
      dl.appendChild(fact("Dormant / unresolved", c.dormant + " / " + c.unresolved));
      dl.appendChild(fact("Observed direct lanes", String(c.directLanes)));
      dl.appendChild(fact("Assignments absent from listing", String(c.absentAssignments)));
      p.appendChild(dl);
      p.appendChild(el("p", "sub", "Historical checklist of predecessor direct lanes only; descendants are excluded. Declared assignment state is separate from observed activity. This does not show which lanes are working now."));
      p.appendChild(el("p", "sub", "Review metadata does not establish migration execution, completion or permission. Dormant work may be intentional. Nothing is resumed or dispatched from this view."));
    }
    return p;
  }

  // ---- Usage: what looptender did, in counted sentences ----
  // The server words every sentence from the loop's recorded figures; the
  // page only lays them out. Nothing here is a warning.
  function usageFor(key) { return state.usage ? list(state.usage.loops).filter(function (x) { return x.key === key; })[0] : null; }
  function usagePending() {
    return state.usageFailed ? "What looptender did could not be read. It is read again with the next update." : "Reading what looptender did…";
  }
  function sentences(items, keys) {
    var ul = el("ul", "usage-lines");
    list(items).forEach(function (s) {
      var li = el("li");
      // A configuration key in the sentence is set as code.
      if (keys) String(s).split(/\b([a-z]+(?:_[a-z]+)+(?:\.[a-z_]+)?)\b/).forEach(function (part, i) { li.appendChild(i % 2 ? el("code", null, part) : document.createTextNode(part)); });
      else li.textContent = s;
      ul.appendChild(li);
    });
    return ul;
  }
  function usageBrief(key) {
    var p = panel("What looptender did this week");
    p.classList.add("usage-brief");
    var x = usageFor(key);
    if (x) p.appendChild(sentences(x.brief));
    else p.appendChild(el("p", "sub", state.usage ? "Not counted yet. It is read again with the next update." : usagePending()));
    p.appendChild(link("#/usage", "Every count, on the Usage page", "work-inspect"));
    return p;
  }
  var DAY_HEADS = ["Day (UTC)", "Lowest", "Highest", "Last", "Window", "Source"];
  function daysTable(x) {
    var id = "usage-days-" + x.key;
    var wrap = el("div", "table-wrap");
    // Keyboard users can scroll it when it is wider than the page.
    wrap.tabIndex = 0;
    wrap.setAttribute("role", "region");
    wrap.setAttribute("aria-labelledby", id);
    var t = el("table", "usage-days");
    var cap = el("caption", null, "Context readings by day, in tokens");
    cap.id = id;
    t.appendChild(cap);
    var head = el("thead"), hr = el("tr");
    DAY_HEADS.forEach(function (h) { var th = el("th", null, h); th.scope = "col"; hr.appendChild(th); });
    head.appendChild(hr);
    t.appendChild(head);
    var body = el("tbody");
    list(x.days).forEach(function (day) {
      var tr = el("tr");
      var th = el("th", null, day.day);
      th.scope = "row";
      tr.appendChild(th);
      [day.lowest, day.highest, day.last, day.window].forEach(function (v) { tr.appendChild(el("td", "num", v)); });
      tr.appendChild(el("td", null, day.estimated ? "estimated by BB" : "reported by BB"));
      body.appendChild(tr);
    });
    t.appendChild(body);
    wrap.appendChild(t);
    return wrap;
  }
  function renderUsage(d, pane) {
    pane.appendChild(back());
    pane.appendChild(paneHead("Usage"));
    var u = state.usage;
    if (!u) {
      pane.appendChild(emptyBox(usagePending(), ""));
      return;
    }
    pane.appendChild(el("p", "lede", u.lede));
    pane.appendChild(el("p", "detail", u.period));
    var all = panel("Across loops");
    all.appendChild(sentences(u.total));
    all.appendChild(el("p", "usage-how", u.mechanism));
    pane.appendChild(all);
    var ctx = panel("What looptender does about context");
    ctx.appendChild(sentences(u.context, true));
    ctx.appendChild(el("p", "usage-how", u.chat));
    pane.appendChild(ctx);
    list(u.loops).forEach(function (x) {
      var p = el("section", "panel usage-loop");
      var h = el("h2");
      h.appendChild(link("#/loop/" + x.key, x.name));
      p.appendChild(h);
      p.appendChild(sentences(x.did));
      var m = x.measurement || {};
      p.appendChild(el("h3", "usage-sub", "Measured usage trend"));
      p.appendChild(el("p", "detail", list(m.details)[0] || "Usage evidence unavailable from this producer."));
      if (list(m.details).length > 1) {
        var evidence = howBox("usage-evidence-" + x.key, "Method, coverage and limitations", false);
        evidence.appendChild(sentences(m.details.slice(1)));
        p.appendChild(evidence);
      }
      p.appendChild(sentences(m.trend));
      p.appendChild(el("h3", "usage-sub", "Observed avoided actions"));
      p.appendChild(el("p", "detail", m.avoided || "Unavailable; no identity-based evidence."));
      p.appendChild(el("h3", "usage-sub", "Estimated savings against a baseline"));
      p.appendChild(sentences(list(m.estimate).length ? m.estimate : ["Unavailable; no accepted comparable baseline. Subscription quota savings are unknown."]));
      p.appendChild(el("h3", "usage-sub", "Context window"));
      p.appendChild(sentences(x.window));
      if (list(x.days).length) p.appendChild(daysTable(x));
      pane.appendChild(p);
    });
  }
  function wantsUsage(r) { return r.view === "usage" || r.view === "loop"; }
  // Read on its own after the report, at low priority on a loop's page
  // where it is not the main content. A failure leaves the last answer.
  // A read that does not answer within usageTimeout() is abandoned and
  // counts as failed, so a stalled request never stops later reads. The
  // page is drawn again only when the answer (apart from its readAt) or
  // the failed state changed, so an unchanged answer does not redraw a
  // loop's page a second time after each read.
  function usageTimeout() { return Math.min(20000, Math.max(3000, refreshSeconds() * 1000 - 2000)); }
  function loadUsage() {
    if (state.usageBusy) return;
    state.usageBusy = true;
    var abort = new AbortController();
    var timer = setTimeout(function () { abort.abort(); }, usageTimeout());
    var wasText = state.usageText, wasFailed = state.usageFailed;
    fetch(USAGE, { cache: "no-store", credentials: "same-origin", headers: { Accept: "application/json" }, priority: route().view === "usage" ? "auto" : "low", signal: abort.signal })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.text(); })
      .then(function (text) {
        var u = JSON.parse(text);
        if (!u || u.schemaVersion !== SCHEMA || !Array.isArray(u.loops)) throw new Error("schema");
        // Every read has its own readAt, which the page does not show; the
        // rest of the answer is what is compared.
        var same = JSON.stringify(Object.assign({}, u, { readAt: null }));
        if (same !== state.usageText) { state.usage = u; state.usageText = same; }
        state.usageFailed = false;
      })
      .catch(function () { state.usageFailed = true; })
      .then(function () {
        clearTimeout(timer);
        state.usageBusy = false;
        var changed = state.usageText !== wasText || state.usageFailed !== wasFailed;
        if (changed && state.data && wantsUsage(route())) render(false);
      });
  }

  // The command that opens a loop's BB thread, to copy and run yourself.
  // Nothing is sent or run from the page; BB documents no link format, so
  // there is no link. The command stays visible and selectable, and the
  // button appears only where the browser can copy.
  var THREAD_ID = /^thr_[A-Za-z0-9_]+$/;
  function threadCommand(id) {
    if (typeof id !== "string" || !THREAD_ID.test(id)) return null;
    return copyCommand("dd", "bb thread open " + id, "copy-thread-command");
  }
  // A command to copy and run yourself, with a copy button where the
  // browser can copy. The page runs nothing.
  // describedBy names what the command is for when a page has several.
  function copyCommand(tagName, text, focus, describedBy) {
    var dd = el(tagName, "copy");
    dd.appendChild(el("code", "command", text));
    if (navigator.clipboard && navigator.clipboard.writeText) {
      var b = el("button", "btn", "Copy the command");
      b.type = "button";
      b.dataset.focus = focus;
      if (describedBy) b.setAttribute("aria-describedby", describedBy);
      b.addEventListener("click", function () {
        // Cleared first, so a second press is announced again.
        state.said = "";
        $("live").textContent = "";
        navigator.clipboard.writeText(text).then(function () {
          announce("Copied. Paste it in a terminal.");
        }, function () {
          announce("Could not copy. Select the command instead.");
        });
      });
      dd.appendChild(b);
    }
    return dd;
  }

  // ---- Changes: pause, stop and resume, on a panel started with --allow-changes ----
  // Every sentence comes from the server. Each change is prepared, shown,
  // and only then confirmed; nothing is retried by itself.
  var ACTION_LABEL = { pause: "Pause", stop: "Stop", resume: "Resume", archive: "Archive", unarchive: "Unarchive" };
  var OBSERVED_ACTION_LABEL = { pause: "Pause checking", resume: "Resume checking" };
  function changesOn() { return !!(state.server && state.server.changes === "on" && state.server.form); }
  function post(path, form, body) {
    return fetch(path, {
      method: "POST", cache: "no-store", credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Keepgoing-Form": form }, body: JSON.stringify(body)
    })
      .then(function (r) {
        if (!r.ok) { var e = new Error("status " + r.status); e.status = r.status; throw e; }
        return r.json();
      })
      .then(function (d) { if (!d || d.schemaVersion !== 1) throw new Error("schema"); return d; });
  }
  function focusOn(name) {
    var n = document.querySelector('[data-focus="' + name + '"]');
    if (n) n.focus();
  }
  function allowanceReply(d, key, step) {
    function text(v) { return typeof v === "string" && v.trim().length > 0 && v.length <= 16384; }
    if (!d || d.schemaVersion !== 1 || (!!d.preview === !!d.result)) throw new Error("allowance reply");
    if (d.result) {
      var outcomes = step === "prepare" ? ["refused", "review_again"] : ["done", "refused", "review_again"];
      if (outcomes.indexOf(d.result.outcome) < 0 || !text(d.result.text)) throw new Error("allowance outcome");
      return d;
    }
    var p = d.preview;
    if (step !== "prepare" || p.action !== "set_send_allowance" || p.key !== key ||
        !text(p.title) || !text(p.confirm) || !text(p.lost) || !text(p.refused) ||
        typeof p.form !== "string" || !/^[a-f0-9]{64}$/.test(p.form) ||
        !isFinite(Date.parse(p.expiresAt)) || !Array.isArray(p.lines) || !p.lines.length || p.lines.length > 32 ||
        !p.lines.every(function (line) { return Array.isArray(line) && line.length > 0 && line.length <= 32 && line.every(function (part) { return part && text(part.t) && !part.at; }); })) throw new Error("allowance review");
    return d;
  }
  function allowancePost(key, step, form, amount) {
    var abort = new AbortController(), timer = setTimeout(function () { abort.abort(); }, 15000);
    var body = { action: "set_send_allowance" };
    if (step === "prepare") body.allowance = amount;
    return fetch("api/v1/send-allowance/" + key + "/" + step, {
      method: "POST", cache: "no-store", credentials: "same-origin", signal: abort.signal,
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Keepgoing-Form": form }, body: JSON.stringify(body)
    }).then(function (r) {
      if (!r.ok) { var e = new Error("status " + r.status); e.status = r.status; throw e; }
      return r.json();
    }).then(function (d) { return allowanceReply(d, key, step); })
      .finally(function () { clearTimeout(timer); });
  }
  function editAllowance(key, issueFp, issueBaseFp, issueIdsMode) {
    state.changeSerial = (state.changeSerial || 0) + 1;
    state.change = { key: key, action: "set_send_allowance", editing: true, draft: "", issueFp: issueFp || "", issueBaseFp: issueBaseFp || "", issueIdsMode: issueIdsMode !== undefined ? !!issueIdsMode : !!(state.data && state.data.identifiers) };
    render(false, true);
    focusOn("allowance-input");
  }
  function prepareChange(key, action, amount, issueFp, issueBaseFp, issueIdsMode) {
    if (!changesOn()) return;
    var curFp = issueFp || (state.change && state.change.key === key ? state.change.issueFp : "");
    var curBaseFp = issueBaseFp != null ? issueBaseFp : (state.change && state.change.key === key ? (state.change.issueBaseFp || "") : "");
    var curIdsMode = issueIdsMode !== undefined ? !!issueIdsMode : (state.change && state.change.key === key && state.change.issueIdsMode !== undefined ? !!state.change.issueIdsMode : !!(state.data && state.data.identifiers));
    var seq = state.changeSerial = (state.changeSerial || 0) + 1;
    state.change = { key: key, action: action, busy: true, issueFp: curFp, issueBaseFp: curBaseFp, issueIdsMode: curIdsMode };
    render(false, true);
    (action === "set_send_allowance" ? allowancePost(key, "prepare", state.server.form, amount) : post("api/v1/loops/" + key + "/prepare", state.server.form, { action: action }))
      .then(function (d) {
        if (seq !== state.changeSerial) return;
        state.change = d.preview ? { key: key, action: action, preview: d.preview, issueFp: curFp, issueBaseFp: curBaseFp, issueIdsMode: curIdsMode } : { key: key, action: action, result: d.result, issueFp: curFp, issueBaseFp: curBaseFp, issueIdsMode: curIdsMode };
      })
      .catch(function () {
        if (seq !== state.changeSerial) return;
        state.change = { key: key, action: action, issueFp: curFp, issueBaseFp: curBaseFp, issueIdsMode: curIdsMode, result: { outcome: "refused", retry: action !== "set_send_allowance", text: "The review could not be prepared: the panel did not answer. Nothing was changed." } };
      })
      .then(function () {
        if (seq !== state.changeSerial) return;
        render(false, true);
        if (!state.change) { focusOn("act-" + action); return; }
        if (state.change.preview) focusOn("change-cancel");
        else if (state.change.result) { focusOn("change-result"); announce(state.change.result.text); }
      });
  }
  // A stop's review may offer options (stop the lanes too, carry it out at
  // once). Each is one button, in the server's words; the body says which.
  function confirmChange(option) {
    var c = state.change, p = c && c.preview;
    if (!p || c.busy) return;
    if (c.issueFp && state.data) {
      if (state.failed || state.data.complete === false) {
        state.issueStaleNotice = "Cannot confirm while reads are failing or incomplete. Wait for a complete read before confirming.";
        render(false, true);
        return;
      }
      var liveLoop = effectiveLoopByKey(state.data, c.key), liveNeed = needFor(c.key);
      var liveFp = liveNeed ? issueFingerprint(liveNeed, liveLoop, true) : "";
      var liveBaseFp = liveNeed ? issueFingerprint(liveNeed, liveLoop, false) : "";
      var curIdsMode = !!state.data.identifiers;
      var fpChanged = liveNeed && (c.issueIdsMode === curIdsMode ? liveFp !== c.issueFp : liveBaseFp !== (c.issueBaseFp || ""));
      if (!liveNeed || liveNeed._retainedIncomplete || (liveLoop && (isLoopRetired(liveLoop) || isLoopSuperseded(liveLoop))) || fpChanged) {
        state.changeSerial = (state.changeSerial || 0) + 1;
        state.change = null;
        state.issueStaleNotice = "Hold, target, or question state changed before confirming. Previous review was cancelled without submitting.";
        render(false, true);
        return;
      }
    }
    var seq = state.changeSerial = (state.changeSerial || 0) + 1;
    state.supersededChangeResult = null;
    state.change = { key: c.key, action: c.action, preview: p, busy: true, issueFp: c.issueFp || "", issueBaseFp: c.issueBaseFp || "", issueIdsMode: !!c.issueIdsMode };
    render(false, true);
    var body = { action: p.action };
    if (option && p.stop) {
      if (option.lanes) body.lanes = true;
      if (option.lanes && option.dropQueued) body.dropQueued = true;
      if (p.stop.immediately) body.immediately = true;
    }
    (c.action === "set_send_allowance" ? allowancePost(c.key, "confirm", p.form) : post("api/v1/loops/" + c.key + "/confirm", p.form, body))
      .then(function (d) {
        if (seq !== state.changeSerial) {
          state.supersededChangeResult = { key: c.key, action: c.action, result: d.result };
          return;
        }
        state.change = { key: c.key, action: c.action, result: d.result };
      })
      // A refusal (4xx) comes before anything is done. Anything else, or no
      // reply: whether it was saved is not known, and it is not sent again.
      .catch(function (err) {
        var refused = err && err.status >= 400 && err.status < 500;
        var res = refused ? { outcome: "refused", retry: c.action !== "set_send_allowance", text: p.refused } : { outcome: "uncertain", text: p.lost };
        if (seq !== state.changeSerial) {
          state.supersededChangeResult = { key: c.key, action: c.action, result: res };
          return;
        }
        state.change = { key: c.key, action: c.action, result: res };
      })
      .then(function () {
        if (seq !== state.changeSerial) {
          render(false);
          return;
        }
        render(false, true);
        focusOn("change-result");
        announce(state.change.result.text);
        clearTimeout(state.timer);
        load(true);
      });
  }
  function cancelChange() {
    var c = state.change;
    state.changeSerial = (state.changeSerial || 0) + 1;
    state.change = null;
    render(false, true);
    if (c) focusOn("act-" + c.action);
  }
  function button(label, focus, cls, onClick, disabled) {
    var b = el("button", "btn" + (cls ? " " + cls : ""), label);
    b.type = "button";
    b.setAttribute("data-focus", focus);
    if (disabled) b.disabled = true;
    b.addEventListener("click", onClick);
    return b;
  }
  // all: the change to every loop at once (key "all"), on the overview.
  function changeBox(l, all, inlineOnly) {
    var c = state.change && state.change.key === l.key ? state.change : null;
    if (!changesOn() && !c) return null;
    var box = el("section", "panel change");
    if (c && c.editing && (!changesOn() || state.server.sendAllowanceEdit !== true)) { state.change = null; return null; }
    if (c && c.editing) {
      var form = el("form", "route-editor");
      form.noValidate = true;
      var title = el("h2", null, "Change send allowance"); title.id = "allowance-title";
      form.setAttribute("aria-labelledby", title.id); form.appendChild(title);
      form.appendChild(el("p", "sub", "Choose a total allowance for automatic sends. You will review counted and remaining attempts before saving. Existing holds stay in place."));
      var field = el("div", "route-field"), label = el("label", null, "Total send allowance"), input = el("input");
      label.htmlFor = "allowance-input"; input.id = "allowance-input"; input.name = "allowance";
      input.type = "text"; input.inputMode = "numeric"; input.maxLength = 16; input.placeholder = "For example, 500";
      input.value = c.draft; input.dataset.focus = "allowance-input"; input.setAttribute("aria-describedby", "allowance-help");
      input.addEventListener("input", function () { c.draft = input.value; input.removeAttribute("aria-invalid"); });
      field.appendChild(label); field.appendChild(input);
      var help = el("p", "sub", c.error || "Enter a positive whole number. This editor does not remove the allowance."); help.id = "allowance-help";
      if (c.error) input.setAttribute("aria-invalid", "true");
      field.appendChild(help); form.appendChild(field);
      var controls = el("div", "change-buttons");
      controls.appendChild(button("Cancel", "change-cancel", "", cancelChange));
      var submit = el("button", "btn is-primary", "Review allowance"); submit.type = "submit"; submit.dataset.focus = "allowance-review"; controls.appendChild(submit);
      form.appendChild(controls);
      form.addEventListener("submit", function (ev) {
        ev.preventDefault();
        var value = c.draft.trim(), amount = Number(value);
        if (!/^[0-9]+$/.test(value) || !Number.isSafeInteger(amount) || amount < 1) {
          c.error = "Enter a positive whole number no larger than 9007199254740991.";
          render(false, true); focusOn("allowance-input"); announce(c.error); return;
        }
        prepareChange(l.key, "set_send_allowance", amount);
      });
      box.appendChild(form); return box;
    }
    if (c && c.preview) {
      var p = c.preview;
      box.classList.add("is-review");
      box.setAttribute("aria-labelledby", "change-title");
      var h = el("h2", null, p.title);
      h.id = "change-title";
      box.appendChild(h);
      list(p.lines).forEach(function (line) { box.appendChild(el("p", "change-line", say(line))); });
      var row = el("div", "change-buttons");
      row.appendChild(button("Cancel", "change-cancel", "", cancelChange, c.busy));
      var options = p.stop ? list(p.stop.options) : [];
      if (options.length) {
        options.forEach(function (o, i) {
          row.appendChild(button(c.busy ? "Saving…" : o.label, i ? "change-confirm-" + i : "change-confirm", i === options.length - 1 ? "is-primary" : "", function () { confirmChange(o); }, c.busy));
        });
      } else {
        row.appendChild(button(c.busy ? "Saving…" : p.confirm, "change-confirm", "is-primary", function () { confirmChange(null); }, c.busy));
      }
      box.appendChild(row);
      return box;
    }
    if (c && c.result) {
      var r = el("p", "change-result is-" + c.result.outcome, c.result.text);
      r.tabIndex = -1;
      r.setAttribute("data-focus", "change-result");
      box.appendChild(r);
      // A change to every loop: each loop's outcome, those not changed first.
      if (list(c.result.lines).length) {
        var ul = el("ul", "change-lines");
        list(c.result.lines).forEach(function (line) { ul.appendChild(el("li", "change-line", line)); });
        box.appendChild(ul);
      }
      var again = el("div", "change-buttons");
      if (c.result.retry && changesOn()) again.appendChild(button("Review again", "change-retry", "", function () { prepareChange(l.key, c.action); }));
      again.appendChild(button("Close", "change-close", "", function () { state.changeSerial = (state.changeSerial || 0) + 1; state.change = null; render(false, true); focusOn("act-" + c.action); }));
      box.appendChild(again);
      if (inlineOnly || !changesOn()) return box;
    }
    if (c && c.busy) {
      box.appendChild(el("p", "change-line", "Preparing the review…"));
      return box;
    }
    var can = list(l.can);
    // The build change, for one loop only, labelled by the server.
    var bc = !all && l.buildChange && l.buildChange.action ? l.buildChange : null;
    var allowance = !all && changesOn() && state.server.sendAllowanceEdit === true;
    if (!can.length && !bc && !allowance) return c ? box : null;
    var actions = el("div", "change-buttons");
    actions.setAttribute("role", "group");
    actions.setAttribute("aria-label", all ? "Change every loop" : "Change this loop");
    can.forEach(function (a) {
      var label = !all && l.observed && OBSERVED_ACTION_LABEL[a] ? OBSERVED_ACTION_LABEL[a] : ACTION_LABEL[a];
      if (label) actions.appendChild(button(label + (all ? " all…" : "…"), "act-" + a, "", function () { prepareChange(l.key, a); }));
    });
    if (allowance) actions.appendChild(button("Change send allowance…", "act-set_send_allowance", "", function () { editAllowance(l.key); }));
    if (bc) actions.appendChild(button(bc.label + "…", "act-" + bc.action, "", function () { prepareChange(l.key, bc.action); }));
    box.appendChild(actions);
    return box;
  }

  // ---- Routes across loops ----
  // Only consistent explicit catalog assignments combine headings. Each
  // route retains its own reading and windows; other routes remain separate
  // under their configured group, with no inferred account identity.
  function allowancesPanel(d) {
    var items = list(d.allowances);
    var p = panel("Allowances", [el("span", "sub", "route readings; grouped by your assignments · not live")]);
    if (!items.length) { p.appendChild(el("p", "sub chain-how", "No route readings to show.")); return p; }
    var ul = el("ul", "allow-list");
    items.forEach(function (a) {
      var li = el("li", "allow" + (a.assigned ? " is-account" : ""));
      var head = el("div", "allow-head");
      head.appendChild(el("h3", "allow-name", a.title));
      var lines = list(a.lines);
      // Said once per block: for BB threads in its own words when every
      // route is one, else as for a headless route.
      var bbOnly = lines.length > 0 && lines.every(function (r) { return r.bbThread; });
      if (a.assigned) head.appendChild(el("span", "sub", bbOnly ? ASSIGNED_BB : ASSIGNED));
      head.appendChild(el("span", "sub", a.sub));
      if (a.assignment) head.appendChild(el("span", "sub", a.assignment));
      li.appendChild(head);
      var body = el("div", "allow-body");
      if (a.assigned) {
        var routes = el("ul", "account-routes allow-routes");
        lines.forEach(function (r) {
          var rl = el("li", "account-route");
          var name = el("p", "account-route-name");
          name.appendChild(link("#/routes/" + r.route, r.title));
          rl.appendChild(name);
          if (r.bbThread && !bbOnly) rl.appendChild(el("p", "sub", ASSIGNED_BB));
          notesOf(r.notes).forEach(function (n) { rl.appendChild(el("p", "route-note is-" + n.tone, n.text)); });
          // The route's own reading: one plain sentence with its age, then
          // its windows. Being out is information here, not a warning.
          var reading = el("p", "allow-reading", say(r.reading));
          var u = routeUsage(d, r.route);
          if (u && u.observedAt) reading.title = fmt(dayTimeFmt, u.observedAt);
          rl.appendChild(reading);
          if (u && u.observedAt) rl.appendChild(el("p", "sub", "Source: recorded route probe · " + fmt(dayTimeFmt, u.observedAt)));
          if (u && u.availability !== "unavailable") {
            var box = el("div", "route-usage");
            if (u.needsPerson && u.ageNote) box.appendChild(el("p", "route-note is-attn usage-age", u.ageNote));
            box.appendChild(meters(u));
            rl.appendChild(box);
          }
          routes.appendChild(rl);
        });
        body.appendChild(routes);
        if (a.linesOmitted) body.appendChild(el("p", "more", a.linesOmitted + (a.linesOmitted === 1 ? " more route is" : " more routes are") + " not shown."));
      } else {
        notesOf(a.notes).forEach(function (n) { body.appendChild(el("p", "route-note is-" + n.tone, n.text)); });
        if (a.usage && a.usage.observedAt) body.appendChild(el("p", "sub", "Source: recorded route probe · " + fmt(dayTimeFmt, a.usage.observedAt)));
        var blk = usageBlock(a.usage);
        if (list(a.next).length) blk.appendChild(el("div", "sub allow-next", say(a.next)));
        body.appendChild(blk);
      }
      li.appendChild(body);
      ul.appendChild(li);
    });
    p.appendChild(ul);
    if (d.allowancesOmitted) p.appendChild(el("p", "more", d.allowancesOmitted + (d.allowancesOmitted === 1 ? " more block is" : " more blocks are") + " not shown."));
    return p;
  }

  // ---- Versions: where each build runs ----
  var versionDots = { current: "dot-good", same_file: "dot-good", newer: "dot-good", older: "dot-limit", different: "dot-limit", different_file: "dot-limit", stale: "dot-calm" };
  // Age is information: "Checked 3 h ago" in plain words, never a badge.
  // An observation older than a day adds a quiet hint to check again.
  // sameCheck leaves out the age and source when the login above already
  // says them for the same check.
  function accountEvidence(e, sameCheck) {
    e = e || {};
    var wrap = el("div", "account-evidence");
    wrap.appendChild(el("span", null, e.text || "Unavailable"));
    if (sameCheck) return wrap;
    if (e.observedAt) {
      var age = accountClock("span", "sub account-age", "checked", e.observedAt);
      age.dataset.checkAgain = e.checkAgain ? "true" : "false";
      wrap.appendChild(age);
    } else if (e.ageSeconds != null) wrap.appendChild(el("span", "sub account-age", "Checked " + span(e.ageSeconds) + " ago · time unavailable"));
    if (e.source) wrap.appendChild(el("span", "sub", e.source));
    return wrap;
  }
  var failedChecks = {
    context_changed: "was saved in a different shell, context, or CLI path",
    timeout: "timed out",
    cancelled: "was cancelled",
    version_unavailable: "could not read the CLI version",
    command_unavailable: "could not run the selected CLI",
    status_unavailable: "returned no recognized login result"
  };
  // When and why the latest check failed. The login and plan shown are then
  // the last good check, or unavailable when there was none.
  function failedCheckLine(c) {
    return "Last check" + (c.failedCheckAt ? " " + fmt(secFmt, c.failedCheckAt) : "") + " " + (failedChecks[c.reason] || "did not complete") + "." +
      (c.attention === "context_changed"
        ? (changesOn() && state.server && state.server.accountSelectionReview
            ? " Review selection for this panel before checking again."
            : " Open a panel with --allow-changes or update the selection before checking again.")
        : "");
  }
  // What on a card needs a person, in words; the warning style is kept for
  // these and never used for age alone. A failed check or a changed context
  // is said once, by failedCheckLine.
  var ACCOUNT_ATTENTION = {
    no_login: "No login reported. Sign in with the provider's own tool, then check again."
  };
  // The connection ID appears only with identifiers, as other identifiers on
  // the page do; otherwise a placeholder to replace.
  function accountId(c) { return state.identifiers && c ? c.id : "CONNECTION_ID"; }
  // The explicit check command to copy, for card c, or with the placeholder.
  function accountCheckCommand(c) {
    return el("code", "account-command", "looptender accounts check --id " + accountId(c) + " --json");
  }
  function accountCheckHint(c) {
    var p = el("p", "sub account-hint");
    p.appendChild(document.createTextNode("To check again, run "));
    p.appendChild(accountCheckCommand(c));
    p.appendChild(document.createTextNode(state.identifiers ? "" : " (Show identifiers to fill in the ID)"));
    return p;
  }
  // Whether a route names the entry, from the routes the report read; the
  // account card that lists them is later work.
  function accountRoutes(c) {
    if (c.routes === "named") return "A route names this account. See Routes.";
    if (c.routes !== "none") return "Not known: not every route could be read.";
    var f = frag();
    f.appendChild(document.createTextNode("No route names this account yet. Add "));
    f.appendChild(el("code", "account-command", "account: " + accountId(c)));
    f.appendChild(document.createTextNode(" to a route to link it (see docs/accounts.md)."));
    return f;
  }
  // A route bound to a catalog entry, as the loop page names it: the
  // person's label and that the binding is theirs, never a verified identity.
  var ASSIGNED = "assigned by you · identity not verified";
  var ASSIGNED_BB = "assigned by you · not verified for BB threads (account set per project)";
  function dockCapital(t) { return t ? t.charAt(0).toUpperCase() + t.slice(1) : t; }
  function boundAccount(a) {
    if (!a || a.assigned !== "by_you" || !a.label || (a.check !== "consistent" && a.check !== "not_verified")) return "";
    return a.label + " · " + (a.check === "not_verified" ? ASSIGNED_BB : ASSIGNED);
  }
  var SELECTIONS = { inherited: "Inherited context", explicit_directory: "Selected directory", bb_thread: "BB thread (account set per project)", externally_managed: "External context" };
  // The reading of a route by its key: the routes view's, else the first
  // loop's chain entry on it (a route past the routes view's cap).
  function routeUsage(d, key) {
    var r = list(d.routes).filter(function (x) { return x.key === key; })[0];
    if (r) return r.usage;
    var u = null;
    list(d.loops).some(function (l) {
      return list(l.chain && l.chain.entries).some(function (e) { if (e.route === key) { u = e.usage; return true; } return false; });
    });
    return u;
  }
  // The routes that use an account, each with the loops that use it and its
  // own reading. Readings are never merged.
  function accountRouteList(d, use, named) {
    var names = Object.create(null);
    list(d.loops).forEach(function (l) { names[l.key] = l.name; });
    var wrap = frag();
    // A route whose check found a mismatch says what the check says in
    // place of "assigned by you".
    if (list(use.routes).some(function (r) { return !r.bbThread && !r.checkText; }) && named) wrap.appendChild(el("p", "sub", ASSIGNED));
    var ul = el("ul", "account-routes");
    list(use.routes).forEach(function (r) {
      var li = el("li", "account-route");
      var head = el("p", "account-route-name");
      head.appendChild(link("#/routes/" + r.route, r.title));
      li.appendChild(head);
      if (r.checkText) li.appendChild(el("p", "sub", dockCapital(r.checkText) + ". Its reading is not counted for this account."));
      else if (r.bbThread && named) li.appendChild(el("p", "sub", ASSIGNED_BB));
      var used = el("p", "sub");
      used.appendChild(document.createTextNode(list(r.loops).length === 1 && !r.omitted ? "Used by the loop " : "Used by the loops "));
      list(r.loops).forEach(function (k, i) {
        if (i) used.appendChild(document.createTextNode(", "));
        used.appendChild(link("#/loop/" + k, names[k] || "a loop"));
      });
      if (r.omitted) used.appendChild(document.createTextNode(" and " + r.omitted + " more"));
      li.appendChild(used);
      li.appendChild(usageBlock(routeUsage(d, r.route)));
      ul.appendChild(li);
    });
    wrap.appendChild(ul);
    if (use.omitted) wrap.appendChild(el("p", "more", use.omitted + (use.omitted === 1 ? " more route is" : " more routes are") + " not shown."));
    return wrap;
  }
  // The newest reading among an account's routes, in the summary. Being out
  // is information here, not a warning.
  function accountReading(use) {
    return list(use && use.reading).length ? el("span", "sub account-reading", say(use.reading)) : null;
  }
  // Missing route bindings are not evidence of additional accounts.
  function unknownAccountSection(d, contexts) {
    var section = el("section", "unknown-account-routes");
    var heading = el("h2", null, "Routes with unknown accounts");
    heading.id = "unknown-account-routes";
    section.setAttribute("aria-labelledby", heading.id);
    section.appendChild(heading);
    section.appendChild(el("p", "sub", "These routes do not name a selected account. They may use an account above; separate routes do not mean separate subscriptions. Check which account each route uses before assigning it."));
    contexts.forEach(function (u) {
      var context = el("div", "unknown-route-context");
      context.appendChild(el("p", "sub", SELECTIONS[u.selection] || "Context as configured"));
      if (u.directory) context.appendChild(el("p", "sub command", u.directory));
      context.appendChild(accountRouteList(d, u, false));
      section.appendChild(context);
    });
    return section;
  }
  // Explicit subscription-quota reads have their own review and never run from
  // a page poll. This state cannot be mistaken for a loop or route action.
  function accountQuotaReply(d, id, step, recovery) {
    function text(v) { return typeof v === "string" && v.trim().length > 0 && v.length <= 16384; }
    if (!d || d.schemaVersion !== 1 || (!!d.preview === !!d.result)) throw new Error("quota reply");
    if (d.result) {
      var outcomes = step === "prepare" ? ["refused", "review_again"] : ["done", "refused", "review_again"];
      if (outcomes.indexOf(d.result.outcome) < 0 || !text(d.result.text)) throw new Error("quota outcome");
      return d;
    }
    var p = d.preview;
    if (step !== "prepare" || p.action !== (recovery ? "review_selection" : "subscription_quota") || p.key !== id ||
        !text(p.title) || !text(p.confirm) || !text(p.lost) || !text(p.refused) ||
        typeof p.form !== "string" || !/^[a-f0-9]{64}$/.test(p.form) ||
        typeof p.expiresAt !== "string" || !isFinite(Date.parse(p.expiresAt)) ||
        !Array.isArray(p.lines) || !p.lines.length || p.lines.length > 32 ||
        !p.lines.every(function (line) { return Array.isArray(line) && line.length > 0 && line.length <= 32 && line.every(function (part) { return part && text(part.t) && !part.at; }); })) throw new Error("quota review");
    return d;
  }
  function accountQuotaPost(id, step, form, recovery) {
    var abort = new AbortController(), timer = setTimeout(function () { abort.abort(); }, 15000);
    return fetch((recovery ? "api/v1/account-selections/" : "api/v1/accounts/") + encodeURIComponent(id) + "/" + step, {
      method: "POST", cache: "no-store", credentials: "same-origin", signal: abort.signal,
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Keepgoing-Form": form },
      body: JSON.stringify({ action: recovery ? "review_selection" : "subscription_quota" })
    }).then(function (r) {
      if (!r.ok) { var e = new Error("status " + r.status); e.status = r.status; throw e; }
      return r.json();
    }).then(function (d) { return accountQuotaReply(d, id, step, recovery); })
      .finally(function () { clearTimeout(timer); });
  }
  function accountQuotaSupported(c) {
    var providers = state.server.accountQuotaProviders;
    return !!state.server.accountQuota && (Array.isArray(providers) ? providers.indexOf(c.provider) >= 0 : c.provider === "codex");
  }
  function accountQuotaSummary(c) {
    if (!c || typeof c !== "object") return null;
    var s = c.quota && c.quota.summary;
    if (!s || typeof s !== "object" || Array.isArray(s) || s.state !== "reported") return null;
    var hasOwn = Object.prototype.hasOwnProperty;
    function parseRFC3339(v) {
      if (typeof v !== "string" || v.length < 20 || v.length > 35) return null;
      var m = /^([0-9]{4})-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):([0-5][0-9]):([0-5][0-9])(?:\.([0-9]{1,9}))?(Z|([+-])([01][0-9]|2[0-3]):([0-5][0-9]))$/.exec(v);
      if (!m) return null;
      var yr = Number(m[1]), mo = Number(m[2]), dy = Number(m[3]), hr = Number(m[4]), mi = Number(m[5]), sc = Number(m[6]);
      if (yr < 2000 || yr > 9999) return null;
      var leap = (yr % 4 === 0 && yr % 100 !== 0) || (yr % 400 === 0);
      var maxDay = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][mo - 1];
      if (dy > maxDay) return null;
      var offsetSec = 0;
      if (m[8] !== "Z") {
        var offSecAbs = Number(m[10]) * 3600 + Number(m[11]) * 60;
        offsetSec = m[9] === "+" ? -offSecAbs : offSecAbs;
      }
      var baseMs = Date.UTC(yr, mo - 1, dy, hr, mi, sc);
      if (!isFinite(baseMs)) return null;
      var epochSec = Math.floor(baseMs / 1000) + offsetSec;
      if (!Number.isSafeInteger(epochSec)) return null;
      var frac = m[7] || "";
      var nanos = (frac + "000000000").slice(0, 9);
      return { epochSec: epochSec, nanos: nanos };
    }
    var hasObs = hasOwn.call(s, "observedAt") && s.observedAt !== undefined;
    var hasRet = hasOwn.call(s, "retrievedAt") && s.retrievedAt !== undefined;
    var legacyAt = null;
    if (c.provider === "codex" && s.source === "codex account/rateLimits/read" && hasObs && !hasRet && (s.freshness === "fresh" || s.freshness === "stale")) {
      legacyAt = parseRFC3339(s.observedAt);
    } else if (c.provider === "claude-code" && s.source === "claude:get_usage" && hasRet && !hasObs && s.freshness === "provider_unknown") {
      legacyAt = parseRFC3339(s.retrievedAt);
    } else if (c.provider === "antigravity" && s.source === "agy:/usage" && hasRet && !hasObs && s.freshness === "provider_unknown") {
      legacyAt = parseRFC3339(s.retrievedAt);
    }
    if (!legacyAt) return null;
    if (hasOwn.call(s, "provenance")) {
      var p = s.provenance;
      if (!p || typeof p !== "object" || Array.isArray(p)) return null;
      var pKeys = Object.keys(p);
      if (pKeys.length !== 3 || !hasOwn.call(p, "version") || !hasOwn.call(p, "retrievedAt") || !hasOwn.call(p, "providerObservedAt")) return null;
      if (typeof p.version !== "number" || p.version !== 1 || p.providerObservedAt !== null) return null;
      var provAt = parseRFC3339(p.retrievedAt);
      if (!provAt || provAt.epochSec !== legacyAt.epochSec || provAt.nanos !== legacyAt.nanos) return null;
    }
    return s;
  }
  function accountQuotaRetrievedAt(saved) {
    if (!saved) return "";
    if (saved.provenance && typeof saved.provenance.retrievedAt === "string") return saved.provenance.retrievedAt;
    return saved.retrievedAt || saved.observedAt || "";
  }
  function accountQuotaMalformedProvenance(c) {
    var s = c && c.quota && c.quota.summary;
    return !!(s && typeof s === "object" && !Array.isArray(s) && Object.prototype.hasOwnProperty.call(s, "provenance") && !accountQuotaSummary(c));
  }
  // Clock labels update independently of saved reads, without replacing a
  // focused disclosure/input or making a provider request. Wall time, rather
  // than timer ticks, also handles background throttling and device sleep.
  function accountSignInNeeded(c) {
    var auth = c.auth || {};
    return auth.state === "reported_absent" || (auth.state === "unchecked" && (c.localAuth === "file_expired" || c.localAuth === "file_missing"));
  }
  function accountClock(kind, cls, mode, at) {
    var label = el(kind, cls);
    label.dataset.accountClock = mode;
    label.dataset.at = at;
    return label;
  }
  function accountClockText(mode, at, now, stale, checkAgain) {
    var time = Date.parse(at), seconds = Math.floor((now - time) / 1000);
    if (!isFinite(time)) return "Time unavailable";
    if (mode === "reset") return seconds >= 0 ? "Reset time passed · refresh to confirm" : "Resets in " + span(Math.max(1, -seconds));
    if (seconds < 0) return "Time unavailable · device clock is before the reading";
    var age = span(seconds) + " ago";
    if (mode === "checked") return "Last checked " + age + (seconds >= 300 ? " · Older check" : "") + (checkAgain || seconds > 86400 ? " · check again to confirm" : "");
    if (mode === "retrieved") return "Retrieved " + age + (stale || seconds > 1800 ? " · Older reading" : "") + " · Provider freshness unknown";
    return "Local read " + age + (stale || seconds > 1800 ? " · Older reading" : " · Saved snapshot") + " · Provider freshness unknown";
  }
  function accountWindowEnded(at, ended, now) {
    return ended || (isFinite(Date.parse(at)) && Date.parse(at) <= now);
  }
  function updateAccountClocks() {
    if (document.hidden || route().view !== "accounts") return;
    var now = Date.now();
    document.querySelectorAll("[data-account-clock]").forEach(function (label) {
      label.textContent = accountClockText(label.dataset.accountClock, label.dataset.at, now, label.dataset.stale === "true", label.dataset.checkAgain === "true");
      var localNote = (label.dataset.accountClock === "retrieved" || label.dataset.accountClock === "quota") ? " (local retrieval time)" : " (local time)";
      label.title = label.dataset.at + " · " + when({ at: label.dataset.at }) + localNote;
    });
    document.querySelectorAll("[data-account-window]").forEach(function (row) {
      var ended = accountWindowEnded(row.dataset.reset, row.dataset.ended === "true", now);
      row.classList.toggle("is-ended", ended);
      row.querySelector("[data-window-value]").textContent = ended ? "Window ended · Last reading: " + row.dataset.value : row.dataset.value;
      var meter = row.querySelector("meter");
      if (meter) meter.hidden = ended;
    });
  }
  function accountWindowValue(row, w, compact) {
    var value = compact ? w.usedPercent + "% used" + (typeof w.remainingPercent === "number" ? " · " + w.remainingPercent + "% remaining" : "") : w.text;
    row.dataset.accountWindow = "true";
    row.dataset.reset = w.resetsAt || "";
    row.dataset.ended = w.ended ? "true" : "false";
    row.dataset.value = value.replace(/^Window ended; refresh needed\. Last reading: /, "");
    if (typeof w.usedPercent === "number" && w.usedPercent >= 100) row.dataset.value += " · Quota exhausted in saved reading";
    var label = el(compact ? "strong" : "p", compact ? null : "quota-value", value);
    label.dataset.windowValue = "true";
    row.appendChild(label);
  }
  function accountQuotaStamp(saved, compact) {
    var at = accountQuotaRetrievedAt(saved);
    var mode = (saved && (saved.provenance || saved.retrievedAt)) ? "retrieved" : "quota";
    var stamp = accountClock(compact ? "span" : "p", "quota-stamp", mode, at);
    stamp.dataset.stale = saved && saved.freshness === "stale" ? "true" : "false";
    return stamp;
  }
  function accountQuotaRequest(c, confirm, recovery) {
    if (!changesOn() || !(recovery ? state.server.accountSelectionReview : accountQuotaSupported(c))) return;
    var old = state.accountQuotaChange, p = confirm && old && old.id === c.id && !!old.recovery === !!recovery && old.preview;
    if (confirm && !p) return;
    var request = { id: c.id, recovery: !!recovery, busy: true, preview: p || null };
    state.accountQuotaChange = request;
    render(false, true);
    accountQuotaPost(c.id, confirm ? "confirm" : "prepare", confirm ? p.form : state.server.form, recovery)
      .then(function (d) { request.preview = d.preview; request.result = d.result; })
      .catch(function (err) {
        var refused = err && err.status >= 400 && err.status < 500;
        request.preview = null;
        request.result = { text: !confirm ? (recovery ? "The selection review could not be prepared. Nothing was updated." : "The quota review could not be prepared. Nothing was refreshed.") : refused ? p.refused : p.lost };
      }).then(function () {
        if (state.accountQuotaChange !== request) return;
        request.busy = false;
        render(false, true);
        focusOn(request.preview ? "quota-cancel-" + c.id : "quota-result-" + c.id);
        if (request.result) announce(request.result.text);
        if (confirm) {
          if (recovery && typeof accountChecks !== "undefined") delete accountChecks.attempted[c.id];
          clearTimeout(state.timer); load(true);
        }
      });
  }
  var accountChecks = { active: false, done: 0, total: 0, attempted: Object.create(null), notice: "" };
  function accountCheckSupported(c) {
    return changesOn() && !!(state.server && state.server.accountCheck) && (!c || c.provider === "claude-code" || c.provider === "codex");
  }
  function accountCheckEligible(c) {
    if (!c || (c.provider !== "claude-code" && c.provider !== "codex")) return false;
    if (c.attention === "context_changed" || c.reason === "context_changed" || c.reason === "unsupported") return false;
    return true;
  }
  function accountNeedsAutoCheck(c) {
    if (!accountCheckEligible(c)) return false;
    if (c.auth && c.auth.state === "unchecked") return true;
    return !!((c.auth && c.auth.checkAgain) || (c.plan && c.plan.checkAgain));
  }
  function accountCheckPost(id, form) {
    var abort = new AbortController(), timer = setTimeout(function () { abort.abort(); }, 15000);
    return fetch("api/v1/account-checks/" + encodeURIComponent(id) + "/run", {
      method: "POST", cache: "no-store", credentials: "same-origin", signal: abort.signal,
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Keepgoing-Form": form },
      body: JSON.stringify({ action: "account_check" })
    }).then(function (r) {
      if (!r.ok) { var e = new Error("status " + r.status); e.status = r.status; throw e; }
      return r.json();
    }).then(function (d) {
      if (!d || d.schemaVersion !== 1 || !d.result || typeof d.result.text !== "string") throw new Error("account check reply");
      return d;
    }).finally(function () { clearTimeout(timer); });
  }
  function runAccountChecks(cards, manual) {
    if (!accountCheckSupported() || accountChecks.active || document.hidden || state.paused || route().view !== "accounts") return;
    var queue = list(cards).filter(accountCheckEligible).slice(0, 16);
    if (!queue.length) return;
    queue.forEach(function (c) { accountChecks.attempted[c.id] = true; });
    accountChecks.active = true;
    accountChecks.done = 0;
    accountChecks.total = queue.length;
    accountChecks.notice = "";
    render(false, true);
    var lastText = "";
    function next(i) {
      if (i >= queue.length || route().view !== "accounts" || document.hidden || state.paused || !accountCheckSupported()) {
        accountChecks.active = false;
        if (manual && lastText) accountChecks.notice = queue.length === 1 ? lastText : "Checked " + accountChecks.done + " of " + accountChecks.total + " accounts.";
        render(false, true);
        if (accountChecks.done > 0) { savedAccounts.status = ""; savedAccounts.data = null; clearTimeout(state.timer); load(true); }
        return;
      }
      accountCheckPost(queue[i].id, state.server.form)
        .then(function (d) { lastText = d.result.text; })
        .catch(function () { lastText = "Account check could not complete."; })
        .then(function () {
          accountChecks.done = i + 1;
          render(false, true);
          next(i + 1);
        });
    }
    next(0);
  }
  function maybeAutoCheckAccounts(cards) {
    if (!accountCheckSupported() || accountChecks.active || document.hidden || state.paused || route().view !== "accounts") return;
    var due = list(cards).filter(function (c) {
      return accountNeedsAutoCheck(c) && !accountChecks.attempted[c.id];
    });
    if (due.length) runAccountChecks(due, false);
  }
  function accountQuotaReason(reason) {
    return ({ not_checked: "No subscription quota reading is saved for this selection.", selection_review_required: "Review this unchanged selection for the panel, then refresh quota. Saved quota was not reused.", context_changed: "The selected context changed. Review it before refreshing quota.", cache_unavailable: "Private quota evidence could not be read. No usage is assumed.",
      time_unavailable: "The reading's time cannot be established.", unsupported_provider: "Subscription quota reads are available for selected Codex contexts only.", unsupported: "This Codex CLI does not support subscription quota reads.",
      timeout: "The quota read timed out.", cancelled: "The quota read was cancelled.", status_unavailable: "Codex could not provide a quota reading.", schema_unavailable: "The quota response was not recognized.",
      no_windows: "Codex reported no subscription windows.", unknown_bucket: "Codex did not report the supported subscription bucket.", unknown_windows: "Codex reported an unsupported quota window shape." })[reason] || "Subscription quota is unavailable.";
  }
  // Connecting is a person-started provider sign-in, never a consequence of
  // polling saved quota. Each attempt stays bound to one selected account.
  var accountConnections = Object.create(null), accountConnectionLabels = Object.create(null);
  function connectProvider(c) { return c.provider === "claude-code" ? "Claude" : "Codex"; }
  function accountConnectSupported(c) {
    return changesOn() && list(state.server.accountConnectProviders).indexOf(c.provider) >= 0;
  }
  function accountConnectReply(d, c, step, attempt) {
    function text(v) { return typeof v === "string" && v.trim().length > 0 && v.length <= 16384; }
    if (!d || d.schemaVersion !== 1 || [d.preview, d.result, d.connect].filter(Boolean).length !== 1) throw new Error("connection reply");
    if (d.preview) {
      var p = d.preview;
      if (step !== "prepare" || p.action !== "connect_account" || p.key !== c.id || !text(p.title) || !text(p.confirm) || !text(p.lost) || !text(p.refused) ||
          typeof p.attemptId !== "string" || !/^[a-f0-9]{32}$/.test(p.attemptId) || typeof p.form !== "string" || !/^[a-f0-9]{64}$/.test(p.form) || !isFinite(Date.parse(p.expiresAt)) ||
          !Array.isArray(p.lines) || !p.lines.length || p.lines.length > 32 || !p.lines.every(function (line) { return Array.isArray(line) && line.length > 0 && line.length <= 32 && line.every(function (part) { return part && text(part.t) && !part.at; }); })) throw new Error("connection preview");
    } else if (d.result) {
      if (["refused", "review_again"].indexOf(d.result.outcome) < 0 || !text(d.result.text)) throw new Error("connection refusal");
    } else {
      var a = d.connect;
      if (step === "prepare" || !/^[a-f0-9]{32}$/.test(a.id) || (attempt && a.id !== attempt) || a.accountId !== c.id || a.provider !== c.provider ||
          ["waiting", "connected", "failed", "cancelled", "expired", "selection_changed"].indexOf(a.state) < 0 || !isFinite(Date.parse(a.expiresAt)) || (a.reason != null && (!text(a.reason) || a.reason.length > 160))) throw new Error("connection attempt");
      if (a.connectedAccountId != null && (a.state !== "connected" || typeof a.connectedAccountId !== "string" || !/^[a-f0-9]{32}$/.test(a.connectedAccountId))) throw new Error("connected account");
      if (a.authorizationUrl) {
        var u = new URL(a.authorizationUrl);
        if (a.provider !== "codex" || a.state !== "waiting" || u.protocol !== "https:" || u.hostname !== "auth.openai.com" || u.port || u.username || u.password || u.hash || u.pathname !== "/oauth/authorize" || a.authorizationUrl.length > 8192) throw new Error("connection URL");
      }
    }
    return d;
  }
  function accountConnectPost(c, step, form, attempt) {
    var ctl = new AbortController(), timer = setTimeout(function () { ctl.abort(); }, 15000);
    var choice = accountConnections[c.id], body = step === "prepare" && choice && choice.newLabel ? { action: "connect_another_account", label: choice.newLabel } : { action: "connect_account" };
    if (attempt && (step === "status" || step === "cancel")) body.attemptId = attempt;
    return fetch("api/v1/account-connect/" + encodeURIComponent(c.id) + "/" + step, {
      method: "POST", cache: "no-store", credentials: "same-origin", signal: ctl.signal,
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Keepgoing-Form": form }, body: JSON.stringify(body)
    }).then(function (r) {
      if (!r.ok) throw new Error("connection status");
      return r.text();
    }).then(function (raw) {
      if (raw.length > 65536) throw new Error("connection size");
      return accountConnectReply(JSON.parse(raw), c, step, attempt);
    }).finally(function () { clearTimeout(timer); });
  }
  function accountConnectRequest(c, step) {
    var old = accountConnections[c.id];
    if (!accountConnectSupported(c) || (old && old.busy)) return;
    var preview = old && old.preview, attempt = old && old.connect && old.connect.id;
    if (old && old.sessionForm && old.sessionForm !== state.server.form) { clearTimeout(old.timer); delete accountConnections[c.id]; return; }
    if ((step === "start" && !preview) || ((step === "status" || step === "cancel") && !attempt)) return;
    if (old) clearTimeout(old.timer);
    var request = { card: c, busy: true, sessionForm: state.server.form, newLabel: old && old.newLabel, preview: step === "start" ? preview : null, connect: old && old.connect, timer: 0 };
    if (step === "start") request.connect = { id: preview.attemptId, accountId: c.id, provider: c.provider, state: "waiting", expiresAt: preview.expiresAt };
    accountConnections[c.id] = request;
    state.accountOpen[c.id] = true;
    render(false, true);
    accountConnectPost(c, step, step === "start" ? preview.form : state.server.form, step === "start" ? preview.attemptId : step === "status" || step === "cancel" ? attempt : null)
      .then(function (reply) { request.preview = reply.preview; request.connect = reply.connect; request.result = reply.result; })
      .catch(function () {
        request.preview = null;
        request.result = { text: step === "prepare" ? "The connection review could not be prepared. No sign-in was started." : "The connection reply was lost or could not be verified. Sign-in may still be running. Check its status; do not start another sign-in." };
      }).then(function () {
        if (accountConnections[c.id] !== request) return;
        if (state.server.form !== request.sessionForm) { delete accountConnections[c.id]; render(false, true); return; }
        request.busy = false;
        render(false, true);
        if (step !== "status") focusOn(request.preview ? "connect-cancel-" + c.id : "connect-result-" + c.id);
        if (request.result) announce(request.result.text);
        if (request.connect && request.connect.state === "connected") { clearTimeout(state.timer); load(true); }
        if (!request.result && request.connect && request.connect.state === "waiting") request.timer = setTimeout(function () {
          if (accountConnections[c.id] === request && !document.hidden && !state.paused && route().view === "accounts" && Date.now() < Date.parse(request.connect.expiresAt)) accountConnectRequest(c, "status");
        }, 2000);
      });
  }
  function accountConnectOpen(c) {
    state.accountOpen[c.id] = true;
    var current = accountConnections[c.id];
    if (current && (current.busy || (current.connect && current.connect.state === "waiting"))) { render(false, true); focusOn("connect-result-" + c.id); return; }
    if (accountConnectSupported(c)) accountConnectRequest(c, "prepare");
    else { accountConnections[c.id] = { card: c, result: { text: "Connecting an account needs a panel started with looptender panel --allow-changes. If Connect is still unavailable, update looptender on this device." } }; render(false, true); focusOn("connect-result-" + c.id); }
  }
  function accountConnectBox(c) {
    var current = accountConnections[c.id];
    if (!current) return null;
    if (current.sessionForm && current.sessionForm !== state.server.form) { clearTimeout(current.timer); delete accountConnections[c.id]; return null; }
    var box = el("section", "account-connect"), title = el("h3", null, "Connect " + connectProvider(c) + " · " + (current.newLabel || c.label));
    box.appendChild(title);
    var controls = el("div", "account-quota-actions");
    function close() { clearTimeout(current.timer); delete accountConnections[c.id]; render(false, true); focusOn("account-connect-" + c.id); }
    if (current.preview) {
      list(current.preview.lines).forEach(function (line) { box.appendChild(el("p", null, say(line))); });
      controls.appendChild(button("Cancel", "connect-cancel-" + c.id, "", close, current.busy));
      controls.appendChild(button(current.busy ? "Opening sign-in…" : current.preview.confirm, "connect-start-" + c.id, "is-primary", function () { accountConnectRequest(c, "start"); }, current.busy));
    } else if (current.connect || current.result) {
      var a = current.connect || {}, texts = {
        waiting: current.newLabel ? "Finish signing in in your browser. This adds a separate account; your existing accounts and routes stay unchanged." : "Finish signing in in your browser. This connects only this selected account; other saved accounts stay unchanged.",
        connected: "Connected. You can now retrieve usage for this account.",
        failed: "Sign-in did not complete. Other accounts were not changed. Review a new connection when you are ready.",
        cancelled: "Sign-in cancelled. Other accounts were not changed.",
        expired: "Sign-in expired. Review a new connection when you are ready.",
        selection_changed: "This account selection changed during sign-in. The connection was not applied. Read the saved state before trying again."
      };
      var result = el("p", null, current.result ? current.result.text : texts[a.state]);
      result.tabIndex = -1; result.setAttribute("data-focus", "connect-result-" + c.id); box.appendChild(result);
      if (a.state === "waiting") {
        if (a.authorizationUrl && !current.result) {
          var authLink = link(a.authorizationUrl, "Continue to " + connectProvider(c), "btn is-primary");
          authLink.target = "_blank"; authLink.rel = "noopener noreferrer"; authLink.referrerPolicy = "no-referrer"; box.appendChild(authLink);
        }
        controls.appendChild(button(current.busy ? "Checking…" : "Check connection", "connect-check-" + c.id, "", function () { accountConnectRequest(c, "status"); }, current.busy));
        controls.appendChild(button("Cancel sign-in", "connect-cancel-" + c.id, "", function () { accountConnectRequest(c, "cancel"); }, current.busy));
      } else {
        if (a.state === "connected" && a.connectedAccountId && a.connectedAccountId !== c.id) controls.appendChild(link("#/accounts/" + a.connectedAccountId, "View connected account", "btn is-primary"));
        controls.appendChild(button("Close", "connect-close-" + c.id, "", close, current.busy));
      }
    } else box.appendChild(el("p", "sub", "Preparing connection…"));
    box.appendChild(controls);
    return box;
  }

  var accountUsageChange = null;
  function accountUsageRequest(c, enable, confirm) {
    if (!changesOn() || !state.server.accountUsageUpdating) return;
    var prior = accountUsageChange, preview = confirm && prior && prior.id === c.id && prior.preview;
    if (confirm && !preview) return;
    var action = enable ? "enable_usage_updates" : "disable_usage_updates";
    var request = { id: c.id, enable: enable, busy: true, preview: preview || null };
    accountUsageChange = request; render(false, true);
    var abort = new AbortController(), timer = setTimeout(function () { abort.abort(); }, 15000);
    fetch("api/v1/account-usage-updates/" + encodeURIComponent(c.id) + "/" + (confirm ? "confirm" : "prepare"), {
      method: "POST", cache: "no-store", credentials: "same-origin", signal: abort.signal,
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Keepgoing-Form": confirm ? preview.form : state.server.form },
      body: JSON.stringify({ action: action })
    }).then(function (r) { if (!r.ok) throw new Error("refused"); return r.json(); })
      .then(function (d) {
        if (!d || d.schemaVersion !== 1 || (!!d.preview === !!d.result)) throw new Error("reply");
        if (d.preview && (d.preview.action !== action || d.preview.key !== c.id || !/^[a-f0-9]{64}$/.test(d.preview.form) || !Array.isArray(d.preview.lines))) throw new Error("review");
        request.preview = d.preview; request.result = d.result;
      }).catch(function () { request.preview = null; request.result = { text: "Usage update settings could not be confirmed. Read the saved state before trying again." }; })
      .finally(function () {
        clearTimeout(timer); request.busy = false;
        if (accountUsageChange !== request) return;
        render(false, true); focusOn("usage-update-" + c.id);
        if (request.result) announce(request.result.text);
        if (confirm) { clearTimeout(state.timer); load(true); }
      });
  }
  function accountUsageBox(c) {
    var box = el("div", "account-usage-updating"), u = c.usageUpdating;
    box.appendChild(el("p", null, u && u.text || "Usage update settings unavailable"));
    if (u && u.nextAttemptAt) box.appendChild(el("p", "sub", "Next eligible fetch " + when({ at: u.nextAttemptAt })));
    var current = accountUsageChange && accountUsageChange.id === c.id ? accountUsageChange : null;
    if (current) {
      if (current.preview) {
        box.appendChild(el("h4", null, current.preview.title));
        list(current.preview.lines).forEach(function (line) { box.appendChild(el("p", "sub", say(line))); });
        var controls = el("div", "account-quota-actions");
        controls.appendChild(button("Cancel", "usage-update-cancel-" + c.id, "", function () { accountUsageChange = null; render(false, true); focusOn("usage-update-" + c.id); }, current.busy));
        controls.appendChild(button(current.busy ? "Saving…" : current.preview.confirm, "usage-update-" + c.id, "", function () { accountUsageRequest(c, current.enable, true); }, current.busy));
        box.appendChild(controls);
      } else if (current.result) {
        var result = el("p", null, current.result.text); result.tabIndex = -1; result.setAttribute("data-focus", "usage-update-" + c.id); box.appendChild(result);
        box.appendChild(button("Close", "usage-update-close-" + c.id, "", function () { accountUsageChange = null; render(false, true); }));
      } else box.appendChild(el("p", "sub", "Preparing usage update review…"));
    } else if (changesOn() && state.server.accountUsageUpdating) {
      var authorized = u && ["enabled", "waiting", "backoff", "paused"].indexOf(u.state) >= 0;
      box.appendChild(button(authorized ? "Pause usage updates…" : "Keep usage updated…", "usage-update-" + c.id, "", function () { accountUsageRequest(c, !authorized, false); }));
      if (u && u.state === "paused") box.appendChild(button("Review and enable again…", "usage-update-review-" + c.id, "", function () { accountUsageRequest(c, true, false); }));
    }
    return box;
  }

  function accountQuotaBox(c) {
    var q = c.quota || {}, box = el("div", "account-quota");
    box.appendChild(el("h3", null, "Account quota"));
    box.appendChild(accountUsageBox(c));
    box.appendChild(el("p", "sub", "Saved for this selection; separate from route readings. Live thread account binding is not verified."));
    var saved = accountQuotaSummary(c) || {};
    var rejectedProv = accountQuotaMalformedProvenance(c);
    if (saved.state === "reported") {
      box.appendChild(accountQuotaStamp(saved));
      if (saved.provenance) {
        box.appendChild(el("p", "sub", c.provider === "codex"
          ? "Provider observation time is unavailable (provenance v1; local retrieval time only). Collection time does not verify provider freshness."
          : "Provider observation time is unavailable (provenance v1; local retrieval time only). Retrieval may return cached or seeded usage."));
      } else {
        box.appendChild(el("p", "sub", c.provider === "codex"
          ? "Provider observation time is unavailable (legacy local read timestamp). Collection time does not verify provider freshness."
          : "Provider observation time is unavailable (legacy retrieval timestamp). Retrieval may return cached or seeded usage."));
      }
      var savedRows = list(saved.rows), nowCheck = Date.now();
      var allInitiallyEnded = savedRows.length > 0 && savedRows.every(function (w) { return accountWindowEnded(w.resetsAt, w.ended, nowCheck); });
      function renderQuotaRowList(rowItems) {
        var ulRows = el("ul", "quota-windows");
        rowItems.forEach(function (w) {
          var row = el("li", "quota-window" + (w.ended ? " is-ended" : ""));
          row.appendChild(el("h4", null, w.name));
          accountWindowValue(row, w, false);
          if (typeof w.usedPercent === "number" && !w.ended) {
            var meter = el("meter", "quota-meter"); meter.min = 0; meter.max = 100; meter.value = w.usedPercent;
            meter.setAttribute("aria-label", w.name + ": " + w.text); meter.textContent = w.text; row.appendChild(meter);
          }
          if (w.resetsAt) {
            row.appendChild(accountClock("p", "sub quota-countdown", "reset", w.resetsAt));
            row.appendChild(el("p", "sub", "Reset: " + when({ at: w.resetsAt }) + " (local time)"));
          }
          else if (typeof w.usedPercent === "number" || typeof w.remainingPercent === "number") row.appendChild(el("p", "sub", "Reset time unavailable."));
          ulRows.appendChild(row);
        });
        return ulRows;
      }
      if (allInitiallyEnded) {
        box.appendChild(uiBadge("plain", "Window ended \u00B7 current quota unknown"));
        var histFold = el("details", "acct-prev");
        histFold.appendChild(el("summary", null, "Previous readings (window ended)"));
        histFold.appendChild(renderQuotaRowList(savedRows));
        box.appendChild(histFold);
      } else {
        box.appendChild(renderQuotaRowList(savedRows));
      }
      if (saved.partial || saved.omitted) box.appendChild(el("p", "sub quota-incomplete", "Partial evidence: some quota detail is unavailable" + (saved.omitted ? "; " + saved.omitted + " additional buckets not shown" : "") + ". Missing values are not zero."));
      box.appendChild(el("p", "sub", "Source: " + saved.source));
    } else if (rejectedProv) {
      box.appendChild(el("p", "sub", "Quota provenance metadata was invalid or unsupported. No usage is assumed."));
    } else box.appendChild(el("p", "sub", (q.summary && q.summary.state !== "reported" && q.summary.text) || accountQuotaReason(q.reason || "schema_unavailable")));
    if (!rejectedProv && saved.failedRefreshAt) box.appendChild(el("p", "sub", "Refresh failed " + when({ at: saved.failedRefreshAt }) + ". " + saved.failedRefreshText + " Any earlier reading retains its original time."));
    if (c.provider !== "codex" && c.provider !== "claude-code" && c.provider !== "antigravity") return box;
    var change = state.accountQuotaChange, current = change && change.id === c.id ? change : null;
    if (current) {
      var review = el("div", "account-quota-review");
      if (current.preview) {
        review.appendChild(el("h4", null, current.preview.title));
        list(current.preview.lines).forEach(function (line) { review.appendChild(el("p", null, say(line))); });
        var controls = el("div", "account-quota-actions");
        controls.appendChild(button("Cancel", "quota-cancel-" + c.id, "", function () { state.accountQuotaChange = null; render(false, true); focusOn(current.recovery ? "selection-review-" + c.id : "quota-refresh-" + c.id); }, current.busy));
        controls.appendChild(button(current.busy ? (current.recovery ? "Updating…" : "Reading…") : current.preview.confirm, "quota-confirm-" + c.id, "is-primary", function () { accountQuotaRequest(c, true, current.recovery); }, current.busy));
        review.appendChild(controls);
      } else if (current.result) {
        var result = el("p", null, current.result.text); result.tabIndex = -1; result.setAttribute("data-focus", "quota-result-" + c.id); review.appendChild(result);
        review.appendChild(button("Close", "quota-close-" + c.id, "", function () { state.accountQuotaChange = null; render(false, true); focusOn("account-" + c.id); }));
      } else review.appendChild(el("p", "sub", current.recovery ? "Preparing selection review…" : "Preparing quota review…"));
      box.appendChild(review);
    }
    var needsSelectionReview = q.reason === "selection_review_required" || q.reason === "context_changed" || c.attention === "context_changed";
    if (needsSelectionReview) {
      if (changesOn() && state.server.accountSelectionReview) {
        if (!current) box.appendChild(button("Review selection for this panel…", "selection-review-" + c.id, "", function () { accountQuotaRequest(c, false, true); }));
      } else box.appendChild(el("p", "sub", "Reviewing this selection needs a panel started with looptender panel --allow-changes; open one, then return to Accounts."));
      return box;
    }
    if (changesOn() && accountQuotaSupported(c)) {
      if (!current) box.appendChild(button((c.provider === "claude-code" || c.provider === "antigravity") ? "Retrieve usage…" : "Refresh subscription quota…", "quota-refresh-" + c.id, "", function () { accountQuotaRequest(c, false); }));
    } else box.appendChild(el("p", "sub", c.provider === "claude-code" ? "Claude usage retrieval requires a panel with changes allowed on Darwin arm64 and the inspected native Claude Code 2.1.326 image. Each selection is checked before a request; other images remain unavailable." : c.provider === "antigravity" ? "Antigravity usage retrieval requires a panel with changes allowed. It runs agy /usage in read-only plan mode without an ordinary model turn." : "Review a refresh in a panel with changes allowed."));
    return box;
  }
  function accountRemoveSupported() {
    return changesOn() && !!(state.server && state.server.accountRemove);
  }
  function accountRemoveReply(d, id, step) {
    function text(v) { return typeof v === "string" && v.trim().length > 0 && v.length <= 16384; }
    if (!d || d.schemaVersion !== 1 || (!!d.preview === !!d.result)) throw new Error("account remove reply");
    if (d.result) {
      var outcomes = step === "prepare" ? ["refused", "review_again"] : ["done", "refused", "review_again"];
      if (outcomes.indexOf(d.result.outcome) < 0 || !text(d.result.text)) throw new Error("account remove outcome");
      return d;
    }
    var p = d.preview;
    if (step !== "prepare" || p.action !== "remove_account" || p.key !== id ||
        !text(p.title) || !text(p.confirm) || !text(p.lost) || !text(p.refused) ||
        typeof p.form !== "string" || !/^[a-f0-9]{64}$/.test(p.form) ||
        typeof p.expiresAt !== "string" || !isFinite(Date.parse(p.expiresAt)) ||
        !Array.isArray(p.lines) || !p.lines.length || p.lines.length > 32 ||
        !p.lines.every(function (line) { return Array.isArray(line) && line.length > 0 && line.length <= 32 && line.every(function (part) { return part && text(part.t) && !part.at; }); })) throw new Error("account remove preview");
    return d;
  }
  function accountRemoveRequest(c, confirm) {
    if (!accountRemoveSupported()) return;
    var old = state.accountRemove, p = confirm && old && old.id === c.id && old.preview;
    if (confirm && !p) return;
    var request = { id: c.id, sessionForm: state.server.form, busy: true, preview: p || null };
    state.accountRemove = request;
    state.accountOpen[c.id] = true;
    render(false, true);
    post("api/v1/account-remove/" + encodeURIComponent(c.id) + "/" + (confirm ? "confirm" : "prepare"),
      confirm ? p.form : state.server.form, { action: "remove_account" })
      .then(function (d) { return accountRemoveReply(d, c.id, confirm ? "confirm" : "prepare"); })
      .then(function (d) { request.preview = d.preview; request.result = d.result; })
      .catch(function (err) {
        var refused = err && err.status >= 400 && err.status < 500;
        request.preview = null;
        request.result = { text: !confirm ? "The account removal review could not be prepared. Nothing was removed." : refused ? p.refused : p.lost };
      }).then(function () {
        if (state.accountRemove !== request) return;
        if (state.server && state.server.form !== request.sessionForm) { state.accountRemove = null; render(false, true); return; }
        request.busy = false;
        render(false, true);
        focusOn(request.preview ? "remove-cancel-" + c.id : "remove-result-" + c.id);
        if (request.result) announce(request.result.text);
        if (confirm && request.result && request.result.outcome === "done") {
          savedAccounts = { reference: "", status: "", data: null, serial: 0, abort: null };
          clearTimeout(state.timer);
          load(true);
        }
      });
  }
  function accountRemoveBox(c) {
    if (!accountRemoveSupported()) return null;
    var current = state.accountRemove && state.accountRemove.id === c.id ? state.accountRemove : null;
    if (current && current.sessionForm && state.server && current.sessionForm !== state.server.form) {
      state.accountRemove = null;
      current = null;
    }
    var box = el("div", "account-remove");
    if (!current) {
      var openBtn = button("Remove from catalog…", "account-remove-" + c.id, "", function () { accountRemoveRequest(c, false); });
      openBtn.setAttribute("aria-label", "Remove " + c.label + " from catalog");
      box.appendChild(openBtn);
      return box;
    }
    var review = el("div", "account-remove-review");
    if (current.preview) {
      review.appendChild(el("h4", null, current.preview.title));
      list(current.preview.lines).forEach(function (line) { review.appendChild(el("p", null, say(line))); });
      var controls = el("div", "account-remove-actions");
      controls.appendChild(button("Cancel", "remove-cancel-" + c.id, "", function () { state.accountRemove = null; render(false, true); focusOn("account-remove-" + c.id); }, current.busy));
      controls.appendChild(button(current.busy ? "Removing…" : current.preview.confirm, "remove-confirm-" + c.id, "is-primary", function () { accountRemoveRequest(c, true); }, current.busy));
      review.appendChild(controls);
    } else if (current.result) {
      var res = el("p", null, current.result.text);
      res.tabIndex = -1; res.setAttribute("data-focus", "remove-result-" + c.id);
      review.appendChild(res);
      review.appendChild(button("Close", "remove-close-" + c.id, "", function () { state.accountRemove = null; render(false, true); focusOn("account-" + c.id); }));
    } else {
      review.appendChild(el("p", "sub", "Preparing removal review…"));
    }
    box.appendChild(review);
    return box;
  }

  function renderAccounts(d, pane) {
    var a = d.accounts || {};
    pane.appendChild(back());
    pane.appendChild(paneHead("Accounts"));
    pane.appendChild(el("p", "lede", "Your selected accounts, with the evidence available on this device."));
    pane.appendChild(el("p", "sub", "Read now reloads saved evidence. Keep usage updated authorizes a running menu, panel or dock process to fetch usage in the background. Check login now checks login and plan; manual quota retrieval has its own review and calls no model. Countdown labels update every 15 seconds; a passed reset needs a new reading to confirm availability."));
    var usage = el("p", "account-usage-link");
    usage.appendChild(link("#/routes", "View recorded usage", "work-inspect"));
    usage.appendChild(el("span", "sub", ". Readings come from configured routes and are not live. Each account shows the readings of the routes that use it."));
    pane.appendChild(usage);
    if (d.accountsReference && (savedAccounts.reference !== d.accountsReference || savedAccounts.status !== "ready")) {
      var failed = savedAccounts.reference === d.accountsReference && savedAccounts.status === "failed";
      var pending = emptyBox(failed ? "Saved accounts unavailable" : "Reading saved accounts", failed ? "This saved snapshot could not be read. Use Read now to try the current panel snapshot. Missing quota is not zero; no provider refresh was made." : "Reading the saved selections and quota evidence for this panel snapshot. No provider is queried.");
      pending.setAttribute("role", "status"); pane.appendChild(pending);
      // A saved-only read after confirmation temporarily replaces the cards.
      // Keep its non-sensitive outcome and focus target through that gap;
      // old selection fields and quota rows remain withheld.
      var outcome = state.accountQuotaChange;
      if (outcome && outcome.result) {
        var feedback = el("p", null, outcome.result.text);
        feedback.tabIndex = -1; feedback.setAttribute("data-focus", "quota-result-" + outcome.id);
        pane.appendChild(feedback);
      }
      var removeOutcome = state.accountRemove;
      if (removeOutcome && removeOutcome.result) {
        var removeFeedback = el("p", null, removeOutcome.result.text);
        removeFeedback.tabIndex = -1; removeFeedback.setAttribute("data-focus", "remove-result-" + removeOutcome.id);
        pane.appendChild(removeFeedback);
      }
      Object.keys(accountConnections).forEach(function (id) {
        var connection = accountConnections[id];
        if (connection.sessionForm && connection.sessionForm !== state.server.form) return;
        if (connection.result || (connection.connect && connection.connect.state === "connected")) {
          var message = el("p", null, connection.result ? connection.result.text : "Account connected. Reading the saved account list…");
          message.tabIndex = -1; message.setAttribute("data-focus", "connect-result-" + id); pane.appendChild(message);
        }
      });
      return;
    }
    if (!a.complete || a.state !== "available") {
      pane.appendChild(emptyBox("Accounts unavailable", "The private account catalog could not be read completely. This does not mean no accounts are selected."));
      return;
    }
    var cards = list(a.cards), uses = list(d.accountUses), byCard = Object.create(null);
    uses.forEach(function (u) { if (u.card) byCard[u.card] = u; });
    var unnamed = uses.filter(function (u) { return u.context; });
    var removeDone = state.accountRemove && state.accountRemove.result && !cards.some(function (x) { return x.id === state.accountRemove.id; }) ? state.accountRemove : null;
    if (removeDone) {
      var doneBox = el("div", "panel account-remove-result");
      var doneText = el("p", null, removeDone.result.text);
      doneText.tabIndex = -1; doneText.setAttribute("data-focus", "remove-result-" + removeDone.id);
      doneBox.appendChild(doneText);
      doneBox.appendChild(button("Close", "remove-close-" + removeDone.id, "", function () { state.accountRemove = null; render(false, true); }));
      pane.appendChild(doneBox);
    }
    if (!cards.length && !unnamed.length) {
      pane.appendChild(emptyBox("No accounts selected here yet", FIRST_RUN));
      pane.appendChild(addAccountBox(true));
      return;
    }
    var acctCounts = { recentLocal: 0, olderLocal: 0, atLimit: 0, endedOrNone: 0, drift: 0 }, provCounts = Object.create(null), nowAc = Date.now();
    function classifyAccountCard(cCard) {
      var isDr = cCard.attention === "context_changed" || cCard.reason === "context_changed" || (cCard.quota && (cCard.quota.reason === "selection_review_required" || cCard.quota.reason === "context_changed"));
      if (isDr) return "drift";
      if (accountQuotaMalformedProvenance(cCard)) return "rejected";
      var qSum = accountQuotaSummary(cCard);
      if (!qSum || qSum.state !== "reported") return "endedOrNone";
      var qRows = list(qSum.rows);
      var allEnd = qRows.length > 0 && qRows.every(function (rw) { return accountWindowEnded(rw.resetsAt, rw.ended, nowAc); });
      if (allEnd) return "endedOrNone";
      if (qRows.some(function (rw) { return typeof rw.usedPercent === "number" && rw.usedPercent >= 100 && !accountWindowEnded(rw.resetsAt, rw.ended, nowAc); })) return "atLimit";
      var atMs = Date.parse(accountQuotaRetrievedAt(qSum));
      if (qSum.freshness === "stale" || !isFinite(atMs) || atMs > nowAc || (nowAc - atMs) > 1800000) return "olderLocal";
      return "recentLocal";
    }
    cards.forEach(function (cCard) {
      var kClass = classifyAccountCard(cCard);
      acctCounts[kClass === "rejected" ? "endedOrNone" : kClass]++;
      if (cCard.provider) provCounts[cCard.provider] = (provCounts[cCard.provider] || 0) + 1;
    });
    if (cards.length) {
      var strip = el("section", "acct-summary-strip");
      strip.setAttribute("aria-label", "Account evidence summary");
      [
        ["num-good", acctCounts.recentLocal, "Recent local read \u00B7 provider time unknown"],
        ["", acctCounts.olderLocal, "Older local read \u00B7 provider time unknown"],
        ["num-limit", acctCounts.atLimit, "At saved limit (100%)"],
        ["", acctCounts.endedOrNone, "Window ended, unavailable, or rejected"],
        ["num-attn", acctCounts.drift, "Context review needed"]
      ].forEach(function (stRow) {
        var stDiv = el("div", "acct-stat");
        stDiv.appendChild(el("span", "num" + (stRow[0] ? " " + stRow[0] : ""), String(stRow[1])));
        stDiv.appendChild(el("span", "lbl", stRow[2]));
        strip.appendChild(stDiv);
      });
      pane.appendChild(strip);

      var acctBar = el("div", "acct-toolbar");
      var fGroup = el("div", "map-filters");
      [
        ["all", "All (" + cards.length + " selected)"],
        ["drift", "Needs review (" + acctCounts.drift + ")"],
        ["limit", "At saved limit (" + acctCounts.atLimit + ")"]
      ].concat(Object.keys(provCounts).map(function (pName) {
        return ["prov:" + pName, (pName === "claude-code" ? "Claude Code" : pName === "codex" ? "Codex" : pName === "antigravity" ? "Antigravity" : pName) + " (" + provCounts[pName] + ")"];
      })).forEach(function (fPair) {
        var fBtn = el("button", "chip-btn", fPair[1]);
        fBtn.type = "button"; fBtn.dataset.focus = "acct-filter-" + fPair[0];
        fBtn.setAttribute("aria-pressed", String(state.acctFilter === fPair[0]));
        fBtn.addEventListener("click", function () { state.acctFilter = fPair[0]; render(false, true); });
        fGroup.appendChild(fBtn);
      });
      acctBar.appendChild(fGroup);
      var denSwitch = el("div", "view-switch");
      denSwitch.setAttribute("role", "group"); denSwitch.setAttribute("aria-label", "Account card density");
      [["detailed", "Detailed"], ["compact", "Compact"]].forEach(function (dPair) {
        var dBtn = el("button", null, dPair[1]);
        dBtn.type = "button"; dBtn.dataset.focus = "acct-density-" + dPair[0];
        dBtn.setAttribute("aria-pressed", String(state.acctDensity === dPair[0]));
        dBtn.addEventListener("click", function () { state.acctDensity = dPair[0]; render(false, true); });
        denSwitch.appendChild(dBtn);
      });
      acctBar.appendChild(denSwitch);
      pane.appendChild(acctBar);
    }
    var visibleAccts = cards.filter(function (cCard) {
      if (route().key === cCard.id) return true;
      if (state.acctFilter === "drift") return classifyAccountCard(cCard) === "drift";
      if (state.acctFilter === "limit") return classifyAccountCard(cCard) === "atLimit";
      if (state.acctFilter.indexOf("prov:") === 0) return cCard.provider === state.acctFilter.slice(5);
      return true;
    });
    pane.appendChild(el("p", "account-count", visibleAccts.length + " of " + cards.length + (cards.length === 1 ? " selected account" : " selected accounts")));
    var templates = cards.filter(accountConnectSupported), eligibleChecks = accountCheckSupported() ? cards.filter(accountCheckEligible) : [];
    if (templates.length || eligibleChecks.length) {
      var add = el("div", "account-quota-actions");
      if (eligibleChecks.length) {
        var checkAllLabel = accountChecks.active
          ? "Checking accounts (" + Math.min(accountChecks.done + 1, accountChecks.total) + " of " + accountChecks.total + ")…"
          : "Check accounts";
        var checkAllBtn = button(checkAllLabel, "account-check-all", "", function () {
          eligibleChecks.forEach(function (item) { delete accountChecks.attempted[item.id]; });
          runAccountChecks(eligibleChecks, true);
        }, accountChecks.active);
        add.appendChild(checkAllBtn);
      }
      ["claude-code", "codex"].forEach(function (provider) {
        var template = templates.find(function (c) { return c.provider === provider; });
        if (!template) return;
        add.appendChild(button("Connect another " + connectProvider(template) + " account", "connect-another-" + provider, "", function () {
          state.accountOpen[template.id] = true; render(false, true); focusOn("connect-label-" + template.id);
        }));
      });
      pane.appendChild(add);
    } else pane.appendChild(addAccountBox(!cards.length));
    if (accountChecks.active) {
      var checkProg = el("p", "sub account-check-status", "Checking local account status (" + Math.min(accountChecks.done + 1, accountChecks.total) + " of " + accountChecks.total + ")…");
      checkProg.setAttribute("role", "status");
      pane.appendChild(checkProg);
    } else if (accountChecks.notice) {
      var checkNotice = el("p", "sub account-check-status", accountChecks.notice);
      checkNotice.setAttribute("role", "status");
      pane.appendChild(checkNotice);
    }
    if (!d.complete || (d.registry || {}).availability !== "available") pane.appendChild(el("p", "sub", "Not every loop could be read, so some routes may be missing here."));
    var ul = el("ul", "accounts-list acct-cards"), retained = Object.create(null);
    ul.dataset.density = state.acctDensity;
    visibleAccts.forEach(function (c) {
      var bucketKind = classifyAccountCard(c);
      var li = el("li", "account-card" + (c.needsPerson || bucketKind === "drift" || bucketKind === "rejected" ? " is-attention" : "")), details = el("details");
      details.open = Object.prototype.hasOwnProperty.call(state.accountOpen, c.id) ? !!state.accountOpen[c.id] : route().key === c.id; retained[c.id] = details.open;
      var summary = el("summary", "account-summary");
      summary.setAttribute("data-focus", "account-" + c.id);
      var name = el("span", "account-name", c.label);
      var provider = el("span", "sub", c.provider === "claude-code" ? "Claude Code" : c.provider === "codex" ? "Codex" : c.provider === "antigravity" ? "Antigravity" : c.provider);
      var identity = el("span", "account-title"); identity.appendChild(name); identity.appendChild(provider);
      identity.appendChild(el("span", "sub", c.selection === "inherited" ? "Inherited context" : c.selection === "explicit_directory" ? "Selected context" : "External context"));
      var badgeWrap = el("span", "acct-badge-row");
      if (bucketKind === "drift") badgeWrap.appendChild(uiBadge("attn", "Context review needed \u2014 not a quota limit"));
      else if (bucketKind === "rejected") badgeWrap.appendChild(uiBadge("warn", "Quota provenance rejected"));
      else if (bucketKind === "atLimit") {
        badgeWrap.appendChild(uiBadge("limit", "At saved limit (100%)"));
        badgeWrap.appendChild(uiBadge("plain", "Provider freshness unknown"));
      } else if (bucketKind === "recentLocal" || bucketKind === "olderLocal") {
        badgeWrap.appendChild(uiBadge("plain", "Provider freshness unknown"));
      }
      if (badgeWrap.childNodes.length) identity.appendChild(badgeWrap);
      summary.appendChild(identity);
      var status = el("span", "account-status");
      if (c.auth && c.auth.state === "unchecked" && c.localAuth) {
        var localWrap = el("div", "account-evidence");
        localWrap.appendChild(el("span", null, c.localAuth === "file_present" ? "Local login file present" : c.localAuth === "file_expired" ? "Local login file expired" : "No local login file"));
        localWrap.appendChild(el("span", "sub", c.localAuth === "file_present" ? "Credential file found; CLI check has not run" : c.localAuth === "file_expired" ? "Saved token expired; sign in again and check" : "Not checked with CLI yet"));
        status.appendChild(localWrap);
      } else {
        status.appendChild(accountEvidence(c.auth));
      }
      // The login and plan shown are the last good check; say when and why the
      // latest one failed, once, with the warning when it needs a person.
      var failed = c.attention === "check_failed" || c.attention === "context_changed" || !!c.failedCheckAt;
      if (c.needsPerson) {
        var need = el("span", "account-attention");
        need.appendChild(tag("needs", "Needs you"));
        need.appendChild(el("span", null, failed ? failedCheckLine(c) : ACCOUNT_ATTENTION[c.attention] || "Something on this account needs a look."));
        status.appendChild(need);
      } else if (failed) status.appendChild(el("span", "sub", failedCheckLine(c)));
      var reading = accountReading(byCard[c.id]);
      if (reading) status.appendChild(reading);
      summary.appendChild(status);
      var plan = el("span", "account-plan");
      plan.appendChild(el("span", "sub", "Reported plan"));
      var auth = c.auth || {}, sameCheck = !!(c.plan && c.plan.observedAt && c.plan.observedAt === auth.observedAt && c.plan.source === auth.source);
      var suppressDupPlan = c.plan && !c.plan.value && ((c.plan.missing === "not_checked" && auth.state !== "reported_present") || (auth.state === "unavailable" && c.plan.text === "Unavailable"));
      if (suppressDupPlan) {
        var dash = el("span", "sub", "—");
        dash.setAttribute("aria-label", auth.state === "unchecked" ? "Reported plan unavailable until checked" : "Reported plan unavailable");
        plan.appendChild(dash);
      } else {
        plan.appendChild(accountEvidence(c.plan, sameCheck));
      }
      summary.appendChild(plan);
      var qs = accountQuotaSummary(c);
      var compact = el("span", "quota-compact");
      if (qs && qs.state === "reported" && bucketKind !== "drift") {
        var qsRows = list(qs.rows), nowSummary = Date.now();
        var sumAllEnded = qsRows.length > 0 && qsRows.every(function (w) { return accountWindowEnded(w.resetsAt, w.ended, nowSummary); });
        if (sumAllEnded) {
          compact.appendChild(el("strong", null, "Window ended \u00B7 current quota unknown"));
          compact.appendChild(accountQuotaStamp(qs, true));
        } else {
          qsRows.filter(function (w) { return typeof w.usedPercent === "number"; }).slice(0, 2).forEach(function (w) {
            var item = el("span", "quota-chip"); item.appendChild(el("span", "sub", w.name));
            accountWindowValue(item, w, true);
            if (w.resetsAt) item.appendChild(accountClock("span", "sub quota-countdown", "reset", w.resetsAt));
            else item.appendChild(el("span", "sub", "Reset time unavailable"));
            compact.appendChild(item);
          });
          if (!compact.childNodes.length) compact.appendChild(el("span", "sub", qs.partial ? "Partial quota evidence" : "Quota details recorded"));
          compact.appendChild(accountQuotaStamp(qs, true));
          compact.title = "Saved account quota; expand for all recorded detail";
        }
      } else if (c.attention === "context_changed" && changesOn() && state.server && state.server.accountSelectionReview && (c.provider === "codex" || c.provider === "claude-code" || c.provider === "antigravity")) {
        var reviewBtn = button("Review selection\u2026", "selection-review-summary-" + c.id, "", function (event) {
          event.preventDefault(); event.stopPropagation();
          state.accountOpen[c.id] = true;
          accountQuotaRequest(c, false, true);
        });
        reviewBtn.setAttribute("aria-label", "Review selection for " + c.label);
        compact.appendChild(reviewBtn);
      } else if (bucketKind === "drift") {
        compact.appendChild(el("strong", null, "Prior quota withheld"));
        compact.appendChild(el("span", "sub", "Context validation changed \u2014 not a quota limit."));
      } else if ((c.provider === "codex" || c.provider === "claude-code") && accountSignInNeeded(c)) {
        compact.appendChild(el("span", "sub", "Account quota unavailable"));
        var connect = button("Connect " + connectProvider(c), "account-connect-" + c.id, "", function (event) { event.preventDefault(); event.stopPropagation(); accountConnectOpen(c); });
        connect.setAttribute("aria-label", "Connect " + connectProvider(c) + " for " + c.label);
        compact.appendChild(connect);
        if (c.auth && c.auth.state === "reported_absent") compact.appendChild(el("span", "sub", "Sign-in required"));
      } else if (bucketKind === "rejected") {
        compact.appendChild(el("strong", null, "Quota reading rejected"));
        compact.appendChild(el("span", "sub", "Quota provenance metadata was invalid or unsupported. No usage is assumed."));
      } else {
        compact.appendChild(el("strong", null, "No saved quota reading"));
        compact.appendChild(el("span", "sub", (c.quota && c.quota.summary && c.quota.summary.state !== "reported" && c.quota.summary.text) || accountQuotaReason((c.quota && c.quota.summary && c.quota.summary.state === "reported" ? "schema_unavailable" : (c.quota && c.quota.reason)) || "not_checked")));
        compact.appendChild(el("span", "sub", "Expand for quota details and available actions"));
      }
      if (c.usageUpdating) {
        var updatingLabel = ({ enabled: "Updates on", off: "Updates off", backoff: "Fetch failed \u00B7 retrying", waiting: "Updates paused \u00B7 scheduler not running", paused: "Updates paused \u00B7 selection changed", unavailable: "Update settings unavailable" })[c.usageUpdating.state];
        if (updatingLabel) compact.appendChild(el("span", "sub", updatingLabel));
      } else {
        compact.appendChild(el("span", "sub", "Updates not reported by this panel build"));
      }
      summary.appendChild(compact);
      details.appendChild(summary);
      var body = el("div", "account-body");
      var connection = accountConnectBox(c);
      if (connection) body.appendChild(connection);
      if (accountConnectSupported(c)) {
        var another = el("form", "account-connect-another"), nameID = "connect-label-" + c.id;
        another.setAttribute("aria-label", "Connect another " + connectProvider(c) + " account"); another.method = "post";
        var label = el("label", null, "Name for another " + connectProvider(c) + " account (required)"); label.htmlFor = nameID;
        var input = el("input"); input.id = nameID; input.name = "accountLabel"; input.type = "text"; input.maxLength = 120; input.required = true; input.autocomplete = "off"; input.value = accountConnectionLabels[c.id] || ""; input.setAttribute("data-focus", "connect-label-" + c.id);
        var submit = el("button", "btn", "Connect another " + connectProvider(c) + " account"); submit.type = "submit";
        var ongoing = accountConnections[c.id];
        input.disabled = submit.disabled = !!(ongoing && (ongoing.busy || (ongoing.connect && ongoing.connect.state === "waiting")));
        another.appendChild(label); another.appendChild(input); another.appendChild(submit);
        another.appendChild(el("p", "sub", "Uses this account’s CLI and working directory with a new private login. This account and its routes stay unchanged."));
        another.addEventListener("submit", function (event) {
          event.preventDefault();
          var name = input.value.trim(), active = accountConnections[c.id];
          if (!name || new TextEncoder().encode(name).length > 120 || /[\x00-\x1f\x7f]/.test(name)) { input.setCustomValidity("Enter a name of at most 120 bytes, without control characters."); input.reportValidity(); return; }
          if (active && (active.busy || (active.connect && active.connect.state === "waiting"))) { focusOn("connect-result-" + c.id); return; }
          accountConnections[c.id] = { newLabel: name }; accountConnectRequest(c, "prepare");
        });
        input.addEventListener("input", function () { accountConnectionLabels[c.id] = input.value; input.setCustomValidity(""); });
        body.appendChild(another);
      }
      var facts = el("dl", "work-facts");
      if (state.identifiers) facts.appendChild(fact("Connection ID", c.id));
      facts.appendChild(fact("Account identity", "Not verified. The name is your selected label."));
      var use = byCard[c.id];
      facts.appendChild(fact("Routes", use && list(use.routes).length ? accountRouteList(d, use, true) : accountRoutes(c)));
      body.appendChild(facts);
      if (c.provider === "codex" || c.provider === "claude-code" || c.provider === "antigravity") body.appendChild(accountQuotaBox(c));
      // The catalog records no model: a route chooses it, and a check never
      // calls one. Said once, quietly, in place of two empty rows.
      if (c.configuredModel && c.configuredModel.missing === "not_recorded" && c.observedModel && c.observedModel.missing === "not_recorded") body.appendChild(el("p", "sub", "Models are not recorded here: a route chooses the model, and a check never calls one."));
      else {
        facts.appendChild(fact("Configured model", accountEvidence(c.configuredModel)));
        facts.appendChild(fact("Actual response model", accountEvidence(c.observedModel)));
      }
      var reasons = {
        context_changed: "This selection was saved under a different shell, context, or CLI path (such as Terminal vs an agent session or an updated binary). Review it for this panel or run looptender accounts update before checking again.",
        unsupported: "Authentication is externally managed or unsupported here. Use the provider's own controls.",
        timeout: "The latest status check timed out. It did not establish a current login.",
        cancelled: "The latest status check was cancelled.",
        version_unavailable: "The selected CLI version could not be established.",
        status_unavailable: "The latest status command did not establish a recognized login result."
      };
      if (c.reason) body.appendChild(el("p", "account-note", reasons[c.reason] || "The latest account check is unavailable."));
      if (accountCheckSupported(c) && accountCheckEligible(c)) {
        var cardActions = el("div", "account-quota-actions");
        cardActions.appendChild(button(accountChecks.active ? "Checking…" : "Check login now", "account-check-" + c.id, "", function () {
          delete accountChecks.attempted[c.id];
          runAccountChecks([c], true);
        }, accountChecks.active));
        body.appendChild(cardActions);
      }
      var removeBox = accountRemoveBox(c);
      if (removeBox) body.appendChild(removeBox);
      details.appendChild(body);
      details.addEventListener("toggle", function () { if (details.isConnected) state.accountOpen[c.id] = details.open; });
      li.appendChild(details);
      // A changed context is updated before it is checked again, so it gets no check command.
      if (c.attention !== "context_changed" && (c.needsPerson || (c.auth && c.auth.checkAgain) || (c.plan && c.plan.checkAgain))) li.appendChild(accountCheckHint(c));
      ul.appendChild(li);
    });
    state.accountOpen = retained;
    if (cards.length) pane.appendChild(ul);
    if (unnamed.length) pane.appendChild(unknownAccountSection(d, unnamed));
    if (d.accountUsesOmitted) pane.appendChild(el("p", "more", d.accountUsesOmitted + " more account entries or route contexts are not shown."));
    // One footnote for the page, naming the command. With one card its ID
    // fills in as on the card's own hint.
    var note = el("p", "sub account-footnote");
    note.appendChild(document.createTextNode("Login and plan reports do not verify quota, model access or continuing availability. Opening Accounts in a panel with changes allowed runs one bounded check for unchecked or older accounts; to check again manually, run "));
    if (!cards.length) return;
    note.appendChild(accountCheckCommand(cards.length === 1 ? cards[0] : null));
    note.appendChild(document.createTextNode(cards.length === 1 && state.identifiers ? "." : state.identifiers ? " with the card's connection ID." : " with the card's connection ID (Show identifiers to see it)."));
    pane.appendChild(note);
    updateAccountClocks();
    maybeAutoCheckAccounts(cards);
  }

  // ---- How to add an account, add a route and change the order ----
  // Text and commands to copy, for a person to run in Terminal. The page
  // runs nothing and changes nothing. A value the page does not know is a
  // placeholder in capitals, with one line saying what to put there.
  var IN_TERMINAL = "Run in Terminal, not in a BB agent thread.";
  var NAMES_ONLY = "This names the account. It does not put it in any loop. To use it, add it to a loop's order.";
  var FIRST_RUN = "An account is a sign-in you already have, with a name you choose. A route is one model on one of your accounts. Each loop works down its own order of routes: it uses the first that has room, moves to the next when one runs out, and moves back up when it can.";
  var ROUTE_ADD_FIELDS = "Put a new ID in place of ROUTE_ID (lowercase letters, digits and hyphens), claude-code or codex for PROVIDER, the model for MODEL, the usage limit it draws on for GROUP, and same_conversation, new_conversation or headless for ENTER.";
  // Whether each disclosure is open, by its key, kept across re-renders.
  var howOpen = Object.create(null);
  function howBox(key, title, open) {
    var d = el("details", "how");
    d.open = key in howOpen ? howOpen[key] : !!open;
    var s = el("summary", "how-summary", title);
    s.setAttribute("data-focus", key);
    d.appendChild(s);
    d.addEventListener("toggle", function () { if (d.isConnected) howOpen[key] = d.open; });
    return d;
  }
  // One command: what it does, which also describes its copy button, the
  // command, and the line that says what to put in place of a placeholder.
  function howStep(focus, what, command, fill, describedBy) {
    var w = el("div", "how-step");
    var p = el("p", null, what);
    p.id = focus + "-what";
    w.appendChild(p);
    w.appendChild(copyCommand("div", command, focus, (describedBy ? describedBy + " " : "") + p.id));
    if (fill) w.appendChild(el("p", "sub", fill));
    return w;
  }
  // Every block ends with where to run its commands.
  function howEnd(box, body) {
    body.appendChild(el("p", "how-terminal", IN_TERMINAL));
    box.appendChild(body);
    return box;
  }
  // "--loop NAME" with the name the server quoted for a shell, or a
  // placeholder when the name is too long to put in a command.
  function loopArg(l) { return l.loopArg || "--loop LOOP_NAME"; }
  var LOOP_NAME_FILL = "Put this loop's name in place of LOOP_NAME, in quotes if it has a space.";
  function withLoopFill(l, fill) { return l.loopArg ? fill : [fill, LOOP_NAME_FILL].filter(Boolean).join(" "); }
  var ROUTE_ID = /^[a-z0-9-]+$/;

  function addAccountBox(open) {
    var box = howBox("how-account", "Add an account", open), body = el("div", "how-body");
    var cases = [
      ["folder", "Another subscription of a provider you use",
        "Sign in with the provider's own tool in a new folder, then name that folder here.",
        "looptender accounts add --label ACCOUNT_LABEL --provider PROVIDER --selection explicit_directory --account-dir FOLDER",
        "Put a name you choose in place of ACCOUNT_LABEL (in quotes if it has a space), claude-code or codex for PROVIDER, and the folder you signed in to for FOLDER."],
      ["managed", "A provider that manages its own login",
        "Cursor and BB keep their own login. Name the account here; looptender does not check it.",
        "looptender accounts add --label ACCOUNT_LABEL --provider PROVIDER --selection externally_managed",
        "Put a name you choose in place of ACCOUNT_LABEL, and cursor or bb for PROVIDER."],
      ["key", "An API key",
        "Give the key its own entry. If you use it through the provider's tool, sign in with it in its own folder and name that folder as above. Otherwise:",
        "looptender accounts add --label ACCOUNT_LABEL --provider other --selection externally_managed",
        "Put a name you choose in place of ACCOUNT_LABEL. looptender never takes the key."]
    ];
    cases.forEach(function (c) {
      var h = el("h3", "how-case", c[1]);
      h.id = "how-account-" + c[0];
      body.appendChild(h);
      body.appendChild(howStep("copy-account-" + c[0], c[2], c[3], c[4], h.id));
    });
    // Said once, after the three cases.
    body.appendChild(el("p", null, NAMES_ONLY));
    return howEnd(box, body);
  }

  // The loop page: add a route to this loop, and change its order. A loop
  // that follows the standard route list changes that list instead.
  function addRouteBox(l) {
    var c = l.chain || {}, arg = loopArg(l);
    var box = howBox("how-route-" + l.key, "Add a route to this loop"), body = el("div", "how-body");
    if (c.standard) {
      body.appendChild(el("p", null, "This loop follows your standard route list, which every loop that follows it uses. Add the route to that list."));
      body.appendChild(howStep("copy-route-add", "Add a route to the standard list.",
        "looptender routes add --id ROUTE_ID --provider PROVIDER --model MODEL --group GROUP --enter ENTER", ROUTE_ADD_FIELDS));
      body.appendChild(el("p", null, "A new route is used only after a check of it passes."));
      body.appendChild(howStep("copy-route-check", "Check it. This is one real request and uses this account's quota.",
        "looptender routes check ROUTE_ID --exact " + arg, withLoopFill(l, "Put the new route's ID in place of ROUTE_ID.")));
      if (!list(c.handoffOn).length) body.appendChild(el("p", null, "Automatic handoff is off for this loop, so it does not move to another route by itself. For a loop that follows the standard list, it is set by errors.failover_on in the loop's configuration file."));
    } else {
      body.appendChild(howStep("copy-route-add", "Add a route. A guide asks for the provider, the account, the model and its place in the order, and shows a preview before it changes anything.",
        "looptender fallbacks add " + arg, withLoopFill(l, "")));
      body.appendChild(el("p", null, "A new route is used only after a check of it passes."));
      body.appendChild(howStep("copy-route-check", "Check it. This is one real request and uses this account's quota.",
        "looptender fallbacks check --id ROUTE_ID --exact " + arg, withLoopFill(l, "Put the new route's ID in place of ROUTE_ID; the guide shows it in its preview.")));
      if (!list(c.handoffOn).length) body.appendChild(howStep("copy-route-handoff", "Automatic handoff is off for this loop, so it does not move to another route by itself. To turn it on:",
        "looptender fallbacks handoff on " + arg, withLoopFill(l, "")));
    }
    return howEnd(box, body);
  }
  function reorderBox(l) {
    var c = l.chain || {}, rest = list(c.entries).filter(function (e) { return e.position > 0; });
    if (rest.length + (c.omitted || 0) < 2) return null;
    var box = howBox("how-order-" + l.key, "Change the order"), body = el("div", "how-body");
    var move = c.standard ? function (id) { return "looptender routes move " + id + " --to PLACE"; } :
      function (id) { return "looptender fallbacks move " + id + " --to PLACE " + loopArg(l); };
    body.appendChild(el("p", null, c.standard ?
      "This loop follows your standard route list, so the order is that list's, and every loop that follows it gets the new order. Put the route's new place in the list in place of PLACE: 1 is the top." :
      "The first choice stays first. Put the route's new place after it in place of PLACE: 1 is the next choice."));
    var ids = rest.filter(function (e) { return e.id && ROUTE_ID.test(e.id); });
    if (state.identifiers && ids.length === rest.length) {
      rest.forEach(function (e) {
        body.appendChild(howStep("copy-move-" + e.position, "Choice " + (e.position + 1) + ": " + e.title, move(e.id), ""));
      });
      if (!c.standard && !l.loopArg) body.appendChild(el("p", "sub", LOOP_NAME_FILL));
    } else {
      var fill = "Put the route's ID in place of ROUTE_ID.";
      if (!state.identifiers) fill = "Put the route's ID in place of ROUTE_ID. Show identifiers to see each route's ID.";
      body.appendChild(howStep("copy-move", "Move a route.", move("ROUTE_ID"), c.standard ? fill : withLoopFill(l, fill)));
    }
    return howEnd(box, body);
  }
  // The Routes page: add a route to the standard list, then have a loop
  // follow it.
  // Route configuration is kept apart from background report refreshes.
  // Draft values are captured on input; a review is immutable server state.
  var retainedRouteEditor = null;
  var routeEdit = { data: null, draft: {}, models: [], busy: false, reply: null, message: "" };
  var loopRouteEdit = {
    loopKey: "",
    panel: "",
    editorSeq: 0,
    reqSeq: 0,
    modelSeq: 0,
    data: null,
    busy: false,
    loadingModels: false,
    models: [],
    modelsText: "",
    manualModel: false,
    draft: { account: "", model: "", at: "0", limitChoice: "", limit: "" },
    touched: Object.create(null),
    submitTried: false,
    pendingOp: "",
    pendingAccountLabel: "",
    reply: null,
    message: "",
    unknownOutcome: false,
    unresolvedSave: null,
    reconcileError: "",
    reconciledNote: ""
  };
  var ROUTE_DOCS = "routes-help.html";
  function routeButton(text, id, run) {
    var b = el("button", "btn", text); b.type = "button"; b.dataset.focus = id; b.disabled = routeEdit.busy;
    b.addEventListener("click", run); return b;
  }
  function routeRequest(step, input, form) {
    routeEdit.busy = true; routeEdit.message = ""; render(false, true);
    return post("api/v1/routes/" + step, form || state.server.form, input)
      .then(function (r) {
        if (r.editor) routeEdit.data = r.editor;
        else if (step === "models") { routeEdit.models = list(r.models); routeEdit.message = r.text || ""; }
        else routeEdit.reply = r;
      }).catch(function () {
        routeEdit.message = step === "confirm" ? "The save result is unknown. Read the current list before preparing another change; this save will not be retried." : "The panel did not answer. Nothing was saved. Try reading the editor again.";
        routeEdit.reply = null;
      }).then(function () {
        routeEdit.busy = false; render(false, true); focusOn("route-feedback");
        announce(routeEdit.message || (routeEdit.reply && routeEdit.reply.text) || "Route editor loaded.");
      });
  }
  function routeField(form, name, label, options, required) {
    var wrap = el("div", "route-field"), id = "route-field-" + name, control;
    wrap.appendChild(el("label", null, label)); wrap.firstChild.htmlFor = id;
    if (options) {
      control = el("select");
      options.forEach(function (o) { var opt = el("option", null, o[1]); opt.value = o[0]; control.appendChild(opt); });
    } else { control = el("input"); control.type = "text"; control.maxLength = 128; control.autocomplete = "off"; }
    control.id = id; control.name = name; control.dataset.focus = id; control.required = !!required;
    control.value = routeEdit.draft[name] || ""; control.disabled = routeEdit.busy;
    control.addEventListener("input", function () {
      if (name === "model" && routeEdit.draft.model !== control.value) { routeEdit.draft.effort = ""; var effort = $("route-field-effort"); if (effort) effort.value = ""; }
      routeEdit.draft[name] = control.value;
    });
    control.addEventListener("change", function () { routeEdit.draft[name] = control.value; if (name === "account") { routeEdit.models = []; routeEdit.draft.model = ""; routeEdit.draft.effort = ""; routeEdit.draft.behavior = ""; routeEdit.draft.newConversation = false; } if (name === "model") routeEdit.draft.effort = ""; if (options) render(false, true); });
    wrap.appendChild(control); form.appendChild(wrap); return control;
  }
  function routeCheck(parent, name, label) {
    var wrap = el("label", "route-check"), input = el("input"); input.type = "checkbox"; input.name = name;
    input.checked = !!routeEdit.draft[name]; input.disabled = routeEdit.busy;
    input.addEventListener("change", function () { routeEdit.draft[name] = input.checked; });
    wrap.appendChild(input); wrap.appendChild(el("span", null, label)); parent.appendChild(wrap); return input;
  }
  function routeProvider(provider) { return provider === "claude-code" ? "Claude Code" : provider === "codex" ? "Codex" : provider || "Configured provider"; }
  function routeTitle(row) { var label = routeEdit.data && routeEdit.data.accountLabels && routeEdit.data.accountLabels[row.id]; if (!label && routeEdit.reply && routeEdit.reply.accountLabels) label = routeEdit.reply.accountLabels[row.id]; return (row.model || "Configured default model") + (row.effort ? " · " + row.effort : "") + " · " + routeProvider(row.provider) + " · " + (label || "Account unknown") + (row.disabled ? " · Off" : ""); }
  function routeEffects(parent, r) {
    var labels = {}, beforeLabels = {};
    list(routeEdit.data && routeEdit.data.standard && routeEdit.data.standard.routes).forEach(function (row) { beforeLabels[row.id] = routeTitle(row); });
    list(routeEdit.data && routeEdit.data.loops).forEach(function (entry) { if (entry.view.loop === r.loop) list(entry.view.routes).forEach(function (row) { beforeLabels[row.id] = routeTitle(row); }); });
    list(r.routes).forEach(function (row) { labels[row.id] = routeTitle(row); });
    function names(ids, source) { return list(ids).map(function (id) { return source[id] || (id === "primary" ? "First route" : "Configured route"); }).join(" → ") || "No standby routes"; }
    var before = el("p", "sub", "Before: " + names(r.before, beforeLabels)), after = el("p", null, "After: " + names(r.after, labels));
    parent.appendChild(before); parent.appendChild(after);
    list(r.loops).forEach(function (l) {
      var item = el("div", "route-effect"); item.appendChild(el("h4", null, l.loop));
      item.appendChild(el("p", null, "Standby routes after saving: " + names(l.after, labels)));
      if (l.problem) item.appendChild(el("p", "c-warn", "Configuration unavailable; effects are unknown."));
      if (l.currentLeavesList) item.appendChild(el("p", "c-warn", "The current route leaves this list. The loop stays there until its ordinary rules move it."));
      if (l.noRouteApplies) item.appendChild(el("p", "c-warn", "No route in this list applies to this loop."));
      if (l.belowMinHealthy) item.appendChild(el("p", "c-warn", "Recorded healthy routes are below this loop's minimum."));
      list(l.newlySkipped).forEach(function (skip) { item.appendChild(el("p", "sub", (labels[skip.id] || "A route") + ": " + skip.reason)); });
      parent.appendChild(item);
    });
    list(r.warnings).forEach(function (text) { parent.appendChild(el("p", "c-warn", text)); });
    if (r.refusal) parent.appendChild(el("p", "c-warn", r.refusal.text));
  }
  function routeReview(parent) {
    var r = routeEdit.reply;
    if (!r && !routeEdit.message && !routeEdit.busy) return false;
    var box = el("section", "route-feedback"); box.id = "route-feedback"; box.tabIndex = -1; box.dataset.focus = "route-feedback";
    box.setAttribute("aria-labelledby", "route-review-title");
    var title = el("h3", null, r && r.form ? "Review route change" : "Route update"); title.id = "route-review-title"; box.appendChild(title);
    box.appendChild(el("p", null, routeEdit.busy ? "Waiting for the panel…" : routeEdit.message || (r && r.text) || ""));
    if (r) list(r.lines).forEach(function (line) { box.appendChild(el("p", null, line)); });
    if (r) list(r.changes).forEach(function (change) {
      if (change.loop) box.appendChild(el("h4", null, change.loop + (r.form ? "" : ": " + change.outcome.replace(/_/g, " "))));
      routeEffects(box, change);
    });
    if (r && r.form) {
      box.appendChild(routeButton("Save reviewed change", "route-save", function () {
        var op = r.changes[0].op;
        // Spend the local confirmation before sending; a lost response is
        // an unknown outcome, never an automatic retry.
        var form = r.form; routeEdit.reply = null;
        routeRequest("confirm", { op: op }, form).then(function () { routeEdit.data = null; render(false, true); });
      }));
      box.appendChild(routeButton("Cancel review", "route-cancel", function () { routeEdit.reply = null; render(false, true); focusOn("route-edit-refresh"); }));
    }
    parent.appendChild(box); return !!(r && r.form);
  }
  function routeEditorBox() {
    if (retainedRouteEditor) return retainedRouteEditor;
    if (!changesOn() || !state.server || state.server.routeChanges !== "person") {
      var bar = el("p", "route-readonly-bar sub");
      bar.appendChild(document.createTextNode(changesOn() && state.server && state.server.routeChanges === "bb_caller" ?
        "Read-only for route edits (opened from BB). " :
        "Read-only window for route edits. "));
      bar.appendChild(link(ROUTE_DOCS, "How to open an owner panel"));
      return bar;
    }
    var box = panel("Configure preferred routes"); box.classList.add("route-editor");
    box.appendChild(el("p", "sub", "One standard order for loops that follow it. Saving a list does not enroll other loops."));
    var reviewing = routeReview(box);
    box.appendChild(routeButton("Read current configuration", "route-edit-refresh", function () { routeEdit.reply = null; routeRequest("read", { op: "read" }); }));
    var data = routeEdit.data;
    if (!data || reviewing || routeEdit.busy) { box.appendChild(link(ROUTE_DOCS, "Route configuration help", "more")); return box; }
    if (data.reason) { box.appendChild(el("p", "c-warn", data.reason)); return box; }
    var standard = data.standard || {}, rows = list(standard.routes);
    box.appendChild(el("h3", null, "Preferred order"));
    var order = el("ol", "route-edit-order");
    rows.forEach(function (row, i) {
      var li = el("li"), name = routeTitle(row);
      li.appendChild(el("p", null, name));
      function action(label, op, to) { return routeButton(label, "route-" + op + "-" + row.id + "-" + to, function () { routeRequest("prepare", { op: op, id: row.id, to: to || 0 }); }); }
      var controls = el("div", "route-actions");
      if (i) controls.appendChild(action("Move earlier", "move", i));
      if (i < rows.length - 1) controls.appendChild(action("Move later", "move", i + 2));
      controls.appendChild(action(row.disabled ? "Turn on" : "Turn off", row.disabled ? "on" : "off"));
      controls.appendChild(action("Remove", "remove"));
      Array.prototype.forEach.call(controls.querySelectorAll("button"), function (b) { b.setAttribute("aria-label", b.textContent + ": " + name); });
      li.appendChild(controls); order.appendChild(li);
    });
    box.appendChild(order);
    if (!rows.length) box.appendChild(el("p", "sub", standard.state === "unreadable" ? "The standard list cannot be read. Resolve it before editing." : "No standard routes yet. Add your first route below."));
    if (list(standard.can).indexOf("add") >= 0) {
      var form = el("form", "route-add-form"); form.method = "post"; form.action = "api/v1/routes/prepare"; form.setAttribute("aria-labelledby", "route-add-title");
      var heading = el("h3", null, "Add a route"); heading.id = "route-add-title"; form.appendChild(heading);
      var accounts = list(data.accounts), chosen = accounts.filter(function (a) { return a.id === routeEdit.draft.account; })[0];
      routeField(form, "account", "Existing account selection", [["", "Choose an account"]].concat(accounts.map(function (a) { return [a.id, a.label + " · " + routeProvider(a.provider)]; })), true);
      form.appendChild(el("p", "sub", "You state which account this route uses. Selection does not verify a live thread's identity, model access or available quota."));
      if (!accounts.length) form.appendChild(link("#/accounts", "No supported account selections. View Accounts."));
      if (chosen) {
        if (routeEdit.models.length) form.appendChild(routeButton("Enter model names instead", "route-model-manual", function () { routeEdit.models = []; render(false, true); }));
        form.appendChild(routeButton("Load models reported by BB", "route-models", function () { routeRequest("models", { op: "models", provider: chosen.provider }); }));
        form.appendChild(el("p", "sub", "Model listing is optional and read-only. Without a listing, enter names supported by your provider."));
        if (routeEdit.models.length) {
          var offered = routeEdit.models.filter(function (m) { return m.model === routeEdit.draft.model; })[0];
          if (!offered) { routeEdit.draft.model = ""; routeEdit.draft.effort = ""; }
          else if (!list(offered.supportedReasoningEfforts).some(function (e) { return e.reasoningEffort === routeEdit.draft.effort; })) routeEdit.draft.effort = "";
        }
        var modelOptions = routeEdit.models.length ? [["", "Choose a model"]].concat(routeEdit.models.map(function (m) { return [m.model, m.displayName || m.model]; })) : null;
        routeField(form, "model", "Model", modelOptions, true);
        var model = routeEdit.models.filter(function (m) { return m.model === routeEdit.draft.model; })[0];
        var efforts = model ? list(model.supportedReasoningEfforts).map(function (e) { return [e.reasoningEffort, e.reasoningEffort]; }) : null;
        if (chosen.provider === "codex" && chosen.selection !== "externally_managed") form.appendChild(el("p", "sub", "Codex CLI routes have no effort override."));
        else routeField(form, "effort", "Effort (optional)", efforts ? [["", "Provider default"]].concat(efforts) : null, false);
        var details = el("details", "route-settings"); details.appendChild(el("summary", null, "How conversation and limit choices work"));
        var behavior = chosen.selection === "externally_managed" ? [["", "Choose conversation behavior"], ["same_conversation", "Continue the loop's BB conversation"], ["new_conversation", "Start a new BB conversation at handoff"]] : [["", "Choose execution behavior"], ["headless", "Run the selected CLI without a conversation window"]];
        routeField(form, "behavior", "How this route runs", behavior, true);
        if (routeEdit.draft.behavior === "new_conversation") routeCheck(form, "newConversation", "Allow this route to start a conversation at handoff. Each loop must also opt in; its existing creation cap and spacing still apply.").required = true;
        details.appendChild(el("p", "sub", "A conversation route uses that conversation's existing login; it does not switch credentials. A CLI route keeps each loop's executable and working directory, using its inherited login or the selected existing directory."));
        var limits = [];
        rows.forEach(function (r) { if (limits.indexOf(r.group) < 0) limits.push(r.group); });
        routeField(form, "limitChoice", "Which recorded limit does it share?", [["", "Choose a limit relationship"], ["new", "New label (relationship checked)"]].concat(limits.map(function (g) { return [g, "Share the limit labelled " + g]; })), true);
        if (routeEdit.draft.limitChoice === "new") routeField(form, "limit", "New limit label", null, true);
        details.appendChild(el("p", "sub", "Labels record your configuration. They do not prove separate subscriptions or independent quota. Routes sharing limits must share a label."));
        form.appendChild(details);
        var submit = el("button", "btn", "Review new route"); submit.type = "submit"; submit.dataset.focus = "route-add-review"; form.appendChild(submit);
      }
      form.addEventListener("submit", function (ev) {
        ev.preventDefault(); if (!form.reportValidity()) return;
        var values = new FormData(form), limitChoice = values.get("limitChoice");
        routeRequest("prepare", { op: "add", account: values.get("account"), model: values.get("model"), effort: values.get("effort") || "", behavior: values.get("behavior"), newConversation: values.get("newConversation") === "on", limit: limitChoice === "new" ? values.get("limit") : limitChoice });
      }); box.appendChild(form);
    }
    if (rows.length) {
      var follow = el("form", "route-follow-form"); follow.method = "post"; follow.action = "api/v1/routes/prepare";
      var fieldset = el("fieldset"); fieldset.appendChild(el("legend", null, "Loops following this order"));
      list(data.loops).forEach(function (entry) {
        var view = entry.view, allowed = list(view.can).indexOf("follow_standard") >= 0;
        if (view.list === "standard") { fieldset.appendChild(el("p", "sub", view.loop + " · Already follows the standard list")); return; }
        var cb = routeCheck(fieldset, "loop-" + entry.key, view.loop + (allowed ? (view.handoff === "off" ? " · Handoff is off and stays off" : "") : " · Cannot follow now: " + (view.canReason || view.problem || "not available").replace(/_/g, " ")));
        cb.disabled = !allowed;
      });
      follow.appendChild(fieldset);
      follow.appendChild(el("p", "sub", "Review and save this separately from route edits. Each loop keeps its holds, budgets, handoff settings and conversation opt-in. Saves are per loop, not atomic."));
      var submit = el("button", "btn", "Review selected loops"); submit.type = "submit"; follow.appendChild(submit);
      follow.addEventListener("submit", function (ev) { ev.preventDefault(); var selected = list(data.loops).filter(function (e) { return list(e.view.can).indexOf("follow_standard") >= 0 && routeEdit.draft["loop-" + e.key]; }).map(function (e) { return e.key; }); if (selected.length > 64) { routeEdit.message = "Select up to 64 loops per review."; render(false, true); focusOn("route-feedback"); return; } routeRequest("prepare", { op: "follow_standard", loops: selected }); });
      box.appendChild(follow);
    }
    box.appendChild(link(ROUTE_DOCS, "Route configuration help and CLI reference", "more")); return box;
  }

  function loopRouteReset(loopKey, nextPanel) {
    loopRouteEdit.editorSeq += 1;
    loopRouteEdit.reqSeq += 1;
    loopRouteEdit.modelSeq += 1;
    loopRouteEdit.loopKey = loopKey || "";
    loopRouteEdit.panel = nextPanel || "";
    loopRouteEdit.data = null;
    loopRouteEdit.busy = false;
    loopRouteEdit.loadingModels = false;
    loopRouteEdit.models = [];
    loopRouteEdit.modelsText = "";
    loopRouteEdit.manualModel = false;
    loopRouteEdit.draft = { account: "", model: "", at: "0", limitChoice: "", limit: "" };
    loopRouteEdit.touched = Object.create(null);
    loopRouteEdit.submitTried = false;
    loopRouteEdit.pendingOp = "";
    loopRouteEdit.pendingAccountLabel = "";
    loopRouteEdit.reply = null;
    loopRouteEdit.message = "";
    loopRouteEdit.unknownOutcome = false;
    loopRouteEdit.unresolvedSave = null;
    loopRouteEdit.reconcileError = "";
    loopRouteEdit.reconciledNote = "";
  }
  function loopRouteSwitchPanel(nextPanel) {
    loopRouteEdit.editorSeq += 1;
    loopRouteEdit.reqSeq += 1;
    loopRouteEdit.modelSeq += 1;
    loopRouteEdit.panel = nextPanel || "";
    loopRouteEdit.busy = false;
    loopRouteEdit.loadingModels = false;
    loopRouteEdit.modelsText = "";
    loopRouteEdit.pendingOp = "";
    loopRouteEdit.pendingAccountLabel = "";
    loopRouteEdit.reply = null;
    loopRouteEdit.message = "";
    loopRouteEdit.reconcileError = "";
    loopRouteEdit.reconciledNote = "";
    if (!nextPanel) {
      loopRouteEdit.unresolvedSave = null;
      loopRouteEdit.unknownOutcome = false;
    }
  }
  function panelRouteMode() {
    if (!state.server || !changesOn()) return "read_only";
    if (state.server.routeChanges === "person") return "all";
    if (state.server.routeChanges === "bb_caller") return "bb_caller";
    return "controls";
  }
  function loopRouteView(ed, key) {
    var entries = list(ed && ed.loops);
    for (var i = 0; i < entries.length; i++) {
      if (entries[i] && entries[i].key === key) return entries[i].view || null;
    }
    return null;
  }
  function loopRouteReadableView(ed, key) {
    var view = loopRouteView(ed, key);
    if (!view || view.problem || view.listUnreadable || view.canReason === "list_unreadable" || !Array.isArray(view.routes)) return null;
    if (view.list !== "own" && view.list !== "none" && view.list !== "standard") return null;
    // An unrelated standard-list failure does not invalidate an own-list read.
    if (view.list === "standard" && (!ed.standard || ed.standard.problem ||
        (ed.standard.state !== "ok" && ed.standard.state !== "missing"))) return null;
    return view;
  }
  function loopRouteEligibleAccounts(ed) {
    return list(ed && ed.accounts).filter(function (acct) {
      return (acct.provider === "claude-code" || acct.provider === "codex") &&
        (acct.selection === "inherited" || acct.selection === "explicit_directory");
    });
  }
  function loopRouteRowTitle(row, acctLabels) {
    var lbl = acctLabels && row && row.id && acctLabels[row.id];
    var pieces = [
      (row && row.model) || "Configured default model",
      row && row.effort ? row.effort : "",
      routeProvider(row && row.provider),
      lbl || "",
      row && row.group ? "limit " + row.group : "",
      row && row.disabled ? "Off" : ""
    ].filter(Boolean);
    return pieces.join(" \u00B7 ");
  }
  function loopRouteReasonText(code) {
    var map = {
      configuration_unavailable: "Loop configuration cannot be loaded. Check looptender validate in Terminal.",
      ownership_unknown: "Loop ownership records cannot be read.",
      agent_identity: "This loop has no recorded owner thread, so only a person outside BB can edit its routes.",
      not_owner: "This loop is owned by another BB thread. Only the owning thread or a person outside BB can edit its routes.",
      list_unreadable: "The route list cannot be read. Repair the file before making edits.",
      no_route_applies: "No route in the standard list applies to this loop.",
      no_list: "This loop does not have a standby list yet."
    };
    return map[code] || (code ? code.replace(/_/g, " ") + "." : "This action is not available right now.");
  }
  function loopRouteButton(labelText, focusId, cls, onClick, isDisabled, expandedVal) {
    var btn = el("button", cls || "btn", labelText);
    btn.type = "button";
    if (focusId) btn.dataset.focus = focusId;
    if (isDisabled) btn.disabled = true;
    if (expandedVal != null) btn.setAttribute("aria-expanded", expandedVal ? "true" : "false");
    btn.addEventListener("click", onClick);
    return btn;
  }
  var LOOP_ROUTE_BOILERPLATE_LINES = [
    "Headless route uses the selected account context as configured; login, model access and quota are not verified. Effort uses the CLI default.",
    "Saving runs no model or probe and does not enable handoff or clear holds and allowances.",
    "Review before saving. Holds, budgets, ownership and conversation-creation limits stay in place. This does not run a model or check account access."
  ];
  function loopRouteIsBoilerplate(lnText) {
    if (!lnText) return false;
    if (loopRouteEdit.pendingOp === "add") {
      var draft = loopRouteEdit.draft;
      var accts = loopRouteEligibleAccounts(loopRouteEdit.data);
      var limit = draft.limitChoice && draft.limitChoice !== "new" ? draft.limitChoice : (draft.limit || "").trim();
      for (var i = 0; i < accts.length; i++) {
        if (accts[i].id !== draft.account) continue;
        // Collapse only the complete known producer line. New suffixes stay visible.
        var accountLine = "Account selection: " + accts[i].label + " (" + accts[i].provider +
          "). Model: " + (draft.model || "").trim() + ". Recorded limit label: " + limit + ".";
        if (lnText === accountLine) return true;
      }
    }
    return LOOP_ROUTE_BOILERPLATE_LINES.indexOf(lnText) >= 0;
  }
  function loopRoutePartitionLines(rawLines) {
    var primary = [], details = [];
    list(rawLines).forEach(function (lnText) {
      if (!lnText) return;
      if (loopRouteIsBoilerplate(lnText)) details.push(lnText);
      else primary.push(lnText);
    });
    return { primary: primary, details: details };
  }
  function loopRouteRequest(step, bodyObj, formToken, loopKey, nextFocus, reqOpts) {
    if (!state.server || !state.server.form) return Promise.resolve();
    if (step === "models") {
      var mEditor = loopRouteEdit.editorSeq;
      var mSeq = ++loopRouteEdit.modelSeq;
      var reqAcctId = (reqOpts && reqOpts.accountId) || "";
      var reqProvider = (reqOpts && reqOpts.provider) || "";
      function modelsAlive() {
        if (loopRouteEdit.loopKey !== loopKey || loopRouteEdit.editorSeq !== mEditor || loopRouteEdit.modelSeq !== mSeq) return false;
        if (loopRouteEdit.draft.account !== reqAcctId) return false;
        var acctsNow = loopRouteEligibleAccounts(loopRouteEdit.data);
        for (var mIdx = 0; mIdx < acctsNow.length; mIdx++) {
          if (acctsNow[mIdx].id === reqAcctId) return acctsNow[mIdx].provider === reqProvider;
        }
        return false;
      }
      loopRouteEdit.loadingModels = true;
      loopRouteEdit.modelsText = "";
      render(false, true);
      return post("api/v1/routes/models", formToken || state.server.form, bodyObj)
        .then(function (res) {
          if (!modelsAlive()) return;
          loopRouteEdit.models = list(res.models);
          loopRouteEdit.modelsText = res.text || "";
          loopRouteEdit.manualModel = !loopRouteEdit.models.length;
        })
        .catch(function () {
          if (!modelsAlive()) return;
          loopRouteEdit.models = [];
          loopRouteEdit.manualModel = true;
          loopRouteEdit.modelsText = "The model list did not answer. Enter a model name manually from your provider's supported settings.";
        })
        .then(function () {
          if (!modelsAlive()) return;
          loopRouteEdit.loadingModels = false;
          render(false, true);
          focusOn(nextFocus || "loop-route-model");
          if (loopRouteEdit.modelsText) announce(loopRouteEdit.modelsText);
        });
    }

    var rEditor = loopRouteEdit.editorSeq;
    var rSeq = ++loopRouteEdit.reqSeq;
    function reqAlive() {
      return loopRouteEdit.loopKey === loopKey && loopRouteEdit.editorSeq === rEditor && loopRouteEdit.reqSeq === rSeq;
    }
    loopRouteEdit.busy = true;
    loopRouteEdit.reconcileError = "";
    loopRouteEdit.reconciledNote = "";
    if (!loopRouteEdit.unresolvedSave) loopRouteEdit.message = "";
    render(false, true);
    return post("api/v1/routes/" + step, formToken || state.server.form, bodyObj)
      .then(function (res) {
        if (!reqAlive()) {
          if (step === "confirm") {
            load(true);
            if (res && res.outcome === "partial") {
              notice("loop-route-bg", "An earlier route save finished partially. Inspect files and read the current list before preparing another change.", true);
            } else if (res && res.outcome === "changed") {
              announce("An earlier route save completed.");
            }
          }
          return;
        }
        if (step === "read") {
          if (res && res.editor && loopRouteReadableView(res.editor, loopKey)) {
            loopRouteEdit.data = res.editor;
            if (loopRouteEdit.unresolvedSave) {
              loopRouteEdit.unresolvedSave = null;
              loopRouteEdit.unknownOutcome = false;
              loopRouteEdit.reconcileError = "";
              loopRouteEdit.reply = null;
              loopRouteEdit.message = "";
              loopRouteEdit.reconciledNote = "Current route list loaded. Check whether the earlier save took effect before preparing another change.";
              load(true);
            }
          } else if (loopRouteEdit.unresolvedSave) {
            loopRouteEdit.reconcileError = "Could not verify the earlier save because the current route list is incomplete or unavailable" +
              (res && res.text ? " (" + res.text + ")" : "") +
              ". The earlier save outcome is still " + loopRouteEdit.unresolvedSave.kind + "; nothing is retried automatically.";
          } else {
            loopRouteEdit.reply = res;
            loopRouteEdit.message = (res && res.text) || "Could not read route configuration.";
          }
        } else {
          loopRouteEdit.reply = res;
          if (step === "confirm") {
            if (res && res.outcome === "partial") {
              loopRouteEdit.unresolvedSave = {
                kind: "partial",
                op: (bodyObj && bodyObj.op) || loopRouteEdit.pendingOp,
                stage: res.stage || "",
                text: res.text || "A fallback file may remain without the configuration update; inspect files before reviewing again. Nothing is retried automatically.",
                reply: res
              };
              loopRouteEdit.unknownOutcome = false;
              loopRouteEdit.reconcileError = "";
            } else {
              loopRouteEdit.unresolvedSave = null;
              loopRouteEdit.unknownOutcome = false;
              loopRouteEdit.reconcileError = "";
            }
            if (res && (res.outcome === "changed" || res.outcome === "unchanged" || res.outcome === "partial" || res.outcome === "review_again")) {
              loopRouteEdit.data = null;
              load(true);
            }
            if (res && res.outcome === "changed" && (loopRouteEdit.panel === "manage" || loopRouteEdit.panel === "own")) {
              post("api/v1/routes/read", state.server.form, { op: "read", loop: loopKey }).then(function (rd) {
                if (loopRouteEdit.loopKey === loopKey && loopRouteEdit.editorSeq === rEditor && loopRouteEdit.reqSeq === rSeq && rd && rd.editor && loopRouteView(rd.editor, loopKey)) {
                  loopRouteEdit.data = rd.editor;
                  render(false, true);
                }
              }).catch(function () {});
            }
            if (res && res.outcome === "changed" && loopRouteEdit.pendingOp === "add") {
              loopRouteEdit.draft = { account: "", model: "", at: "0", limitChoice: "", limit: "" };
              loopRouteEdit.touched = Object.create(null);
              loopRouteEdit.submitTried = false;
              loopRouteEdit.models = [];
              loopRouteEdit.modelsText = "";
              loopRouteEdit.manualModel = false;
            }
          }
        }
      })
      .catch(function () {
        if (!reqAlive()) {
          if (step === "confirm") {
            load(true);
            notice("loop-route-bg", "An earlier route save lost its reply (outcome unknown). Read the current route list before preparing another change; nothing is retried automatically.", true);
          }
          return;
        }
        if (step === "confirm") {
          loopRouteEdit.unresolvedSave = {
            kind: "unknown",
            op: (bodyObj && bodyObj.op) || loopRouteEdit.pendingOp,
            stage: "",
            text: "The save result is unknown. Read the current route list before preparing another change; this save will not be retried automatically.",
            reply: null
          };
          loopRouteEdit.unknownOutcome = true;
          loopRouteEdit.reply = null;
          loopRouteEdit.data = null;
          loopRouteEdit.reconcileError = "";
          loopRouteEdit.message = loopRouteEdit.unresolvedSave.text;
        } else if (loopRouteEdit.unresolvedSave) {
          loopRouteEdit.reconcileError = "The panel did not answer while reading the current route list. The earlier save may still have taken effect (outcome still " +
            loopRouteEdit.unresolvedSave.kind + "). Try reading the list again before preparing another change; nothing is retried automatically.";
        } else {
          loopRouteEdit.reply = null;
          loopRouteEdit.message = "The panel did not answer. Nothing was changed. Try reading the route list again.";
        }
      })
      .then(function () {
        if (!reqAlive()) return;
        loopRouteEdit.busy = false;
        render(false, true);
        if (loopRouteEdit.unresolvedSave || loopRouteEdit.reconcileError || loopRouteEdit.reconciledNote) {
          focusOn("loop-route-feedback");
        } else if (nextFocus) {
          focusOn(nextFocus);
        } else if (step !== "read" || loopRouteEdit.message) {
          focusOn("loop-route-feedback");
        }
        var spoken = loopRouteEdit.reconcileError ||
          (loopRouteEdit.unresolvedSave && loopRouteEdit.unresolvedSave.text) ||
          loopRouteEdit.reconciledNote ||
          loopRouteEdit.message ||
          (loopRouteEdit.reply && loopRouteEdit.reply.text) || "";
        if (spoken) announce(spoken);
      });
  }
  function loopRouteEffects(parent, chg, lKey) {
    var ed = loopRouteEdit.data;
    var labelsBefore = Object.create(null);
    var labelsAfter = Object.create(null);
    var acctLabels = Object.create(null);
    if (ed && ed.accountLabels) {
      Object.keys(ed.accountLabels).forEach(function (k) { acctLabels[k] = ed.accountLabels[k]; });
    }
    if (loopRouteEdit.reply && loopRouteEdit.reply.accountLabels) {
      Object.keys(loopRouteEdit.reply.accountLabels).forEach(function (k) { acctLabels[k] = loopRouteEdit.reply.accountLabels[k]; });
    }
    if (chg && chg.route && loopRouteEdit.pendingAccountLabel && !acctLabels[chg.route]) {
      acctLabels[chg.route] = loopRouteEdit.pendingAccountLabel;
    }
    var curView = loopRouteView(ed, lKey);
    list(curView && curView.routes).forEach(function (row) { labelsBefore[row.id] = loopRouteRowTitle(row, acctLabels); });
    list(ed && ed.standard && ed.standard.routes).forEach(function (row) {
      if (!labelsBefore[row.id]) labelsBefore[row.id] = loopRouteRowTitle(row, acctLabels);
    });
    list(chg && chg.routes).forEach(function (row) {
      labelsAfter[row.id] = loopRouteRowTitle(row, acctLabels);
      if (!labelsBefore[row.id]) labelsBefore[row.id] = labelsAfter[row.id];
    });
    function joinOrder(ids, source) {
      var arr = list(ids);
      if (!arr.length) return "No standby routes";
      return arr.map(function (id) { return source[id] || (id === "primary" ? "First route" : "Configured route"); }).join(" -> ");
    }
    if (chg.op === "add" && chg.route && labelsAfter[chg.route]) {
      var posNote = chg.to > 0 ? " (Choice " + (chg.to + 1) + ")" : "";
      parent.appendChild(el("p", "loop-route-effect-main", "New route: " + labelsAfter[chg.route] + posNote));
    }
    parent.appendChild(el("p", "sub", "Before: " + joinOrder(chg.before, labelsBefore)));
    parent.appendChild(el("p", null, "After: " + joinOrder(chg.after, labelsAfter)));
    list(chg.loops).forEach(function (lp) {
      var item = el("div", "loop-route-effect");
      if (lp.problem) item.appendChild(el("p", "c-warn", "Configuration unavailable; effects are unknown."));
      if (lp.currentLeavesList) item.appendChild(el("p", "c-warn", "The current route leaves this list. The loop stays there until its ordinary rules move it."));
      if (lp.noRouteApplies) item.appendChild(el("p", "c-warn", "No route in this list applies to this loop."));
      if (lp.belowMinHealthy) item.appendChild(el("p", "c-warn", "Recorded healthy routes are below this loop's minimum."));
      list(lp.newlySkipped).forEach(function (sk) {
        item.appendChild(el("p", "sub", (labelsAfter[sk.id] || "A route") + ": " + sk.reason));
      });
      if (item.childNodes.length) parent.appendChild(item);
    });
    list(chg.warnings).forEach(function (wText) { parent.appendChild(el("p", "c-warn", wText)); });
    if (chg.refusal && chg.refusal.text) parent.appendChild(el("p", "c-warn", chg.refusal.text));
  }
  function loopRouteFeedbackCard(l, c) {
    var unres = loopRouteEdit.unresolvedSave;
    var res = unres ? (unres.reply || null) : loopRouteEdit.reply;
    var recNote = !unres ? loopRouteEdit.reconciledNote : "";
    if (!unres && !recNote && !res && !loopRouteEdit.message && !loopRouteEdit.unknownOutcome && !loopRouteEdit.busy) return null;
    var isPreview = !unres && !!(res && res.outcome === "preview" && res.form);
    var outcome = unres ? unres.kind : (recNote ? "reconciled" : (res && res.outcome ? res.outcome : (loopRouteEdit.unknownOutcome ? "unknown" : "refused")));
    var boxCls = isPreview ? "loop-route-review" : "loop-route-result is-" + outcome;
    var card = el("section", boxCls);
    card.id = "loop-route-feedback";
    card.tabIndex = -1;
    card.dataset.focus = "loop-route-feedback";
    card.setAttribute("aria-labelledby", "loop-route-feedback-title");

    var headText = "Route update";
    if (unres) headText = unres.kind === "partial" ? "Partial save: inspect files" : "Save result unknown";
    else if (recNote) headText = "Current list loaded";
    else if (loopRouteEdit.busy) headText = "Working...";
    else if (isPreview) headText = loopRouteEdit.pendingOp === "add" ? "Review new route" : "Review route change";
    else if (loopRouteEdit.unknownOutcome) headText = "Save result unknown";
    else if (outcome === "changed") headText = "Saved";
    else if (outcome === "unchanged") headText = "Already up to date";
    else if (outcome === "review_again") headText = "Review again before saving";
    else if (outcome === "partial") headText = "Partial save: inspect files";
    else if (outcome === "refused") headText = "Update refused";
    var head = el("h4", null, headText);
    head.id = "loop-route-feedback-title";
    card.appendChild(head);

    if (unres) {
      card.appendChild(el("p", null, unres.text));
      if (loopRouteEdit.busy) card.appendChild(el("p", "sub", "Reading current route list..."));
      if (loopRouteEdit.reconcileError) card.appendChild(el("p", "c-warn loop-route-reconcile-err", loopRouteEdit.reconcileError));
      if (res) list(res.changes).forEach(function (chg) { loopRouteEffects(card, chg, l.key); });
    } else if (recNote) {
      card.appendChild(el("p", null, recNote));
    } else {
      var rawText = loopRouteEdit.busy ? "Waiting for the panel..." : (loopRouteEdit.message || (res && res.text) || "");
      var partitioned = res ? loopRoutePartitionLines(res.lines) : { primary: [], details: [] };
      var primaryTextLines = partitioned.primary.slice();
      var detailTextLines = partitioned.details.slice();
      if (isPreview && rawText) {
        if (loopRouteIsBoilerplate(rawText)) detailTextLines.unshift(rawText);
        else primaryTextLines.unshift(rawText);
      } else if (rawText) {
        card.appendChild(el("p", null, rawText));
      }

      if (res) list(res.changes).forEach(function (chg) { loopRouteEffects(card, chg, l.key); });
      primaryTextLines.forEach(function (lnText) { card.appendChild(el("p", "sub", lnText)); });
    }

    var acts = el("div", "loop-route-actions");
    if (isPreview) {
      var confirmLabel = loopRouteEdit.pendingOp === "add" ? "Save reviewed route" : "Save reviewed change";
      acts.appendChild(loopRouteButton(confirmLabel, "loop-route-save", "btn is-primary", function () {
        var token = res.form;
        var opName = (res.changes && res.changes[0] && res.changes[0].op) || loopRouteEdit.pendingOp;
        // Clear the single-use preview before sending; never retry automatically.
        loopRouteEdit.reply = null;
        loopRouteRequest("confirm", { op: opName, loop: l.key }, token, l.key, "loop-route-feedback");
      }, loopRouteEdit.busy));
      acts.appendChild(loopRouteButton("Cancel review", "loop-route-review-cancel", "btn", function () {
        loopRouteEdit.reqSeq += 1;
        loopRouteEdit.reply = null;
        loopRouteEdit.message = "";
        render(false, true);
        focusOn(loopRouteEdit.panel === "manage" ? "loop-route-refresh" : loopRouteEdit.panel === "own" ? "loop-route-own-prepare" : "loop-route-account");
      }, loopRouteEdit.busy));
    } else if (!loopRouteEdit.busy) {
      if (unres || outcome === "review_again" || outcome === "partial" || loopRouteEdit.unknownOutcome) {
        var isReconcile = !!(unres || outcome === "partial" || loopRouteEdit.unknownOutcome);
        var againLabel = outcome === "review_again" ? "Read current list & review again" : "Read current list";
        var againFocus = outcome === "review_again" ? "loop-route-review-again" : "loop-route-read-again";
        acts.appendChild(loopRouteButton(againLabel, againFocus, "btn is-primary", function () {
          if (!isReconcile) {
            loopRouteEdit.reply = null;
            loopRouteEdit.message = "";
          }
          loopRouteRequest("read", { op: "read", loop: l.key }, null, l.key, loopRouteEdit.panel === "manage" ? "loop-route-refresh" : "loop-route-account", { reconcile: isReconcile });
        }, false));
      }
      acts.appendChild(loopRouteButton("Dismiss", "loop-route-dismiss", "btn", function () {
        loopRouteEdit.reply = null;
        loopRouteEdit.message = "";
        loopRouteEdit.unknownOutcome = false;
        loopRouteEdit.unresolvedSave = null;
        loopRouteEdit.reconcileError = "";
        loopRouteEdit.reconciledNote = "";
        render(false, true);
        focusOn(loopRouteEdit.panel === "add" ? "loop-route-add-open" : loopRouteEdit.panel === "manage" ? "loop-route-manage-open" : "loop-route-own-open");
      }, false));
      if (!unres && outcome === "changed" && loopRouteEdit.panel === "add") {
        acts.appendChild(loopRouteButton("Add another route", "loop-route-add-another", "btn", function () {
          loopRouteEdit.reply = null;
          loopRouteEdit.message = "";
          loopRouteEdit.unknownOutcome = false;
          loopRouteEdit.reconciledNote = "";
          loopRouteRequest("read", { op: "read", loop: l.key }, null, l.key, "loop-route-account");
        }, false));
      }
    }
    if (acts.childNodes.length) card.appendChild(acts);

    if (!unres && !recNote && isPreview && detailTextLines.length) {
      var det = el("details", "loop-route-details");
      det.appendChild(el("summary", "loop-route-details-summary", "Policy and verification details"));
      var detBody = el("div", "loop-route-details-body");
      detailTextLines.forEach(function (lnText) { detBody.appendChild(el("p", "sub", lnText)); });
      det.appendChild(detBody);
      card.appendChild(det);
    }

    if (!unres && outcome === "changed" && loopRouteEdit.pendingOp === "add") {
      var savedChg = res && res.changes && res.changes[0];
      var nextBox = el("div", "loop-route-next");
      nextBox.appendChild(el("p", "sub", "Optional Terminal checks (not run automatically):"));
      var argText = loopArg(l);
      var routeId = savedChg && savedChg.route && ROUTE_ID.test(savedChg.route) ? savedChg.route : "ROUTE_ID";
      nextBox.appendChild(howStep("loop-copy-check-added", "Test only the newly added route (one real request using its account quota):",
        "looptender fallbacks check --id " + routeId + " --exact " + argText, ""));
      if (!list(c.handoffOn).length) {
        nextBox.appendChild(howStep("loop-copy-handoff-on", "Automatic handoff is still off for this loop. Turn it on when ready:",
          "looptender fallbacks handoff on " + argText, ""));
      }
      card.appendChild(nextBox);
    }
    return card;
  }
  function loopRouteValidate(hasGroups) {
    var d = loopRouteEdit.draft;
    var errs = Object.create(null);
    if (!d.account) errs.account = "Choose an eligible account selection.";
    var mVal = (d.model || "").trim();
    if (!mVal) errs.model = "Enter or choose a model name.";
    else if (mVal.length > 128 || !/^[A-Za-z0-9._:/-]+$/.test(mVal)) {
      errs.model = "Use letters, digits, dots, colons, slashes, underscores or hyphens (up to 128 characters).";
    }
    if (hasGroups) {
      if (!d.limitChoice) errs.limitChoice = "Choose a limit relationship or enter a new label.";
      else if (d.limitChoice === "new") {
        var nLim = (d.limit || "").trim();
        if (!nLim) errs.limit = "Enter a new limit label.";
        else if (nLim.length > 128 || !/^[A-Za-z0-9._-]+$/.test(nLim)) {
          errs.limit = "Use letters, digits, dots, underscores or hyphens (up to 128 characters).";
        }
      }
    } else {
      var dLim = (d.limit || "").trim();
      if (!dLim) errs.limit = "Enter a limit label.";
      else if (dLim.length > 128 || !/^[A-Za-z0-9._-]+$/.test(dLim)) {
        errs.limit = "Use letters, digits, dots, underscores or hyphens (up to 128 characters).";
      }
    }
    return errs;
  }
  function loopRouteShowError(fieldName, errs) {
    if (!errs[fieldName]) return "";
    if (!loopRouteEdit.submitTried && !loopRouteEdit.touched[fieldName]) return "";
    return errs[fieldName];
  }
  function loopRouteSyncFieldErr(fieldWrap, ctrlEl, fieldName, hasGroups) {
    var errId = "loop-route-err-" + fieldName;
    var existing = fieldWrap.querySelector("#" + errId);
    var errsNow = loopRouteValidate(hasGroups);
    var errText = loopRouteShowError(fieldName, errsNow);
    if (errText) {
      ctrlEl.setAttribute("aria-invalid", "true");
      ctrlEl.setAttribute("aria-describedby", errId);
      if (!existing) {
        existing = el("p", "loop-route-error", errText);
        existing.id = errId;
        existing.setAttribute("role", "alert");
        fieldWrap.appendChild(existing);
      } else {
        existing.textContent = errText;
      }
    } else {
      ctrlEl.removeAttribute("aria-invalid");
      if (ctrlEl.getAttribute("aria-describedby") === errId) ctrlEl.removeAttribute("aria-describedby");
      if (existing) existing.remove();
    }
  }
  function loopRouteAddForm(l, view, ed) {
    var wrap = el("div", "loop-route-panel");
    var feedback = loopRouteFeedbackCard(l, l.chain || {});
    if (feedback) wrap.appendChild(feedback);
    if (loopRouteEdit.unresolvedSave) return wrap;
    if (loopRouteEdit.reply && ((loopRouteEdit.reply.outcome === "preview" && loopRouteEdit.reply.form) || loopRouteEdit.reply.outcome === "changed")) return wrap;
    if (!ed && loopRouteEdit.busy) {
      wrap.appendChild(el("p", "sub", "Loading loop configuration and eligible accounts..."));
      return wrap;
    }
    if (!ed) {
      wrap.appendChild(loopRouteButton("Read route configuration", "loop-route-read-retry", "btn", function () {
        loopRouteRequest("read", { op: "read", loop: l.key }, null, l.key, "loop-route-account");
      }, loopRouteEdit.busy));
      return wrap;
    }
    if (view && (view.problem || view.listUnreadable)) {
      wrap.appendChild(el("p", "c-warn", loopRouteReasonText(view.canReason || view.problem || "list_unreadable")));
      return wrap;
    }
    if (view && view.canReason && view.canReason !== "no_list") {
      wrap.appendChild(el("p", "c-warn", loopRouteReasonText(view.canReason)));
      return wrap;
    }

    var accts = loopRouteEligibleAccounts(ed);
    if (!accts.length) {
      var empty = emptyBox("No eligible Claude Code or Codex account selections",
        "Adding a headless route here requires an existing Claude Code or Codex selection using inherited login or an explicit directory.");
      empty.appendChild(link("#/accounts", "View Accounts", "btn"));
      wrap.appendChild(empty);
      wrap.appendChild(el("p", "sub", "BB conversation routes and externally managed accounts remain configurable from Terminal commands (advanced) or on Routes for standard-list loops."));
      return wrap;
    }

    var chosen = accts.filter(function (a) { return a.id === loopRouteEdit.draft.account; })[0] || null;
    if (!chosen && loopRouteEdit.draft.account) loopRouteEdit.draft.account = "";

    var standby = list(view && view.routes).filter(function (r) { return !r.notInList; });
    var groups = [];
    if (view && view.first && view.first.group) groups.push(view.first.group);
    standby.forEach(function (r) { if (r.group && groups.indexOf(r.group) < 0) groups.push(r.group); });
    var hasGroups = groups.length > 0;

    var formEl = el("form", "loop-route-form");
    formEl.method = "post";
    formEl.action = "api/v1/routes/prepare";
    formEl.noValidate = true;
    formEl.setAttribute("aria-labelledby", "loop-route-add-heading");
    var heading = el("h3", null, "Add standby route");
    heading.id = "loop-route-add-heading";
    formEl.appendChild(heading);

    // 1. Account selection
    var acctDiv = el("div", "loop-route-field");
    var acctLbl = el("label", null, "Account selection");
    acctLbl.htmlFor = "loop-route-account";
    acctDiv.appendChild(acctLbl);
    var acctSel = el("select");
    acctSel.id = "loop-route-account";
    acctSel.name = "account";
    acctSel.dataset.focus = "loop-route-account";
    acctSel.required = true;
    acctSel.disabled = loopRouteEdit.busy;
    [["", "Choose an account"]].concat(accts.map(function (a) {
      var howSel = a.selection === "explicit_directory" ? "explicit directory" : "inherited login";
      return [a.id, a.label + " \u00B7 " + routeProvider(a.provider) + " (" + howSel + ")"];
    })).forEach(function (o) {
      var opt = el("option", null, o[1]);
      opt.value = o[0];
      acctSel.appendChild(opt);
    });
    acctSel.value = loopRouteEdit.draft.account || "";
    acctSel.addEventListener("change", function () {
      loopRouteEdit.modelSeq += 1;
      loopRouteEdit.loadingModels = false;
      loopRouteEdit.draft.account = acctSel.value;
      loopRouteEdit.touched.account = true;
      loopRouteEdit.models = [];
      loopRouteEdit.modelsText = "";
      loopRouteEdit.manualModel = false;
      loopRouteEdit.draft.model = "";
      loopRouteEdit.touched.model = false;
      render(false, true);
      focusOn("loop-route-account");
    });
    acctSel.addEventListener("blur", function () {
      loopRouteEdit.touched.account = true;
      loopRouteSyncFieldErr(acctDiv, acctSel, "account", hasGroups);
    });
    acctDiv.appendChild(acctSel);
    loopRouteSyncFieldErr(acctDiv, acctSel, "account", hasGroups);
    formEl.appendChild(acctDiv);

    // 2. Model (manual input or optional BB listing)
    var modelDiv = el("div", "loop-route-field");
    var modelLbl = el("label", null, "Model");
    modelLbl.htmlFor = "loop-route-model";
    modelDiv.appendChild(modelLbl);
    var modelTools = el("div", "loop-route-model-tools");
    var loadBtn = loopRouteButton(loopRouteEdit.loadingModels ? "Loading models..." : "Load models from BB", "loop-route-load-models", "btn", function () {
      if (!chosen) return;
      loopRouteRequest("models", { op: "models", provider: chosen.provider }, null, l.key, "loop-route-model", { accountId: chosen.id, provider: chosen.provider });
    }, loopRouteEdit.busy || loopRouteEdit.loadingModels || !chosen);
    modelTools.appendChild(loadBtn);
    if (loopRouteEdit.models.length) {
      var togLabel = loopRouteEdit.manualModel ? "Choose from BB list" : "Enter model manually";
      modelTools.appendChild(loopRouteButton(togLabel, "loop-route-model-manual", "btn", function () {
        loopRouteEdit.manualModel = !loopRouteEdit.manualModel;
        render(false, true);
        focusOn("loop-route-model");
      }, loopRouteEdit.busy));
    }
    modelDiv.appendChild(modelTools);

    var useSelect = loopRouteEdit.models.length > 0 && !loopRouteEdit.manualModel;
    var modelCtrl;
    if (useSelect) {
      modelCtrl = el("select");
      [["", "Choose a model"]].concat(loopRouteEdit.models.map(function (m) {
        return [m.model, m.displayName && m.displayName !== m.model ? m.displayName + " (" + m.model + ")" : m.model];
      })).forEach(function (o) {
        var opt = el("option", null, o[1]);
        opt.value = o[0];
        modelCtrl.appendChild(opt);
      });
      var hasModelInList = loopRouteEdit.models.some(function (m) { return m.model === loopRouteEdit.draft.model; });
      if (!hasModelInList) loopRouteEdit.draft.model = "";
    } else {
      modelCtrl = el("input");
      modelCtrl.type = "text";
      modelCtrl.maxLength = 128;
      modelCtrl.autocomplete = "off";
      modelCtrl.placeholder = chosen && chosen.provider === "codex" ? "e.g. gpt-5.2-codex" : "e.g. claude-sonnet-4-5";
    }
    modelCtrl.id = "loop-route-model";
    modelCtrl.name = "model";
    modelCtrl.dataset.focus = "loop-route-model";
    modelCtrl.required = true;
    modelCtrl.disabled = loopRouteEdit.busy;
    modelCtrl.value = loopRouteEdit.draft.model || "";
    modelCtrl.addEventListener("input", function () {
      loopRouteEdit.draft.model = modelCtrl.value;
      if (loopRouteEdit.touched.model || loopRouteEdit.submitTried) loopRouteSyncFieldErr(modelDiv, modelCtrl, "model", hasGroups);
    });
    modelCtrl.addEventListener("change", function () {
      loopRouteEdit.draft.model = modelCtrl.value;
      loopRouteEdit.touched.model = true;
      loopRouteSyncFieldErr(modelDiv, modelCtrl, "model", hasGroups);
    });
    modelCtrl.addEventListener("blur", function () {
      loopRouteEdit.draft.model = modelCtrl.value;
      loopRouteEdit.touched.model = true;
      loopRouteSyncFieldErr(modelDiv, modelCtrl, "model", hasGroups);
    });
    modelDiv.appendChild(modelCtrl);
    if (loopRouteEdit.modelsText) modelDiv.appendChild(el("p", "sub loop-route-hint", loopRouteEdit.modelsText));
    loopRouteSyncFieldErr(modelDiv, modelCtrl, "model", hasGroups);
    formEl.appendChild(modelDiv);

    // 3. Position in fallback order (at)
    var atDiv = el("div", "loop-route-field");
    var atLbl = el("label", null, "Order position");
    atLbl.htmlFor = "loop-route-at";
    atDiv.appendChild(atLbl);
    var atSel = el("select");
    atSel.id = "loop-route-at";
    atSel.name = "at";
    atSel.dataset.focus = "loop-route-at";
    atSel.disabled = loopRouteEdit.busy;
    var atOpts = [["0", "Add at end (choice " + (standby.length + 2) + ")"]];
    for (var pos = 1; pos <= standby.length + 1; pos++) {
      atOpts.push([String(pos), pos === 1 ? "Choice 2 (next after first)" : "Choice " + (pos + 1)]);
    }
    atOpts.forEach(function (o) {
      var opt = el("option", null, o[1]);
      opt.value = o[0];
      atSel.appendChild(opt);
    });
    atSel.value = loopRouteEdit.draft.at || "0";
    atSel.addEventListener("change", function () { loopRouteEdit.draft.at = atSel.value; });
    atDiv.appendChild(atSel);
    formEl.appendChild(atDiv);

    // 4. Recorded limit label
    if (hasGroups) {
      var lcDiv = el("div", "loop-route-field");
      var lcLbl = el("label", null, "Limit relationship");
      lcLbl.htmlFor = "loop-route-limit-choice";
      lcDiv.appendChild(lcLbl);
      var lcSel = el("select");
      lcSel.id = "loop-route-limit-choice";
      lcSel.name = "limitChoice";
      lcSel.dataset.focus = "loop-route-limit-choice";
      lcSel.required = true;
      lcSel.disabled = loopRouteEdit.busy;
      [["", "Choose a limit relationship"]].concat(groups.map(function (g) {
        return [g, "Share limit: " + g];
      })).concat([["new", "New limit label"]]).forEach(function (o) {
        var opt = el("option", null, o[1]);
        opt.value = o[0];
        lcSel.appendChild(opt);
      });
      lcSel.value = loopRouteEdit.draft.limitChoice || "";
      lcSel.addEventListener("change", function () {
        loopRouteEdit.draft.limitChoice = lcSel.value;
        loopRouteEdit.touched.limitChoice = true;
        render(false, true);
        focusOn(lcSel.value === "new" ? "loop-route-limit" : "loop-route-limit-choice");
      });
      lcSel.addEventListener("blur", function () {
        loopRouteEdit.touched.limitChoice = true;
        loopRouteSyncFieldErr(lcDiv, lcSel, "limitChoice", hasGroups);
      });
      lcDiv.appendChild(lcSel);
      loopRouteSyncFieldErr(lcDiv, lcSel, "limitChoice", hasGroups);
      formEl.appendChild(lcDiv);
    }

    if (!hasGroups || loopRouteEdit.draft.limitChoice === "new") {
      var limDiv = el("div", "loop-route-field");
      var limLbl = el("label", null, hasGroups ? "New limit label" : "Limit label");
      limLbl.htmlFor = "loop-route-limit";
      limDiv.appendChild(limLbl);
      var limInput = el("input");
      limInput.type = "text";
      limInput.id = "loop-route-limit";
      limInput.name = "limit";
      limInput.dataset.focus = "loop-route-limit";
      limInput.maxLength = 128;
      limInput.autocomplete = "off";
      limInput.required = true;
      limInput.disabled = loopRouteEdit.busy;
      limInput.placeholder = "e.g. pool-standby";
      limInput.value = loopRouteEdit.draft.limit || "";
      limInput.addEventListener("input", function () {
        loopRouteEdit.draft.limit = limInput.value;
        if (loopRouteEdit.touched.limit || loopRouteEdit.submitTried) loopRouteSyncFieldErr(limDiv, limInput, "limit", hasGroups);
      });
      limInput.addEventListener("blur", function () {
        loopRouteEdit.draft.limit = limInput.value;
        loopRouteEdit.touched.limit = true;
        loopRouteSyncFieldErr(limDiv, limInput, "limit", hasGroups);
      });
      limDiv.appendChild(limInput);
      loopRouteSyncFieldErr(limDiv, limInput, "limit", hasGroups);
      formEl.appendChild(limDiv);
    }

    formEl.appendChild(el("p", "sub loop-route-guarantee", "Headless CLI route; runs no probe and leaves handoff and holds unchanged."));

    var subRow = el("div", "loop-route-actions");
    var subBtn = el("button", "btn is-primary", "Review route");
    subBtn.type = "submit";
    subBtn.dataset.focus = "loop-route-review";
    subBtn.disabled = loopRouteEdit.busy;
    subRow.appendChild(subBtn);
    subRow.appendChild(loopRouteButton("Cancel", "loop-route-cancel", "btn", function () {
      loopRouteSwitchPanel("");
      render(false, true);
      focusOn("loop-route-add-open");
    }, loopRouteEdit.busy));
    formEl.appendChild(subRow);

    var addDet = el("details", "loop-route-details");
    addDet.appendChild(el("summary", "loop-route-details-summary", "How headless routes and limit labels work"));
    var addDetBody = el("div", "loop-route-details-body");
    addDetBody.appendChild(el("p", "sub", "Uses this loop's executable and project folder without verifying login, model access or quota."));
    addDetBody.appendChild(el("p", "sub", "Loading models from BB is optional and read-only; effort uses the CLI default."));
    addDetBody.appendChild(el("p", "sub", "Limit labels use letters, digits, dots, underscores or hyphens to record your configuration and do not verify separate quota."));
    addDet.appendChild(addDetBody);
    formEl.appendChild(addDet);

    formEl.addEventListener("submit", function (ev) {
      ev.preventDefault();
      loopRouteEdit.submitTried = true;
      var checkErrs = loopRouteValidate(hasGroups);
      var errKeys = Object.keys(checkErrs);
      if (errKeys.length) {
        render(false, true);
        var firstBad = errKeys[0] === "limitChoice" ? "loop-route-limit-choice" : "loop-route-" + errKeys[0];
        focusOn(firstBad);
        announce(checkErrs[errKeys[0]]);
        return;
      }
      var limitVal = hasGroups && loopRouteEdit.draft.limitChoice !== "new"
        ? loopRouteEdit.draft.limitChoice
        : (loopRouteEdit.draft.limit || "").trim();
      var atNum = parseInt(loopRouteEdit.draft.at || "0", 10);
      if (!isFinite(atNum) || atNum < 0) atNum = 0;
      loopRouteEdit.pendingOp = "add";
      loopRouteEdit.pendingAccountLabel = chosen ? chosen.label : "";
      loopRouteRequest("prepare", {
        op: "add",
        loop: l.key,
        account: loopRouteEdit.draft.account,
        model: (loopRouteEdit.draft.model || "").trim(),
        limit: limitVal,
        behavior: "headless",
        at: atNum
      }, null, l.key, "loop-route-feedback");
    });

    wrap.appendChild(formEl);
    return wrap;
  }
  function loopRouteManagePanel(l, view, ed) {
    var wrap = el("div", "loop-route-panel");
    var headRow = el("div", "loop-route-panel-head");
    headRow.appendChild(el("h3", null, "Manage route order"));
    var headActs = el("div", "loop-route-actions");
    headActs.appendChild(loopRouteButton("Refresh list", "loop-route-refresh", "btn", function () {
      loopRouteEdit.reply = null;
      loopRouteRequest("read", { op: "read", loop: l.key }, null, l.key, "loop-route-refresh");
    }, loopRouteEdit.busy));
    headActs.appendChild(loopRouteButton("Close", "loop-route-manage-close", "btn", function () {
      loopRouteSwitchPanel("");
      render(false, true);
      focusOn("loop-route-manage-open");
    }, loopRouteEdit.busy));
    headRow.appendChild(headActs);
    wrap.appendChild(headRow);

    var feedback = loopRouteFeedbackCard(l, l.chain || {});
    if (feedback) wrap.appendChild(feedback);
    if (loopRouteEdit.unresolvedSave) return wrap;
    if (!ed && loopRouteEdit.busy) {
      wrap.appendChild(el("p", "sub", "Reading this loop's routes..."));
      return wrap;
    }
    if (!ed || !view) {
      wrap.appendChild(el("p", "sub", "Read the current list to review route order and availability."));
      return wrap;
    }
    if (view.problem || view.listUnreadable) {
      wrap.appendChild(el("p", "c-warn", loopRouteReasonText(view.canReason || view.problem || "list_unreadable")));
      return wrap;
    }

    var acctLabels = ed.accountLabels || Object.create(null);
    var standby = list(view.routes).filter(function (row) { return !row.notInList; });
    var outsideRows = list(view.routes).filter(function (row) { return !!row.notInList; });
    outsideRows.forEach(function (row) {
      wrap.appendChild(el("p", "c-warn", loopRouteRowTitle(row, acctLabels) + " is currently in use outside the configured list."));
    });
    if (!standby.length) {
      wrap.appendChild(el("p", "sub", "No standby routes configured yet. Use + Add route to add the first standby route."));
      return wrap;
    }
    var canList = list(view.can);
    if (view.canReason && canList.indexOf("move") < 0 && canList.indexOf("off") < 0 && canList.indexOf("remove") < 0) {
      wrap.appendChild(el("p", "c-warn", loopRouteReasonText(view.canReason)));
    }

    var ol = el("ol", "loop-route-order");
    standby.forEach(function (row, idx) {
      var li = el("li", "loop-route-row");
      var top = el("div", "loop-route-row-head");
      var titleStr = loopRouteRowTitle(row, acctLabels);
      top.appendChild(el("span", "loop-route-row-title", titleStr));
      if (row.current) top.appendChild(tag("good", "Current route"));
      if (row.disabled) top.appendChild(tag("calm", "Off"));
      li.appendChild(top);

      var acts = el("div", "loop-route-row-actions");
      function mkOp(labelText, opName, toPlace) {
        var b = loopRouteButton(labelText, "loop-route-" + opName + "-" + idx + "-" + (toPlace || 0), "btn", function () {
          loopRouteEdit.pendingOp = opName;
          loopRouteEdit.pendingAccountLabel = "";
          var payload = { op: opName, loop: l.key, id: row.id };
          if (opName === "move") payload.to = toPlace;
          loopRouteRequest("prepare", payload, null, l.key, "loop-route-feedback");
        }, loopRouteEdit.busy);
        b.setAttribute("aria-label", labelText + ": " + titleStr);
        return b;
      }
      if (idx > 0 && canList.indexOf("move") >= 0) acts.appendChild(mkOp("Move earlier", "move", idx));
      if (idx < standby.length - 1 && canList.indexOf("move") >= 0) acts.appendChild(mkOp("Move later", "move", idx + 2));
      if (row.disabled && canList.indexOf("on") >= 0) {
        acts.appendChild(mkOp("Turn on", "on", 0));
      } else if (!row.disabled && canList.indexOf("off") >= 0) {
        if (row.current) {
          var offDisabled = loopRouteButton("Turn off", "loop-route-off-disabled-" + idx, "btn", function () {}, true);
          offDisabled.setAttribute("aria-label", "Turn off unavailable while current: " + titleStr);
          acts.appendChild(offDisabled);
        } else {
          acts.appendChild(mkOp("Turn off", "off", 0));
        }
      }
      if (canList.indexOf("remove") >= 0) acts.appendChild(mkOp("Remove", "remove", 0));
      li.appendChild(acts);
      if (row.current && !row.disabled) {
        li.appendChild(el("p", "sub loop-route-row-note", "In use now: switch or move away before turning off."));
      }
      ol.appendChild(li);
    });
    wrap.appendChild(ol);
    return wrap;
  }
  function loopRouteOwnPanel(l, view, ed) {
    var wrap = el("div", "loop-route-panel");
    wrap.appendChild(el("h3", null, "Copy standard routes to this loop's own list"));
    var feedback = loopRouteFeedbackCard(l, l.chain || {});
    if (feedback) wrap.appendChild(feedback);
    if (loopRouteEdit.unresolvedSave) return wrap;
    if (loopRouteEdit.reply && ((loopRouteEdit.reply.outcome === "preview" && loopRouteEdit.reply.form) || loopRouteEdit.reply.outcome === "changed")) return wrap;
    if (!ed && loopRouteEdit.busy) {
      wrap.appendChild(el("p", "sub", "Reading this loop's route eligibility..."));
      return wrap;
    }
    if (!view) {
      wrap.appendChild(el("p", "sub", "Could not read route eligibility for this loop."));
      return wrap;
    }
    if (list(view.can).indexOf("own") < 0) {
      wrap.appendChild(el("p", "c-warn", loopRouteReasonText(view.canReason || view.problem || "no_route_applies")));
      return wrap;
    }
    wrap.appendChild(el("p", "sub", "Copies the resolved standard routes into fallbacks.yaml beside this loop's configuration so you can add headless routes or reorder choices for this loop alone."));
    var acts = el("div", "loop-route-actions");
    acts.appendChild(loopRouteButton("Review copy to own list", "loop-route-own-prepare", "btn is-primary", function () {
      loopRouteEdit.pendingOp = "own";
      loopRouteEdit.pendingAccountLabel = "";
      loopRouteRequest("prepare", { op: "own", loop: l.key }, null, l.key, "loop-route-feedback");
    }, loopRouteEdit.busy));
    acts.appendChild(loopRouteButton("Cancel", "loop-route-own-cancel", "btn", function () {
      loopRouteSwitchPanel("");
      render(false, true);
      focusOn("loop-route-own-open");
    }, loopRouteEdit.busy));
    wrap.appendChild(acts);
    return wrap;
  }
  function loopRouteMgmtPanel(mode) {
    var wrap = el("div", "loop-route-mgmt");
    var isBB = mode === "bb_caller";
    var isPanel = served() === "panel";
    wrap.appendChild(el("h3", null, "How to enable route editing"));
    if (!isPanel) {
      wrap.appendChild(el("p", null, "looptender dock is always read-only. Start a full-mode panel in Terminal outside BB:"));
      wrap.appendChild(howStep("loop-copy-panel-start", "Start a full-mode panel (run in Terminal outside BB):",
        "looptender panel --allow-changes", ""));
    } else if (isBB) {
      wrap.appendChild(el("p", null, "This panel was started from a BB thread. Reopening a window cannot change its startup caller. Stop it and start a person-owned full-mode panel in Terminal outside BB:"));
      wrap.appendChild(howStep("loop-copy-panel-stop", "1. Stop the running panel (loop supervision continues):",
        "looptender panel stop", ""));
      wrap.appendChild(howStep("loop-copy-panel-allow", "2. Start full panel mode in Terminal outside BB:",
        "looptender panel --allow-changes", ""));
    } else {
      wrap.appendChild(el("p", null, mode === "read_only"
        ? "This panel was started with --read-only. Route editing requires restarting the panel in full mode from Terminal outside BB:"
        : "This panel is in Controls mode (pause, stop and resume only). Route editing requires restarting the panel in full mode from Terminal outside BB:"));
      wrap.appendChild(howStep("loop-copy-panel-stop", "1. Stop the running panel (loop supervision continues):",
        "looptender panel stop", ""));
      wrap.appendChild(howStep("loop-copy-panel-allow", "2. Start full panel mode in Terminal outside BB:",
        "looptender panel --allow-changes", ""));
    }
    wrap.appendChild(link(ROUTE_DOCS, "Route configuration help", "more"));
    return wrap;
  }
  function loopRouteFlowBox(l) {
    if (loopRouteEdit.loopKey !== l.key) loopRouteReset(l.key, "");
    var c = l.chain || {}, entries = list(c.entries);
    if (!c.standard && loopRouteEdit.panel === "own") loopRouteEdit.panel = "manage";
    var mode = panelRouteMode();
    var boxEl = el("div", "loop-route-flow");

    var bar = el("div", "loop-route-bar");
    var modeInfo = el("div", "loop-route-mode");
    if (mode !== "all") {
      var modeLabel = mode === "bb_caller" ? "Opened from BB" : mode === "controls" ? "Controls mode" : "Read-only mode";
      var modeDesc = mode === "bb_caller"
        ? "Route editing requires a person-started full-mode panel."
        : "Route editing requires full panel mode.";
      modeInfo.appendChild(el("span", "ui-badge", modeLabel));
      modeInfo.appendChild(el("span", "sub", modeDesc));
      bar.appendChild(modeInfo);

      var barActs = el("div", "loop-route-bar-actions");
      barActs.appendChild(loopRouteButton("Enable route editing...", "loop-route-mgmt-open", "btn", function () {
        loopRouteSwitchPanel(loopRouteEdit.panel === "mgmt" ? "" : "mgmt");
        render(false, true);
        focusOn("loop-route-mgmt-open");
      }, false, loopRouteEdit.panel === "mgmt"));
      bar.appendChild(barActs);
      boxEl.appendChild(bar);
      if (loopRouteEdit.panel === "mgmt") boxEl.appendChild(loopRouteMgmtPanel(mode));
      return boxEl;
    }

    var stateLbl = c.standard ? "Follows standard list" : entries.length > 1 ? "Own route list" : "No standby list yet";
    modeInfo.appendChild(el("span", "ui-badge ui-badge-good", "Full mode"));
    modeInfo.appendChild(el("span", "sub", stateLbl));
    bar.appendChild(modeInfo);

    var acts = el("div", "loop-route-bar-actions");
    if (c.standard) {
      acts.appendChild(link("#/routes", "Edit standard list", "btn"));
      acts.appendChild(loopRouteButton("Use own list...", "loop-route-own-open", "btn", function () {
        if (loopRouteEdit.panel === "own") {
          loopRouteSwitchPanel("");
          render(false, true);
          return;
        }
        loopRouteSwitchPanel("own");
        if (!loopRouteEdit.data) loopRouteRequest("read", { op: "read", loop: l.key }, null, l.key, "loop-route-own-prepare");
        else { render(false, true); focusOn("loop-route-own-prepare"); }
      }, loopRouteEdit.busy, loopRouteEdit.panel === "own"));
      bar.appendChild(acts);
      boxEl.appendChild(bar);
      if (loopRouteEdit.panel === "own") {
        boxEl.appendChild(loopRouteOwnPanel(l, loopRouteView(loopRouteEdit.data, l.key), loopRouteEdit.data));
      }
      return boxEl;
    }

    acts.appendChild(loopRouteButton("+ Add route", "loop-route-add-open", "btn is-primary", function () {
      if (loopRouteEdit.panel === "add") {
        loopRouteSwitchPanel("");
        render(false, true);
        return;
      }
      loopRouteSwitchPanel("add");
      if (!loopRouteEdit.data) loopRouteRequest("read", { op: "read", loop: l.key }, null, l.key, "loop-route-account");
      else { render(false, true); focusOn("loop-route-account"); }
    }, loopRouteEdit.busy, loopRouteEdit.panel === "add"));
    acts.appendChild(loopRouteButton("Manage order", "loop-route-manage-open", "btn", function () {
      if (loopRouteEdit.panel === "manage") {
        loopRouteSwitchPanel("");
        render(false, true);
        return;
      }
      loopRouteSwitchPanel("manage");
      if (!loopRouteEdit.data) loopRouteRequest("read", { op: "read", loop: l.key }, null, l.key, "loop-route-refresh");
      else { render(false, true); focusOn("loop-route-refresh"); }
    }, loopRouteEdit.busy, loopRouteEdit.panel === "manage"));
    bar.appendChild(acts);
    boxEl.appendChild(bar);

    var ed = loopRouteEdit.data;
    var view = loopRouteView(ed, l.key);
    if (loopRouteEdit.panel === "add") boxEl.appendChild(loopRouteAddForm(l, view, ed));
    else if (loopRouteEdit.panel === "manage") boxEl.appendChild(loopRouteManagePanel(l, view, ed));
    return boxEl;
  }

  function renderVersions(d, pane) {
    var v = d.versions || {};
    pane.appendChild(back());
    pane.appendChild(paneHead("Versions"));
    pane.appendChild(el("p", "lede", v.lede || ""));
    var rows = list(v.rows);
    if (!rows.length) {
      pane.appendChild(emptyBox("Nothing to show.", ""));
      return;
    }
    if (v.summary) pane.appendChild(el("p", "route-summary", v.summary));
    var wrap = panel("Where each build runs", v.clients === "unavailable" ? [el("span", "sub", "the clients' reports could not be read")] : []);
    var ul = el("ul", "allow-list");
    rows.forEach(function (x) {
      var li = el("li", "allow");
      var head = el("div", "allow-head");
      var name = el("h3", "allow-name");
      if (x.key) name.appendChild(link("#/loop/" + x.key, x.title));
      else name.textContent = x.title;
      head.appendChild(name);
      if (x.label) head.appendChild(el("code", "sub", x.label));
      li.appendChild(head);
      var body = el("div", "allow-body");
      var line = el("p", "route-status");
      line.appendChild(el("span", "dot " + (versionDots[x.availability] || "dot-unknown")));
      line.appendChild(el("span", null, x.text));
      body.appendChild(line);
      if (x.update) body.appendChild(el("p", "allow-next", x.update));
      if (x.seen) body.appendChild(el("p", "allow-next sub", x.seen));
      if (x.seenAt) body.appendChild(el("p", "allow-next sub", "Last reported " + fmt(dateYearFmt, x.seenAt) + "."));
      li.appendChild(body);
      ul.appendChild(li);
    });
    wrap.appendChild(ul);
    var none = list(v.unrecorded);
    if (none.length) {
      var p = el("p", "more", "Which build ran the last check is not recorded for " + (none.length === 1 ? "this loop" : "these " + none.length + " loops") + ": ");
      none.forEach(function (x, i) {
        if (i) p.appendChild(document.createTextNode(", "));
        p.appendChild(link("#/loop/" + x.key, x.name));
      });
      p.appendChild(document.createTextNode("."));
      wrap.appendChild(p);
    }
    pane.appendChild(wrap);
  }

  function routeDisplayTitle(n) {
    if (n.title !== "BB thread · model not configured") return n.title;
    var group = "";
    list(n.meta).forEach(function (m) {
      if (!group && typeof m === "string" && m.indexOf("account group ") === 0) group = m.slice(14);
    });
    return group ? "BB thread (" + group + ")" : "BB thread · default";
  }
  function renderRoutes(d, pane) {
    pane.appendChild(back());
    pane.appendChild(paneHead("Routes"));
    pane.appendChild(el("p", "lede", "Configured failover routes and which loops are using them."));
    var routes = list(d.routes), rs = d.routesSummary || {};
    var editable = changesOn() && state.server && state.server.routeChanges === "person";
    if (!routes.length) {
      pane.appendChild(emptyBox(d.registry.availability === "available" ? "No routes to show." : "Unknown: the registry could not be read.", ""));
      if (d.registry.availability === "available") pane.appendChild(routeEditorBox());
      return;
    }
    var sum = el("p", "route-summary");
    sum.appendChild(el("span", null, rs.total || routes.length + " routes"));
    if (rs.inUseText) sum.appendChild(el("span", "sub", rs.inUseText));
    if (rs.trouble && rs.first) sum.appendChild(link("#/routes/" + rs.first, rs.problem, "c-warn"));
    else if (rs.problem) sum.appendChild(el("span", "sub", rs.problem));
    pane.appendChild(sum);
    if (editable) pane.appendChild(routeEditorBox());

    // Loops on the route, one per line with their state.
    function loopLines(items) {
      var u = el("ul");
      items.forEach(function (x) {
        var li = el("li");
        li.appendChild(link("#/loop/" + x.key, x.loop));
        li.appendChild(el("span", "sub", " · " + (x.line || x.phrase || "")));
        u.appendChild(li);
      });
      return u;
    }
    // Loops that only keep it on standby, grouped by which choice it is for
    // them, since the order is the point of preferred routes.
    function byChoice(groups) {
      var w = el("div");
      groups.forEach(function (g) {
        var p = el("p", "standby", g.label + " ");
        list(g.loops).forEach(function (x, i) {
          if (i) p.appendChild(document.createTextNode(", "));
          p.appendChild(link("#/loop/" + x.key, x.loop));
        });
        w.appendChild(p);
      });
      return w;
    }
    function buildRouteCard(n) {
      var running = list(n.runningNow), recorded = list(n.recordedFor), standby = list(n.standby), unk = list(n.unknown);
      var boxes = [];
      if (running.length) boxes.push(box("Running now", loopLines(running)));
      if (recorded.length) boxes.push(box("Current", loopLines(recorded)));
      if (standby.length) boxes.push(box("Standby", byChoice(standby)));
      if (unk.length) boxes.push(box("Listed (current route unread)", byChoice(unk)));
      if (n.more) boxes.push(el("p", "sub", n.more));
      var hide = n.readiness === "current" || n.readiness === "main" || (n.readiness === "unknown" && n.probeAgeSeconds == null);
      var allNotes = notesOf(n.notes);
      var primaryNotes = allNotes.filter(function (x) { return x.tone === "limit" || x.tone === "attn"; }).slice(0, 1);
      var extraNotes = allNotes.filter(function (x) { return primaryNotes.indexOf(x) < 0; });
      return routeItem({
        anchor: "route-" + n.key, route: n.key, title: routeDisplayTitle(n), titleAccount: n.titleAccount, active: running.length > 0, use: n.useText,
        readiness: hide ? "" : n.readiness, phrase: n.phrase, notes: primaryNotes, extraNotes: extraNotes, meta: n.meta, usage: n.usage, boxes: boxes, compact: true
      });
    }

    var configured = [], defaults = [];
    routes.forEach(function (n) {
      if (n.title === "BB thread · model not configured") defaults.push(n);
      else configured.push(n);
    });

    if (configured.length) {
      var wrap = panel("Configured routes", rs.trouble ? [el("span", "sub", "at a limit first")] : []);
      var ul = el("ul", "route-list");
      configured.forEach(function (n) { ul.appendChild(buildRouteCard(n)); });
      wrap.appendChild(ul);
      if (d.routesOmitted) wrap.appendChild(el("p", "more", d.routesOmitted + (d.routesOmitted === 1 ? " more route is" : " more routes are") + " not shown."));
      pane.appendChild(wrap);
    }

    if (defaults.length) {
      var targetKey = route().view === "routes" ? route().key : "";
      var hasTarget = targetKey && defaults.some(function (n) { return n.key === targetKey; });
      var defLoopCount = 0;
      defaults.forEach(function (n) { defLoopCount += list(n.runningNow).length + list(n.recordedFor).length; });
      var fold = el("details", "panel panel-fold");
      if (hasTarget || !configured.length) fold.open = true;
      var label = "Default BB threads (" + defaults.length + ")" + (defLoopCount ? " · " + defLoopCount + (defLoopCount === 1 ? " loop" : " loops") + " without a configured model" : "");
      fold.appendChild(el("summary", "panel-fold-summary", label));
      var foldBody = el("div", "panel-fold-body");
      var dul = el("ul", "route-list");
      defaults.forEach(function (n) { dul.appendChild(buildRouteCard(n)); });
      foldBody.appendChild(dul);
      fold.appendChild(foldBody);
      pane.appendChild(fold);
    }

    if (!editable) pane.appendChild(routeEditorBox());
  }

  // ---- Reading, rendering, and what a screen reader is told ----
  function renderReadline() {
    var r = $("readline");
    if (!state.lastOk) { r.textContent = state.failed ? "Could not read yet" : "Reading…"; return; }
    var every = refreshSeconds();
    // This is when the panel read its records, not how new they are: the
    // oldest usage reading shown is named when one is past stale-after.
    var old = state.data.oldestUsageAgeSeconds;
    r.textContent = "Saved records read at " + fmt(secFmt, state.data.readAt) + (state.paused ? " · updates paused" : " · read again every " + (every >= 60 ? span(every) : every + " s")) +
      (old != null ? " · oldest usage reading " + span(old) + " old" : "") +
      (state.failed ? " · the last read failed" : "");
    r.classList.toggle("is-stale", state.failedLong);
    // The line keeps a fixed box; when it is cut there, its tooltip has it whole.
    if (r.scrollHeight > r.clientHeight + 1) r.title = r.textContent; else r.removeAttribute("title");
  }
  // Only what changes for a person is announced: the needs-you line, and a
  // read that failed. The time of each read is not.
  function announce(text) {
    if (text === state.said) return;
    state.said = text;
    $("live").textContent = text;
  }
  // A focusable element is found again after a redraw by a key that does
  // not change when its text does (an age, a count): its tag, its link
  // target, its id or data-focus name, and which of those it was in order.
  var FOCUSABLE = "a[href], button, input, select, textarea, summary, [tabindex]";
  function signature(n) { return n.tagName + "|" + (n.getAttribute("href") || "") + "|" + (n.id || "") + "|" + (n.getAttribute("data-focus") || ""); }
  function focusKey(node, root) {
    if (!node || node === document.body || !root.contains(node) || !node.matches(FOCUSABLE)) return null;
    var sig = signature(node), nth = 0, all = root.querySelectorAll(FOCUSABLE);
    for (var i = 0; i < all.length && all[i] !== node; i++) if (signature(all[i]) === sig) nth++;
    return { sig: sig, nth: nth };
  }
  function findKey(key, root) {
    var all = root.querySelectorAll(FOCUSABLE), nth = 0;
    for (var i = 0; i < all.length; i++) {
      if (signature(all[i]) !== key.sig) continue;
      if (nth++ === key.nth) return all[i];
    }
    return null;
  }
  function selecting() {
    var s = window.getSelection && window.getSelection();
    return !!s && !s.isCollapsed && $("layout").contains(s.anchorNode);
  }

  function editingAllowanceInput(r, pane) {
    var c = state.change, active = document.activeElement;
    return r.view === "loop" && c && c.key === r.key && c.action === "set_send_allowance" && c.editing &&
      changesOn() && state.server.sendAllowanceEdit === true && loopByKey(r.key) &&
      pane.contains(active) && active.id === "allowance-input";
  }

  // force redraws even while text is selected: hiding identifiers must not wait.
  function render(navigated, force) {
    var d = state.data;
    if (!d) return;
    // Text someone is selecting is not replaced under them; the next read,
    // or the end of the selection, brings the page up to date.
    if (!navigated && !force && selecting()) { state.pending = true; renderReadline(); return; }
    state.pending = false;
    var r = route(), pane = $("pane");
    if (!navigated && !force && r.view === "routes" && pane.contains(document.activeElement) && document.activeElement.matches("input, select, textarea")) { state.pending = true; renderReadline(); return; }
    if (!navigated && !force && r.view === "loop" && pane.contains(document.activeElement) && document.activeElement.closest && document.activeElement.closest(".loop-route-flow") && document.activeElement.matches("input, select, textarea")) { state.pending = true; renderReadline(); return; }
    if (!navigated && !force && r.view === "home" && pane.contains(document.activeElement) && document.activeElement.matches("input.map-search")) { state.pending = true; renderReadline(); return; }
    // Keep the live input (including caret and selection) while typing through
    // periodic reads. Explicit privacy/navigation/capability changes still win.
    if (!navigated && !force && editingAllowanceInput(r, pane)) { state.pending = true; renderReadline(); return; }
    var top = navigated ? 0 : window.scrollY;
    if (state.mapObserver) { state.mapObserver.disconnect(); state.mapObserver = null; }
    reconcileActiveIssue(d);
    var active = document.activeElement, inPane = pane.contains(active), inPicker = $("picker").contains(active);
    var key = inPane ? focusKey(active, pane) : inPicker ? focusKey(active, $("picker")) : null;
    retainedRouteEditor = !navigated && !force && r.view === "routes" && routeEdit.data && changesOn() && state.server.routeChanges === "person" ? pane.querySelector(".route-editor") : null;
    // Before the page is drawn, so the list it shows includes this read.
    if (state.noticeRead !== d) { state.noticeRead = d; postBrowserNotices(noticeStep(d)); }
    try {
      $("layout").dataset.view = r.view;
      renderBuildPill(d);
      renderPicker(d, r);
      pane.replaceChildren();
      pane.setAttribute("aria-labelledby", "pane-title");
      if (r.view === "loop") renderLoop(d, pane, r.key);
      else if (r.view === "routes") renderRoutes(d, pane);
      else if (r.view === "versions") renderVersions(d, pane);
      else if (r.view === "usage") renderUsage(d, pane);
      else if (r.view === "accounts") renderAccounts(d, pane);
      else renderHome(d, pane);
    } catch (err) {
      retainedRouteEditor = null;
      pane.replaceChildren(emptyBox("This page could not show the reply it was sent.", "Reload the page, or restart keepgoing " + served() + "."));
      notice("render", "This page could not show the reply it was sent. Reload the page, or restart keepgoing " + served() + ".", true);
      renderReadline();
      return false;
    }
    retainedRouteEditor = null;
    // A render that worked ends the failure banner an earlier render left.
    if (state.notice.indexOf("render") === 0) notice("render", "");
    renderReadline();
    updateTitle();
    // The needs-you line is announced when it changes, not on every read, so
    // a read does not talk over the outcome of a change.
    var needsSaid = (d.needsLine || "Needs you: unknown") + ".";
    if (needsSaid !== state.needsSaid) { state.needsSaid = needsSaid; announce(needsSaid); }
    window.scrollTo(0, top);
    if (navigated) {
      // A link to one route lands on that route; any other lands on the
      // view's heading. A read that is not a change of view moves nothing.
      var at = r.view === "routes" && r.key ? $("route-" + r.key) : null;
      if (at) { at.focus({ preventScroll: true }); at.scrollIntoView({ block: "start" }); }
      else focusPaneTitle();
      var cur = $("picker").querySelector('.pick-scroll [aria-current="page"]');
      if (cur && cur.scrollIntoView) cur.scrollIntoView({ block: "nearest" });
    } else if (key) {
      // Keep the keyboard where it was: the same link or button, rebuilt.
      var again = findKey(key, inPane ? pane : $("picker"));
      if (again) again.focus({ preventScroll: true });
    }
  }

  // The banner sits over the foot of the window, so it never moves what is
  // being read. It is neutral while the page recovers on its own and turns
  // to a warning only once a person is needed (alarm). It has no live role:
  // the polite region says each episode once, when it starts and when it
  // turns into an alarm, not on every read that fails again.
  function notice(key, text, alarm) {
    var b = $("banner");
    b.hidden = !text;
    b.textContent = text || "";
    b.classList.toggle("is-alarm", !!alarm);
    document.body.classList.toggle("has-banner", !!text);
    var said = text ? key + (alarm ? "!" : "") : "";
    if (said !== state.notice) {
      state.notice = said;
      if (text) { state.said = ""; announce(text); }
    }
  }
  function refreshSeconds() {
    var n = Number(state.data && state.data.refreshSeconds);
    return n >= 5 && n <= 3600 ? n : 15;
  }

  // Which command serves this page: "dock" until the server says otherwise.
  function served() { return state.server && state.server.servedBy === "panel" ? "panel" : "dock"; }

  function put(node, parts) {
    parts.forEach(function (part) { node.appendChild(typeof part === "string" ? document.createTextNode(part) : part); });
  }

  // The footer's first line: whether this page can change anything.
  function renderServed() {
    var m = $("changes");
    if (!m || !state.server) return;
    // The line's own words, kept from the first call so a repeat never stacks.
    if (m.getAttribute("data-base") === null) m.setAttribute("data-base", m.textContent);
    var base = list(state.server.changesText).length ? say(state.server.changesText) : m.getAttribute("data-base");
    // The build serving this page leads the same line; nothing when an older
    // server sends none.
    var v = typeof state.server.versionText === "string" ? state.server.versionText : "";
    m.textContent = v ? v + " " + base : base;
    renderBuildPill(state.data);
  }

  // How long one read may take: a little under the refresh interval, from 4
  // to 30 seconds, so a read that never answers cannot stop the updates. A
  // read that runs out of time fails like any other failed read.
  function readTimeoutMs() { return Math.min(30, Math.max(4, refreshSeconds() - 2)) * 1000; }
  // Validate raw dock/saved-accounts payloads before JSON.parse collapses duplicate
  // or escaped-equivalent keys in accounts/cards/quota/summary/provenance.
  function parseDockQuotaJSON(rawText) {
    if (typeof rawText !== "string" || rawText.length > 16777216) throw new Error("schema");
    var parsedObj = JSON.parse(rawText);
    if (!parsedObj || typeof parsedObj !== "object" || Array.isArray(parsedObj)) return parsedObj;
    var jLen = rawText.length, jPos = 0, MAX_JSON_DEPTH = 64;
    var hasOwn = Object.prototype.hasOwnProperty;
    var rootAcctDup = false, anyProvSeen = false, cardFlags = [], rootSeen = Object.create(null);
    function jSkipWs() {
      while (jPos < jLen) {
        var cCode = rawText.charCodeAt(jPos);
        if (cCode === 32 || cCode === 9 || cCode === 10 || cCode === 13) jPos++;
        else break;
      }
    }
    function jReadKey() {
      var kStart = jPos;
      jPos++;
      while (jPos < jLen) {
        var cCode = rawText.charCodeAt(jPos++);
        if (cCode === 92) {
          if (jPos < jLen) jPos++;
        } else if (cCode === 34) {
          return JSON.parse(rawText.slice(kStart, jPos));
        }
      }
      throw new Error("schema");
    }
    function jSkipVal(jDepth) {
      if (jDepth > MAX_JSON_DEPTH) throw new Error("schema");
      jSkipWs();
      if (jPos >= jLen) throw new Error("schema");
      var chCode = rawText.charCodeAt(jPos);
      if (chCode === 34) {
        jReadKey();
        return;
      }
      if (chCode === 123) {
        jPos++; jSkipWs();
        if (rawText.charCodeAt(jPos) === 125) { jPos++; return; }
        while (jPos < jLen) {
          jSkipWs();
          jReadKey();
          jSkipWs();
          if (rawText.charCodeAt(jPos) !== 58) throw new Error("schema");
          jPos++;
          jSkipVal(jDepth + 1);
          jSkipWs();
          var objSep = rawText.charCodeAt(jPos);
          if (objSep === 44) { jPos++; continue; }
          if (objSep === 125) { jPos++; return; }
          throw new Error("schema");
        }
        throw new Error("schema");
      }
      if (chCode === 91) {
        jPos++; jSkipWs();
        if (rawText.charCodeAt(jPos) === 93) { jPos++; return; }
        while (jPos < jLen) {
          jSkipVal(jDepth + 1);
          jSkipWs();
          var arrSep = rawText.charCodeAt(jPos);
          if (arrSep === 44) { jPos++; continue; }
          if (arrSep === 93) { jPos++; return; }
          throw new Error("schema");
        }
        throw new Error("schema");
      }
      while (jPos < jLen) {
        var pCode = rawText.charCodeAt(jPos);
        if (pCode === 44 || pCode === 93 || pCode === 125 || pCode === 32 || pCode === 9 || pCode === 10 || pCode === 13) return;
        jPos++;
      }
    }
    function jScanObj(jDepth, onEntry) {
      if (jDepth > MAX_JSON_DEPTH) throw new Error("schema");
      jSkipWs();
      if (rawText.charCodeAt(jPos) !== 123) { jSkipVal(jDepth); return; }
      jPos++; jSkipWs();
      if (rawText.charCodeAt(jPos) === 125) { jPos++; return; }
      while (jPos < jLen) {
        jSkipWs();
        var kName = jReadKey();
        jSkipWs();
        if (rawText.charCodeAt(jPos) !== 58) throw new Error("schema");
        jPos++; jSkipWs();
        onEntry(kName, jDepth + 1);
        jSkipWs();
        var sepCode = rawText.charCodeAt(jPos);
        if (sepCode === 44) { jPos++; continue; }
        if (sepCode === 125) { jPos++; return; }
        throw new Error("schema");
      }
      throw new Error("schema");
    }
    function jFlagCard(cIdx) {
      if (!cardFlags[cIdx]) cardFlags[cIdx] = { dup: false, sawProv: false };
      return cardFlags[cIdx];
    }
    function jScanProvenance(cIdx, jDepth) {
      var st = jFlagCard(cIdx);
      st.sawProv = true;
      anyProvSeen = true;
      var provKeys = Object.create(null);
      jScanObj(jDepth, function (pKey, valDepth) {
        if (hasOwn.call(provKeys, pKey)) st.dup = true;
        provKeys[pKey] = true;
        jSkipVal(valDepth);
      });
    }
    function jScanSummary(cIdx, jDepth) {
      var st = jFlagCard(cIdx);
      var sumKeys = Object.create(null);
      jScanObj(jDepth, function (sKey, valDepth) {
        if (hasOwn.call(sumKeys, sKey)) st.dup = true;
        sumKeys[sKey] = true;
        if (sKey === "provenance") jScanProvenance(cIdx, valDepth);
        else jSkipVal(valDepth);
      });
    }
    function jScanQuota(cIdx, jDepth) {
      var st = jFlagCard(cIdx);
      var qKeys = Object.create(null);
      jScanObj(jDepth, function (qKey, valDepth) {
        if (hasOwn.call(qKeys, qKey)) st.dup = true;
        qKeys[qKey] = true;
        if (qKey === "summary") jScanSummary(cIdx, valDepth);
        else jSkipVal(valDepth);
      });
    }
    function jScanCard(cIdx, jDepth) {
      var st = jFlagCard(cIdx);
      var cKeys = Object.create(null);
      jScanObj(jDepth, function (cKey, valDepth) {
        if (hasOwn.call(cKeys, cKey)) st.dup = true;
        cKeys[cKey] = true;
        if (cKey === "quota") jScanQuota(cIdx, valDepth);
        else jSkipVal(valDepth);
      });
    }
    function jScanCardsArr(jDepth) {
      if (jDepth > MAX_JSON_DEPTH) throw new Error("schema");
      jSkipWs();
      if (rawText.charCodeAt(jPos) !== 91) { jSkipVal(jDepth); return; }
      jPos++; jSkipWs();
      if (rawText.charCodeAt(jPos) === 93) { jPos++; return; }
      var arrIdx = 0;
      while (jPos < jLen) {
        jSkipWs();
        jScanCard(arrIdx, jDepth + 1);
        arrIdx++;
        jSkipWs();
        var arrSep = rawText.charCodeAt(jPos);
        if (arrSep === 44) { jPos++; continue; }
        if (arrSep === 93) { jPos++; return; }
        throw new Error("schema");
      }
      throw new Error("schema");
    }
    function jScanAccounts(jDepth) {
      var acctKeys = Object.create(null);
      jScanObj(jDepth, function (aKey, valDepth) {
        if (aKey === "cards") {
          if (hasOwn.call(acctKeys, aKey)) rootAcctDup = true;
          acctKeys[aKey] = true;
          jScanCardsArr(valDepth);
        } else {
          jSkipVal(valDepth);
        }
      });
    }
    jSkipWs();
    jScanObj(0, function (rootKey, valDepth) {
      if (rootKey === "accounts") {
        if (hasOwn.call(rootSeen, "accounts")) rootAcctDup = true;
        rootSeen["accounts"] = true;
        jScanAccounts(valDepth);
      } else {
        jSkipVal(valDepth);
      }
    });
    function jTaintCard(cardObj, asProv) {
      if (!cardObj || typeof cardObj !== "object" || Array.isArray(cardObj)) return;
      if (!cardObj.quota || typeof cardObj.quota !== "object" || Array.isArray(cardObj.quota)) cardObj.quota = {};
      if (asProv) {
        cardObj.quota.summary = { state: "unavailable", freshness: "unavailable", provenance: null, rows: [] };
      } else {
        cardObj.quota.summary = { state: "reported", source: "", freshness: "unavailable", rows: [] };
      }
      delete cardObj.quota.windows;
      delete cardObj.quota.observedAt;
      delete cardObj.quota.retrievedAt;
    }
    if (parsedObj.accounts && typeof parsedObj.accounts === "object" && Array.isArray(parsedObj.accounts.cards)) {
      if (rootAcctDup) {
        for (var kIdx = 0; kIdx < parsedObj.accounts.cards.length; kIdx++) {
          jTaintCard(parsedObj.accounts.cards[kIdx], anyProvSeen);
        }
      } else {
        for (var jIdx = 0; jIdx < cardFlags.length; jIdx++) {
          if (cardFlags[jIdx] && cardFlags[jIdx].dup && jIdx < parsedObj.accounts.cards.length) {
            jTaintCard(parsedObj.accounts.cards[jIdx], cardFlags[jIdx].sawProv);
          }
        }
      }
    }
    return parsedObj;
  }

  function getJSON(url) {
    var ctl = new AbortController();
    var timer = setTimeout(function () { ctl.abort(); }, readTimeoutMs());
    return fetch(url, { cache: "no-store", credentials: "same-origin", headers: { Accept: "application/json" }, signal: ctl.signal })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.text(); })
      .then(function (rawBody) { return parseDockQuotaJSON(rawBody); })
      .finally(function () { clearTimeout(timer); });
  }

  // A bounded companion to the current main read on Accounts and Home. No
  // independent timer or provider refresh. A new snapshot or privacy view
  // invalidates both successful and delayed replies from the previous read.
  function loadSavedAccounts(retry) {
    var d = state.data;
    if (!d || !d.accountsReference || (route().view !== "accounts" && route().view !== "home")) return;
    var ref = d.accountsReference, asked = state.identifiers;
    if (savedAccounts.reference === ref && savedAccounts.status === "ready") { d.accounts = savedAccounts.data; return; }
    if (savedAccounts.reference === ref && (savedAccounts.status === "loading" || (!retry && savedAccounts.status === "failed"))) return;
    if (savedAccounts.abort) savedAccounts.abort.abort();
    var ctl = new AbortController(), seq = ++savedAccounts.serial;
    savedAccounts.reference = ref; savedAccounts.status = "loading"; savedAccounts.data = null; savedAccounts.abort = ctl;
    var timer = setTimeout(function () { ctl.abort(); }, readTimeoutMs());
    function current() { return seq === savedAccounts.serial && state.data && state.data.accountsReference === ref && state.identifiers === asked; }
    fetch(SAVED_ACCOUNTS + "?reference=" + encodeURIComponent(ref), { cache: "no-store", credentials: "same-origin", headers: { Accept: "application/json" }, signal: ctl.signal })
      .then(function (r) { if (!r.ok) throw new Error("saved accounts unavailable"); return r.text(); })
      .then(function (text) {
        if (new TextEncoder().encode(text).length > 1048576) throw new Error("saved accounts bound");
        var result = parseDockQuotaJSON(text);
        if (!result || result.schemaVersion !== SCHEMA || result.reference !== ref || !result.accounts || !Array.isArray(result.accounts.cards) || result.accounts.cards.length > 64) throw new Error("saved accounts schema");
        if (!current()) return;
        savedAccounts.status = "ready"; savedAccounts.data = result.accounts; state.data.accounts = result.accounts;
      })
      .catch(function () { if (current()) savedAccounts.status = "failed"; })
      .finally(function () {
        clearTimeout(timer);
        if (!current()) return;
        savedAccounts.abort = null;
        if (route().view === "accounts" || route().view === "home") render(false, true);
      });
  }

  function loadServer() {
    getJSON(SERVER)
      .then(function (d) {
        if (!d || d.schemaVersion !== 1) return;
        var was = changesOn(), allowanceWas = state.server && state.server.sendAllowanceEdit === true;
        state.server = d;
        renderServed();
        if ((changesOn() !== was || allowanceWas !== (d.sendAllowanceEdit === true)) && state.data) render(false, true);
      })
      .catch(function () { /* the page works without it */ });
  }

  function schedule() {
    clearTimeout(state.timer);
    if (state.paused) return;
    state.timer = setTimeout(load, refreshSeconds() * 1000);
  }

  function findDirectBadge(container) {
    for (var i = 0; i < container.children.length; i++) {
      if (container.children[i].classList && container.children[i].classList.contains("unconfirmed-badge")) return container.children[i];
    }
    return null;
  }
  function syncTaskUnconfirmedDOM() {
    var pane = $("pane");
    if (!pane || !state.data) return;
    Array.prototype.forEach.call(pane.querySelectorAll(".task-card[data-issue-key]"), function (card) {
      var k = card.dataset.issueKey;
      var txt = taskUnconfirmedText(state.data, needFor(k), effectiveLoopByKey(state.data, k));
      var badge = findDirectBadge(card);
      if (txt) {
        card.dataset.unconfirmed = "1";
        if (!badge) {
          badge = el("span", "unconfirmed-badge", txt);
          var topRow = card.querySelector(".task-top");
          card.insertBefore(badge, topRow ? topRow.nextSibling : card.firstChild);
        } else {
          badge.textContent = txt;
        }
      } else if (badge) {
        delete card.dataset.unconfirmed;
        badge.remove();
      }
    });
    Array.prototype.forEach.call(pane.querySelectorAll(".task-drawer[data-drawer-key]"), function (dr) {
      var dk = dr.dataset.drawerKey;
      var dtxt = taskUnconfirmedText(state.data, needFor(dk), effectiveLoopByKey(state.data, dk));
      var dbadge = findDirectBadge(dr);
      if (dtxt) {
        if (!dbadge) {
          dbadge = el("span", "unconfirmed-badge", dtxt);
          var dHead = dr.querySelector(".task-drawer-head");
          dr.insertBefore(dbadge, dHead ? dHead.nextSibling : dr.firstChild);
        } else {
          dbadge.textContent = dtxt;
        }
      } else if (dbadge) {
        dbadge.remove();
      }
    });
  }

  function load(byHand) {
    if (state.busy) { state.again = true; return; }
    // A hidden tab reads nothing, not even what serves the page: any read
    // keeps the panel from stopping by itself when idle. It reads once when
    // it is shown again.
    if (document.hidden && !byHand) { state.missed = true; return; }
    state.missed = false;
    // What serves the page and whether it allows changes, read again with
    // every read so the footer's ages stay current.
    if (state.data) loadServer();
    state.busy = true;
    var btn = $("refresh");
    btn.setAttribute("aria-busy", "true");
    var asked = state.identifiers;
    getJSON(API + (asked ? "?identifiers=1" : ""))
      .then(function (d) {
        if (!d || d.schemaVersion !== SCHEMA || !d.registry || !Array.isArray(d.loops)) throw new Error("schema");
        // Identifiers were turned on or off while this read was out: the
        // reply is for the other view, so it is dropped and read again.
        if (asked !== state.identifiers) { state.again = true; return; }
        if (savedAccounts.reference !== d.accountsReference && savedAccounts.abort) { savedAccounts.abort.abort(); ++savedAccounts.serial; }
        if (d.accountsReference && savedAccounts.reference === d.accountsReference && savedAccounts.status === "ready") d.accounts = savedAccounts.data;
        var prevData = state.data;
        var prevNeeds = prevData ? effectiveNeeds(prevData) : [];
        if (d.complete === false) {
          var seenNeedKeys = Object.create(null);
          list(d.needsYou).forEach(function (n) { seenNeedKeys[n.key] = true; });
          var seenLoopMap = Object.create(null);
          list(d.loops).forEach(function (l) { seenLoopMap[l.key] = l; });
          var prevLoops = Object.create(null);
          if (state.retainedLoops) Object.keys(state.retainedLoops).forEach(function (k) { prevLoops[k] = state.retainedLoops[k]; });
          if (prevData) list(prevData.loops).forEach(function (l) { prevLoops[l.key] = l; });
          var keptNeeds = [];
          var keptLoops = Object.create(null);
          var hideIds = !d.identifiers || !state.identifiers;
          prevNeeds.forEach(function (pn) {
            if (!seenNeedKeys[pn.key]) {
              var cloneNeed = Object.assign({}, pn, { _retainedIncomplete: true });
              if (hideIds) cloneNeed = scrubNeedForHiddenIds(cloneNeed);
              keptNeeds.push(cloneNeed);
              if (!seenLoopMap[pn.key] && prevLoops[pn.key]) {
                keptLoops[pn.key] = hideIds ? scrubLoopForHiddenIds(prevLoops[pn.key]) : prevLoops[pn.key];
              }
            }
          });
          state.retainedNeeds = keptNeeds;
          state.retainedLoops = keptLoops;
        } else {
          state.retainedNeeds = [];
          state.retainedLoops = Object.create(null);
        }
        state.data = d; state.lastOk = Date.now(); state.failed = false; state.failedSince = 0; state.failedLong = false;
        if (wantsUsage(route())) loadUsage();
        if (render(false) !== false) notice("cut", d.complete === false ? "This read was cut off before every loop was read. Loops that were not read are listed as could not be read; it reads again shortly." : "");
        loadSavedAccounts(true);
      })
      .catch(function (err) {
        state.failed = true;
        if (!state.failedSince) state.failedSince = Date.now();
        // A read can fail for a moment and recover by itself; only reads
        // failing for FAIL_LONG_MS need a person.
        state.failedLong = Date.now() - state.failedSince >= FAIL_LONG_MS;
        var schema = err && err.message === "schema";
        var cmd = "keepgoing " + served();
        var again = served() === "panel" ? "run looptender panel again." : "start it again and open the new address it prints.";
        var text = schema ? "This page does not understand the reply it was sent. Reload the page, or restart " + cmd + "." :
          !state.data ? "Could not read from " + cmd + ". It may have stopped; " + again :
          state.failedLong ? "Reads have failed since " + secFmt.format(new Date(state.failedSince)) + ", so what you see is out of date. " + cmd + " may have stopped; if so, " + again :
          "The last read failed, so what you see may be out of date. " + cmd + " may have stopped; it tries again shortly.";
        notice(schema ? "schema" : "failed", text, schema || state.failedLong);
        if (!state.data) {
          $("picker").replaceChildren(el("p", "placeholder", "Nothing could be read."));
          $("pane").replaceChildren(paneHead("Could not read"), emptyBox("Nothing could be read from " + cmd + ".", "It may have stopped; " + again));
        } else {
          render(false);
          syncTaskUnconfirmedDOM();
        }
        renderReadline();
      })
      .then(function () {
        state.busy = false;
        btn.removeAttribute("aria-busy");
        if (state.again) { state.again = false; load(true); return; }
        schedule();
      });
  }

  document.addEventListener("DOMContentLoaded", function () {
    $("refresh").addEventListener("click", function () { clearTimeout(state.timer); load(true); });
    $("pause").addEventListener("click", function () {
      state.paused = !state.paused;
      this.setAttribute("aria-pressed", String(state.paused));
      if (state.paused) clearTimeout(state.timer); else load(true);
      renderReadline();
    });
    $("identifiers").addEventListener("click", function () {
      state.identifiers = !state.identifiers;
      this.setAttribute("aria-pressed", String(state.identifiers));
      if (!state.identifiers) scrubRetainedForHiddenIds();
      // Hide (or keep hidden) at once from the data in hand: identifiers and
      // unmasked notes never stay on screen waiting for a read that is slow or failing.
      render(false, true);
      clearTimeout(state.timer);
      load(true);
    });
    // The skip link moves focus to the details without changing the view.
    document.querySelector("a.skip").addEventListener("click", function (ev) {
      ev.preventDefault();
      focusPaneTitle(true);
    });
    window.addEventListener("hashchange", function () {
      var nextRoute = route();
      if (nextRoute.view !== "loop" || nextRoute.key !== loopRouteEdit.loopKey) loopRouteReset("", "");
      render(true);
      loadSavedAccounts(false);
      if (wantsUsage(route())) loadUsage();
    });
    document.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape") {
        if (state.change && state.change.preview && !state.change.busy) { ev.preventDefault(); cancelChange(); }
        else if (loopRouteEdit.reply && loopRouteEdit.reply.form && !loopRouteEdit.busy) {
          ev.preventDefault(); loopRouteEdit.reqSeq += 1; loopRouteEdit.reply = null; loopRouteEdit.message = ""; render(false, true);
        } else if (loopRouteEdit.panel && !loopRouteEdit.busy) {
          ev.preventDefault(); loopRouteSwitchPanel(""); render(false, true);
        }
        else if (state.activeDrawer) { ev.preventDefault(); state.activeDrawer = null; render(false, true); }
        else if (state.mapSelected) { ev.preventDefault(); state.mapSelected = null; render(false, true); }
      }
    });
    document.addEventListener("selectionchange", function () {
      if (state.pending && !selecting()) render(false);
    });
    setInterval(updateAccountClocks, 15000);
    document.addEventListener("visibilitychange", function () {
      updateAccountClocks();
      if (!document.hidden && !state.paused && (state.missed || (state.lastOk && Date.now() - state.lastOk > 5000))) { clearTimeout(state.timer); load(true); }
    });
    loadServer();
    load(true);
  });
})();
