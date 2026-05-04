const REFRESH_INTERVAL = 5000;

const state = {
  chargePoints: [],
  lastRefresh: null,
};

const elements = {
  chargepoints: document.getElementById("chargepoints"),
  sessionsList: document.getElementById("sessions-list"),
  summary: document.getElementById("summary"),
  lastRefresh: document.getElementById("last-refresh"),
  refreshButton: document.getElementById("refresh-button"),
  statTotal: document.getElementById("stat-total"),
  statOnline: document.getElementById("stat-online"),
  statSessions: document.getElementById("stat-sessions"),
  statEnergy: document.getElementById("stat-energy"),
  remoteStartForm: document.getElementById("remote-start-form"),
  remoteStartCP: document.getElementById("remote-start-cp"),
  remoteStartIdTag: document.getElementById("remote-start-idTag"),
  remoteStartConnector: document.getElementById("remote-start-connector"),
  remoteStopForm: document.getElementById("remote-stop-form"),
  remoteStopTransaction: document.getElementById("remote-stop-transaction"),
  remoteStopCP: document.getElementById("remote-stop-cp"),
  remoteStopId: document.getElementById("remote-stop-id"),
  resetForm: document.getElementById("reset-form"),
  resetCP: document.getElementById("reset-cp"),
  resetType: document.getElementById("reset-type"),
  unlockForm: document.getElementById("unlock-form"),
  unlockCP: document.getElementById("unlock-cp"),
  unlockConnector: document.getElementById("unlock-connector"),
};

let refreshTimer = null;
let messageTimer = null;
let isRefreshing = false;

init();

function init() {
  elements.refreshButton.addEventListener("click", () => refreshData(true));
  elements.remoteStartForm.addEventListener("submit", handleRemoteStart);
  elements.remoteStopForm.addEventListener("submit", handleRemoteStop);
  elements.remoteStopTransaction.addEventListener("change", handleTransactionSelect);
  elements.resetForm.addEventListener("submit", handleReset);
  elements.unlockForm.addEventListener("submit", handleUnlock);

  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      stopAutoRefresh();
    } else {
      refreshData();
      startAutoRefresh();
    }
  });

  refreshData();
  startAutoRefresh();
}

function startAutoRefresh() {
  stopAutoRefresh();
  refreshTimer = setInterval(refreshData, REFRESH_INTERVAL);
}

function stopAutoRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
}

async function refreshData(manual = false) {
  if (isRefreshing) return;
  isRefreshing = true;
  setButtonLoading(elements.refreshButton, true);

  try {
    const data = await fetchChargePoints();
    state.chargePoints = Array.isArray(data) ? data : [];
    state.lastRefresh = Date.now();

    renderCPCards(state.chargePoints);
    renderSessionsList(state.chargePoints);
    renderStats(state.chargePoints);
    updateChargePointSelects(state.chargePoints);
    updateTransactionOptions(state.chargePoints);
    updateSummary(state.chargePoints);
    updateLastRefresh();

    if (manual) {
      displayMessage("success", "Data refreshed successfully.");
    }
  } catch (error) {
    displayMessage("error", `Failed to load data: ${error.message}`);
  } finally {
    isRefreshing = false;
    setButtonLoading(elements.refreshButton, false);
  }
}

async function fetchChargePoints() {
  const response = await fetch("/api/chargepoints", {
    headers: { Accept: "application/json" },
  });
  if (!response.ok) {
    let message = response.statusText;
    try {
      const payload = await response.json();
      if (payload && payload.error) message = payload.error;
    } catch (_) {}
    throw new Error(message || "Unexpected server response");
  }
  return response.json();
}

// ===== RENDER: Stats bar =====

