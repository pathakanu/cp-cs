# cp-cs demo

A minimal playground that wires a Central System and a Charge Point using the [`ocpp-go`](https://github.com/lorenzodonini/ocpp-go) v1.6 stack.

## Prerequisites
- Go 1.20 or newer

## Run the Central System
1. Open a terminal in the project root.
2. Start the server:
   ```bash
   go run ./cmd/cs
   ```
   The listener binds to `ws://localhost:8887/{ws}` and will log any incoming OCPP 1.6 core messages.
3. Visit the web console at [http://localhost:8080](http://localhost:8080). The page lists every charge point the central system has seen and shows its current state.

### Using the Frontend
- **Refresh & monitor:** The dashboard auto-refreshes every 5 s. Use the `Refresh` button if you want an immediate update.
- **Remote Start Transaction:** Choose a charge point, enter an IdTag (and optional connector) in the *Remote Start* card, then submit to request a session start.
- **Remote Stop Transaction:** Pick an ongoing transaction from the dropdown, or manually provide the charge point and transaction id, then submit to stop it.
- **Reset Charge Point:** Select the station, choose *Soft* or *Hard* reset, and send the command.
- **Unlock Connector:** Choose the station, enter the connector id, and submit to force an unlock attempt.
- Each action’s result appears as a toast message at the top of the page, so you immediately know whether the CP accepted the request.

## Run the Charge Point
1. Make sure the central system is already running.
2. In a separate terminal, launch the charge point simulator:
   ```bash
   go run ./cmd/cp
   ```
   It connects as `CP-001`, performs a BootNotification, and sends periodic heartbeats using the interval negotiated with the central system.

## Optional: Build Binaries
```bash
mkdir -p bin
go build -o bin/cs ./cmd/cs
go build -o bin/cp ./cmd/cp
```

## Troubleshooting
If the Go toolchain cannot write to the default build cache (common in restricted environments), point `GOCACHE` to a writable directory inside the project, for example:
```bash
GOCACHE=$(pwd)/.gocache go run ./cmd/cs
```
Apply the same flag when running the charge point if needed.

## Run Tests
Execute the HTTP/API unit tests from the project root:
```bash
GOCACHE=$(pwd)/.gocache go test ./cmd/cs
```
You can drop the `GOCACHE` prefix if your Go toolchain can write to the default cache directory.
