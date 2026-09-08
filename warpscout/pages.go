package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type pagesPayload struct {
	UpdatedAt     string          `json:"updated_at"`
	Timestamp     int64           `json:"timestamp"`
	Protocol      string          `json:"protocol"`
	TotalScanned  int             `json:"total_scanned"`
	WorkingCount  int             `json:"working_count"`
	BestLatencyMs int64           `json:"best_latency_ms"`
	Regions       []regionStat    `json:"regions"`
	Account       pagesAccount    `json:"account"`
	Endpoints     []pagesEndpoint `json:"endpoints"`
}

type pagesAccount struct {
	IPv4          string    `json:"ipv4"`
	IPv6          string    `json:"ipv6"`
	PeerPublicKey string    `json:"peer_public_key"`
	PrivateKey    string    `json:"private_key"`
	AmneziaWG     *pagesAWG `json:"amnezia_wg,omitempty"`
}

type pagesAWG struct {
	Jc   int    `json:"jc"`
	Jmin int    `json:"jmin"`
	Jmax int    `json:"jmax"`
	S1   int    `json:"s1"`
	S2   int    `json:"s2"`
	H1   int    `json:"h1"`
	H2   int    `json:"h2"`
	H3   int    `json:"h3"`
	H4   int    `json:"h4"`
	I1   string `json:"i1,omitempty"`
}

type regionStat struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Flag  string `json:"flag"`
	Count int    `json:"count"`
}

type pagesEndpoint struct {
	ID          int     `json:"id"`
	Endpoint    string  `json:"endpoint"`
	IP          string  `json:"ip"`
	Port        int     `json:"port"`
	Subnet      string  `json:"subnet"`
	TunPingMs   int64   `json:"tun_ping_ms"`
	EpPingMs    int64   `json:"ep_ping_ms"`
	LossPct     float32 `json:"loss_pct"`
	SpeedMbps   float64 `json:"speed_mbps"`
	Country     string  `json:"country"`
	CountryName string  `json:"country_name"`
	Flag        string  `json:"flag"`
	Colo        string  `json:"colo"`
	ColoCity    string  `json:"colo_city"`
	Location    string  `json:"location"`
	Working     bool    `json:"working"`
	Torn        bool    `json:"torn"`
}

func countryChineseName(iso string) string {
	switch strings.ToUpper(strings.TrimSpace(iso)) {
	case "JP":
		return "日本"
	case "HK":
		return "香港"
	case "SG":
		return "新加坡"
	case "TW":
		return "台湾"
	case "KR":
		return "韩国"
	case "US":
		return "美国"
	case "GB", "UK":
		return "英国"
	case "DE":
		return "德国"
	case "FR":
		return "法国"
	case "NL":
		return "荷兰"
	case "CA":
		return "加拿大"
	case "AU":
		return "澳大利亚"
	case "RU":
		return "俄罗斯"
	case "IN":
		return "印度"
	case "MY":
		return "马来西亚"
	case "TH":
		return "泰国"
	case "VN":
		return "越南"
	case "PH":
		return "菲律宾"
	case "ID":
		return "印尼"
	case "CN":
		return "中国"
	default:
		if iso == "" {
			return "未知"
		}
		return iso
	}
}

func checkOrRegisterAccount(ctx context.Context, opts *options) error {
	if _, err := os.Stat(opts.accountPath); err == nil {
		return loadScanAccount(opts.accountPath)
	}

	fallbackPath := filepath.Join("data", filepath.Base(opts.accountPath))
	if _, err := os.Stat(fallbackPath); err == nil {
		opts.accountPath = fallbackPath
		return loadScanAccount(opts.accountPath)
	}

	if !opts.autoRegister {
		return fmt.Errorf("no WARP account at %s: run \"warpscout register\" first", opts.accountPath)
	}

	fmt.Fprintf(os.Stderr, "WARP account not found at %s. Registering automatically...\n", opts.accountPath)
	regOpts := *opts
	regOpts.proto = protoAWG
	regOpts.perSubnet = 2
	regOpts.timeoutSec = 5
	if regOpts.relay == "" {
		regOpts.relay = defaultRelay
	}
	if err := runRegisterCmd(ctx, regOpts); err != nil {
		return fmt.Errorf("automatic account registration failed: %w", err)
	}
	return loadScanAccount(opts.accountPath)
}