function renderStats(chargePoints) {
  const total = chargePoints.length;
  const online = chargePoints.filter((cp) => cp.connected).length;
  const activeSessions = chargePoints.reduce(
    (acc, cp) => acc + (Array.isArray(cp.activeTransactions) ? cp.activeTransactions.length : 0),
    0,
  );

  // Latest completed transaction energy across all CPs
  let latestEnergyKWh = 0;
  let latestStoppedAt = null;
  chargePoints.forEach((cp) => {
    if (cp.lastCompletedTransaction) {
      const tx = cp.lastCompletedTransaction;
      const t = tx.stoppedAt ? new Date(tx.stoppedAt).getTime() : 0;
      if (!latestStoppedAt || t > latestStoppedAt) {
        latestStoppedAt = t;
        const kwh = tx.energyKWh ? parseFloat(tx.energyKWh) : (tx.energyWh > 0 ? tx.energyWh / 1000 : 0);
        latestEnergyKWh = kwh;
      }
    }
  });

  setStatValue(elements.statTotal, total > 0 ? String(total) : "0");
  setStatValue(elements.statOnline, online > 0 ? String(online) : "0");
  setStatValue(elements.statSessions, activeSessions > 0 ? String(activeSessions) : "0");
  setStatValue(
    elements.statEnergy,
    latestEnergyKWh > 0 ? `${latestEnergyKWh.toFixed(2)} kWh` : "—",
  );
}

function setStatValue(el, text) {
  if (!el) return;
  const val = el.querySelector(".stat-box__value");
  if (val) val.textContent = text;
}

// ===== RENDER: CP Cards =====

function renderCPCards(list) {
  const container = elements.chargepoints;
  if (!list.length) {
    container.innerHTML = `
      <div class="empty-state">
        <div class="empty-state__icon">🔌</div>
        <strong>No charge points connected</strong>
        <span>Start the simulator in <code>cmd/cp</code> to connect a charger.</span>
      </div>
    `;
    return;
  }
  container.innerHTML = list.map((cp) => renderCPCard(cp)).join("");
}

function renderCPCard(cp) {
  const isOnline = cp.connected;
  const cardClass = isOnline ? "cp-card cp-card--online" : "cp-card";

  const statusHtml = isOnline
    ? `<div class="status-pill status-pill--online">● Online</div>`
    : `<div class="status-pill status-pill--offline">○ Offline</div>`;

  const hb = getHeartbeatStatus(cp.lastHeartbeat, isOnline);
  const heartbeatHtml = `
    <div class="heartbeat-wrap">
      <div class="heartbeat-dot heartbeat-dot--${escapeHtml(hb.dotClass)}" title="${escapeHtml(hb.title)}"></div>
      <span class="heartbeat-label">${hb.label}</span>
    </div>
  `;

  const hw = [cp.vendor, cp.model].filter(Boolean).join(" / ") || "—";
  const fw = cp.firmwareVersion ? `FW ${escapeHtml(cp.firmwareVersion)}` : "—";
  const bootAgo = cp.lastBoot ? formatRelative(cp.lastBoot) : "—";
  const connStatus = isOnline ? "Connected" : "Last Seen";
  const connTime = isOnline
    ? (cp.lastConnectedAt ? formatRelative(cp.lastConnectedAt) : "—")
    : (cp.lastDisconnectedAt ? formatRelative(cp.lastDisconnectedAt) : "—");
  const meterInfo = cp.lastMeterValues
    ? `${cp.lastMeterValueEntries} entries · ${formatRelative(cp.lastMeterValues)}`
    : "—";

  const connectors = Array.isArray(cp.connectors) ? cp.connectors : [];
  const connectorsSection = connectors.length
    ? `<div>
        <div class="cp-section-label">Connectors</div>
        <div class="connectors-row">${connectors.map((c) => renderConnectorPill(c)).join("")}</div>
       </div>`
    : "";

  const activeTxs = Array.isArray(cp.activeTransactions) ? cp.activeTransactions : [];
  const activeTxSection = activeTxs.length
    ? `<div>
        <div class="cp-section-label">Active Sessions</div>
        <div class="active-sessions">${activeTxs.map((tx) => renderActiveTxItem(tx)).join("")}</div>
       </div>`
    : "";

  const lastTxSection = cp.lastCompletedTransaction
    ? renderLastSessionInCard(cp.lastCompletedTransaction)
    : "";

  return `
    <div class="${cardClass}">
      <div class="cp-card__header">
        <div>
          <div class="cp-card__id">${escapeHtml(cp.id)}</div>
          <div class="cp-card__hw">${escapeHtml(hw)} · ${escapeHtml(fw)}</div>
        </div>
        <div class="cp-card__status">${statusHtml}</div>
      </div>

      <div class="cp-card__meta">
        <div class="meta-item">
          <div class="meta-item__key">Heartbeat</div>
          <div class="meta-item__val">${heartbeatHtml}</div>
        </div>
        <div class="meta-item">
          <div class="meta-item__key">Last Boot</div>
          <div class="meta-item__val" title="${formatAbsolute(cp.lastBoot)}">${escapeHtml(bootAgo)}</div>
        </div>
        <div class="meta-item">
          <div class="meta-item__key">${escapeHtml(connStatus)}</div>
          <div class="meta-item__val">${escapeHtml(connTime)}</div>
        </div>
        <div class="meta-item">
          <div class="meta-item__key">Meter Values</div>
          <div class="meta-item__val">${escapeHtml(meterInfo)}</div>
        </div>
      </div>

      ${connectorsSection}
      ${activeTxSection}
      ${lastTxSection}
    </div>
  `;
}

