const REFRESH_INTERVAL = 5000;

const state = {
  chargePoints: [],
  lastRefresh: null,
};

const elements = {
  chargepoints: document.getElementById("chargepoints"),
  notifications: document.getElementById("notifications"),
  summary: document.getElementById("summary"),
  lastRefresh: document.getElementById("last-refresh"),
  refreshButton: document.getElementById("refresh-button"),
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
  elements.remoteStopTransaction.addEventListener(
    "change",
    handleTransactionSelect,
  );
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

    renderChargePoints(state.chargePoints);
    updateChargePointSelects(state.chargePoints);
    updateTransactionOptions(state.chargePoints);
    updateSummary(state.chargePoints);
    updateLastRefresh();

    if (manual) {
      displayMessage("success", "Charge point list refreshed.");
    }
  } catch (error) {
    displayMessage("error", `Failed to load charge points: ${error.message}`);
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
      if (payload && payload.error) {
        message = payload.error;
      }
    } catch (_) {
      // ignore
    }
    throw new Error(message || "Unexpected server response");
  }
  return response.json();
}

function renderChargePoints(list) {
  const container = elements.chargepoints;
  container.innerHTML = "";

  if (!list.length) {
    container.innerHTML = `
      <div class="empty-state">
        <strong>No charge points yet.</strong>
        <span>Start the simulator in <code>cmd/cp</code> to connect a charger.</span>
      </div>
    `;
    return;
  }

  const table = document.createElement("table");
  table.innerHTML = `
    <thead>
      <tr>
        <th>Charge Point</th>
        <th>Connection</th>
        <th>Active Transactions</th>
        <th>Connectors</th>
        <th>Meter Values</th>
        <th>Last Event</th>
      </tr>
    </thead>
    <tbody></tbody>
  `;

  const tbody = table.querySelector("tbody");
  tbody.innerHTML = list
    .map((cp) => renderChargePointRow(cp))
    .join("");

  container.appendChild(table);
}

function renderChargePointRow(cp) {
  const connectedClass = cp.connected
    ? "status-pill status-pill--ok"
    : "status-pill status-pill--error";
  const connectionStatus = cp.connected ? "Connected" : "Disconnected";

  const heartbeat = formatRelative(cp.lastHeartbeat);
  const heartbeatLine = cp.lastHeartbeat
    ? `<span class="meta-line" title="${formatAbsolute(cp.lastHeartbeat)}">Heartbeat <strong>${heartbeat}</strong></span>`
    : `<span class="meta-line empty">No heartbeat yet</span>`;

  const bootLine = cp.lastBoot
    ? `<span class="meta-line" title="${formatAbsolute(cp.lastBoot)}">Boot acknowledged ${formatRelative(cp.lastBoot)}</span>`
    : `<span class="meta-line empty">No boot notification</span>`;

  const meterLine =
    cp.lastMeterValues && cp.lastMeterValueEntries
      ? `<span class="meta-line" title="${formatAbsolute(cp.lastMeterValues)}">Meter values (${cp.lastMeterValueEntries}) ${formatRelative(cp.lastMeterValues)}</span>`
      : "";

  const hardwareInfo = [
    [cp.vendor, cp.model].filter(Boolean).join(" • ") || "—",
    cp.firmwareVersion ? `FW ${escapeHtml(cp.firmwareVersion)}` : "",
  ]
    .filter(Boolean)
    .join(" · ");

  const activeTransactions = Array.isArray(cp.activeTransactions)
    ? cp.activeTransactions
    : [];
  const activeHtml = activeTransactions.length
    ? `<ul class="list--stack">${activeTransactions
        .map(
          (tx) => `
            <li>
              <strong>Tx ${escapeHtml(tx.transactionId)}</strong>
              ${tx.idTag ? `· ${escapeHtml(tx.idTag)}` : ""}
              <span class="meta-line" title="${formatAbsolute(tx.startedAt)}">
                Connector #${escapeHtml(tx.connectorId ?? "—")} • Started ${formatRelative(tx.startedAt)}
              </span>
            </li>
          `,
        )
        .join("")}</ul>`
    : `<span class="empty">None</span>`;

  const connectors = Array.isArray(cp.connectors) ? cp.connectors : [];
  const connectorsHtml = connectors.length
    ? `<div class="connector-list">${connectors
        .map((connector) => {
          const status = connector.status || "Unknown";
          const tagClass = `tag ${classifyConnectorStatus(status)}`;
          const errorBadge =
            connector.error &&
            connector.error !== "NoError" &&
            connector.error !== "NoError".toLowerCase()
              ? `<span class="badge badge--error">${escapeHtml(connector.error)}</span>`
              : "";
          return `
            <div class="connector">
              <div class="connector__label">
                <span>#${escapeHtml(connector.connectorId)}</span>
                <span class="${tagClass}">${escapeHtml(status)}</span>
                ${errorBadge}
              </div>
              <div class="connector__meta" title="${formatAbsolute(connector.updatedAt)}">
                Updated ${formatRelative(connector.updatedAt)}
              </div>
            </div>
          `;
        })
        .join("")}</div>`
    : `<span class="empty">Waiting for status notifications</span>`;

  const lastEvent = renderLastEvent(cp);
  const meterValuesHtml = renderMeterValues(cp);

  return `
    <tr data-connected="${cp.connected ? "true" : "false"}">
      <td>
        <div class="cp-id">${escapeHtml(cp.id)}</div>
        <div class="cp-meta">${escapeHtml(hardwareInfo)}</div>
      </td>
      <td>
        <div class="status-line">
          <span class="${connectedClass}">${connectionStatus}</span>
          ${heartbeatLine}
          ${bootLine}
          ${meterLine}
        </div>
      </td>
      <td>${activeHtml}</td>
      <td>${connectorsHtml}</td>
      <td>${meterValuesHtml}</td>
      <td>${lastEvent}</td>
    </tr>
  `;
}

