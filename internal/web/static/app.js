(() => {
  const $ = (s, el = document) => el.querySelector(s);
  const state = { sessions: [], current: null, session: null, status: null, statusErr: null, tab: "timeline", filter: "all" };

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const ago = (iso) => {
    const ms = Date.now() - new Date(iso).getTime();
    const m = Math.floor(ms / 60000), h = Math.floor(m / 60), d = Math.floor(h / 24);
    return d > 0 ? `${d}d ago` : h > 0 ? `${h}h ago` : m > 0 ? `${m}m ago` : "just now";
  };
  const time = (iso) => new Date(iso).toLocaleString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });

  // enough markdown for review comments: fences, inline code, bold, bullets
  function md(src) {
    const parts = String(src ?? "").split(/```/);
    let out = "";
    for (let i = 0; i < parts.length; i++) {
      if (i % 2 === 1) {
        const nl = parts[i].indexOf("\n");
        const info = nl >= 0 ? parts[i].slice(0, nl).trim() : "";
        const body = nl >= 0 ? parts[i].slice(nl + 1) : parts[i];
        out += `<pre class="${info === "suggestion" ? "suggestion" : ""}">${esc(body.replace(/\n$/, ""))}</pre>`;
        continue;
      }
      const lines = parts[i].split("\n");
      let para = [], list = [];
      const flushP = () => { if (para.length) { out += `<p>${inline(para.join(" "))}</p>`; para = []; } };
      const flushL = () => { if (list.length) { out += `<ul>${list.map((l) => `<li>${inline(l)}</li>`).join("")}</ul>`; list = []; } };
      for (const raw of lines) {
        const l = raw.trim();
        if (!l) { flushP(); flushL(); continue; }
        if (/^[-*] /.test(l)) { flushP(); list.push(l.slice(2)); continue; }
        if (/^#{1,6} /.test(l)) { flushP(); flushL(); out += `<p><strong>${inline(l.replace(/^#+ /, ""))}</strong></p>`; continue; }
        flushL(); para.push(l);
      }
      flushP(); flushL();
    }
    return `<div class="md">${out}</div>`;
  }
  function inline(s) {
    return esc(s).replace(/`([^`]+)`/g, "<code>$1</code>").replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  }

  async function api(path) {
    const r = await fetch(path);
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || r.statusText);
    return j;
  }

  async function loadSessions() {
    try { state.sessions = await api("/api/sessions"); } catch (e) { return; }
    const el = $("#sessions");
    if (!state.sessions.length) { el.innerHTML = `<div class="muted pad">No review sessions yet.</div>`; return; }
    el.innerHTML = state.sessions.map((s) => `
      <div class="session ${s.id === state.current ? "active" : ""}" data-id="${esc(s.id)}">
        <div class="title">#${s.number} ${esc(s.title)}</div>
        <div class="muted small">${esc(s.owner)}/${esc(s.repo)} · ${esc(s.author)}</div>
        <div class="muted small">${s.events} events · ${s.posted} posted · ${ago(s.updated_at)}</div>
      </div>`).join("");
    el.querySelectorAll(".session").forEach((n) => n.onclick = () => select(n.dataset.id));
    if (!state.current && state.sessions.length) select(location.hash.slice(1) || state.sessions[0].id);
  }

  async function select(id) {
    state.current = id;
    location.hash = id;
    $("#empty").classList.add("hidden");
    $("#detail").classList.remove("hidden");
    document.querySelectorAll(".session").forEach((n) => n.classList.toggle("active", n.dataset.id === id));
    await Promise.all([loadSession(), loadStatus(true)]);
  }

  async function loadSession() {
    if (!state.current) return;
    try { state.session = await api(`/api/sessions/${state.current}`); } catch (e) { return; }
    renderHead(); renderTimeline();
  }

  async function loadStatus(force) {
    if (!state.current) return;
    try {
      state.status = await api(`/api/sessions/${state.current}/status${force ? "?refresh=1" : ""}`);
      state.statusErr = null;
    } catch (e) { state.statusErr = e.message; }
    renderHead(); renderStats(); renderComments(); renderActivity();
  }

  function renderHead() {
    const s = state.session; if (!s) return;
    $("#pr-repo").textContent = `${s.owner}/${s.repo}`;
    $("#pr-title").textContent = `#${s.number} ${s.title}`;
    $("#pr-meta").textContent = `${s.author} · ${s.head_ref} → ${s.base_ref} · session started ${new Date(s.started_at).toLocaleString()}`;
    $("#pr-link").href = s.url;
    const st = state.status;
    const badge = $("#pr-state");
    if (st) { badge.textContent = st.state + (st.draft ? " · draft" : ""); badge.className = `badge ${st.state}`; }
    else { badge.textContent = "…"; badge.className = "badge neutral"; }
  }

  function renderStats() {
    const st = state.status, el = $("#stats");
    if (!st) { el.innerHTML = state.statusErr ? `<div class="err">Gitea status unavailable: ${esc(state.statusErr)}</div>` : ""; return; }
    const t = st.totals;
    const ciClass = st.ci === "success" ? "good" : st.ci === "pending" ? "warn" : st.ci === "none" ? "" : "bad";
    const posted = (state.session?.posted || []).length;
    el.innerHTML = `
      <div class="stat"><div class="label">Inline comments</div><div class="value">${t.comments}</div><div class="sub">${posted} post${posted === 1 ? "" : "s"} from this session</div></div>
      <div class="stat"><div class="label">Resolved by author</div><div class="value good">${t.resolved}</div><div class="sub">${t.comments ? Math.round(100 * t.resolved / t.comments) : 0}% done</div></div>
      <div class="stat"><div class="label">Still open</div><div class="value ${t.open ? "warn" : "good"}">${t.open}</div><div class="sub">${t.outdated} outdated by newer commits</div></div>
      <div class="stat"><div class="label">Commits since review</div><div class="value">${st.new_commits.length}</div><div class="sub">${st.reviewed_sha ? "reviewed at " + st.reviewed_sha.slice(0, 8) : "no review posted yet"}</div></div>
      <div class="stat"><div class="label">CI</div><div class="value ${ciClass}">${esc(st.ci)}</div><div class="sub">${st.checks.length} check${st.checks.length === 1 ? "" : "s"} · ${st.mergeable ? "mergeable" : "not mergeable"}</div></div>`;
  }

  function renderTimeline() {
    const s = state.session, el = $("#tab-timeline");
    if (!s || !s.events?.length) { el.innerHTML = `<div class="muted">Nothing recorded yet.</div>`; return; }
    // runs of tool calls collapse into one row so notes and posts stand out
    const groups = [];
    for (const ev of s.events) {
      const last = groups[groups.length - 1];
      if (ev.kind === "tool" && last && last.kind === "tools") last.items.push(ev);
      else if (ev.kind === "tool") groups.push({ kind: "tools", items: [ev] });
      else groups.push({ kind: ev.kind, ev });
    }
    el.innerHTML = `<div class="timeline">${groups.map((g) => {
      if (g.kind === "tools") {
        const n = g.items.length;
        return `<div class="ev tool">
          <div class="row"><span class="who">Claude</span><span class="t">${n === 1 ? esc(g.items[0].title) : `Investigated: ${n} tool calls`}</span><span class="when">${time(g.items[0].at)}</span></div>
          ${n > 1 ? `<details><summary>show calls</summary><div class="tool-group">${g.items.map((e) => `<div>${time(e.at)} · <code>${esc(e.tool)}</code> ${esc(e.title)}</div>`).join("")}</div></details>` : ""}
        </div>`;
      }
      const e = g.ev;
      const who = e.kind === "note" ? (e.tool || "note") : e.kind === "post" ? "posted" : e.kind === "error" ? "error" : e.kind;
      return `<div class="ev ${e.kind}">
        <div class="row"><span class="who">${esc(who)}</span><span class="t">${esc(e.title)}</span><span class="when">${time(e.at)}</span></div>
        ${e.detail ? (e.kind === "post" && e.tool === "post_review" ? renderPostDetail(e.detail) : `<div class="detail-body">${md(e.detail)}</div>`) : ""}
      </div>`;
    }).join("")}</div>`;
  }

  function renderPostDetail(json) {
    let d; try { d = JSON.parse(json); } catch { return `<div class="detail-body">${md(json)}</div>`; }
    return `<div class="detail-body">${md(d.summary)}${(d.comments || []).map((c) => `
      <div style="margin-top:8px"><code>${esc(c.path)}:${c.side === "old" ? "old " : ""}${c.line}</code>${md(c.body)}</div>`).join("")}</div>`;
  }

  function renderComments() {
    const st = state.status, el = $("#tab-comments");
    if (!st) { el.innerHTML = `<div class="muted">${state.statusErr ? esc(state.statusErr) : "Loading…"}</div>`; return; }
    const all = st.comments;
    const shown = all.filter((c) => state.filter === "all" || (state.filter === "open" ? !c.resolved : c.resolved));
    el.innerHTML = `
      <div class="filters">
        ${["all", "open", "resolved"].map((f) => `<button class="chip ${state.filter === f ? "active" : ""}" data-f="${f}">${f} (${f === "all" ? all.length : f === "open" ? st.totals.open : st.totals.resolved})</button>`).join("")}
      </div>
      ${shown.length ? `<table><thead><tr><th>Status</th><th>Location</th><th>Comment</th><th>By</th></tr></thead><tbody>
        ${shown.map((c) => `<tr class="${c.resolved ? "resolved" : ""}">
          <td>${c.resolved ? `<span class="badge ok">resolved</span><div class="muted small">by ${esc(c.resolver)}</div>` : `<span class="badge warn">open</span>`}${c.outdated ? `<div class="muted small">commits since</div>` : ""}</td>
          <td class="loc"><a href="${esc(c.url)}" target="_blank" rel="noopener">${esc(c.path)}:${c.side === "old" ? "old " : ""}${c.line}</a></td>
          <td>${md(c.body)}</td>
          <td class="small">${esc(c.author)}<div class="muted">${ago(c.created_at)}</div></td>
        </tr>`).join("")}</tbody></table>` : `<div class="muted">No ${state.filter === "all" ? "" : state.filter + " "}inline comments.</div>`}
      ${st.issue_comments.length ? `<div class="section-title">Conversation</div>${st.issue_comments.map((c) => `<div class="ev"><div class="row"><span class="who">${esc(c.author)}</span><span class="t"></span><span class="when">${ago(c.created_at)}</span></div><div class="detail-body">${md(c.body)}</div></div>`).join("")}` : ""}`;
    el.querySelectorAll(".chip").forEach((b) => b.onclick = () => { state.filter = b.dataset.f; renderComments(); });
  }

  function renderActivity() {
    const st = state.status, el = $("#tab-activity");
    if (!st) { el.innerHTML = `<div class="muted">Loading…</div>`; return; }
    el.innerHTML = `
      <div class="section-title">Commits since the review (${st.new_commits.length})</div>
      ${st.new_commits.length ? st.new_commits.map((c) => `<div class="commit"><a class="sha" href="${esc(c.url)}" target="_blank" rel="noopener">${c.sha.slice(0, 8)}</a><div>${esc(c.message)}<div class="muted small">${esc(c.author)} · ${new Date(c.date).toLocaleString()}</div></div></div>`).join("")
        : `<div class="muted">${st.reviewed_sha ? "Nothing pushed since the review." : "No review posted yet, so there is no reference point."}</div>`}
      <div class="section-title">CI checks on ${st.head_sha.slice(0, 8)}</div>
      ${st.checks.length ? st.checks.map((c) => `<div class="commit"><span class="badge ${c.status === "success" ? "ok" : c.status === "pending" ? "warn" : "bad"}">${esc(c.status)}</span><div>${c.url ? `<a href="${esc(c.url)}" target="_blank" rel="noopener">${esc(c.context)}</a>` : esc(c.context)}<div class="muted small">${esc(c.description)}</div></div></div>`).join("") : `<div class="muted">No CI statuses reported.</div>`}
      <div class="section-title">Reviews on this PR</div>
      ${st.reviews.length ? st.reviews.map((r) => `<div class="commit"><span class="badge ${r.state === "APPROVED" ? "ok" : r.state === "REQUEST_CHANGES" ? "bad" : "neutral"}">${esc(r.state)}</span><div><a href="${esc(r.url)}" target="_blank" rel="noopener">${esc(r.author)}</a> · ${r.comments} inline${r.stale ? " · <span class='muted'>stale (new commits)</span>" : ""}${r.dismissed ? " · dismissed" : ""}<div class="muted small">${new Date(r.submitted_at).toLocaleString()}</div></div></div>`).join("") : `<div class="muted">No reviews yet.</div>`}
      <div class="muted small" style="margin-top:16px">Fetched ${time(st.fetched_at)} · refreshes automatically</div>`;
  }

  document.querySelectorAll(".tab").forEach((b) => b.onclick = () => {
    state.tab = b.dataset.tab;
    document.querySelectorAll(".tab").forEach((x) => x.classList.toggle("active", x === b));
    document.querySelectorAll(".tab-panel").forEach((p) => p.classList.toggle("hidden", p.id !== `tab-${state.tab}`));
  });

  loadSessions();
  setInterval(loadSessions, 4000);
  setInterval(loadSession, 3000);
  setInterval(() => loadStatus(false), 15000);
})();
