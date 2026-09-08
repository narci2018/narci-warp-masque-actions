package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

//go:embed web/*
var webFS embed.FS

type webEndpoint struct {
	Endpoint     string  `json:"endpoint"`
	IP           string  `json:"ip"`
	Subnet       string  `json:"subnet"`
	EpPingMs     int64   `json:"ep_ping_ms"`
	TunPingMs    int64   `json:"tun_ping_ms"`
	LossPct      float32 `json:"loss_pct"`
	SeenAs       string  `json:"seen_as"`
	Node         string  `json:"node"`
	NodeLocation string  `json:"node_location"`
	Torn         bool    `json:"torn"`
	Working      bool    `json:"working"`
	SpeedMbps    float64 `json:"speed_mbps"`
}

type webState struct {
	mu           sync.RWMutex
	scanning     bool
	scanCancel   context.CancelFunc
	scanPhase    string
	scanTotal    int
	scanDone     int
	scanProto    string

	// SOCKS proxy
	socksActive   bool
	socksCancel   context.CancelFunc
	socksEndpoint string
	socksProto    string
	socksPort     int
	socksListen   string

	// Results
	lastResults  []webEndpoint
	lastProtoRun protoRun
	lastOpts     options

	// SSE clients
	clientsMu sync.Mutex
	clients   map[chan string]struct{}
}

var serverState = &webState{
	socksPort:   29881,
	socksListen: "0.0.0.0",
	clients:     make(map[chan string]struct{}),
}

func (s *webState) broadcast(event string, data any) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(bytes))

	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func findSubnet(ip netip.Addr) string {
	for _, p := range pools {
		if p.Contains(ip) {
			return p.String()
		}
	}
	if ip.Is4() {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

func runWebCmd(ctx context.Context, opts options) error {
	// Check environment variable overrides for ports
	if envPort := os.Getenv("PORT_WEB"); envPort != "" {
		host, _, err := net.SplitHostPort(opts.listen)
		if err != nil {
			host = "0.0.0.0"
		}
		opts.listen = net.JoinHostPort(host, envPort)
	}
	if envSocks := os.Getenv("PORT_SOCKS"); envSocks != "" {
		if p, err := strconv.Atoi(envSocks); err == nil && p > 0 {
			opts.port = p
		}
	}
	serverState.socksPort = opts.port

	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/status", handleStatus(opts))
	mux.HandleFunc("/api/account", handleAccount(opts))
	mux.HandleFunc("/api/account/register", handleRegister(opts))
	mux.HandleFunc("/api/scan/start", handleScanStart(opts))
	mux.HandleFunc("/api/scan/stop", handleScanStop)
	mux.HandleFunc("/api/scan/events", handleScanEvents)
	mux.HandleFunc("/api/scan/results", handleScanResults)
	mux.HandleFunc("/api/config/export", handleConfigExport(opts))
	mux.HandleFunc("/api/config/batch_export", handleConfigBatchExport(opts))
	mux.HandleFunc("/api/socks/start", handleSocksStart(opts))
	mux.HandleFunc("/api/socks/stop", handleSocksStop)
	mux.HandleFunc("/api/sub/fetch", handleSubFetch)
	mux.HandleFunc("/api/sub/list", handleSubList)
	mux.HandleFunc("/api/sub/select", handleSubSelect)

	// Static Files from embed.FS
	subFS, err := fs.Sub(webFS, "web")
	if err != nil {
		return fmt.Errorf("failed to load web filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Disable caching for UI development
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		fileServer.ServeHTTP(w, r)
	}))

	server := &http.Server{
		Addr:    opts.listen,
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
		case <-ctx.Done():
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Println()
	fmt.Println("==================================================================")
	fmt.Println("       WARPSCOUT WebUI Server is now running!                    ")
	fmt.Printf("  -> Web Dashboard : http://%s\n", opts.listen)
	fmt.Printf("  -> SOCKS5 Port   : %d\n", opts.port)
	fmt.Println("==================================================================")
	fmt.Println()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server failed: %v", err)
	}
	return nil
}

func handleStatus(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		serverState.mu.RLock()
		defer serverState.mu.RUnlock()

		acct, err := loadAccount(opts.accountPath)
		hasAcct := (err == nil && acct.PrivateKey != "")

		resp := map[string]any{
			"version":        version,
			"account_exists": hasAcct,
			"account_file":   opts.accountPath,
			"scanning":       serverState.scanning,
			"scan_phase":     serverState.scanPhase,
			"scan_done":      serverState.scanDone,
			"scan_total":     serverState.scanTotal,
			"scan_proto":     serverState.scanProto,
			"socks_active":   serverState.socksActive,
			"socks_endpoint": serverState.socksEndpoint,
			"socks_proto":    serverState.socksProto,
			"socks_port":     serverState.socksPort,
			"results_count":  len(serverState.lastResults),
			"active_proxy":   globalSubState.ActiveNode,
		}
		if hasAcct {
			resp["account_ipv4"] = acct.IPv4
			resp["account_ipv6"] = acct.IPv6
			resp["account_has_masque"] = (acct.Masque != nil)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleAccount(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		acct, err := loadAccount(opts.accountPath)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Account not found: " + err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         acct.ID,
			"ipv4":       acct.IPv4,
			"ipv6":       acct.IPv6,
			"has_masque": acct.Masque != nil,
			"file":       opts.accountPath,
		})
	}
}

