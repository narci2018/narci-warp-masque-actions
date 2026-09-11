package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"net/url"
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

// multiAccountPool holds independently registered WARP accounts. Each account
// has its own Cloudflare identity and therefore gets a different egress IP.
// The primary account (from warpscout-account.json) is always index 0.
var (
	accountPoolMu sync.Mutex
	accountPool   []account // loaded or registered accounts
)

func getPoolAccount(idx int) account {
	accountPoolMu.Lock()
	defer accountPoolMu.Unlock()
	if len(accountPool) == 0 {
		var a account
		a.PrivateKey = warpPrivateKey
		a.PeerPublicKey = warpPublicKey
		a.IPv4 = warpAddress
		a.IPv6 = warpAddressV6
		if masqueAcct != nil {
			a.Masque = masqueAcct
		}
		return a
	}
	return accountPool[idx%len(accountPool)]
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
	_ = loadScanAccount(opts.accountPath)
	initAccountManager("data")
	initSubscriptionManager("data")

	// Initialize account pool with the primary account
	if a, err := loadAccount(opts.accountPath); err == nil {
		accountPoolMu.Lock()
		accountPool = []account{a}
		// Load any extra accounts from pool files
		for i := 1; i <= 20; i++ {
			poolPath := fmt.Sprintf("%s.pool%d", opts.accountPath, i)
			if pa, perr := loadAccount(poolPath); perr == nil {
				accountPool = append(accountPool, pa)
			} else {
				break
			}
		}
		accountPoolMu.Unlock()
	}

	// Auto-expand account pool in background to at least 5 accounts if needed
	go func() {
		time.Sleep(3 * time.Second)
		accountPoolMu.Lock()
		curCount := len(accountPool)
		accountPoolMu.Unlock()
		if curCount < 5 {
			fmt.Printf("[AccountPool] 当前独立账号数(%d)较少，正在后台自动补充注册至 5 个独立 WARP 账户(保障出口IP多样化)...\n", curCount)
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
			_ = applyRelay(&regOpts)
			_, ips, _ := setupScan(regOpts)
			timeout := time.Duration(regOpts.timeoutSec) * time.Second
			for curCount < 5 {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				a, err := obtainAccount(ctx, regOpts, ips, timeout, account{})
				cancel()
				if err != nil {
					fmt.Printf("[AccountPool] 自动注册新账号失败: %v，稍后重试\n", err)
					break
				}
				accountPoolMu.Lock()
				poolIdx := len(accountPool)
				accountPool = append(accountPool, a)
				curCount = len(accountPool)
				accountPoolMu.Unlock()

				poolPath := fmt.Sprintf("%s.pool%d", opts.accountPath, poolIdx)
				_ = saveAccount(poolPath, a)
				fmt.Printf("[AccountPool] 成功自动扩容第 %d 个独立 WARP 账号 (IPv4: %s)\n", poolIdx+1, a.IPv4)
				time.Sleep(1 * time.Second)
			}
		}
	}()

	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/status", handleStatus(opts))
	mux.HandleFunc("/api/account", handleAccount(opts))
	mux.HandleFunc("/api/account/register", handleRegister(opts))
	mux.HandleFunc("/api/account/pool", handleAccountPool(opts))
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
	mux.HandleFunc("/api/sub/warp-on-warp", handleSubWarpOnWarp(opts))
	mux.HandleFunc("/api/sub/dialer-warp", handleSubDialerWarp(opts))
	mux.HandleFunc("/api/sub/clash-local", handleSubClashLocal(opts))
	mux.HandleFunc("/api/sub/singbox-local", handleSubSingboxLocal(opts))
	mux.HandleFunc("/api/matrix/nodes", handleMatrixNodes)

	// WARP 账号管理 API
	mux.HandleFunc("/api/accounts", handleAccounts)
	mux.HandleFunc("/api/accounts/create", handleAccountCreate)
	mux.HandleFunc("/api/accounts/update", handleAccountUpdate)
	mux.HandleFunc("/api/accounts/delete", handleAccountDelete)
	mux.HandleFunc("/api/accounts/bind", handleAccountBind)
	mux.HandleFunc("/api/accounts/schedule", handleAccountSchedule)

	// 多订阅底座管理 API
	mux.HandleFunc("/api/subs/sources", handleSubsSources)
	mux.HandleFunc("/api/subs/sources/delete", handleSubsSourcesDelete)
	mux.HandleFunc("/api/subs/sources/update", handleSubsSourcesUpdate)
	mux.HandleFunc("/api/subs/sources/refresh", handleSubsSourcesRefresh)
	mux.HandleFunc("/api/subs/nodes", handleSubsNodes)
	mux.HandleFunc("/api/subs/nodes/ping", handleSubsNodesPing)
	mux.HandleFunc("/api/subs/nodes/speedtest", handleSubsNodesSpeedtest)

	// 纯净多国 WARP API
	mux.HandleFunc("/api/pure/status", handlePureStatus)
	mux.HandleFunc("/api/pure/start", handlePureStart)
	mux.HandleFunc("/api/pure/stop", handlePureStop)
	mux.HandleFunc("/api/pure/countries", handlePureCountries)
	mux.HandleFunc("/api/pure/clash", handlePureClash)
	mux.HandleFunc("/api/pure/singbox", handlePureSingbox)
	mux.HandleFunc("/api/pure/v2rayn", handlePureV2rayN)
	mux.HandleFunc("/api/pure/ping", handlePurePing)
	mux.HandleFunc("/api/pure/speedtest", handlePureSpeedtest)
	mux.HandleFunc("/api/pure/refresh", handlePureRefresh)

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
	// 自动启动纯净 WARP 多国出海引擎 (默认法国 FR 出口)
	go func() {
		time.Sleep(1 * time.Second)
		fmt.Println("[PureWARP] 正在自动拉起纯净出海引擎 (默认法国 FR 出口)...")
		_ = globalPureMgr.start("FR")
	}()

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
		accountPoolMu.Lock()
		poolSize := len(accountPool)
		accountPoolMu.Unlock()
		resp["account_pool_size"] = poolSize

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

// handleAccountPool batch-registers additional independent WARP accounts.
// Each account gets a different Cloudflare egress IP.
// GET: returns current pool status
// POST {"count": N}: registers N additional accounts (max 10 total)
func handleAccountPool(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
			accountPoolMu.Lock()
			poolSize := len(accountPool)
			var poolInfo []map[string]string
			for i, a := range accountPool {
				label := fmt.Sprintf("账户 #%d", i+1)
				if i == 0 {
					label += " (主账户)"
				}
				poolInfo = append(poolInfo, map[string]string{
					"label": label,
					"ipv4":  a.IPv4,
					"id":    a.ID,
				})
			}
			accountPoolMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pool_size": poolSize,
				"accounts":  poolInfo,
			})
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Count int    `json:"count"`
			Proxy string `json:"proxy"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Count <= 0 {
			req.Count = 5
		}

		accountPoolMu.Lock()
		currentSize := len(accountPool)
		accountPoolMu.Unlock()

		maxTotal := 10
		needed := maxTotal - currentSize
		if needed <= 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "full",
				"message": fmt.Sprintf("账户池已满 (%d 个)，无需追加注册", currentSize),
				"pool_size": currentSize,
			})
			return
		}
		if req.Count > needed {
			req.Count = needed
		}

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
			p := req.Proxy
			if strings.Contains(p, "127.0.0.1") {
				p = strings.ReplaceAll(p, "127.0.0.1", "host.docker.internal")
			} else if strings.Contains(p, "localhost") {
				p = strings.ReplaceAll(p, "localhost", "host.docker.internal")
			}
			regOpts.proxy = p
		}

		_ = applyRelay(&regOpts)
		_, ips, _ := setupScan(regOpts)
		timeout := time.Duration(regOpts.timeoutSec) * time.Second

		serverState.broadcast("log", map[string]any{
			"text": fmt.Sprintf("开始批量注册 %d 个独立 WARP 账户（用于不同出口 IP）...", req.Count),
			"type": "info",
		})

		registered := 0
		for i := 0; i < req.Count; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			a, err := obtainAccount(ctx, regOpts, ips, timeout, account{})
			cancel()
			if err != nil {
				serverState.broadcast("log", map[string]any{
					"text": fmt.Sprintf("账户 #%d 注册失败: %v", currentSize+i+1, err),
					"type": "error",
				})
				continue
			}

			// Save to pool file
			accountPoolMu.Lock()
			poolIdx := len(accountPool)
			accountPool = append(accountPool, a)
			accountPoolMu.Unlock()

			poolPath := fmt.Sprintf("%s.pool%d", opts.accountPath, poolIdx)
			_ = saveAccount(poolPath, a)

			registered++
			serverState.broadcast("log", map[string]any{
				"text": fmt.Sprintf("账户 #%d 注册成功! IPv4: %s", poolIdx+1, a.IPv4),
				"type": "success",
			})
		}

		accountPoolMu.Lock()
		finalSize := len(accountPool)
		accountPoolMu.Unlock()

		serverState.broadcast("log", map[string]any{
			"text": fmt.Sprintf("批量注册完成: 成功 %d 个，账户池共 %d 个独立账户", registered, finalSize),
			"type": "success",
		})

		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     "done",
			"registered": registered,
			"pool_size":  finalSize,
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
			TargetCount    int    `json:"target_count"`
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
		scanOpts.countries = splitList(req.Country, "-country")
		scanOpts.colos = splitList(req.Node, "-node")
		scanOpts.dropCountries = splitList(req.ExcludeCountry, "-exclude-country")
		scanOpts.dropColos = splitList(req.ExcludeNode, "-exclude-node")
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

		if !scanOpts.full && req.Target == "" {
			targetCount := req.TargetCount
			if targetCount <= 0 {
				targetCount = 100
			}
			numPools := len(pools)
			if numPools <= 0 {
				numPools = 14
			}
			perSubnet := (targetCount + numPools - 1) / numPools
			if perSubnet < 1 {
				perSubnet = 1
			}
			scanOpts.perSubnet = perSubnet
			ips = expandPools(perSubnet)
			rand.Shuffle(len(ips), func(i, j int) { ips[i], ips[j] = ips[j], ips[i] })
			if len(ips) > targetCount {
				ips = ips[:targetCount]
			}
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
			if len(opts.countries) > 0 {
				match := false
				for _, c := range opts.countries {
					if strings.EqualFold(c, m.exit) {
						match = true
						break
					}
				}
				if !match {
					return
				}
			}
			if len(opts.dropCountries) > 0 {
				drop := false
				for _, c := range opts.dropCountries {
					if strings.EqualFold(c, m.exit) {
						drop = true
						break
					}
				}
				if drop {
					return
				}
			}
			if len(opts.colos) > 0 {
				match := false
				for _, c := range opts.colos {
					if strings.EqualFold(c, m.colo) {
						match = true
						break
					}
				}
				if !match {
					return
				}
			}
			if len(opts.dropColos) > 0 {
				drop := false
				for _, c := range opts.dropColos {
					if strings.EqualFold(c, m.colo) {
						drop = true
						break
					}
				}
				if drop {
					return
				}
			}
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
			// 合并为包含完整规则与分组的 Clash / Mihomo 订阅文件
			sb.WriteString("# Generated by WARPSCOUT WebUI Batch Export\n")
			sb.WriteString("port: 7890\n")
			sb.WriteString("socks-port: 7891\n")
			sb.WriteString("allow-lan: true\n")
			sb.WriteString("mode: rule\n")
			sb.WriteString("log-level: info\n")
			sb.WriteString("ipv6: false\n")
			sb.WriteString("external-controller: 127.0.0.1:9090\n\n")

			// 国内极速抗污染 DNS 配置 (彻底消除 1.1.1.1 带来的超时与测速挂起)
			sb.WriteString("dns:\n")
			sb.WriteString("  enable: true\n")
			sb.WriteString("  ipv6: false\n")
			sb.WriteString("  default-nameserver:\n")
			sb.WriteString("    - 223.5.5.5\n")
			sb.WriteString("    - 119.29.29.29\n")
			sb.WriteString("    - 114.114.114.114\n")
			sb.WriteString("  enhanced-mode: fake-ip\n")
			sb.WriteString("  fake-ip-range: 198.18.0.1/16\n")
			sb.WriteString("  use-hosts: true\n")
			sb.WriteString("  nameserver:\n")
			sb.WriteString("    - https://sm2.doh.pub/dns-query\n")
			sb.WriteString("    - https://dns.alidns.com/dns-query\n")
			sb.WriteString("    - 223.5.5.5\n")
			sb.WriteString("    - 119.29.29.29\n")
			sb.WriteString("  fallback:\n")
			sb.WriteString("    - 1.1.1.1\n")
			sb.WriteString("    - 8.8.8.8\n\n")

			sb.WriteString("proxies:\n")

			var allNodeNames []string
			var usNodeNames []string
			var masqueNodeNames []string
			var wgNodeNames []string
			var dialerProxyName string // 前置代理节点名，非空表示通过代理转发

			// 检查是否有订阅底座已选的前置代理节点
			activeProxy := globalSubState.ActiveNode
			if activeProxy != nil && activeProxy.ProxyURL != "" {
				// 解析代理 URL 获取类型/地址/端口
				pu, perr := url.Parse(activeProxy.ProxyURL)
				if perr == nil {
					proxyType := "socks5"
					if pu.Scheme == "http" || pu.Scheme == "https" {
						proxyType = "http"
					}
					proxyHost := pu.Hostname()
					proxyPort := pu.Port()
					if proxyPort == "" {
						if proxyType == "http" {
							proxyPort = "80"
						} else {
							proxyPort = "1080"
						}
					}
					// 如果是容器内地址 127.0.0.1，需要替换为 host.docker.internal
					// 但 Clash 运行在宿主机上，所以保持 127.0.0.1 即可
					// sing-box 监听在 0.0.0.0:29891，端口已映射到宿主机
					country := strings.ToUpper(activeProxy.Country)
					if country == "" {
						country = "XX"
					}
					dialerProxyName = fmt.Sprintf("🔗 [%s] 前置代理 %s", country, activeProxy.Name)
					pPort, _ := strconv.Atoi(proxyPort)
					proxyEntry := []kv{
						{"name", quoted(dialerProxyName)},
						{"type", proxyType},
						{"server", proxyHost},
						{"port", pPort},
					}
					// 如果有用户名密码
					if pu.User != nil {
						proxyEntry = append(proxyEntry, kv{"username", pu.User.Username()})
						if pw, ok := pu.User.Password(); ok {
							proxyEntry = append(proxyEntry, kv{"password", pw})
						}
					}
					var b strings.Builder
					writeYAML(&b, proxyEntry, "  ", "- ")
					sb.WriteString(b.String())
					allNodeNames = append(allNodeNames, dialerProxyName)
				}
			}

			// 确保账户已载入
			if masqueAcct == nil {
				if opts.accountPath != "" {
					_ = loadScanAccount(opts.accountPath)
				}
				if masqueAcct == nil && baseOpts.accountPath != "" {
					_ = loadScanAccount(baseOpts.accountPath)
				}
			}

			// 1. 🌐 MASQUE 官方防封节点群
			if masqueAcct != nil {
				// 1.1 MASQUE-H2 (TCP / HTTPS)
				masqueRunH2 := protoRun{kindMASQUEH2, protoMASQUEH2}
				h2Endpoints := []struct {
					host string
					port int
				}{
					{"162.159.199.188", 443},
					{"162.159.199.188", 8443},
					{"162.159.199.188", 2053},
					{"162.159.199.188", 2083},
					{"162.159.199.188", 2087},
					{"162.159.199.188", 2096},
					{"162.159.199.188", 8095},
					{"162.159.199.188", 1701},
					{"162.159.199.1", 443},
					{"162.159.199.1", 8443},
					{"162.159.199.1", 2053},
					{"162.159.199.1", 2083},
					{"162.159.199.1", 8095},
					{"162.159.199.2", 443},
					{"162.159.199.2", 8443},
					{"162.159.199.2", 2053},
					{"162.159.199.2", 2083},
					{"162.159.199.2", 8095},
					{"162.159.199.3", 443},
					{"162.159.199.3", 8443},
					{"162.159.198.1", 443},
					{"162.159.198.2", 443},
				}
				for i, item := range h2Endpoints {
					ep := fmt.Sprintf("%s:%d", item.host, item.port)
					acct := getPoolAccount(i)
					var nodeName string
					if dialerProxyName != "" {
						nodeName = fmt.Sprintf("🌐 [US-MASQUE] TCP %02d (%s)", i+1, ep)
					} else {
						nodeName = fmt.Sprintf("🌐 [MASQUE] TCP %02d (%s)", i+1, ep)
					}
					var mP []kv
					var err error
					if acct.Masque != nil && acct.Masque.PrivateKey != "" {
						mP, err = mihomoProxyWithAccount(opts, nodeName, ep, masqueRunH2, 0, nil, acct)
					} else {
						mP, err = mihomoProxy(opts, nodeName, ep, masqueRunH2, 0, nil)
					}
					if err != nil {
						continue
					}
					if dialerProxyName != "" {
						mP = append(mP, kv{"dialer-proxy", dialerProxyName})
						usNodeNames = append(usNodeNames, nodeName)
					}
					var b strings.Builder
					writeYAML(&b, mP, "  ", "- ")
					sb.WriteString(b.String())
					masqueNodeNames = append(masqueNodeNames, nodeName)
					allNodeNames = append(allNodeNames, nodeName)
				}

				// 1.2 MASQUE-H3 (QUIC)
				masqueRunH3 := protoRun{kindMASQUE, protoMASQUE}
				h3Endpoints := []struct {
					host string
					port int
				}{
					{"162.159.198.1", 443},
					{"162.159.198.1", 8443},
					{"162.159.198.1", 2053},
					{"162.159.198.1", 2083},
					{"162.159.198.1", 500},
					{"162.159.198.1", 1701},
					{"162.159.198.1", 4500},
					{"162.159.198.1", 8095},
					{"162.159.198.2", 443},
					{"162.159.198.2", 8443},
					{"162.159.198.2", 2053},
					{"162.159.198.2", 2083},
					{"162.159.198.2", 500},
					{"162.159.198.2", 1701},
					{"162.159.198.2", 4500},
					{"162.159.198.2", 8095},
				}
				for i, item := range h3Endpoints {
					ep := fmt.Sprintf("%s:%d", item.host, item.port)
					acct := getPoolAccount(i + len(h2Endpoints))
					var nodeName string
					if dialerProxyName != "" {
						nodeName = fmt.Sprintf("🛡️ [US-MASQUE] QUIC %02d (%s)", i+1, ep)
					} else {
						nodeName = fmt.Sprintf("🛡️ [MASQUE] QUIC %02d (%s)", i+1, ep)
					}
					var mP []kv
					var err error
					if acct.Masque != nil && acct.Masque.PrivateKey != "" {
						mP, err = mihomoProxyWithAccount(opts, nodeName, ep, masqueRunH3, 0, nil, acct)
					} else {
						mP, err = mihomoProxy(opts, nodeName, ep, masqueRunH3, 0, nil)
					}
					if err != nil {
						continue
					}
					if dialerProxyName != "" {
						mP = append(mP, kv{"dialer-proxy", dialerProxyName})
						usNodeNames = append(usNodeNames, nodeName)
					}
					var b strings.Builder
					writeYAML(&b, mP, "  ", "- ")
					sb.WriteString(b.String())
					masqueNodeNames = append(masqueNodeNames, nodeName)
					allNodeNames = append(allNodeNames, nodeName)
				}
			}

			// 2. ⚡ WireGuard / AWG 优选直连节点
			for i, ep := range req.Endpoints {
				acct := getPoolAccount(i + len(masqueNodeNames))
				var nodeName string
				if dialerProxyName != "" {
					// 通过代理出口，标注美国
					nodeName = fmt.Sprintf("🇺🇸 [US-WG] 代理转发 %02d (%s)", i+1, ep)
				} else {
					// 直连中国出口，诚实标注
					nodeName = fmt.Sprintf("⚡ [CN-WG] 直连 %02d (%s)", i+1, ep)
				}
				var dP []kv
				var err error
				if acct.PrivateKey != "" && acct.PeerPublicKey != "" {
					dP, err = mihomoProxyWithAccount(opts, nodeName, ep, run, opts.mtu, nil, acct)
				} else {
					dP, err = mihomoProxy(opts, nodeName, ep, run, opts.mtu, nil)
				}
				if err == nil {
					if dialerProxyName != "" {
						dP = append(dP, kv{"dialer-proxy", dialerProxyName})
						usNodeNames = append(usNodeNames, nodeName)
					}
					var b strings.Builder
					writeYAML(&b, dP, "  ", "- ")
					sb.WriteString(b.String())
					wgNodeNames = append(wgNodeNames, nodeName)
					allNodeNames = append(allNodeNames, nodeName)
				}
			}

			sb.WriteString("\nproxy-groups:\n")

			// 1. 🚀 手动选择 (客户端置顶主入口)
			sb.WriteString("  - name: \"🚀 手动选择\"\n")
			sb.WriteString("    type: select\n")
			sb.WriteString("    proxies:\n")
			if len(usNodeNames) > 0 {
				sb.WriteString("      - \"🇺🇸 美国出口优选\"\n")
			}
			if len(masqueNodeNames) > 0 {
				sb.WriteString("      - \"🌐 MASQUE 防封优选\"\n")
			}
			if len(wgNodeNames) > 0 {
				sb.WriteString("      - \"⚡ WireGuard 优选\"\n")
			}
			sb.WriteString("      - \"♻️ 全协议自动优选\"\n")
			for _, n := range allNodeNames {
				sb.WriteString(fmt.Sprintf("      - %q\n", n))
			}

			// 2. 🇺🇸 美国出口优选
			if len(usNodeNames) > 0 {
				sb.WriteString("  - name: \"🇺🇸 美国出口优选\"\n")
				sb.WriteString("    type: url-test\n")
				sb.WriteString("    url: http://www.gstatic.com/generate_204\n")
				sb.WriteString("    interval: 300\n")
				sb.WriteString("    tolerance: 50\n")
				sb.WriteString("    proxies:\n")
				for _, n := range usNodeNames {
					sb.WriteString(fmt.Sprintf("      - %q\n", n))
				}
			}

			// 3. 🌐 MASQUE 防封优选
			if len(masqueNodeNames) > 0 {
				sb.WriteString("  - name: \"🌐 MASQUE 防封优选\"\n")
				sb.WriteString("    type: url-test\n")
				sb.WriteString("    url: http://www.gstatic.com/generate_204\n")
				sb.WriteString("    interval: 300\n")
				sb.WriteString("    tolerance: 50\n")
				sb.WriteString("    proxies:\n")
				for _, n := range masqueNodeNames {
					sb.WriteString(fmt.Sprintf("      - %q\n", n))
				}
			}

			// 4. ⚡ WireGuard 优选
			if len(wgNodeNames) > 0 {
				sb.WriteString("  - name: \"⚡ WireGuard 优选\"\n")
				sb.WriteString("    type: url-test\n")
				sb.WriteString("    url: http://www.gstatic.com/generate_204\n")
				sb.WriteString("    interval: 300\n")
				sb.WriteString("    tolerance: 50\n")
				sb.WriteString("    proxies:\n")
				for _, n := range wgNodeNames {
					sb.WriteString(fmt.Sprintf("      - %q\n", n))
				}
			}

			// 5. ♻️ 全协议自动优选
			sb.WriteString("  - name: \"♻️ 全协议自动优选\"\n")
			sb.WriteString("    type: url-test\n")
			sb.WriteString("    url: http://www.gstatic.com/generate_204\n")
			sb.WriteString("    interval: 300\n")
			sb.WriteString("    tolerance: 50\n")
			sb.WriteString("    proxies:\n")
			for _, n := range allNodeNames {
				sb.WriteString(fmt.Sprintf("      - %q\n", n))
			}

			sb.WriteString("\nrules:\n")
			sb.WriteString("  - MATCH,🚀 手动选择\n")

			w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment; filename=\"warpscout-clash.yml\"")
			_, _ = w.Write([]byte(sb.String()))
			return
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
			serverState.socksCancel = nil
			serverState.mu.Unlock()
			time.Sleep(500 * time.Millisecond)
			serverState.mu.Lock()
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
		if opts.through == "" {
			opts.through = "8.39.204.2:1701"
		}
		opts.innerProto = req.InnerProto
		if opts.innerProto == "" {
			opts.innerProto = protoWG
		}
		opts.port = serverState.socksPort
		opts.listen = serverState.socksListen

		runProto := opts.innerProto
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
	timeout := 10 * time.Second

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

func handleSubWarpOnWarp(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format := strings.ToLower(r.URL.Query().Get("format"))
		if format == "singbox" || format == "sing-box" {
			paths := []string{"/data/warpscout-75nodes-singbox.json", "warpscout-75nodes-singbox.json", "public/data/warpscout-75nodes-singbox.json"}
			for _, p := range paths {
				if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
					_, _ = w.Write(b)
					return
				}
			}
			conf, err := GenerateWarpOnWarpSingBox(opts)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
			_, _ = w.Write([]byte(conf))
			return
		}

		paths := []string{"/data/warpscout-75nodes-clash.yaml", "warpscout-75nodes-clash.yaml", "public/data/warpscout-75nodes-clash.yaml"}
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
				w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
				w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
				_, _ = w.Write(b)
				return
			}
		}

		conf, err := GenerateWarpOnWarpClash(opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
		_, _ = w.Write([]byte(conf))
	}
}

func handleSubDialerWarp(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format := strings.ToLower(r.URL.Query().Get("format"))
		if format == "singbox" || format == "sing-box" {
			paths := []string{"/data/warpscout-75nodes-singbox.json", "warpscout-75nodes-singbox.json", "public/data/warpscout-75nodes-singbox.json"}
			for _, p := range paths {
				if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
					_, _ = w.Write(b)
					return
				}
			}
			conf, err := GenerateWarpOnWarpSingBox(opts)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
			_, _ = w.Write([]byte(conf))
			return
		}

		conf, err := GenerateDialerWarpClash(opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
		_, _ = w.Write([]byte(conf))
	}
}

func handleSubClashLocal(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paths := []string{"/data/warpscout-75nodes-clash.yaml", "warpscout-75nodes-clash.yaml", "public/data/warpscout-75nodes-clash.yaml"}
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
				w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
				w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
				_, _ = w.Write(b)
				return
			}
		}

		serverState.mu.RLock()
		port := serverState.socksPort
		serverState.mu.RUnlock()
		if port <= 0 {
			port = 29881
		}
		conf := GenerateLocalGatewayClash(port)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
		_, _ = w.Write([]byte(conf))
	}
}

func handleSubSingboxLocal(opts options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paths := []string{"/data/warpscout-75nodes-singbox.json", "warpscout-75nodes-singbox.json", "public/data/warpscout-75nodes-singbox.json"}
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824000; expire=0")
				_, _ = w.Write(b)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "Sing-box subscription file not found"})
	}
}

func handleMatrixNodes(w http.ResponseWriter, r *http.Request) {
	paths := []string{"/data/matrix_nodes.json", "matrix_nodes.json", "public/data/matrix_nodes.json"}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte("[]"))
}

func handleGatewayTest(w http.ResponseWriter, r *http.Request) {
	serverState.mu.RLock()
	port := serverState.socksPort
	active := serverState.socksActive
	serverState.mu.RUnlock()

	if !active || port <= 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "本地出海网关尚未启动"})
		return
	}

	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 6 * time.Second,
	}

	start := time.Now()
	resp, err := client.Get("http://ip-api.com/json")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "网关连接测试失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "解析响应失败"})
		return
	}

	info["latency_ms"] = time.Since(start).Milliseconds()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(info)
}





// -----------------------------------------------------------------------
// Account Management API Handlers
// -----------------------------------------------------------------------

func handleAccounts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if globalAccountMgr == nil {
		initAccountManager("data")
	}

	if r.Method == http.MethodGet {
		accounts := globalAccountMgr.List()
		sched := globalAccountMgr.GetSchedulerStatus()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"accounts":  accounts,
			"scheduler": sched,
		})
		return
	}

	if r.Method == http.MethodPost {
		var req WarpAccount
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.PrivateKey == "" {
			http.Error(w, "私钥不能为空", http.StatusBadRequest)
			return
		}
		if req.PeerPublicKey == "" {
			req.PeerPublicKey = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="
		}
		if req.IPv4 == "" {
			req.IPv4 = "172.16.0.2"
		}
		if err := globalAccountMgr.Add(req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "账号已添加"})
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handleAccountCreate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalAccountMgr == nil {
		initAccountManager("data")
	}
	var req struct {
		Country  string `json:"country"`
		ProxyURL string `json:"proxy_url"`
		ViaNote  string `json:"via_note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()

	var acc *WarpAccount
	var err error
	if req.Country != "" && req.Country != "direct" {
		acc, err = globalAccountMgr.RegisterWARPViaCountry(ctx, req.Country)
	} else if req.ProxyURL != "" {
		acc, err = globalAccountMgr.RegisterFreshWARP(ctx, req.ProxyURL, req.ViaNote)
	} else if globalSubMgr != nil && globalSubMgr.GetActiveNode() != nil {
		acc, err = globalAccountMgr.RegisterWARPViaCountry(ctx, globalSubMgr.GetActiveNode().Country)
	} else {
		acc, err = globalAccountMgr.RegisterFreshWARP(ctx, "", req.ViaNote)
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"account": acc,
		"message": "成功注册新 WARP 独立账号！",
	})
}

func handleAccountUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalAccountMgr == nil {
		initAccountManager("data")
	}
	var req WarpAccount
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := globalAccountMgr.Update(req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "账号已更新"})
}

func handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalAccountMgr == nil {
		initAccountManager("data")
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := globalAccountMgr.Delete(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "账号已删除"})
}

func handleAccountBind(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalAccountMgr == nil {
		initAccountManager("data")
	}
	var req struct {
		AccountID string `json:"account_id"`
		NodeID    string `json:"node_id"`
		NodeName  string `json:"node_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := globalAccountMgr.BindNode(req.AccountID, req.NodeID, req.NodeName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "绑定成功"})
}

func handleAccountSchedule(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if globalAccountMgr == nil {
		initAccountManager("data")
	}
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(globalAccountMgr.GetSchedulerStatus())
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Action      string `json:"action"` // "start", "stop"
			IntervalMin int    `json:"interval_min"`
			TargetCount int    `json:"target_count"`
			RelayMode   string `json:"relay_mode"`
			CustomProxy string `json:"custom_proxy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Action == "stop" {
			globalAccountMgr.StopScheduler()
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "定时任务已停止"})
			return
		}
		if err := globalAccountMgr.StartScheduler(req.IntervalMin, req.TargetCount, req.RelayMode, req.CustomProxy); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "定时注册任务已启动"})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// -----------------------------------------------------------------------