function renderConnectorPill(connector) {
  const status = connector.status || "Unknown";
  const cls = classifyConnectorPillStatus(status);
  const errorPart =
    connector.error && connector.error !== "NoError" && connector.error !== "NoError".toLowerCase()
      ? ` <span class="connector-error">${escapeHtml(connector.error)}</span>`
      : "";
  return `
    <div class="connector-pill connector-pill--${cls}" title="Updated ${formatRelative(connector.updatedAt)}">
      <span class="connector-pill__num">#${escapeHtml(connector.connectorId)}</span>
      <span class="connector-dot"></span>
      ${escapeHtml(status)}${errorPart}
    </div>
  `;
}

function renderActiveTxItem(tx) {
  return `
    <div class="active-session-item">
      <span class="active-session-item__badge">
        <span class="live-dot"></span>LIVE
      </span>
      <div class="active-session-item__info">
        Tx <strong>${escapeHtml(tx.transactionId)}</strong>
        ${tx.idTag ? ` · ${escapeHtml(tx.idTag)}` : ""}
        · Connector #${escapeHtml(tx.connectorId ?? "?")}
      </div>
      <div class="active-session-item__meta" title="${formatAbsolute(tx.startedAt)}">
        ${formatRelative(tx.startedAt)}
      </div>
    </div>
  `;
}

function renderLastSessionInCard(tx) {
  const energyKWh = tx.energyKWh
    ? parseFloat(tx.energyKWh)
    : tx.energyWh > 0
    ? tx.energyWh / 1000
    : 0;

  return `
    <div>
      <div class="cp-section-label">Last Session</div>
      <div class="last-session">
        <div class="last-session__icon">✓</div>
        <div class="last-session__content">
          <div class="last-session__title">
            Tx #${escapeHtml(tx.transactionId)}${tx.idTag ? ` · ${escapeHtml(tx.idTag)}` : ""}
          </div>
          <div class="last-session__meta">
            ${energyKWh > 0 ? `<span class="last-session__energy">⚡ ${energyKWh.toFixed(3)} kWh</span>` : ""}
            ${tx.reason ? `<span>${escapeHtml(tx.reason)}</span>` : ""}
            <span title="${formatAbsolute(tx.stoppedAt)}">${formatRelative(tx.stoppedAt)}</span>
          </div>
        </div>
      </div>
    </div>
  `;
}