function renderLastEvent(cp) {
  if (cp.lastCompletedTransaction) {
    const tx = cp.lastCompletedTransaction;
    const energySummary = formatTransactionEnergy(tx);
    const energySegment = energySummary ? `${energySummary} · ` : "";
    return `
      <div class="status-line">
        <strong>Stopped transaction #${escapeHtml(tx.transactionId)}</strong>
        <span class="meta-line" title="${formatAbsolute(tx.stoppedAt)}">
          Connector #${escapeHtml(tx.connectorId ?? "—")} · ${energySegment}${
            tx.reason ? escapeHtml(tx.reason) : "Reason unknown"
          } · ${formatRelative(tx.stoppedAt)}
        </span>
      </div>
    `;
  }

  if (cp.lastMeterValues) {
    return `
      <div class="status-line">
        <strong>Meter values received</strong>
        <span class="meta-line" title="${formatAbsolute(cp.lastMeterValues)}">
          ${cp.lastMeterValueEntries || 0} entries · ${formatRelative(
            cp.lastMeterValues,
          )}
        </span>
      </div>
    `;
  }

  return `<span class="empty">No events yet</span>`;
}

function renderMeterValues(cp) {
  const history = Array.isArray(cp.meterValues) ? cp.meterValues : [];
  if (!history.length) {
    return `<span class="empty">No meter readings yet</span>`;
  }

  const connectorsHtml = history
    .map((connector) => {
      const entries = Array.isArray(connector.entries)
        ? connector.entries.slice(0, 5)
        : [];
      if (!entries.length) {
        return `
          <div class="meter-values__connector">
            <div class="connector__label">Connector #${escapeHtml(connector.connectorId)}</div>
            <span class="empty">No readings yet</span>
          </div>
        `;
      }

      const entriesHtml = entries
        .map((entry) => {
          const samples = Array.isArray(entry.sampledValues)
            ? entry.sampledValues
            : [];
          const samplesHtml = samples.length
            ? samples.map(renderSampledValue).join("")
            : `<span class="empty">No sampled values</span>`;
          return `
            <div class="meter-values__entry">
              <div class="meter-values__timestamp" title="${formatAbsolute(entry.timestamp)}">
                Recorded ${formatRelative(entry.timestamp)}
              </div>
              ${samplesHtml}
            </div>
          `;
        })
        .join("");

      return `
        <div class="meter-values__connector">
          <div class="connector__label">Connector #${escapeHtml(connector.connectorId)}</div>
          ${entriesHtml}
        </div>
      `;
    })
    .join("");

  return `<div class="meter-values">${connectorsHtml}</div>`;
}