// Multi-Subscription API Handlers
// -----------------------------------------------------------------------

func handleSubsSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if globalSubMgr == nil {
		initSubscriptionManager("data")
	}
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"sources": globalSubMgr.GetSources(),
			"total":   len(globalSubMgr.GetAllNodes()),
		})
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.URL) == "" {
			http.Error(w, "请提供订阅 URL", http.StatusBadRequest)
			return
		}
		src, err := globalSubMgr.AddSource(req.Name, req.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"source":  src,
			"message": "订阅源添加成功，节点已更新",
		})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func handleSubsSourcesDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalSubMgr == nil {
		initSubscriptionManager("data")
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := globalSubMgr.DeleteSource(req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "订阅源已删除"})
}

func handleSubsSourcesUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalSubMgr == nil {
		initSubscriptionManager("data")
	}
	var req SubscriptionSource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := globalSubMgr.UpdateSource(req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "订阅源已更新"})
}

func handleSubsSourcesRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalSubMgr == nil {
		initSubscriptionManager("data")
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	if req.ID != "" {
		if err := globalSubMgr.RefreshSource(ctx, req.ID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
	} else {
		if err := globalSubMgr.RefreshAll(ctx); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"sources": globalSubMgr.GetSources(),
		"nodes":   globalSubMgr.GetAllNodes(),
		"message": "订阅节点已全部刷新！",
	})
}

func handleSubsNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if globalSubMgr == nil {
		initSubscriptionManager("data")
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"nodes":       globalSubMgr.GetAllNodes(),
		"active_node": globalSubMgr.GetActiveNode(),
	})
}


func handleSubsNodesPing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalSubMgr == nil {
		initSubscriptionManager("data")
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.ID == "" || req.ID == "all" {
		results := globalSubMgr.TestAllNodesLatency()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"results": results,
			"nodes":   globalSubMgr.GetAllNodes(),
			"message": fmt.Sprintf("已完成全部 %d 个中继节点的延迟测活！", len(results)),
		})
		return
	}

	lat, err := globalSubMgr.TestNodeLatency(req.ID)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     "error",
			"id":         req.ID,
			"latency_ms": 0,
			"error":      err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"id":         req.ID,
		"latency_ms": lat,
		"message":    fmt.Sprintf("延迟测试完成: %d ms", lat),
	})
}

func handleSubsNodesSpeedtest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if globalSubMgr == nil {
		initSubscriptionManager("data")
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.ID == "" {
		http.Error(w, "missing node id", http.StatusBadRequest)
		return
	}

	speed, err := globalSubMgr.TestNodeSpeed(req.ID)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "error",
			"id":     req.ID,
			"error":  err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"id":         req.ID,
		"speed_mbps": speed,
		"message":    fmt.Sprintf("测速完成: %.1f Mbps", speed),
	})
}
