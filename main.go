package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ─── ANSI colours ─────────────────────────────────────────────────────────────

const (
	cReset  = "\033[0m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cBlue   = "\033[34m"
	cCyan   = "\033[36m"
	cBold   = "\033[1m"
)

var allANSI = []string{cReset, cRed, cGreen, cYellow, cBlue, cCyan, cBold}

func stripANSI(s string) string {
	for _, c := range allANSI {
		s = strings.ReplaceAll(s, c, "")
	}
	return s
}

// ─── STATUS ───────────────────────────────────────────────────────────────────

type Status int

const (
	PASS Status = iota
	FAIL
	WARN
	INFO
)

func (s Status) icon() string {
	switch s {
	case PASS:
		return "[ OK ]"
	case FAIL:
		return "[FAIL]"
	case WARN:
		return "[WARN]"
	default:
		return "[INFO]"
	}
}

func (s Status) color() string {
	switch s {
	case PASS:
		return cGreen
	case FAIL:
		return cRed
	case WARN:
		return cYellow
	default:
		return cCyan
	}
}

// ─── RESULT ───────────────────────────────────────────────────────────────────

type CheckResult struct {
	Name     string
	Status   Status
	Detail   string
	Duration time.Duration
}

// ─── GLOBALS ──────────────────────────────────────────────────────────────────

var results []CheckResult
var logFile *os.File

// out writes to both stdout and the report file (ANSI stripped for file).
func out(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Print(msg)
	if logFile != nil {
		_, _ = logFile.WriteString(stripANSI(msg))
	}
}

func section(title string) {
	out("\n%s%s  ┌─────────────────────────────────────────────────┐%s\n", cBold, cBlue, cReset)
	out("%s%s  │  %-48s│%s\n", cBold, cBlue, title, cReset)
	out("%s%s  └─────────────────────────────────────────────────┘%s\n", cBold, cBlue, cReset)
}

func addResult(r CheckResult) {
	results = append(results, r)
	ms := ""
	if r.Duration > 0 {
		ms = fmt.Sprintf(" [%dms]", r.Duration.Milliseconds())
	}
	out("  %s%s%s  %-42s %s%s\n",
		r.Status.color(), r.Status.icon(), cReset,
		r.Name, r.Detail, ms)
}

// ─── HELPERS ──────────────────────────────────────────────────────────────────

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(strings.TrimSpace(s), "%f", &f)
	return f
}

// zeroReader streams zero-bytes without allocating a large buffer (used for upload tests).
type zeroReader struct{ remaining int64 }

func (z *zeroReader) Read(p []byte) (n int, err error) {
	if z.remaining <= 0 {
		return 0, io.EOF
	}
	n = len(p)
	if int64(n) > z.remaining {
		n = int(z.remaining)
	}
	for i := range p[:n] {
		p[i] = 0
	}
	z.remaining -= int64(n)
	return n, nil
}

// pingHost runs a system ping (3 packets) and returns success + avg RTT string.
func pingHost(host string) (bool, time.Duration, string) {
	start := time.Now()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "-n", "3", "-w", "1500", host)
	} else {
		cmd = exec.Command("ping", "-c", "3", "-W", "2", host)
	}
	raw, err := cmd.Output()
	elapsed := time.Since(start)
	output := string(raw)

	if err != nil {
		if strings.Contains(output, "could not find host") || strings.Contains(output, "Unknown host") {
			return false, elapsed, "DNS lookup failed"
		}
		return false, elapsed, "no response (timed out)"
	}

	// Extract average RTT from output
	if runtime.GOOS == "windows" {
		if idx := strings.Index(output, "Average = "); idx != -1 {
			rest := output[idx+10:]
			if nl := strings.IndexByte(rest, '\n'); nl != -1 {
				rest = rest[:nl]
			}
			return true, elapsed, "avg " + strings.TrimSpace(rest)
		}
		return true, elapsed, "reachable"
	}
	// Linux / macOS
	if idx := strings.Index(output, "min/avg/max"); idx != -1 {
		rest := output[idx:]
		if eq := strings.Index(rest, " = "); eq != -1 {
			parts := strings.Split(rest[eq+3:], "/")
			if len(parts) >= 2 {
				return true, elapsed, fmt.Sprintf("avg %.1f ms", parseFloat(parts[1]))
			}
		}
	}
	return true, elapsed, "reachable"
}