function renderSampledValue(sample) {
  if (!sample || sample.value === undefined || sample.value === null) {
    return "";
  }

  const valueParts = [escapeHtml(sample.value)];
  if (sample.unit) {
    valueParts.push(escapeHtml(sample.unit));
  }

  const details = [];
  if (sample.measurand) {
    details.push(escapeHtml(sample.measurand));
  }
  if (sample.phase) {
    details.push(`Phase ${escapeHtml(sample.phase)}`);
  }
  if (sample.location) {
    details.push(escapeHtml(sample.location));
  }
  if (sample.context) {
    details.push(escapeHtml(sample.context));
  }
  if (sample.format) {
    details.push(escapeHtml(sample.format));
  }

  const detailsHtml = details.length
    ? `<span class="meta-line">${details.join(" · ")}</span>`
    : "";

  return `
    <div class="meter-values__sample">
      <strong>${valueParts.join(" ")}</strong>
      ${detailsHtml}
    </div>
  `;
}

function formatTransactionEnergy(tx) {
  if (!tx) return "";

  const kwh = typeof tx.energyKWh === "string" ? tx.energyKWh.trim() : tx.energyKWh;
  if (kwh) {
    return `${escapeHtml(kwh)} kWh delivered`;
  }

  const energyWh = Number.parseFloat(tx.energyWh);
  if (Number.isFinite(energyWh) && energyWh > 0) {
    return `${escapeHtml(energyWh)} Wh delivered`;
  }

  return "";
}