// ===== RENDER: Sessions Table =====

function renderSessionsList(chargePoints) {
  const container = elements.sessionsList;
  if (!container) return;

  const sessions = [];

  chargePoints.forEach((cp) => {
    (cp.activeTransactions || []).forEach((tx) => {
      sessions.push({
        type: "active",
        cpId: cp.id,
        transactionId: tx.transactionId,
        idTag: tx.idTag,
        connectorId: tx.connectorId,
        startedAt: tx.startedAt,
        stoppedAt: null,
        energyKWh: null,
        reason: null,
      });
    });

    if (cp.lastCompletedTransaction) {
      const tx = cp.lastCompletedTransaction;
      const kwh = tx.energyKWh
        ? parseFloat(tx.energyKWh)
        : tx.energyWh > 0
        ? tx.energyWh / 1000
        : 0;
      sessions.push({
        type: "completed",
        cpId: cp.id,
        transactionId: tx.transactionId,
        idTag: tx.idTag,
        connectorId: tx.connectorId,
        startedAt: null,
        stoppedAt: tx.stoppedAt,
        energyKWh: kwh > 0 ? kwh : null,
        reason: tx.reason,
        meterStart: tx.meterStart,
        meterStop: tx.meterStop,
        energyWh: tx.energyWh,
      });
    }
  });

  if (!sessions.length) {
    container.innerHTML = `
      <div class="empty-state">
        <div class="empty-state__icon">📋</div>
        <span>No sessions recorded yet. Start a transaction to see it here.</span>
      </div>
    `;
    return;
  }

  sessions.sort((a, b) => {
    if (a.type === "active" && b.type !== "active") return -1;
    if (b.type === "active" && a.type !== "active") return 1;
    const tA = new Date(a.stoppedAt || a.startedAt || 0).getTime();
    const tB = new Date(b.stoppedAt || b.startedAt || 0).getTime();
    return tB - tA;
  });

  const rows = sessions.map((s) => renderSessionRow(s)).join("");

  container.innerHTML = `
    <table class="sessions-table">
      <thead>
        <tr>
          <th>Status</th>
          <th>Charge Point</th>
          <th>Driver</th>
          <th>Connector</th>
          <th>Transaction</th>
          <th>Time</th>
          <th>Energy</th>
        </tr>
      </thead>
      <tbody>${rows}</tbody>
    </table>
  `;
}

function renderSessionRow(session) {
  const isActive = session.type === "active";
  const rowClass = isActive ? "session-row--active" : "";

  const statusBadge = isActive
    ? `<span class="session-badge session-badge--active"><span class="live-dot"></span>Active</span>`
    : `<span class="session-badge session-badge--done">Completed</span>`;

  const timeCell = isActive
    ? `<span title="${formatAbsolute(session.startedAt)}">Started ${formatRelative(session.startedAt)}</span>`
    : `<span title="${formatAbsolute(session.stoppedAt)}">Stopped ${formatRelative(session.stoppedAt)}</span>`;

  const energyCell =
    session.energyKWh && session.energyKWh > 0
      ? `<span class="energy-cell">⚡ ${session.energyKWh.toFixed(3)} kWh</span>`
      : `<span class="muted-cell">—</span>`;

  return `
    <tr class="${rowClass}">
      <td>${statusBadge}</td>
      <td><span class="cp-id-cell">${escapeHtml(session.cpId)}</span></td>
      <td><span class="driver-cell">${session.idTag ? escapeHtml(session.idTag) : "—"}</span></td>
      <td>#${escapeHtml(session.connectorId ?? "?")}</td>
      <td>${escapeHtml(session.transactionId)}</td>
      <td>${timeCell}</td>
      <td>${energyCell}</td>
    </tr>
  `;
}

// ===== Heartbeat status helper =====