func handleRegister(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Proxy string `json:"proxy"`
			Fresh bool   `json:"fresh"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		regOpts := opts
		if regOpts.relay == "" {
			regOpts.relay = defaultRelay
		}
		if regOpts.proto == "" {
			regOpts.proto = protoWG
		}
		if regOpts.timeoutSec <= 0 {
			regOpts.timeoutSec = 4
		}
		if regOpts.perSubnet <= 0 {
			regOpts.perSubnet = 5
		}
		if req.Proxy != "" {
			// 在 Docker 容器中，宿主机代理不能使用 127.0.0.1，自动转为 host.docker.internal
			p := req.Proxy
			if strings.Contains(p, "127.0.0.1") {
				p = strings.ReplaceAll(p, "127.0.0.1", "host.docker.internal")
			} else if strings.Contains(p, "localhost") {
				p = strings.ReplaceAll(p, "localhost", "host.docker.internal")
			}
			regOpts.proxy = p
		}
		if req.Fresh {
			regOpts.freshAccount = true
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		if err := applyRelay(&regOpts); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Relay error: " + err.Error()})
			return
		}
		_, ips, err := setupScan(regOpts)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Setup error: " + err.Error()})
			return
		}
		timeout := time.Duration(regOpts.timeoutSec) * time.Second

		var existing account
		if !regOpts.freshAccount {
			existing, _ = accountToRotate(regOpts.accountPath)
		}

		serverState.broadcast("log", map[string]any{"text": "正在向 Cloudflare 申请注册 WARP 账号（自动尝试直接请求、反代中继与临时隧道自举）...", "type": "info"})
		a, err := obtainAccount(ctx, regOpts, ips, timeout, existing)
		if err != nil {
			errMsg := fmt.Sprintf("注册失败: %v。提示：若网络完全被墙，可在弹窗中填入前置代理（如 http://host.docker.internal:7890）", err)
			serverState.broadcast("log", map[string]any{"text": errMsg, "type": "error"})
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": errMsg})
			return
		}

		if err := saveAccount(regOpts.accountPath, a); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		applyAccount(a)
		serverState.broadcast("log", map[string]any{"text": "Account registered successfully and saved!", "type": "success"})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"ipv4":   a.IPv4,
			"ipv6":   a.IPv6,
		})
	}
}

func handleScanEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	msgChan := make(chan string, 64)
	serverState.clientsMu.Lock()
	serverState.clients[msgChan] = struct{}{}
	serverState.clientsMu.Unlock()

	defer func() {
		serverState.clientsMu.Lock()
		delete(serverState.clients, msgChan)
		serverState.clientsMu.Unlock()
	}()

	// Initial ping
	fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case msg := <-msgChan:
			fmt.Fprint(w, msg)
			flusher.Flush()
		}
	}
}

func handleScanStart(baseOpts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		serverState.mu.Lock()
		if serverState.scanning {
			serverState.mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Scan already running"})
			return
		}

		var req struct {
			Proto          string `json:"proto"`
			TunPing        bool   `json:"tun_ping"`
			Sample         int    `json:"sample"`
			Full           bool   `json:"full"`
			GenI1          string `json:"gen_i1"`
			ExcludeNode    string `json:"exclude_node"`
			ExcludeCountry string `json:"exclude_country"`
			Country        string `json:"country"`
			Node           string `json:"node"`
			Port           int    `json:"port"`
			Through        string `json:"through"`
			InnerProto     string `json:"inner_proto"`
			IPv6           bool   `json:"ipv6"`
			Target         string `json:"target"`
			Proxy          string `json:"proxy"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		scanOpts := baseOpts
		if req.Proxy != "" {
			p := req.Proxy
			if strings.Contains(p, "127.0.0.1") {
				p = strings.ReplaceAll(p, "127.0.0.1", "host.docker.internal")
			} else if strings.Contains(p, "localhost") {
				p = strings.ReplaceAll(p, "localhost", "host.docker.internal")
			}
			scanOpts.proxy = p
		} else if globalSubState.ActiveNode != nil && globalSubState.ActiveNode.ProxyURL != "" {
			p := globalSubState.ActiveNode.ProxyURL
			if strings.Contains(p, "127.0.0.1") {
				p = strings.ReplaceAll(p, "127.0.0.1", "host.docker.internal")
			} else if strings.Contains(p, "localhost") {
				p = strings.ReplaceAll(p, "localhost", "host.docker.internal")
			}
			scanOpts.proxy = p
		}

		if req.Proto != "" {
			scanOpts.proto = req.Proto
		} else {
			scanOpts.proto = protoAWG
		}
		scanOpts.tunPingCheck = req.TunPing
		if scanOpts.tunPingCheck {
			scanOpts.tunPingCount = durabilityPings
		} else {
			scanOpts.tunPingCount = 0
		}
		scanOpts.perSubnet = req.Sample
		if scanOpts.perSubnet <= 0 {
			scanOpts.perSubnet = 5
		}
		scanOpts.full = req.Full
		scanOpts.genI1 = req.GenI1
		scanOpts.excludeNode = req.ExcludeNode
		scanOpts.excludeCountry = req.ExcludeCountry
		scanOpts.country = req.Country
		scanOpts.node = req.Node
		scanOpts.port = req.Port
		scanOpts.ipv6 = req.IPv6
		scanOpts.through = req.Through
		scanOpts.innerProto = req.InnerProto
		if scanOpts.through != "" {
			if scanOpts.innerProto == "" {
				scanOpts.innerProto = protoWG
			}
			scanOpts.tunnelParallel = 1
		} else {
			scanOpts.tunnelParallel = defaultTunnelJobs
		}
		if req.Target != "" {
			targets, err := parseTargets(req.Target)
			if err != nil {
				serverState.mu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "Invalid target format: " + err.Error()})
				return
			}
			scanOpts.targets = targets
		}
		scanOpts.wantMeta = true
		scanOpts.timeoutSec = 2

		if err := loadScanAccount(scanOpts.accountPath); err != nil {
			serverState.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "No account: please register first"})
			return
		}

		run, ips, err := setupScan(scanOpts)
		if err != nil {
			serverState.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		if run.isAWG() && scanOpts.genI1 != "" {
			_ = regenI1(scanOpts)
		}

		ctx, cancel := context.WithCancel(context.Background())
		serverState.scanning = true
		serverState.scanCancel = cancel
		serverState.scanPhase = "Starting scan..."
		serverState.scanProto = run.name
		serverState.scanDone = 0
		serverState.scanTotal = len(ips)
		serverState.lastProtoRun = run
		serverState.lastOpts = scanOpts
		serverState.lastResults = nil
		serverState.mu.Unlock()

		go executeScan(ctx, scanOpts, run, ips)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "started",
			"proto":  run.name,
			"target": len(ips),
		})
	}
}