function updateChargePointSelects(chargePoints) {
  const ids = chargePoints.map((cp) => cp.id).sort();
  const selects = document.querySelectorAll('select[data-select="chargepoint"]');

  selects.forEach((select) => {
    const previous = select.value;
    select.innerHTML = "";

    if (!ids.length) {
      const option = new Option("No charge points", "", true, true);
      select.add(option);
      select.disabled = true;
      return;
    }

    select.disabled = false;
    const placeholder = new Option("Select…", "", true, !previous);
    select.add(placeholder);
    ids.forEach((id) => {
      select.add(new Option(id, id, false, id === previous));
    });
    if (previous) {
      select.value = previous;
    }
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
  if (!option || !option.dataset.cp) {
    return;
  }

  elements.remoteStopCP.value = option.dataset.cp;
  elements.remoteStopId.value = option.dataset.transaction;
}

async function handleRemoteStart(event) {
  event.preventDefault();

  const chargePointId = elements.remoteStartCP.value.trim();
  const idTag = elements.remoteStartIdTag.value.trim();
  const connectorValue = elements.remoteStartConnector.value.trim();

  if (!chargePointId || !idTag) {
    displayMessage("error", "Charge point and id tag are required.");
    return;
  }

  const payload = {
    chargePointId,
    idTag,
  };

  if (connectorValue) {
    const parsed = Number.parseInt(connectorValue, 10);
    if (Number.isNaN(parsed) || parsed <= 0) {
      displayMessage("error", "Connector id must be a positive number.");
      return;
    }
    payload.connectorId = parsed;
  }

  const submitButton =
    elements.remoteStartForm.querySelector('button[type="submit"]');
  setButtonLoading(submitButton, true);

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
    setButtonLoading(submitButton, false);
  }
}

async function handleRemoteStop(event) {
  event.preventDefault();

  const selectedOption = elements.remoteStopTransaction.selectedOptions[0];
  const selectedChargePoint =
    selectedOption && selectedOption.dataset.cp
      ? selectedOption.dataset.cp
      : elements.remoteStopCP.value.trim();
  const transactionIdValue =
    selectedOption && selectedOption.dataset.transaction
      ? selectedOption.dataset.transaction
      : elements.remoteStopId.value.trim();

  if (!selectedChargePoint || !transactionIdValue) {
    displayMessage(
      "error",
      "Select a charge point and provide a transaction id.",
    );
    return;
  }

  const transactionId = Number.parseInt(transactionIdValue, 10);
  if (Number.isNaN(transactionId) || transactionId <= 0) {
    displayMessage("error", "Transaction id must be a positive number.");
    return;
  }

  const submitButton =
    elements.remoteStopForm.querySelector('button[type="submit"]');
  setButtonLoading(submitButton, true);

  try {
    const response = await postJSON("/api/remote-stop", {
      chargePointId: selectedChargePoint,
      transactionId,
    });
    displayMessage(
      "success",
      `Remote stop sent to ${selectedChargePoint} (status: ${response.status}).`,
    );
    elements.remoteStopForm.reset();
    updateTransactionOptions(state.chargePoints);
    refreshData();
  } catch (error) {
    displayMessage("error", error.message);
  } finally {
    setButtonLoading(submitButton, false);
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

  const submitButton =
    elements.resetForm.querySelector('button[type="submit"]');
  setButtonLoading(submitButton, true);

  try {
    const response = await postJSON("/api/reset", {
      chargePointId,
      type,
    });
    displayMessage(
      "success",
      `Reset (${type}) sent to ${chargePointId} (status: ${response.status}).`,
    );
    refreshData();
  } catch (error) {
    displayMessage("error", error.message);
  } finally {
    setButtonLoading(submitButton, false);
  }
}

async function handleUnlock(event) {
  event.preventDefault();

  const chargePointId = elements.unlockCP.value.trim();
  const connectorValue = elements.unlockConnector.value.trim();

  if (!chargePointId || !connectorValue) {
    displayMessage(
      "error",
      "Select a charge point and provide a connector id.",
    );
    return;
  }

  const connectorId = Number.parseInt(connectorValue, 10);
  if (Number.isNaN(connectorId) || connectorId <= 0) {
    displayMessage("error", "Connector id must be a positive number.");
    return;
  }

  const submitButton =
    elements.unlockForm.querySelector('button[type="submit"]');
  setButtonLoading(submitButton, true);

  try {
    const response = await postJSON("/api/unlock", {
      chargePointId,
      connectorId,
    });
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
    setButtonLoading(submitButton, false);
  }
}

function updateSummary(chargePoints) {
  const total = chargePoints.length;
  const connected = chargePoints.filter((cp) => cp.connected).length;
  const summaryEl = elements.summary;

  if (total === 0) {
    summaryEl.textContent = "No charge points connected";
    summaryEl.className = "chip chip--muted";
    summaryEl.title = "";
    return;
  }

  summaryEl.textContent = `${connected} of ${total} charge point${
    total === 1 ? "" : "s"
  } connected`;
  summaryEl.title = "";

  if (connected === 0) {
    summaryEl.className = "chip chip--alert";
  } else if (connected === total) {
    summaryEl.className = "chip chip--positive";
  } else {
    summaryEl.className = "chip chip--muted";
  }
}

function updateLastRefresh() {
  if (!state.lastRefresh) {
    elements.lastRefresh.textContent = "Waiting for data";
    elements.lastRefresh.title = "";
    return;
  }
  const date = new Date(state.lastRefresh);
  elements.lastRefresh.textContent = `Updated ${formatRelative(date)}`;
  elements.lastRefresh.title = date.toLocaleString();
}

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
    const message =
      (data && data.error) || response.statusText || "Request failed";
    throw new Error(message);
  }

  return data;
}

function displayMessage(type, text) {
  const container = elements.notifications;
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
      if (container.contains(message)) {
        container.removeChild(message);
      }
    }, 250);
  }, type === "error" ? 7000 : 4000);
}

function classifyConnectorStatus(status) {
  const normalized = (status || "").toLowerCase();
  if (
    normalized === "available" ||
    normalized === "preparing" ||
    normalized === "charging"
  ) {
    return "tag--ok";
  }
  if (
    normalized === "finishing" ||
    normalized === "suspendedev" ||
    normalized === "suspendedevse" ||
    normalized === "reserved"
  ) {
    return "tag--warn";
  }
  if (
    normalized === "faulted" ||
    normalized === "unavailable" ||
    normalized === "blocked"
  ) {
    return "tag--error";
  }
  return "tag--muted";
}

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
  const years = Math.round(days / 365);
  return `${years} years ${tense}`;
}

function formatAbsolute(value) {
  if (!value) return "";
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}