function getHeartbeatStatus(lastHeartbeat, isConnected) {
  if (!isConnected) {
    return { dotClass: "dead", label: "Offline", title: "Charge point disconnected" };
  }
  if (!lastHeartbeat) {
    return { dotClass: "dead", label: "No heartbeat yet", title: "No heartbeat received" };
  }
  const date = new Date(lastHeartbeat);
  if (isNaN(date.getTime())) {
    return { dotClass: "dead", label: "No heartbeat", title: "" };
  }
  const ageSec = (Date.now() - date.getTime()) / 1000;
  if (ageSec < 20) {
    return {
      dotClass: "alive",
      label: `<strong>${Math.round(ageSec)}s ago</strong>`,
      title: `Last heartbeat: ${date.toLocaleString()}`,
    };
  }
  if (ageSec < 90) {
    return {
      dotClass: "stale",
      label: `<strong>${formatRelative(lastHeartbeat)}</strong>`,
      title: `Last heartbeat: ${date.toLocaleString()} — slightly stale`,
    };
  }
  return {
    dotClass: "dead",
    label: `<strong>${formatRelative(lastHeartbeat)}</strong> — missed`,
    title: `Last heartbeat: ${date.toLocaleString()} — likely lost`,
  };
}

// ===== Connector status classifier =====

function classifyConnectorPillStatus(status) {
  const n = (status || "").toLowerCase();
  if (n === "charging") return "charging";
  if (n === "available" || n === "preparing") return "available";
  if (n === "finishing" || n === "suspendedev" || n === "suspendedevse" || n === "reserved")
    return "warn";
  if (n === "faulted" || n === "unavailable" || n === "blocked") return "error";
  return "muted";
}

// ===== Select / Transaction helpers =====

function updateChargePointSelects(chargePoints) {
  const ids = chargePoints.map((cp) => cp.id).sort();
  const selects = document.querySelectorAll('select[data-select="chargepoint"]');

  selects.forEach((select) => {
    const previous = select.value;
    select.innerHTML = "";

    if (!ids.length) {
      select.add(new Option("No charge points", "", true, true));
      select.disabled = true;
      return;
    }

    select.disabled = false;
    const placeholder = new Option("Select…", "", true, !previous);
    select.add(placeholder);
    ids.forEach((id) => select.add(new Option(id, id, false, id === previous)));
    if (previous) select.value = previous;
  });
}

function updateTransactionOptions(chargePoints) {
  const select = elements.remoteStopTransaction;
  const previousCP = select.selectedOptions[0]?.dataset?.cp ?? null;
  const previousTx = select.value;

  select.innerHTML = "";

  const active = [];
  chargePoints.forEach((cp) => {
    (cp.activeTransactions || []).forEach((tx) => {
      active.push({
        cpId: cp.id,
        transactionId: tx.transactionId,
        connectorId: tx.connectorId,
        idTag: tx.idTag,
        startedAt: tx.startedAt,
      });
    });
  });

  if (!active.length) {
    select.add(new Option("No active transactions", "", true, true));
    select.disabled = true;
    return;
  }

  select.disabled = false;
  const placeholder = new Option(
    "Select active transaction…",
    "",
    true,
    !(previousCP && previousTx),
  );
  select.add(placeholder);

  active.forEach((tx) => {
    const label = `${tx.cpId} · Tx ${tx.transactionId} (Connector #${tx.connectorId ?? "?"})`;
    const option = new Option(label, tx.transactionId);
    option.dataset.cp = tx.cpId;
    option.dataset.transaction = tx.transactionId;
    option.dataset.connector = tx.connectorId ?? "";
    option.dataset.idTag = tx.idTag ?? "";
    option.dataset.startedAt = tx.startedAt ?? "";
    option.selected =
      tx.cpId === previousCP &&
      String(tx.transactionId) === String(previousTx) &&
      previousTx !== "";
    select.add(option);
  });

  if (previousCP && previousTx) {
    const match = [...select.options].find(
      (opt) =>
        opt.dataset &&
        opt.dataset.cp === previousCP &&
        opt.dataset.transaction === String(previousTx),
    );
    if (match) {
      match.selected = true;
      placeholder.selected = false;
    }
  }
}