func executeScan(ctx context.Context, opts options, run protoRun, ips []netip.Addr) {
	defer func() {
		serverState.mu.Lock()
		serverState.scanning = false
		serverState.scanCancel = nil
		serverState.scanPhase = "Done"
		serverState.mu.Unlock()
	}()

	var count atomic.Int64
	var total atomic.Int64
	total.Store(int64(len(ips)))

	emit := func(msg tea.Msg) {
		switch m := msg.(type) {
		case stepMsg:
			serverState.mu.Lock()
			serverState.scanPhase = m.label + ": " + m.summary
			serverState.mu.Unlock()
			serverState.broadcast("step", map[string]any{
				"label":   m.label,
				"summary": m.summary,
				"done":    m.done,
				"fail":    m.fail,
			})
		case barBeginMsg:
			total.Store(int64(m.total))
			count.Store(0)
			serverState.mu.Lock()
			serverState.scanPhase = m.label
			serverState.scanTotal = m.total
			serverState.scanDone = 0
			serverState.mu.Unlock()
			serverState.broadcast("progress", map[string]any{
				"label": m.label,
				"done":  0,
				"total": m.total,
			})
		case probedMsg:
			done := count.Add(1)
			serverState.mu.Lock()
			serverState.scanDone = int(done)
			serverState.mu.Unlock()
			serverState.broadcast("progress", map[string]any{
				"done":  done,
				"total": total.Load(),
			})
		case foundMsg:
			serverState.broadcast("found", map[string]any{
				"endpoint":      m.endpoint,
				"ep_ping_ms":    m.epPing.Milliseconds(),
				"tun_ping_ms":   m.tunPing.Milliseconds(),
				"loss_pct":      m.loss * 100,
				"seen_as":       m.exit,
				"node":          m.colo,
				"node_location": m.colo,
				"torn":          m.torn,
			})
		case barEndMsg:
			serverState.broadcast("step", map[string]any{
				"label":   m.label,
				"summary": m.summary,
				"done":    true,
			})
		}
	}

	if opts.through != "" {
		serverState.broadcast("log", map[string]any{
			"text": fmt.Sprintf("WARP-in-WARP: 正在连接外层跳板隧道 (%s)...", opts.through),
			"type": "info",
		})
		outerTimeout := 5 * time.Second
		if time.Duration(opts.timeoutSec)*time.Second > outerTimeout {
			outerTimeout = time.Duration(opts.timeoutSec) * time.Second
		}
		n, err := dialOuter(ctx, opts, outerTimeout)
		if err != nil {
			serverState.broadcast("log", map[string]any{"text": "外层跳板连接失败: " + err.Error(), "type": "error"})
			serverState.broadcast("done", map[string]any{"status": "error", "error": err.Error()})
			return
		}
		defer func() {
			n.tunnel.Close()
			outer = nil
		}()
		outer = n
		serverState.broadcast("log", map[string]any{
			"text": fmt.Sprintf("外层跳板已连通 (%s)。现正在机房内发起内层二次路由扫描 (出口已变为 US 原生出口)...", n.label),
			"type": "success",
		})
	}

	serverState.broadcast("log", map[string]any{"text": fmt.Sprintf("Phase 1: Probing reachable WARP ports for %s...", run.name), "type": "info"})
	ph, err := runScan(ctx, opts, run, ips, time.Duration(opts.timeoutSec)*time.Second, emit)
	if err != nil {
		serverState.broadcast("log", map[string]any{"text": "Scan error: " + err.Error(), "type": "error"})
		serverState.broadcast("done", map[string]any{"status": "error", "error": err.Error()})
		return
	}

	if filtered(opts) {
		ph = applyFilters(ph, opts)
	}

	// Group and sort results
	var webRes []webEndpoint
	for _, r := range ph.results {
		if !r.ok {
			continue
		}
		webRes = append(webRes, webEndpoint{
			Endpoint:     r.endpoint,
			IP:           r.ip.String(),
			Subnet:       findSubnet(r.ip),
			EpPingMs:     r.epPing.Milliseconds(),
			TunPingMs:    r.tunPing.Milliseconds(),
			LossPct:      r.loss * 100,
			SeenAs:       exitRegion(r.exit),
			Node:         exitColo(r.exit),
			NodeLocation: exitColoLocation(r.exit),
			Torn:         !r.durable,
			Working:      r.durable,
			SpeedMbps:    r.speed,
		})
	}

	// Sort best endpoints first: 0 loss first, then lowest tunPing, then epPing
	sort.Slice(webRes, func(i, j int) bool {
		if webRes[i].Torn != webRes[j].Torn {
			return !webRes[i].Torn
		}
		if webRes[i].LossPct != webRes[j].LossPct {
			return webRes[i].LossPct < webRes[j].LossPct
		}
		if webRes[i].TunPingMs != webRes[j].TunPingMs {
			return webRes[i].TunPingMs < webRes[j].TunPingMs
		}
		return webRes[i].EpPingMs < webRes[j].EpPingMs
	})

	serverState.mu.Lock()
	serverState.lastResults = webRes
	serverState.mu.Unlock()

	serverState.broadcast("done", map[string]any{
		"status":  "success",
		"total":   len(webRes),
		"results": webRes,
	})
	serverState.broadcast("log", map[string]any{"text": fmt.Sprintf("Scan completed! Found %d endpoints.", len(webRes)), "type": "success"})
}

func handleScanStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	serverState.mu.Lock()
	defer serverState.mu.Unlock()
	if serverState.scanning && serverState.scanCancel != nil {
		serverState.scanCancel()
		serverState.scanning = false
		serverState.scanCancel = nil
		serverState.scanPhase = "Cancelled"
		serverState.broadcast("log", map[string]any{"text": "Scan stopped by user.", "type": "warn"})
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "stopped"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "idle"})
}

func handleScanResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	serverState.mu.RLock()
	defer serverState.mu.RUnlock()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"results": serverState.lastResults,
		"count":   len(serverState.lastResults),
	})
}

func handleConfigExport(baseOpts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Endpoint   string `json:"endpoint"`
			Proto      string `json:"proto"`
			ConfType   string `json:"conf_type"` // native, mihomo, mihomo-json
			Through    string `json:"through"`
			InnerProto string `json:"inner_proto"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		serverState.mu.RLock()
		opts := serverState.lastOpts
		run := serverState.lastProtoRun
		serverState.mu.RUnlock()

		if req.Proto != "" {
			parsed, err := parseProto(req.Proto)
			if err == nil {
				run = parsed
			}
		}
		if req.ConfType != "" {
			opts.confType = req.ConfType
		} else {
			opts.confType = confTypeNative
		}

		if req.Through != "" {
			opts.through = req.Through
			if req.InnerProto != "" {
				opts.innerProto = req.InnerProto
			} else {
				opts.innerProto = protoWG
			}
			endpoint, err := parseEndpointSpec("-through", opts.through)
			if err == nil {
				outerRun, _ := parseProto(opts.proto)
				outer = &nest{
					endpoint: endpoint,
					run:      outerRun,
					label:    endpoint,
				}
				defer func() { outer = nil }()
			}
		}

		confBytes, err := renderConfFor(opts, req.Endpoint, run)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(confBytes)
	}
}

func handleConfigBatchExport(baseOpts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Endpoints  []string `json:"endpoints"`
			Proto      string   `json:"proto"`
			ConfType   string   `json:"conf_type"` // native, mihomo, ip-list
			Through    string   `json:"through"`
			InnerProto string   `json:"inner_proto"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Endpoints) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "请至少选择一个端点进行导出"})
			return
		}

		// 纯 IP:Port 文本格式
		if req.ConfType == "ip-list" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(strings.Join(req.Endpoints, "\n") + "\n"))
			return
		}

		serverState.mu.RLock()
		opts := serverState.lastOpts
		run := serverState.lastProtoRun
		serverState.mu.RUnlock()

		if req.Proto != "" {
			if parsed, err := parseProto(req.Proto); err == nil {
				run = parsed
			}
		}
		if req.ConfType != "" {
			opts.confType = req.ConfType
		} else {
			opts.confType = confTypeNative
		}

		if req.Through != "" {
			opts.through = req.Through
			if req.InnerProto != "" {
				opts.innerProto = req.InnerProto
			} else {
				opts.innerProto = protoWG
			}
			endpoint, err := parseEndpointSpec("-through", opts.through)
			if err == nil {
				outerRun, _ := parseProto(opts.proto)
				outer = &nest{
					endpoint: endpoint,
					run:      outerRun,
					label:    endpoint,
				}
				defer func() { outer = nil }()
			}
		}

		var sb strings.Builder

		if req.ConfType == "mihomo" {
			// 合并为包含 proxies: 的统一 Clash / Mihomo 配置
			sb.WriteString("# Generated by WARPSCOUT WebUI Batch Export\n")
			sb.WriteString("proxies:\n")
			for i, ep := range req.Endpoints {
				confBytes, err := renderConfFor(opts, ep, run)
				if err != nil {
					continue
				}
				confStr := strings.TrimSpace(string(confBytes))
				lines := strings.Split(confStr, "\n")
				for _, line := range lines {
					if strings.HasPrefix(strings.TrimSpace(line), "proxies:") {
						continue
					}
					// 保证每一项是 2 空格缩进
					if strings.HasPrefix(line, "  - ") || strings.HasPrefix(line, "- ") {
						if !strings.HasPrefix(line, "  ") {
							line = "  " + line
						}
						// 自定义每个节点的名称，防止重名
						if strings.Contains(line, "name:") {
							line = fmt.Sprintf("  - name: \"WARP-%d (%s)\"", i+1, ep)
						}
					}
					sb.WriteString(line)
					sb.WriteString("\n")
				}
			}
		} else {
			// Native / WireGuard 分段合并配置
			sb.WriteString(fmt.Sprintf("### WARPSCOUT 批量导出配置 (共 %d 个端点) ###\n\n", len(req.Endpoints)))
			for i, ep := range req.Endpoints {
				sb.WriteString(fmt.Sprintf("### ========================================\n"))
				sb.WriteString(fmt.Sprintf("### 节点 %d: %s\n", i+1, ep))
				sb.WriteString(fmt.Sprintf("### ========================================\n"))
				confBytes, err := renderConfFor(opts, ep, run)
				if err == nil {
					sb.Write(confBytes)
				}
				sb.WriteString("\n\n")
			}
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(sb.String()))
	}
}

