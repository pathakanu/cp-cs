# cp-cs Codebase Walkthrough

This document explains how the charge point (`cmd/cp`) and central system (`cmd/cs`) simulators are put together, how they communicate with each other, and how the supporting assets (HTTP API, web UI, and tests) fit into the overall design.

---

## Repository Layout

- `cmd/cp/cp.go` – Go program that bootstraps a simulated OCPP 1.6 charge point. It connects to the central system, responds to remote commands, and manages transaction state.
- `cmd/cs/cs.go` – Go program that hosts an OCPP 1.6 central system and an accompanying HTTP + static web UI. It tracks charge-point state, exposes REST endpoints for remote control, and bridges OCPP events into an in-memory model.
- `cmd/cs/web_embed.go` – Embeds the frontend assets into the `cmd/cs` binary using Go’s `embed` package.
- `cmd/cs/web/` – Frontend bundle served by the central system.
  - `index.html` – Page shell that wires the dashboard layout and action forms.
  - `static/app.js` – Plain JavaScript bundle that polls the backend, renders the dashboard, and calls REST endpoints.
  - `static/style.css` – Styling for the console (no build tooling, just CSS).
- `cmd/cs/cs_test.go` – Unit tests for the HTTP layer; uses a stubbed central system to exercise the async callbacks.
- `go.mod` + `go.sum` – Declare the module (`github.com/pathakanu/cp-cs`) and pin dependencies.
- `cp` and `cs` – Pre-built binaries (optional artifacts; sources live under `cmd/`).
- `README.md` – Quickstart instructions for running both executables.

---

## System Overview

The project simulates the two actors in an OCPP 1.6 deployment:

1. **Central System (`cmd/cs`)** – Listens for WebSocket connections from charge points on `ws://localhost:8887/{ws}`. It also serves a REST + browser UI on `http://localhost:8080` so a human operator can monitor the fleet and trigger remote commands.
2. **Charge Point (`cmd/cp`)** – Connects to the central system, performs a `BootNotification`, keeps a heartbeat, and responds to remote start/stop/reset/unlock commands routed through OCPP.

Communication between both halves happens exclusively through the OCPP wire protocol, implemented by the external `ocpp-go` library. The HTTP+web UI is an operator-facing surface that talks to the central system; it never contacts the charge point directly.

---

## Charge Point (`cmd/cp/cp.go`)

### Boot Sequence

1. Create an OCPP charge point via `ocpp16.NewChargePoint("CP-001", nil, nil)`.
2. Wrap it in a `ChargePointHandler`, which implements the central system–invoked callbacks (e.g., remote reset).
3. Register the handler with `cp.SetCoreHandler(handler)`.
4. Dial the central system WebSocket at `ws://localhost:8887` (`cp.Start`).
5. Send `BootNotification` and log the confirmation. If the central system returns a heartbeat interval, spin up a ticker goroutine that sends `Heartbeat` messages regularly.

### Remote Command Handling

The handler methods correspond to core OCPP 1.6 features. Each method logs the request and returns an accepted confirmation so the central system knows the command was received.

- `OnRemoteStartTransaction` spawns `startTransaction` asynchronously to avoid blocking the callback thread. `startTransaction`:
  - Derives the connector ID (defaults to 1 if omitted).
  - Generates a synthetic meter reading and timestamp.
  - Calls `cp.StartTransaction(...)`.
  - Updates the handler’s shared state with the active transaction info (protected by a mutex).
- `OnRemoteStopTransaction` triggers `stopTransaction`, which:
  - Reads the current transaction details (under lock).
  - Calls `cp.StopTransaction(...)`, optionally keeping the originating `IdTag` and the reason supplied by the central system.
  - Clears the active transaction tracking fields.

Other callbacks (`OnChangeAvailability`, `OnUnlockConnector`, etc.) simply acknowledge the request with an accepted status, mimicking a cooperative charge point.

### Internal State & Synchronization

`ChargePointHandler` keeps the minimal state necessary to coordinate start/stop requests:

- `activeTxID`, `activeConnector`, `activeIdTag` – Last started transaction.
- `meterStart` – Baseline meter value used when the transaction started.
- `mu` – Mutex to protect concurrent access; remote start/stop handlers run in separate goroutines.

This handling ensures the simulated device behaves coherently when the central system issues commands in quick succession.

---

## Central System (`cmd/cs/cs.go`)

The central system layers three responsibilities into one executable.

### 1. OCPP Central System Listener

- Instantiated via `ocpp16.NewCentralSystem(nil, nil)`.
- Registers `CentralSystemHandler` as the core protocol handler (`cs.SetCoreHandler`).
- Hooks connection lifecycle events (`SetNewChargePointHandler`, `SetChargePointDisconnectedHandler`) to keep track of which stations are online.
- `startCentralSystem` listens on `centralSystemPort` (`8887`) and blocks inside the OCPP library; it runs in a dedicated goroutine to avoid blocking the rest of the app.

### 2. HTTP API + Web Server

- `startHTTP` launches a standard `net/http` server on port `8080`.
- `routes()` wires REST endpoints under `/api/*` and serves the embedded static UI bundle. The `mountStatic` helper:
  - Extracts the embedded filesystem (`//go:embed` in `web_embed.go`).
  - Serves `/static/*` from the `web/static` directory.
  - Serves `/` (and `/index.html`) with the single-page dashboard.