func runPagesCmd(ctx context.Context, opts options) error {
	outDir := opts.outDir
	if outDir == "" {
		outDir = "public"
	}
	dataDir := filepath.Join(outDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directories: %w", err)
	}

	if err := checkOrRegisterAccount(ctx, &opts); err != nil {
		return err
	}

	acct, _ := loadAccount(opts.accountPath)

	opts.wantMeta = true
	opts.plain = true
	opts.emoji = true
	showEmoji = true

	run, ips, err := setupScan(opts)
	if err != nil {
		return err
	}

	timeout := time.Duration(opts.timeoutSec) * time.Second
	startTime := time.Now()

	fmt.Fprintf(os.Stderr, "Starting scan for Cloudflare Pages static export...\n")
	fmt.Fprintf(os.Stderr, "Protocol: %s | Sample: %d | Timeout: %v | Parallel: %d\n",
		run.name, opts.perSubnet, timeout, opts.tunnelParallel)

	var emit emitter = func(msg tea.Msg) {
		switch m := msg.(type) {
		case stepMsg:
			if m.done {
				fmt.Fprintf(os.Stderr, "[✓] %s: %s\n", m.label, m.summary)
			} else if m.fail {
				fmt.Fprintf(os.Stderr, "[!] %s: %s\n", m.label, m.summary)
			}
		case barBeginMsg:
			fmt.Fprintf(os.Stderr, ">>> %s (Total: %d)\n", m.label, m.total)
		case foundMsg:
			flag := flagEmoji(m.exit)
			cName := countryChineseName(m.exit)
			fmt.Fprintf(os.Stderr, "  [+] %-21s | Ping: %-6s | TunPing: %-6s | Loss: %-4s | Colo: %-4s (%s %s)\n",
				m.endpoint, latencyStr(m.epPing), latencyStr(m.tunPing), fmt.Sprintf("%.0f%%", m.loss*100), m.colo, flag, cName)
		}
	}

	ph, err := runScan(ctx, opts, run, ips, timeout, emit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Scan encountered errors: %v\n", err)
	}

	if filtered(opts) {
		ph = applyFilters(ph, opts)
	}

	if opts.speed {
		measureSpeed(ctx, ph, timeout, emit)
	}

	// Filter and sort working endpoints
	var workingResults []endpointResult
	for _, r := range ph.results {
		if r.ok {
			workingResults = append(workingResults, r)
		}
	}

	sort.Slice(workingResults, func(i, j int) bool {
		if workingResults[i].durable != workingResults[j].durable {
			return workingResults[i].durable
		}
		if workingResults[i].loss != workingResults[j].loss {
			return workingResults[i].loss < workingResults[j].loss
		}
		if workingResults[i].tunPing != workingResults[j].tunPing {
			return workingResults[i].tunPing < workingResults[j].tunPing
		}
		return workingResults[i].epPing < workingResults[j].epPing
	})

	var bestLatency int64 = 9999
	regionMap := make(map[string]int)
	var endpointItems []pagesEndpoint

	for i, r := range workingResults {
		tunMs := r.tunPing.Milliseconds()
		epMs := r.epPing.Milliseconds()
		if tunMs > 0 && tunMs < bestLatency {
			bestLatency = tunMs
		} else if epMs > 0 && epMs < bestLatency {
			bestLatency = epMs
		}

		cCode := r.exit.coloISO
		if cCode == "" {
			cCode = r.exit.loc
		}
		cCode = strings.ToUpper(strings.TrimSpace(cCode))
		if cCode != "" {
			regionMap[cCode]++
		}

		cName := countryChineseName(cCode)
		flag := flagEmoji(cCode)
		host, portStr, _ := netSplit(r.endpoint)
		port, _ := strconv.Atoi(portStr)

		endpointItems = append(endpointItems, pagesEndpoint{
			ID:          i + 1,
			Endpoint:    r.endpoint,
			IP:          host,
			Port:        port,
			Subnet:      findSubnet(r.ip),
			TunPingMs:   tunMs,
			EpPingMs:    epMs,
			LossPct:     r.loss * 100,
			SpeedMbps:   r.speed,
			Country:     cCode,
			CountryName: cName,
			Flag:        flag,
			Colo:        r.exit.colo,
			ColoCity:    r.exit.coloCity,
			Location:    coloLocation(r.exit),
			Working:     r.durable,
			Torn:        !r.durable,
		})
	}

	if bestLatency == 9999 {
		bestLatency = 0
	}

	// Build regions list
	var regions []regionStat
	for code, count := range regionMap {
		regions = append(regions, regionStat{
			Code:  code,
			Name:  countryChineseName(code),
			Flag:  flagEmoji(code),
			Count: count,
		})
	}
	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Count > regions[j].Count
	})

	// Prepare account parameters
	var awgParams *pagesAWG
	if run.kind == kindAWG {
		awgParams = &pagesAWG{
			Jc:   awgJc,
			Jmin: awgJmin,
			Jmax: awgJmax,
			S1:   0,
			S2:   0,
			H1:   1,
			H2:   2,
			H3:   3,
			H4:   4,
			I1:   awgI1,
		}
	}

	payload := pagesPayload{
		UpdatedAt:     time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		Timestamp:     time.Now().Unix(),
		Protocol:      run.name,
		TotalScanned:  len(ph.results),
		WorkingCount:  len(endpointItems),
		BestLatencyMs: bestLatency,
		Regions:       regions,
		Account: pagesAccount{
			IPv4:          acct.IPv4,
			IPv6:          acct.IPv6,
			PeerPublicKey: acct.PeerPublicKey,
			PrivateKey:    acct.PrivateKey,
			AmneziaWG:     awgParams,
		},
		Endpoints: endpointItems,
	}

	// 1. Write data/results.json
	resultsJSON, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode results.json: %w", err)
	}
	resultsPath := filepath.Join(dataDir, "results.json")
	if err := os.WriteFile(resultsPath, resultsJSON, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", resultsPath, err)
	}
	fmt.Fprintf(os.Stderr, "Generated: %s (%d endpoints)\n", resultsPath, len(endpointItems))

	// 2. Write data/endpoints.txt
	var epLines []string
	for _, ep := range endpointItems {
		epLines = append(epLines, ep.Endpoint)
	}
	epTxtPath := filepath.Join(dataDir, "endpoints.txt")
	_ = os.WriteFile(epTxtPath, []byte(strings.Join(epLines, "\n")+"\n"), 0644)

	// 3. Write data/clash-sub.yaml and data/clash-provider.yaml
	if err := generateClashSubscriptions(dataDir, opts, run, endpointItems); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to generate clash subscription: %v\n", err)
	}

	// 4. Write data/wireguard.conf
	if err := generateWireGuardConfig(dataDir, opts, run, endpointItems); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to generate wireguard.conf: %v\n", err)
	}

	// 5. Write public/index.html (dashboard)
	indexPath := filepath.Join(outDir, "index.html")
	if err := os.WriteFile(indexPath, []byte(embeddedPagesHTML), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", indexPath, err)
	}
	fmt.Fprintf(os.Stderr, "Generated: %s (Modern Cloudflare Pages Dashboard)\n", indexPath)

	fmt.Fprintf(os.Stderr, "\nScan & Export completed in %v! All static files ready in %s\n",
		time.Since(startTime).Round(time.Millisecond), outDir)
	return nil
}