// getGateway returns the default gateway IP.
func getGateway() string {
	switch runtime.GOOS {
	case "windows":
		raw, err := runCmd("cmd", "/C", "route print 0.0.0.0")
		if err != nil {
			return ""
		}
		scanner := bufio.NewScanner(strings.NewReader(raw))
		for scanner.Scan() {
			f := strings.Fields(scanner.Text())
			if len(f) >= 3 && f[0] == "0.0.0.0" && f[1] == "0.0.0.0" && net.ParseIP(f[2]) != nil {
				return f[2]
			}
		}
	default:
		for _, sh := range []string{
			"ip route show default | awk '{print $3}'",
			"netstat -rn | grep default | awk '{print $2}'",
		} {
			raw, err := runCmd("sh", "-c", sh)
			if err == nil {
				gw := strings.TrimSpace(raw)
				if net.ParseIP(gw) != nil {
					return gw
				}
			}
		}
	}
	return ""
}

// ─── CHECK FUNCTIONS ──────────────────────────────────────────────────────────

// 1. Network adapters + IP assignment
func checkAdapters() {
	section("1. NETWORK ADAPTERS")
	ifaces, err := net.Interfaces()
	if err != nil {
		addResult(CheckResult{"Network Interfaces", FAIL, err.Error(), 0})
		return
	}

	activeCount := 0
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		var ipv4s []string
		hasAPIPA := false
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ip := ipnet.IP.String()
				ipv4s = append(ipv4s, ip)
				if strings.HasPrefix(ip, "169.254.") {
					hasAPIPA = true
				}
			}
		}

		isUp := iface.Flags&net.FlagUp != 0
		status := INFO
		detail := "(no IPv4 assigned)"
		mac := iface.HardwareAddr.String()
		macPfx := ""
		if mac != "" {
			macPfx = mac + "  "
		}

		if len(ipv4s) > 0 && isUp {
			activeCount++
			detail = macPfx + strings.Join(ipv4s, ", ")
			if hasAPIPA {
				status = WARN
				detail += "  ← APIPA: DHCP server unreachable!"
			} else {
				status = PASS
			}
		} else if len(ipv4s) > 0 {
			detail = macPfx + strings.Join(ipv4s, ", ") + "  (adapter is DOWN)"
			status = WARN
		} else if isUp {
			detail = macPfx + "Up but no IP assigned"
			status = WARN
		}

		name := iface.Name
		if len(name) > 38 {
			name = name[:38] + "…"
		}
		addResult(CheckResult{name, status, detail, 0})
	}

	if activeCount == 0 {
		addResult(CheckResult{"Active Adapters", FAIL, "No adapter has a valid IP — check drivers / cable / WiFi", 0})
	}
}

// 2. Default gateway
func checkGateway() {
	section("2. DEFAULT GATEWAY")
	gw := getGateway()
	if gw == "" {
		addResult(CheckResult{"Default Gateway", FAIL, "Cannot determine gateway — routing table empty", 0})
		return
	}
	addResult(CheckResult{"Gateway IP", INFO, gw, 0})
	ok, dur, detail := pingHost(gw)
	if ok {
		addResult(CheckResult{"Gateway Ping", PASS, detail, dur})
	} else {
		addResult(CheckResult{"Gateway Ping", FAIL, detail + " — router may be down or unreachable", dur})
	}
}