- REST endpoints are purposely thin:
  - `GET /api/chargepoints` – Returns the latest view model built from the in-memory state.
  - `POST /api/remote-start`, `/api/remote-stop`, `/api/reset`, `/api/unlock` – Validate JSON payloads, confirm the charge point is connected, invoke the corresponding OCPP request, and wait (with a 10 s timeout) for the async confirmation callback.
  - Each handler captures the OCPP callback result into a channel (`remoteStartResult`, etc.) so it can translate the outcome into an HTTP response.

### 3. State Tracking & View Model

All OCPP notifications converge into a `CSApp` instance that maintains per-charge-point state in memory:

- `chargePoints map[string]*chargePointState` – Master registry keyed by OCPP client ID.
- `chargePointState` – Tracks connection flags, hardware metadata, last-seen timestamps, connector status snapshots, active transactions, and the most recently completed transaction.
- Methods like `recordBootNotification`, `recordHeartbeat`, `recordStatusNotification`, `recordStartTransaction`, and `recordStopTransaction` update this structure whenever the OCPP handler receives the matching message.
- Access is guarded by a `sync.RWMutex` to avoid races between the HTTP handlers (reads) and the OCPP callbacks (writes).

When the UI or API requests the current state, `listChargePoints` translates each `chargePointState` into a serializable `chargePointView`, sorting connectors and transactions for deterministic output. Helper types (`connectorView`, `transactionView`, `transactionSummary`) map directly onto the JSON schema expected by the frontend.

### Central System Handler (`CentralSystemHandler`)

This struct is the bridge between the OCPP library and the application state. Each callback:

1. Logs the event for visibility.
2. Calls the appropriate `CSApp.record*` helper to mutate in-memory state.
3. Returns an OCPP confirmation (usually accepting the request). For example:
   - `OnBootNotification` stores hardware metadata and approves registration.
   - `OnAuthorize` always authorizes the presented `IdTag`.
   - `OnStatusNotification` snapshots per-connector status/error codes.
   - `OnStartTransaction` allocates a synthetic transaction ID (using the current Unix timestamp) and accepts the session.
   - `OnStopTransaction` saves the summary of the finished transaction.

Together, these handlers model a permissive central system that happily tracks whatever the simulated charge point does.

---

## Embedded Web Console

The browser UI is a static bundle under `cmd/cs/web/` (no build step or framework).

- `index.html` defines the layout: a hero header with connection stats, a table for charge point summaries, and four action cards (remote start/stop, reset, unlock).
- `static/style.css` styles the dashboard with responsive cards, tables, badges, and form controls.
- `static/app.js` drives the UX:
  1. On load, it wires event listeners for the manual refresh button and each form.
  2. Starts a 5-second polling loop (`refreshData`) that fetches `/api/chargepoints`, updates the select fields, renders the table, and updates summary chips.
  3. Submits form payloads to the backend helpers (`postJSON`) and displays toast messages via `displayMessage`.
  4. Derives dynamic presentational cues (e.g., colored tags for connector status) purely on the client.

Because the assets are embedded, the resulting `cs` binary is self-contained: running `go run ./cmd/cs` serves both the OCPP listener and the operator interface.

---

## Testing (`cmd/cs/cs_test.go`)

The tests focus on the HTTP API because it contains the most logic beyond basic wiring:

- A `stubCentralSystem` mirrors the `ocpp16.CentralSystem` interface but allows tests to inject custom behavior for async callbacks (`remoteStartFn`, `remoteStopFn`, etc.).
- `TestHandleRemoteStartSuccess` exercises the happy path, ensuring the handler waits for the callback and returns the status to the client.
- `TestHandleRemoteStartRejectsWhenDisconnected` validates that requests are rejected if the target charge point is offline.
- `TestHandleRemoteStartPropagatesError` and `TestHandleRemoteStartCallbackError` confirm that synchronous and asynchronous errors propagate with the right HTTP status codes.
- `TestChargePointViewSorting` verifies deterministic ordering of the JSON payload.

These tests run with `go test ./cmd/cs` and do not require the OCPP stack to be online; all network interactions are mocked.

---

## External Dependencies

The code relies heavily on [`github.com/lorenzodonini/ocpp-go`](https://github.com/lorenzodonini/ocpp-go), which provides:

- Client (`ChargePoint`) and server (`CentralSystem`) abstractions for OCPP 1.6.
- Strongly typed request/confirmation structs across the core profile (and imports for extensions used in the test doubles).

Beyond `ocpp-go`, only the Go standard library is used (HTTP server, JSON encoding, synchronization, time helpers, and embedding).

---

## Putting It Together

1. Start the central system (`go run ./cmd/cs`):
   - WebSocket listener awaits charge points.
   - HTTP server serves the operator UI and API.
   - In-memory store is ready to track events.
2. Start the charge point (`go run ./cmd/cp`):
   - Connects over WebSocket, sends a boot notification, and begins heartbeats.
   - Accepts remote commands issued from the central system or via the UI.
3. Use the web console to monitor the simulated station and trigger commands:
   - REST endpoints translate user actions into OCPP requests.
   - Callbacks update state, which is reflected in the next poll cycle.

Every file in the repository participates in this loop: the Go backends orchestrate protocol logic, the embedded assets give operators visibility, and the tests keep the HTTP façade reliable.

---

## Extending the Demo

To go further:

- Add more OCPP profiles by extending `CentralSystemHandler` and surfacing new REST endpoints.
- Track richer metrics in `chargePointState` (e.g., energy consumption history) and expose them to the UI.
- Simulate multiple charge points by tweaking `cmd/cp/cp.go` to accept IDs and connector counts as CLI flags.

The current structure keeps responsibilities separated (charge client vs. central controller vs. UI) while remaining small enough to understand end-to-end.