func netSplit(endpoint string) (string, string, error) {
	idx := strings.LastIndex(endpoint, ":")
	if idx == -1 {
		return endpoint, "", fmt.Errorf("no port")
	}
	return endpoint[:idx], endpoint[idx+1:], nil
}

func generateClashSubscriptions(dataDir string, opts options, run protoRun, endpoints []pagesEndpoint) error {
	opts.confType = confTypeMihomo

	var proxyNames []string
	var proxyBlocks []string
	regionProxyMap := make(map[string][]string)

	for _, ep := range endpoints {
		confBytes, err := renderConfFor(opts, ep.Endpoint, run)
		if err != nil {
			continue
		}

		flag := ep.Flag
		if flag == "" {
			flag = "🌐"
		}
		colo := ep.Colo
		if colo == "" {
			colo = "WARP"
		}
		cCode := ep.Country
		if cCode == "" {
			cCode = "ANY"
		}

		pName := fmt.Sprintf("%s [%s-%s] WARP %s", flag, cCode, colo, ep.Endpoint)
		proxyNames = append(proxyNames, pName)
		regionProxyMap[cCode] = append(regionProxyMap[cCode], pName)

		confStr := strings.TrimSpace(string(confBytes))
		lines := strings.Split(confStr, "\n")
		var cleanLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "proxies:") {
				continue
			}
			if strings.Contains(line, "name:") {
				line = fmt.Sprintf("  - name: \"%s\"", pName)
			} else if strings.HasPrefix(line, "- ") {
				line = "  " + line
			}
			cleanLines = append(cleanLines, line)
		}
		proxyBlocks = append(proxyBlocks, strings.Join(cleanLines, "\n"))
	}

	// 1. Provider file (data/clash-provider.yaml)
	var provSb strings.Builder
	provSb.WriteString("# WARPSCOUT Cloudflare Pages Proxy Provider\n")
	provSb.WriteString(fmt.Sprintf("# Updated: %s | Total: %d\n", time.Now().UTC().Format(time.RFC3339), len(endpoints)))
	provSb.WriteString("proxies:\n")
	for _, block := range proxyBlocks {
		provSb.WriteString(block)
		provSb.WriteString("\n")
	}
	_ = os.WriteFile(filepath.Join(dataDir, "clash-provider.yaml"), []byte(provSb.String()), 0644)

	// 2. Full Subscription file (data/clash-sub.yaml)
	var subSb strings.Builder
	subSb.WriteString("# ==========================================================\n")
	subSb.WriteString("# WARPSCOUT Cloudflare Pages Full Subscription\n")
	subSb.WriteString(fmt.Sprintf("# Generated: %s | Working Endpoints: %d\n", time.Now().UTC().Format(time.RFC3339), len(endpoints)))
	subSb.WriteString("# Compatible with: Clash Verge Rev, Clash Nyanpasu, Mihomo, Flclash\n")
	subSb.WriteString("# ==========================================================\n\n")
	subSb.WriteString("port: 7890\n")
	subSb.WriteString("socks-port: 7891\n")
	subSb.WriteString("allow-lan: false\n")
	subSb.WriteString("mode: rule\n")
	subSb.WriteString("log-level: info\n")
	subSb.WriteString("ipv6: true\n\n")
	subSb.WriteString("dns:\n")
	subSb.WriteString("  enable: true\n")
	subSb.WriteString("  listen: 0.0.0.0:1053\n")
	subSb.WriteString("  ipv6: false\n")
	subSb.WriteString("  default-nameserver:\n")
	subSb.WriteString("    - 1.1.1.1\n")
	subSb.WriteString("    - 8.8.8.8\n")
	subSb.WriteString("  nameserver:\n")
	subSb.WriteString("    - https://dns.cloudflare.com/dns-query\n")
	subSb.WriteString("    - https://dns.google/dns-query\n\n")

	subSb.WriteString("proxies:\n")
	for _, block := range proxyBlocks {
		subSb.WriteString(block)
		subSb.WriteString("\n")
	}
	subSb.WriteString("\n")

	// Proxy Groups
	subSb.WriteString("proxy-groups:\n")

	// Main Selector
	subSb.WriteString("  - name: \"🚀 节点选择\"\n")
	subSb.WriteString("    type: select\n")
	subSb.WriteString("    proxies:\n")
	subSb.WriteString("      - \"⚡ 自动优选\"\n")
	for cCode := range regionProxyMap {
		subSb.WriteString(fmt.Sprintf("      - \"%s %s节点\"\n", flagEmoji(cCode), countryChineseName(cCode)))
	}
	for _, name := range proxyNames {
		subSb.WriteString(fmt.Sprintf("      - \"%s\"\n", name))
	}
	subSb.WriteString("      - DIRECT\n\n")

	// Auto Test
	subSb.WriteString("  - name: \"⚡ 自动优选\"\n")
	subSb.WriteString("    type: url-test\n")
	subSb.WriteString("    url: http://www.gstatic.com/generate_204\n")
	subSb.WriteString("    interval: 300\n")
	subSb.WriteString("    tolerance: 50\n")
	subSb.WriteString("    proxies:\n")
	for _, name := range proxyNames {
		subSb.WriteString(fmt.Sprintf("      - \"%s\"\n", name))
	}
	subSb.WriteString("\n")

	// Per-Region Groups
	for cCode, pList := range regionProxyMap {
		subSb.WriteString(fmt.Sprintf("  - name: \"%s %s节点\"\n", flagEmoji(cCode), countryChineseName(cCode)))
		subSb.WriteString("    type: url-test\n")
		subSb.WriteString("    url: http://www.gstatic.com/generate_204\n")
		subSb.WriteString("    interval: 300\n")
		subSb.WriteString("    tolerance: 50\n")
		subSb.WriteString("    proxies:\n")
		for _, name := range pList {
			subSb.WriteString(fmt.Sprintf("      - \"%s\"\n", name))
		}
		subSb.WriteString("\n")
	}

	// Rules
	subSb.WriteString("rules:\n")
	subSb.WriteString("  - GEOIP,lan,DIRECT,no-resolve\n")
	subSb.WriteString("  - MATCH,🚀 节点选择\n")

	return os.WriteFile(filepath.Join(dataDir, "clash-sub.yaml"), []byte(subSb.String()), 0644)
}

