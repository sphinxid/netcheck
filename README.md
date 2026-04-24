# net-check

A lightweight command-line network diagnostic tool written in Go.  
Run it on any Windows (or Linux/macOS) machine to collect evidence about connectivity problems — useful for IT troubleshooting when users report slow or broken internet.

## What it checks

| # | Section | What it detects |
|---|---------|-----------------|
| 1 | **Network Adapters** | MAC address, IP assignment, APIPA (DHCP failure), adapter state |
| 2 | **Default Gateway** | Gateway IP, ICMP reachability |
| 3 | **WiFi Status** | SSID, signal strength (%), radio type, channel, link speed |
| 4 | **DNS & Name Resolution** | Configured DNS servers, resolves `google.com`, `microsoft.com`, `cloudflare.com` |
| 5 | **Internet Reachability** | ICMP ping to `1.1.1.1` / `8.8.8.8`, TCP handshake to major sites |
| 6 | **HTTP / HTTPS Access** | Validates actual HTTP/HTTPS requests |
| 7 | **Speed Test** | Download: 1 MB / 10 MB / 100 MB — Upload: 1 MB / 10 MB (via Cloudflare) |
| 8 | **Proxy Settings** | WinHTTP proxy, IE/system proxy (Windows) |

After all checks, the tool prints a **DIAGNOSIS** with the most likely cause and a suggested action.

## Sample output

```
╔══════════════════════════════════════════════════╗
║         NET-CHECK  —  Network Diagnostic         ║
╚══════════════════════════════════════════════════╝
  Host : LAPTOP-01
  OS   : windows / amd64
  Time : 2026-04-22  07:33:34

  ┌─────────────────────────────────────────────────┐
  │  1. NETWORK ADAPTERS                             │
  └─────────────────────────────────────────────────┘
  [ OK ]  Ethernet                                   d4:5d:64:aa:bb:cc  192.168.1.105

  ┌─────────────────────────────────────────────────┐
  │  7. SPEED TEST — DOWNLOAD & UPLOAD               │
  └─────────────────────────────────────────────────┘
  [ OK ]  Download   1 MB                            77.56 Mbps  (976 KB in 100 ms)
  [ OK ]  Download  10 MB                            128.05 Mbps  (9765 KB in 610 ms)
  [ OK ]  Download 100 MB                            134.22 Mbps  (97656 KB in 5820 ms)
  [ OK ]  Upload    1 MB                             76.87 Mbps  (976 KB in 104 ms)
  [ OK ]  Upload   10 MB                             141.04 Mbps  (9765 KB in 568 ms)

══════════════════════════════════════════════════════
  DIAGNOSIS
══════════════════════════════════════════════════════

  [ALL CLEAR] Connection appears healthy.
  Issue may be intermittent, site-specific, or limited to a particular application.

  Report saved → netcheck_LAPTOP-01_20260422_073334.txt
```

## Report file

Every run saves a plain-text report named:

```
netcheck_<hostname>_<YYYYMMDD_HHMMSS>.txt
```

The file is saved in the same folder as the executable. Users can email this file to IT support as evidence.

## Build

Requires [Go 1.21+](https://go.dev/dl/). No external dependencies.

```powershell
# Clone or copy the source, then:
go build -o netcheck.exe .
```

Cross-compile for Windows from Linux/macOS:

```bash
GOOS=windows GOARCH=amd64 go build -o netcheck.exe .
```

## Run

**Double-click** `netcheck.exe` (it keeps the window open until you press Enter), or run from a terminal:

```powershell
.\netcheck.exe
```

On Linux / macOS:

```bash
go run main.go
# or after building:
./netcheck
```

> **Note:** Some checks (WiFi details, proxy settings) are Windows-only and are skipped gracefully on other platforms.

## Diagnosis logic

The tool evaluates the chain from hardware → LAN → gateway → DNS → internet and maps failures to the most specific cause:

| Symptom | Diagnosis |
|---------|-----------|
| No adapter / APIPA IP | Hardware / driver problem |
| Gateway unreachable + poor WiFi | Poor WiFi signal |
| Gateway unreachable | Local network problem (cable / switch / router) |
| Gateway OK, internet down | ISP / WAN outage |
| DNS fails, IP ping works | DNS misconfiguration |
| Proxy enabled + HTTPS fails | Proxy misconfiguration |
| Everything passes | Connection appears healthy |

---

## License

MIT License

Copyright (c) 2026

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