// 3. WiFi signal & details (Windows only)
func checkWifi() {
	section("3. WIFI STATUS")
	if runtime.GOOS != "windows" {
		addResult(CheckResult{"WiFi Details", INFO, "netsh available on Windows only; skipping", 0})
		return
	}
	raw, err := runCmd("netsh", "wlan", "show", "interfaces")
	if err != nil || strings.Contains(raw, "There is no wireless interface") || strings.TrimSpace(raw) == "" {
		addResult(CheckResult{"WiFi", INFO, "No wireless adapter detected (wired connection?)", 0})
		return
	}

	// Parse a single field from netsh output
	field := func(key string) string {
		scanner := bufio.NewScanner(strings.NewReader(raw))
		for scanner.Scan() {
			trimmed := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(trimmed, key) {
				if idx := strings.IndexByte(trimmed, ':'); idx != -1 {
					return strings.TrimSpace(trimmed[idx+1:])
				}
			}
		}
		return ""
	}

	state := field("State")
	ssid := field("SSID")
	signal := field("Signal")
	radio := field("Radio type")
	channel := field("Channel")
	txRate := field("Transmit rate (Mbps)")
	rxRate := field("Receive rate (Mbps)")

	if state != "connected" {
		addResult(CheckResult{"WiFi State", FAIL, "Not connected  (State: " + state + ")", 0})
		return
	}

	var sigPct int
	fmt.Sscanf(strings.TrimSuffix(signal, "%"), "%d", &sigPct)
	connStatus := PASS
	connDetail := fmt.Sprintf("SSID: %-22s Signal: %-5s Radio: %s", ssid, signal, radio)
	switch {
	case sigPct > 0 && sigPct < 30:
		connStatus = FAIL
		connDetail += "  ← POOR SIGNAL!"
	case sigPct > 0 && sigPct < 60:
		connStatus = WARN
		connDetail += "  ← Weak signal"
	}
	addResult(CheckResult{"WiFi Connection", connStatus, connDetail, 0})
	addResult(CheckResult{"Channel / Link Speed", INFO,
		fmt.Sprintf("Ch: %-4s  TX: %s Mbps   RX: %s Mbps", channel, txRate, rxRate), 0})
}

// 4. DNS servers + name resolution
func checkDNS() {
	section("4. DNS & NAME RESOLUTION")

	// Show configured DNS servers (Windows ipconfig /all)
	if runtime.GOOS == "windows" {
		raw, err := runCmd("cmd", "/C", "ipconfig /all")
		if err == nil {
			var servers []string
			inDNS := false
			scanner := bufio.NewScanner(strings.NewReader(raw))
			for scanner.Scan() {
				line := scanner.Text()
				if strings.Contains(line, "DNS Servers") {
					inDNS = true
					if idx := strings.IndexByte(line, ':'); idx != -1 {
						if ip := net.ParseIP(strings.TrimSpace(line[idx+1:])); ip != nil {
							servers = append(servers, ip.String())
						}
					}
				} else if inDNS {
					trimmed := strings.TrimSpace(line)
					if net.ParseIP(trimmed) != nil {
						servers = append(servers, trimmed)
					} else {
						inDNS = false
					}
				}
			}
			if len(servers) > 0 {
				addResult(CheckResult{"Configured DNS", INFO, strings.Join(servers, ", "), 0})
			}
		}
	}

	// Resolve test domains
	for _, domain := range []string{"google.com", "microsoft.com", "cloudflare.com"} {
		start := time.Now()
		addrs, err := net.LookupHost(domain)
		elapsed := time.Since(start)
		if err != nil {
			addResult(CheckResult{"Resolve " + domain, FAIL, err.Error(), elapsed})
		} else {
			addResult(CheckResult{"Resolve " + domain, PASS, addrs[0], elapsed})
		}
	}
}

