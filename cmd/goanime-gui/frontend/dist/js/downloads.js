import { app, errText } from "./bridge.js";
import { state } from "./state.js";
import { clear, els, humanBytes, setStatus, toast } from "./dom.js";

export async function downloadEpisode(ep) {
  try {
    await app().StartDownload(state.result, ep, state.quality);
    toast(`Episódio ${ep.number || ep.num} na fila`);
    openDrawer();
  } catch (err) {
    console.error(err);
    setStatus(`Não foi possível iniciar o download: ${errText(err)}`, {
      error: true,
    });
  }
}

function activeDownloadCount() {
  let n = 0;
  for (const d of state.downloads.values()) {
    if (d.state === "resolving" || d.state === "downloading") n++;
  }
  return n;
}

function refreshDownloadsChip() {
  const active = activeDownloadCount();
  els.downloadsCount.textContent = active || state.downloads.size || 0;
  els.downloadsCount.classList.toggle("active", active > 0);
}

function renderDownloads() {
  const items = [...state.downloads.values()].sort(
    (a, b) => jobNum(b.id) - jobNum(a.id)
  );

  els.downloadsEmpty.hidden = items.length > 0;
  clear(els.downloadsList);

  for (const d of items) {
    const li = document.createElement("li");
    li.className = `dl ${d.state}`;

    const top = document.createElement("div");
    top.className = "dl-top";

    const title = document.createElement("span");
    title.className = "dl-title";
    title.textContent = d.title;
    top.appendChild(title);

    if (d.state === "resolving" || d.state === "downloading") {
      const cancel = document.createElement("button");
      cancel.type = "button";
      cancel.className = "icon-btn";
      cancel.textContent = "Cancelar";
      cancel.addEventListener("click", () => app().CancelDownload(d.id));
      top.appendChild(cancel);
    }
    li.appendChild(top);

    const bar = document.createElement("div");
    bar.className = "dl-bar";
    const fill = document.createElement("div");
    fill.className = "dl-fill";
    fill.style.width = `${Math.max(0, Math.min(100, d.percent || 0))}%`;
    bar.appendChild(fill);
    li.appendChild(bar);

    const meta = document.createElement("div");
    meta.className = "dl-meta";

    const left = document.createElement("span");
    left.textContent = stateLabel(d);
    meta.appendChild(left);

    const right = document.createElement("span");
    right.textContent = d.total
      ? `${humanBytes(d.received)} / ${humanBytes(d.total)}`
      : humanBytes(d.received);
    meta.appendChild(right);
    li.appendChild(meta);

    if (d.error) {
      const err = document.createElement("div");
      err.className = "dl-error";
      err.textContent = d.error;
      li.appendChild(err);
    }

    els.downloadsList.appendChild(li);
  }

  refreshDownloadsChip();
}

function stateLabel(d) {
  switch (d.state) {
    case "resolving":
      return "Obtendo o link…";
    case "downloading":
      return `${Math.round(d.percent || 0)}%`;
    case "done":
      return "Concluído";
    case "cancelled":
      return "Cancelado";
    case "error":
      return "Falhou";
    default:
      return d.state;
  }
}

function jobNum(id) {
  return parseInt(String(id).replace("job-", ""), 10) || 0;
}

export function openDrawer() {
  els.drawer.hidden = false;
  renderDownloads();
}

export async function loadDownloads() {
  try {
    const jobs = (await app().DownloadStatus()) || [];
    state.downloads = new Map(jobs.map((j) => [j.id, j]));
  } catch (err) {
    console.warn("DownloadStatus() failed", err);
  }
  try {
    els.downloadsPath.textContent = await app().DownloadsFolder();
  } catch {
    /* path is a nicety, not worth surfacing a failure for */
  }
  renderDownloads();
}

// wireDownloadEvents subscribes to the backend's progress stream. Falls back
// to nothing if the runtime is absent (e.g. the page opened outside Wails).
export function wireDownloadEvents() {
  if (!window.runtime || !window.runtime.EventsOn) {
    console.warn(
      "Wails runtime events unavailable; downloads won't live-update"
    );
    return;
  }
  window.runtime.EventsOn("download:progress", (p) => {
    if (!p || !p.id) return;
    state.downloads.set(p.id, p);
    if (!els.drawer.hidden) renderDownloads();
    else refreshDownloadsChip();

    if (p.state === "done") toast(`Baixado: ${p.title}`);
    if (p.state === "error") toast(`Falha no download: ${p.title}`);
  });
}