func handleSocksStart(baseOpts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Endpoint   string `json:"endpoint"`
			Proto      string `json:"proto"`
			Through    string `json:"through"`
			InnerProto string `json:"inner_proto"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Endpoint is required"})
			return
		}

		serverState.mu.Lock()
		if serverState.socksActive && serverState.socksCancel != nil {
			serverState.socksCancel()
			serverState.socksActive = false
		}

		opts := baseOpts
		if req.Proto != "" {
			opts.proto = req.Proto
		} else if serverState.lastProtoRun.name != "" {
			opts.proto = serverState.lastProtoRun.name
		} else {
			opts.proto = protoAWG
		}
		opts.endpoint = req.Endpoint
		opts.through = req.Through
		opts.innerProto = req.InnerProto
		if opts.through != "" && opts.innerProto == "" {
			opts.innerProto = protoWG
		}
		opts.port = serverState.socksPort
		opts.listen = serverState.socksListen

		runProto := opts.proto
		if opts.through != "" {
			runProto = opts.innerProto
		}
		run, err := parseProto(runProto)
		if err != nil {
			serverState.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		serverState.socksCancel = cancel
		serverState.socksEndpoint = req.Endpoint
		serverState.socksProto = run.name
		serverState.mu.Unlock()

		readyCh := make(chan error, 1)
		go func() {
			err := runWebSOCKS(ctx, opts, run, req.Endpoint, readyCh)
			serverState.mu.Lock()
			serverState.socksActive = false
			serverState.socksCancel = nil
			serverState.mu.Unlock()
			if err != nil {
				serverState.broadcast("log", map[string]any{"text": "SOCKS5 stopped: " + err.Error(), "type": "error"})
			}
		}()

		select {
		case err := <-readyCh:
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			serverState.mu.Lock()
			serverState.socksActive = true
			serverState.mu.Unlock()
			modeDesc := "直连模式"
			if opts.through != "" {
				modeDesc = "WARP-in-WARP 双层链式模式 (出口 US)"
			}
			serverState.broadcast("log", map[string]any{
				"text": fmt.Sprintf("SOCKS5 代理启动成功 [%s] 端口 %d -> %s", modeDesc, serverState.socksPort, req.Endpoint),
				"type": "success",
			})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":   "running",
				"endpoint": req.Endpoint,
				"through":  opts.through,
				"port":     serverState.socksPort,
				"url":      fmt.Sprintf("socks5://127.0.0.1:%d", serverState.socksPort),
			})
		case <-time.After(15 * time.Second):
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "SOCKS5 启动超时"})
		}
	}
}

func runWebSOCKS(ctx context.Context, opts options, run protoRun, endpoint string, readyCh chan<- error) error {
	if err := loadScanAccount(opts.accountPath); err != nil {
		readyCh <- err
		return err
	}
	timeout := 4 * time.Second

	if opts.through != "" {
		n, err := dialOuter(ctx, opts, timeout)
		if err != nil {
			readyCh <- err
			return err
		}
		defer func() {
			n.tunnel.Close()
			outer = nil
		}()
		outer = n
	}

	tn, err := newTunnel(run)
	if err != nil {
		readyCh <- err
		return err
	}
	defer tn.Close()

	if !tn.handshake(ctx, endpoint, timeout) {
		err := fmt.Errorf("handshake failed with %s over %s", endpoint, run.name)
		readyCh <- err
		return err
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(opts.listen, strconv.Itoa(opts.port)))
	if err != nil {
		readyCh <- err
		return err
	}
	defer ln.Close()

	readyCh <- nil // Handshake and listen successful!

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveSOCKS(ctx, c, tn.stack().tnet)
	}
}

func handleSocksStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	serverState.mu.Lock()
	defer serverState.mu.Unlock()
	if serverState.socksActive && serverState.socksCancel != nil {
		serverState.socksCancel()
		serverState.socksActive = false
		serverState.socksCancel = nil
		serverState.broadcast("log", map[string]any{"text": "SOCKS5 proxy stopped.", "type": "warn"})
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "stopped"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "idle"})
}

func handleSubFetch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.URL) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "请提供有效的订阅链接 URL"})
		return
	}

	subURL := strings.TrimSpace(req.URL)
	serverState.broadcast("log", map[string]any{"text": fmt.Sprintf("正在拉取订阅节点: %s ...", subURL), "type": "info"})

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	nodes, err := fetchSubscription(ctx, subURL)
	if err != nil {
		serverState.broadcast("log", map[string]any{"text": "拉取订阅失败: " + err.Error(), "type": "error"})
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	globalSubState.URL = subURL
	globalSubState.UpdateTime = time.Now()
	globalSubState.Nodes = nodes

	serverState.broadcast("log", map[string]any{
		"text": fmt.Sprintf("成功解析订阅！共获取 %d 个节点。", len(nodes)),
		"type": "success",
	})

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "success",
		"total":       len(nodes),
		"nodes":       nodes,
		"update_time": globalSubState.UpdateTime.Format("2006-01-02 15:04:05"),
	})
}

func handleSubList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"url":         globalSubState.URL,
		"update_time": globalSubState.UpdateTime.Format("2006-01-02 15:04:05"),
		"total":       len(globalSubState.Nodes),
		"nodes":       globalSubState.Nodes,
		"active_node": globalSubState.ActiveNode,
	})
}

func handleSubSelect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeID   string `json:"node_id"`
		ProxyURL string `json:"proxy_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.NodeID == "" && req.ProxyURL == "" {
		// 取消当前选中的前置代理
		stopSingBoxNode()
		globalSubState.ActiveNode = nil
		serverState.broadcast("log", map[string]any{"text": "已清除扫描前置代理，将使用直连/跳板模式扫描", "type": "warn"})
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "cleared"})
		return
	}

	var chosen *SubNode
	for _, n := range globalSubState.Nodes {
		if n.ID == req.NodeID {
			copied := n
			chosen = &copied
			break
		}
	}

	if chosen == nil && req.ProxyURL != "" {
		chosen = &SubNode{
			ID:       "custom",
			Name:     "自定义前置代理",
			ProxyURL: req.ProxyURL,
			Country:  detectCountryFromText(req.ProxyURL),
		}
	}

	if chosen != nil {
		serverState.broadcast("log", map[string]any{"text": fmt.Sprintf("正在拉起本地代理核心连接节点 [%s] %s ...", chosen.Country, chosen.Name), "type": "info"})
		localProxy, err := startSingBoxNode(chosen)
		if err != nil {
			serverState.broadcast("log", map[string]any{"text": "启动代理客户端失败: " + err.Error(), "type": "error"})
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "启动代理核心失败: " + err.Error()})
			return
		}
		chosen.ProxyURL = localProxy
		globalSubState.ActiveNode = chosen
		serverState.broadcast("log", map[string]any{
			"text": fmt.Sprintf("代理核心已就绪！出口节点: [%s] %s (本地转发: %s)", chosen.Country, chosen.Name, chosen.ProxyURL),
			"type": "success",
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "selected",
			"node":   chosen,
		})
		return
	}

	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "未找到指定的节点"})
}