// 5. Internet reachability by IP (ICMP + TCP)
func checkInternet() {
	section("5. INTERNET REACHABILITY")

	// ICMP pings (bypass DNS)
	for _, t := range []struct{ name, ip string }{
		{"Ping 1.1.1.1 (Cloudflare)", "1.1.1.1"},
		{"Ping 8.8.8.8 (Google DNS)", "8.8.8.8"},
	} {
		ok, dur, detail := pingHost(t.ip)
		if ok {
			addResult(CheckResult{t.name, PASS, detail, dur})
		} else {
			addResult(CheckResult{t.name, FAIL, detail + " (ICMP may be blocked — see TCP below)", dur})
		}
	}

	// TCP connections (more reliable in firewalled environments)
	for _, t := range []struct{ name, addr string }{
		{"TCP google.com:443", "google.com:443"},
		{"TCP microsoft.com:443", "microsoft.com:443"},
		{"TCP cloudflare.com:443", "cloudflare.com:443"},
	} {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", t.addr, 5*time.Second)
		elapsed := time.Since(start)
		if err != nil {
			addResult(CheckResult{t.name, FAIL, err.Error(), elapsed})
		} else {
			conn.Close()
			addResult(CheckResult{t.name, PASS, "TCP handshake OK", elapsed})
		}
	}
}

// 6. HTTP / HTTPS access
func checkHTTP() {
	section("6. HTTP / HTTPS ACCESS")
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, url := range []string{
		"http://connectivitycheck.gstatic.com/generate_204",
		"https://www.google.com",
		"https://www.cloudflare.com",
		"https://www.microsoft.com",
	} {
		start := time.Now()
		resp, err := client.Get(url)
		elapsed := time.Since(start)
		if err != nil {
			addResult(CheckResult{url, FAIL, err.Error(), elapsed})
		} else {
			resp.Body.Close()
			st := PASS
			if resp.StatusCode >= 400 {
				st = WARN
			}
			addResult(CheckResult{url, st, fmt.Sprintf("HTTP %d", resp.StatusCode), elapsed})
		}
	}
}

// speedResult builds a CheckResult from bytes transferred and elapsed time.
func speedResult(label string, bytes, received int64, elapsed time.Duration) CheckResult {
	if received == 0 {
		return CheckResult{label, FAIL, "No data transferred", elapsed}
	}
	mbps := float64(received) / elapsed.Seconds() / 125000
	detail := fmt.Sprintf("%.2f Mbps  (%d KB in %d ms)", mbps, received/1024, elapsed.Milliseconds())
	st := PASS
	switch {
	case mbps < 1:
		st = FAIL
		detail += "  ← VERY SLOW!"
	case mbps < 5:
		st = WARN
		detail += "  ← Slow"
	}
	return CheckResult{label, st, detail, elapsed}
}

