const POLL_MS = 10000;

function timeAgoMs(ms) {
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 60) return `${Math.round(s)}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

function timeAgo(iso) {
  if (!iso) return "never";
  return timeAgoMs(new Date(iso).getTime());
}

function bars(m) {
  // Prefer per-day bars: each bar is a whole day (a "much bigger window").
  if (Array.isArray(m.daily) && m.daily.length) {
    const recent = m.daily.slice(-40);
    return recent
      .map((b) => {
        const pct = b.pct ?? 100;
        const cls = pct <= 0 ? "down" : pct < 100 ? "warn" : "up";
        const title = `${b.day} — ${b.up}/${b.total} checks (${pct.toFixed(1)}% up)`;
        return `<div class="bar ${cls}" title="${title.replace(/"/g, "&quot;")}"></div>`;
      })
      .join("");
  }

  // Fallback (no per-day history): the most recent ~40 checks as a strip.
  const recent = (m.history || []).slice(-40);
  return recent
    .map((r) => {
      const cls = r.up ? "up" : "down";
      const title = `${new Date(r.time).toLocaleString()} — ${
        r.up ? `${r.latency_ms.toFixed(0)}ms` : (r.err || "down")
      }`;
      return `<div class="bar ${cls}" title="${title.replace(/"/g, "&quot;")}"></div>`;
    })
    .join("");
}

function renderVersion(v) {
  const el = document.getElementById("version");
  if (!el || !v) return;
  const repo = v.repo || "https://github.com/zachpmanson/downtime";
  const sep = `<span class="sep">·</span>`;

  const parts = [
    `powered by <a href="${repo}" target="_blank" rel="noopener"><strong>downtime</strong></a>`,
  ];
  if (v.commit && v.commit !== "dev") {
    parts.push(
      `<a href="${repo}/commit/${v.commit}" target="_blank" rel="noopener" class="commit">${v.commit}</a>`
    );
  }
  if (v.built_unix) {
    const d = new Date(v.built_unix * 1000);
    parts.push(
      `deployed ${d.toLocaleDateString(undefined, {
        day: "numeric", month: "short", year: "numeric",
      })}`
    );
  }

  // Join with the same .sep separator the footer uses between "updated" and
  // this block, so every dot is spaced identically.
  el.innerHTML = parts.join(sep);
}

const STATUS_LABELS = {
  up: "Operational",
  down: "Down",
  disabled: "Disabled",
  pending: "Pending",
  unknown: "Unknown",
};

function card(m) {
  const label = STATUS_LABELS[m.status] || "Pending";

  // A disabled monitor is decommissioned: skip the bars/uptime/latency stats
  // (there's no probe data) and just note it's not being checked.
  if (m.status === "disabled") {
    return `
      <div class="card disabled">
        <div class="card-top">
          <div>
            <div class="name">${m.name}</div>
            <div class="target">${m.target}</div>
          </div>
          <div class="status disabled"><span class="dot"></span>${label}</div>
        </div>
        <div class="meta"><span>not being checked</span></div>
      </div>`;
  }

  // An unknown monitor is a coverage gap: it has no probe data, so skip the
  // bars/uptime stats and show when it was last seen instead.
  if (m.status === "unknown") {
    return `
      <div class="card unknown">
        <div class="card-top">
          <div>
            <div class="name">${m.name}</div>
            <div class="target">${m.target}</div>
          </div>
          <div class="status unknown"><span class="dot"></span>${label}</div>
        </div>
        <div class="meta"><span>no data since ${m.since ? timeAgo(m.since) : "—"} — monitor gap</span></div>
      </div>`;
  }

  return `
    <div class="card">
      <div class="card-top">
        <div>
          <div class="name">${m.name}</div>
          <div class="target">${m.target}</div>
        </div>
        <div class="status ${m.status}"><span class="dot"></span>${label}</div>
      </div>
      <div class="bars">${bars(m)}</div>
      <div class="meta">
        <span>${m.uptime_pct.toFixed(2)}% uptime (all-time)</span>
        <span>${m.last_latency_ms ? m.last_latency_ms.toFixed(0) + "ms" : "—"}</span>
        <span>checked ${timeAgo(m.last_check)}</span>
      </div>
    </div>`;
}

// --- Sorting (linkable via ?sort=) ---

const SORTS = [
  ["default", "Default"],
  ["alpha", "A–Z"],
  ["worst", "Worst first"],
];

function currentSort() {
  const s = new URLSearchParams(location.search).get("sort");
  return SORTS.some(([k]) => k === s) ? s : "default";
}

function sortMonitors(monitors, mode) {
  const arr = monitors.slice();
  if (mode === "alpha") {
    arr.sort((a, b) => a.name.localeCompare(b.name));
  } else if (mode === "worst") {
    // down (worst) → unknown → pending → up → disabled, then least-reliable first.
    const rank = { down: 0, unknown: 1, pending: 2, up: 3, disabled: 4 };
    arr.sort(
      (a, b) =>
        (rank[a.status] ?? 4) - (rank[b.status] ?? 4) ||
        a.uptime_pct - b.uptime_pct ||
        a.name.localeCompare(b.name)
    );
  }
  return arr; // "default" keeps config order
}

function renderControls() {
  const el = document.getElementById("controls");
  if (!el) return;
  const mode = currentSort();
  el.innerHTML =
    "sort: " +
    SORTS.map(([k, label]) => {
      const href = k === "default" ? "?" : `?sort=${k}`;
      return k === mode
        ? `<span class="active">${label}</span>`
        : `<a href="${href}">${label}</a>`;
    }).join("");
}

let lastData = null;
let lastFetchMs = null; // client time of the last successful poll
let lastError = false;

// Update just the "updated Xs ago" stamp. Driven by a 1s ticker so it counts
// up between polls and keeps growing (signalling staleness) if polling stalls.
function updateStamp() {
  const el = document.getElementById("updated");
  if (!el) return;
  if (lastFetchMs == null) {
    el.textContent = "connecting…";
    return;
  }
  const ago = timeAgoMs(lastFetchMs);
  el.textContent = lastError ? `stale — last updated ${ago}` : `updated ${ago}`;
}

function render(data) {
  if (!data) return;
  const mons = sortMonitors(data.monitors || [], currentSort());

  document.getElementById("monitors").innerHTML = mons.map(card).join("");

  const anyDown = mons.some((m) => m.status === "down");
  const overall = document.getElementById("overall");
  if (anyDown) {
    overall.textContent = "Some systems down";
    overall.className = "overall down";
  } else {
    overall.textContent = "All systems operational";
    overall.className = "overall up";
  }
  updateStamp();
  renderVersion(data.version);
  renderControls();
}

async function refresh() {
  try {
    const res = await fetch("api/status", { cache: "no-store" });
    lastData = await res.json();
    lastFetchMs = Date.now();
    lastError = false;
    render(lastData);
  } catch (e) {
    lastError = true;
    updateStamp(); // keep showing the (now growing) last-updated time
  }
}

// Re-sort instantly (no refetch) when a sort link is clicked, and keep the URL
// in sync so the view is shareable.
document.addEventListener("click", (e) => {
  const a = e.target.closest("#controls a");
  if (!a) return;
  e.preventDefault();
  history.pushState(null, "", a.getAttribute("href"));
  render(lastData);
});
window.addEventListener("popstate", () => render(lastData));

renderControls();
refresh();
setInterval(refresh, POLL_MS);
setInterval(updateStamp, 1000); // tick the "updated Xs ago" stamp every second