function handleTransactionSelect() {
  const option = elements.remoteStopTransaction.selectedOptions[0];
  if (!option || !option.dataset.cp) return;
  elements.remoteStopCP.value = option.dataset.cp;
  elements.remoteStopId.value = option.dataset.transaction;
}

// ===== Form handlers =====

async function handleRemoteStart(event) {
  event.preventDefault();

  const chargePointId = elements.remoteStartCP.value.trim();
  const idTag = elements.remoteStartIdTag.value.trim();
  const connectorValue = elements.remoteStartConnector.value.trim();

  if (!chargePointId || !idTag) {
    displayMessage("error", "Charge point and ID tag are required.");
    return;
  }

  const payload = { chargePointId, idTag };

  if (connectorValue) {
    const parsed = Number.parseInt(connectorValue, 10);
    if (Number.isNaN(parsed) || parsed <= 0) {
      displayMessage("error", "Connector id must be a positive number.");
      return;
    }
    payload.connectorId = parsed;
  }

  const btn = elements.remoteStartForm.querySelector('button[type="submit"]');
  setButtonLoading(btn, true);

  try {
    const response = await postJSON("/api/remote-start", payload);
    displayMessage(
      "success",
      `Remote start sent to ${chargePointId} (status: ${response.status}).`,
    );
    elements.remoteStartForm.reset();
    elements.remoteStartCP.value = chargePointId;
    refreshData();
  } catch (error) {
    displayMessage("error", error.message);
  } finally {
    setButtonLoading(btn, false);
  }
}

async function handleRemoteStop(event) {
  event.preventDefault();

  const selectedOption = elements.remoteStopTransaction.selectedOptions[0];
  const selectedCP =
    selectedOption && selectedOption.dataset.cp
      ? selectedOption.dataset.cp
      : elements.remoteStopCP.value.trim();
  const txValue =
    selectedOption && selectedOption.dataset.transaction
      ? selectedOption.dataset.transaction
      : elements.remoteStopId.value.trim();

  if (!selectedCP || !txValue) {
    displayMessage("error", "Select a charge point and provide a transaction id.");
    return;
  }

  const transactionId = Number.parseInt(txValue, 10);
  if (Number.isNaN(transactionId) || transactionId <= 0) {
    displayMessage("error", "Transaction id must be a positive number.");
    return;
  }

  const btn = elements.remoteStopForm.querySelector('button[type="submit"]');
  setButtonLoading(btn, true);

  try {
    const response = await postJSON("/api/remote-stop", {
      chargePointId: selectedCP,
      transactionId,
    });
    displayMessage("success", `Remote stop sent to ${selectedCP} (status: ${response.status}).`);
    elements.remoteStopForm.reset();
    updateTransactionOptions(state.chargePoints);
    refreshData();
  } catch (error) {
    displayMessage("error", error.message);
  } finally {
    setButtonLoading(btn, false);
  }
}

async function handleReset(event) {
  event.preventDefault();

  const chargePointId = elements.resetCP.value.trim();
  const type = elements.resetType.value;

  if (!chargePointId) {
    displayMessage("error", "Select a charge point to reset.");
    return;
  }

  const btn = elements.resetForm.querySelector('button[type="submit"]');
  setButtonLoading(btn, true);

  try {
    const response = await postJSON("/api/reset", { chargePointId, type });
    displayMessage(
      "success",
      `Reset (${type}) sent to ${chargePointId} (status: ${response.status}).`,
    );
    refreshData();
  } catch (error) {
    displayMessage("error", error.message);
  } finally {
    setButtonLoading(btn, false);
  }
}