// 7. Download + Upload speed tests
func checkSpeed() {
	section("7. SPEED TEST — DOWNLOAD & UPLOAD")

	// ── Download: 1 MB, 10 MB, 100 MB ──
	// Sizes use metric MB (10^6) — Cloudflare's __down endpoint caps at 100,000,000 bytes exactly.
	dlClient := &http.Client{Timeout: 180 * time.Second}
	dlTests := []struct {
		label string
		bytes int64
	}{
		{"Download 10 MB", 10_000_000},
		{"Download 50 MB", 50_000_000},
	}
	for i, t := range dlTests {
		if i > 0 {
			// out("  (waiting 3s before next test…)\n")
			time.Sleep(3 * time.Second)
		}
		out("  (%s…)\n", t.label)
		start := time.Now()
		resp, err := dlClient.Get(fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", t.bytes))
		if err != nil {
			addResult(CheckResult{t.label, FAIL, err.Error(), 0})
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			addResult(CheckResult{t.label, FAIL, fmt.Sprintf("HTTP %d from speed server", resp.StatusCode), 0})
			continue
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		addResult(speedResult(t.label, t.bytes, n, time.Since(start)))
	}

	// ── Upload: 1 MB, 10 MB ──
	// out("  (waiting 3s before upload tests…)\n")
	time.Sleep(3 * time.Second)
	upClient := &http.Client{Timeout: 120 * time.Second}
	upTests := []struct {
		label string
		bytes int64
	}{
		{"Upload   10 MB", 10_000_000},
		{"Upload   50 MB", 50_000_000},
	}
	for i, t := range upTests {
		if i > 0 {
			// out("  (waiting 3s before next test…)\n")
			time.Sleep(3 * time.Second)
		}
		out("  (%s…)\n", t.label)
		start := time.Now()
		req, err := http.NewRequest("POST", "https://speed.cloudflare.com/__up",
			&zeroReader{remaining: t.bytes})
		if err != nil {
			addResult(CheckResult{t.label, FAIL, err.Error(), 0})
			continue
		}
		req.ContentLength = t.bytes
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := upClient.Do(req)
		if err != nil {
			addResult(CheckResult{t.label, FAIL, err.Error(), 0})
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		addResult(speedResult(t.label, t.bytes, t.bytes, time.Since(start)))
	}
}

// 8. Proxy settings (Windows)
func checkProxy() {
	section("8. PROXY SETTINGS")
	if runtime.GOOS != "windows" {
		addResult(CheckResult{"Proxy", INFO, "netsh proxy check available on Windows only; skipping", 0})
		return
	}

	// WinHTTP proxy (used by system / some apps)
	raw, err := runCmd("netsh", "winhttp", "show", "proxy")
	if err != nil {
		addResult(CheckResult{"WinHTTP Proxy", WARN, "Could not query WinHTTP proxy", 0})
	} else if strings.Contains(raw, "Direct access") {
		addResult(CheckResult{"WinHTTP Proxy", PASS, "Direct access (no proxy)", 0})
	} else {
		for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.Contains(line, "Current WinHTTP") {
				addResult(CheckResult{"WinHTTP Proxy", INFO, line, 0})
			}
		}
	}

	// IE / system proxy (used by browsers and most apps)
	raw, err = runCmd("cmd", "/C",
		`reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyEnable`)
	if err == nil {
		if strings.Contains(raw, "0x1") {
			raw2, _ := runCmd("cmd", "/C",
				`reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyServer`)
			proxyAddr := ""
			for _, l := range strings.Split(raw2, "\n") {
				if strings.Contains(l, "ProxyServer") {
					parts := strings.Fields(l)
					if len(parts) >= 3 {
						proxyAddr = parts[len(parts)-1]
					}
				}
			}
			addResult(CheckResult{"IE/System Proxy", WARN,
				"ENABLED: " + proxyAddr + "  ← may affect browser connections", 0})
		} else {
			addResult(CheckResult{"IE/System Proxy", PASS, "Not enabled", 0})
		}
	}
}

// ─── DIAGNOSIS ────────────────────────────────────────────────────────────────

func diagnose() string {
	var (
		noAdapter    bool
		noGateway    bool
		poorWifi     bool
		noDNS        bool
		noInternet   bool
		proxyWarning bool
		httpFail     bool
	)

	for _, r := range results {
		n, s, d := r.Name, r.Status, r.Detail
		switch {
		case s == FAIL && strings.Contains(n, "Active Adapters"):
			noAdapter = true
		case s == FAIL && strings.Contains(n, "Gateway"):
			noGateway = true
		case (s == FAIL || s == WARN) && strings.Contains(n, "WiFi"):
			poorWifi = true
		case s == FAIL && strings.Contains(n, "Resolve"):
			noDNS = true
		case s == FAIL && (strings.Contains(n, "Ping ") || strings.Contains(n, "TCP ")):
			noInternet = true
		case s == WARN && strings.Contains(n, "Proxy") && strings.Contains(d, "ENABLED"):
			proxyWarning = true
		case s == FAIL && strings.Contains(n, "https"):
			httpFail = true
		}
	}

	switch {
	case noAdapter:
		return cRed + "[CRITICAL] Hardware / Driver problem" + cReset +
			" — No working adapter found.\n" +
			"  Action: check NIC drivers in Device Manager or try a different cable/port."

	case noGateway && poorWifi:
		return cRed + "[CRITICAL] Poor WiFi signal" + cReset +
			" — Cannot reach the router due to weak signal.\n" +
			"  Action: move closer to the access point, check AP status, or try Ethernet."

	case noGateway:
		return cRed + "[CRITICAL] Local network problem" + cReset +
			" — Cannot reach the default gateway (router/switch).\n" +
			"  Action: verify cable is plugged in, WiFi is connected, or the router is powered on."

	case noInternet && noDNS:
		return cRed + "[CRITICAL] ISP / WAN outage" + cReset +
			" — Gateway is reachable but internet is down.\n" +
			"  Action: check router WAN status (router admin page), contact ISP, or try mobile hotspot."

	case noInternet && !noDNS:
		return cYellow + "[WARNING] ISP outage or firewall block" + cReset +
			" — DNS resolves but direct internet IPs are unreachable.\n" +
			"  Action: check router WAN/firewall rules, or ask ISP if there is an outage."

	case noDNS && !noInternet:
		return cYellow + "[WARNING] DNS misconfiguration" + cReset +
			" — Internet reachable by IP but domain names fail.\n" +
			"  Action: set DNS to 8.8.8.8 or 1.1.1.1 in adapter settings, or flush DNS cache."

	case proxyWarning && httpFail:
		return cYellow + "[WARNING] Proxy misconfiguration" + cReset +
			" — A system proxy is set but HTTPS is failing.\n" +
			"  Action: verify proxy settings or try disabling the proxy (Settings → Proxy)."

	case proxyWarning:
		return cYellow + "[NOTE] System proxy is enabled" + cReset +
			" — All checks passed but a proxy is active.\n" +
			"  Action: if specific sites fail, check proxy allowlist/blocklist."

	case poorWifi:
		return cYellow + "[WARNING] Weak WiFi signal" + cReset +
			" — Connection is up but signal is below recommended level.\n" +
			"  Action: move closer to the AP, reduce interference, or consider Ethernet."

	default:
		return cGreen + "[ALL CLEAR] Connection appears healthy." + cReset +
			"\n  Issue may be intermittent, site-specific, or limited to a particular application."
	}
}

// ─── MAIN ─────────────────────────────────────────────────────────────────────

func main() {
	hostname, _ := os.Hostname()
	now := time.Now()

	// Create report file
	reportName := fmt.Sprintf("netcheck_%s_%s.txt", hostname, now.Format("20060102_150405"))
	var ferr error
	logFile, ferr = os.Create(reportName)
	if ferr != nil {
		fmt.Printf("  [WARN] Cannot create report file: %v\n", ferr)
	} else {
		defer logFile.Close()
	}

	// ── Banner ──
	out("\n")
	out("%s%s╔══════════════════════════════════════════════════╗%s\n", cBold, cCyan, cReset)
	out("%s%s║         NET-CHECK  —  Network Diagnostic         ║%s\n", cBold, cCyan, cReset)
	out("%s%s╚══════════════════════════════════════════════════╝%s\n", cBold, cCyan, cReset)
	out("  Host : %s\n", hostname)
	out("  OS   : %s / %s\n", runtime.GOOS, runtime.GOARCH)
	out("  Time : %s\n", now.Format("2006-01-02  15:04:05"))

	// ── Checks ──
	checkAdapters()
	checkGateway()
	checkWifi()
	checkDNS()
	checkInternet()
	checkHTTP()
	checkSpeed()
	checkProxy()

	// ── Diagnosis ──
	cause := diagnose()
	out("\n%s%s══════════════════════════════════════════════════════%s\n", cBold, cCyan, cReset)
	out("%s%s  DIAGNOSIS%s\n", cBold, cCyan, cReset)
	out("%s%s══════════════════════════════════════════════════════%s\n\n", cBold, cCyan, cReset)
	out("  %s%s%s\n\n", cBold, cause, cReset)

	if logFile != nil {
		out("  Report saved → %s\n\n", reportName)
	}

	// Keep window open when double-clicked on Windows
	if runtime.GOOS == "windows" {
		out("  Press ENTER to exit…\n")
		fmt.Scanln()
	}
}