func generateWireGuardConfig(dataDir string, opts options, run protoRun, endpoints []pagesEndpoint) error {
	opts.confType = confTypeNative

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### WARPSCOUT 优选端点 WireGuard 配置 ###\n"))
	sb.WriteString(fmt.Sprintf("### 生成时间: %s | 端点总数: %d ###\n\n", time.Now().UTC().Format(time.RFC3339), len(endpoints)))

	limit := len(endpoints)
	if limit > 20 {
		limit = 20 // top 20 for native single conf file
	}

	for i := 0; i < limit; i++ {
		ep := endpoints[i]
		sb.WriteString(fmt.Sprintf("### -----------------------------------------------------\n"))
		sb.WriteString(fmt.Sprintf("### [%d] %s | %s %s (%s) | 延迟: %dms | 丢包: %.0f%%\n",
			ep.ID, ep.Endpoint, ep.Flag, ep.CountryName, ep.Colo, ep.TunPingMs, ep.LossPct))
		sb.WriteString(fmt.Sprintf("### -----------------------------------------------------\n"))
		confBytes, err := renderConfFor(opts, ep.Endpoint, run)
		if err == nil {
			sb.Write(confBytes)
		}
		sb.WriteString("\n\n")
	}

	return os.WriteFile(filepath.Join(dataDir, "wireguard.conf"), []byte(sb.String()), 0644)
}