async function handleUnlock(event) {
  event.preventDefault();

  const chargePointId = elements.unlockCP.value.trim();
  const connectorValue = elements.unlockConnector.value.trim();

  if (!chargePointId || !connectorValue) {
    displayMessage("error", "Select a charge point and provide a connector id.");
    return;
  }

  const connectorId = Number.parseInt(connectorValue, 10);
  if (Number.isNaN(connectorId) || connectorId <= 0) {
    displayMessage("error", "Connector id must be a positive number.");
    return;
  }

  const btn = elements.unlockForm.querySelector('button[type="submit"]');
  setButtonLoading(btn, true);

  try {
    const response = await postJSON("/api/unlock", { chargePointId, connectorId });
    displayMessage(
      "success",
      `Unlock request sent to ${chargePointId} (status: ${response.status}).`,
    );
    elements.unlockForm.reset();
    elements.unlockCP.value = chargePointId;
    refreshData();
  } catch (error) {
    displayMessage("error", error.message);
  } finally {
    setButtonLoading(btn, false);
  }
}

// ===== Summary & refresh label =====

function updateSummary(chargePoints) {
  const total = chargePoints.length;
  const connected = chargePoints.filter((cp) => cp.connected).length;
  const el = elements.summary;

  if (total === 0) {
    el.textContent = "No charge points";
    el.style.color = "";
    return;
  }

  el.textContent = `${connected} / ${total} online`;
}

function updateLastRefresh() {
  if (!state.lastRefresh) {
    elements.lastRefresh.textContent = "Waiting for data…";
    return;
  }
  const date = new Date(state.lastRefresh);
  elements.lastRefresh.textContent = `Updated ${formatRelative(date)}`;
  elements.lastRefresh.title = date.toLocaleString();
}

// ===== UI helpers =====

function setButtonLoading(button, isLoading) {
  if (!button) return;
  if (isLoading) {
    button.setAttribute("data-loading", "true");
    button.disabled = true;
  } else {
    button.removeAttribute("data-loading");
    button.disabled = false;
  }
}

async function postJSON(url, payload) {
  const response = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(payload),
  });

  const text = await response.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : {};
  } catch (_) {
    data = {};
  }

  if (!response.ok) {
    const message = (data && data.error) || response.statusText || "Request failed";
    throw new Error(message);
  }

  return data;
}

function displayMessage(type, text) {
  const container = elements.notifications || document.getElementById("notifications");
  if (!container) return;
  container.innerHTML = "";
  if (!text) return;

  const message = document.createElement("div");
  message.className = `message message--${type}`;
  message.textContent = text;
  container.appendChild(message);

  clearTimeout(messageTimer);
  messageTimer = setTimeout(() => {
    message.classList.add("is-hidden");
    setTimeout(() => {
      if (container.contains(message)) container.removeChild(message);
    }, 270);
  }, type === "error" ? 7000 : 4000);
}

// ===== Formatters =====

function escapeHtml(value) {
  if (value === null || value === undefined) return "";
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

function formatRelative(value) {
  if (!value) return "—";
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "—";

  const diff = Date.now() - date.getTime();
  const tense = diff >= 0 ? "ago" : "from now";
  const abs = Math.abs(diff);
  const seconds = Math.round(abs / 1000);
  const minutes = Math.round(seconds / 60);
  const hours = Math.round(minutes / 60);
  const days = Math.round(hours / 24);

  if (seconds < 45) return tense === "ago" ? "just now" : "soon";
  if (seconds < 90) return "1 minute " + tense;
  if (minutes < 45) return `${minutes} minutes ${tense}`;
  if (minutes < 90) return "1 hour " + tense;
  if (hours < 24) return `${hours} hours ${tense}`;
  if (hours < 42) return "1 day " + tense;
  if (days < 30) return `${days} days ${tense}`;
  const months = Math.round(days / 30);
  if (months < 18) return `${months} months ${tense}`;
  return `${Math.round(days / 365)} years ${tense}`;
}

function formatAbsolute(value) {
  if (!value) return "";
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}
