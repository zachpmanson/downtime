const POLL_MS = 10000;

function timeAgo(iso) {
  if (!iso) return "never";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${Math.round(s)}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

function bars(history) {
  // Show the most recent ~40 checks as a kuma-style strip.
  const recent = history.slice(-40);
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
  const name = `powered by <a href="${repo}" target="_blank" rel="noopener"><strong>downtime</strong></a>`;

  let commit = "";
  if (v.commit && v.commit !== "dev") {
    commit = ` · <a href="${repo}/commit/${v.commit}" target="_blank" rel="noopener" class="commit">${v.commit}</a>`;
  }

  let deployed = "";
  if (v.built_unix) {
    const d = new Date(v.built_unix * 1000);
    deployed = ` · deployed ${d.toLocaleDateString(undefined, {
      day: "numeric", month: "short", year: "numeric",
    })}`;
  }

  el.innerHTML = name + commit + deployed;
}

function card(m) {
  const label = m.status === "up" ? "Operational" : m.status === "down" ? "Down" : "Pending";
  return `
    <div class="card">
      <div class="card-top">
        <div>
          <div class="name">${m.name}</div>
          <div class="target">${m.target}</div>
        </div>
        <div class="status ${m.status}"><span class="dot"></span>${label}</div>
      </div>
      <div class="bars">${bars(m.history)}</div>
      <div class="meta">
        <span>${m.uptime_pct.toFixed(2)}% uptime</span>
        <span>${m.last_latency_ms ? m.last_latency_ms.toFixed(0) + "ms" : "—"}</span>
        <span>checked ${timeAgo(m.last_check)}</span>
      </div>
    </div>`;
}

async function refresh() {
  try {
    const res = await fetch("api/status", { cache: "no-store" });
    const data = await res.json();
    const mons = data.monitors || [];

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
    document.getElementById("updated").textContent = "updated " + timeAgo(data.generated);
    renderVersion(data.version);
  } catch (e) {
    document.getElementById("updated").textContent = "connection error — retrying";
  }
}

refresh();
setInterval(refresh, POLL_MS);
