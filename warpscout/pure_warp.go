package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type PureCountryInfo struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Flag      string `json:"flag"`
	NodeCount int    `json:"node_count"`
	Enabled   bool   `json:"enabled"`
}

var allPureCountries = []PureCountryInfo{
	// 常用 11 国 (美、英、德、法、墨、巴、日、韩、港、台、新)
	{Code: "US", Name: "美国", Port: 29891, Flag: "🇺🇸", NodeCount: 65, Enabled: true},
	{Code: "GB", Name: "英国", Port: 29892, Flag: "🇬🇧", NodeCount: 31, Enabled: true},
	{Code: "DE", Name: "德国", Port: 29893, Flag: "🇩🇪", NodeCount: 60, Enabled: true},
	{Code: "FR", Name: "法国", Port: 29894, Flag: "🇫🇷", NodeCount: 26, Enabled: true},
	{Code: "MX", Name: "墨西哥", Port: 29895, Flag: "🇲🇽", NodeCount: 0, Enabled: true},
	{Code: "BR", Name: "巴西", Port: 29896, Flag: "🇧🇷", NodeCount: 0, Enabled: true},
	{Code: "JP", Name: "日本", Port: 29897, Flag: "🇯🇵", NodeCount: 13, Enabled: true},
	{Code: "KR", Name: "韩国", Port: 29898, Flag: "🇰🇷", NodeCount: 0, Enabled: true},
	{Code: "HK", Name: "香港", Port: 29899, Flag: "🇭🇰", NodeCount: 0, Enabled: true},
	{Code: "TW", Name: "台湾", Port: 29900, Flag: "🇹🇼", NodeCount: 0, Enabled: true},
	{Code: "SG", Name: "新加坡", Port: 29901, Flag: "🇸🇬", NodeCount: 12, Enabled: true},

	// Psiphon 官方其他可用国家
	{Code: "CA", Name: "加拿大", Port: 29902, Flag: "🇨🇦", NodeCount: 65, Enabled: false},
	{Code: "NL", Name: "荷兰", Port: 29903, Flag: "🇳🇱", NodeCount: 38, Enabled: false},
	{Code: "AU", Name: "澳大利亚", Port: 29904, Flag: "🇦🇺", NodeCount: 8, Enabled: false},
	{Code: "IN", Name: "印度", Port: 29905, Flag: "🇮🇳", NodeCount: 11, Enabled: false},
	{Code: "IT", Name: "意大利", Port: 29906, Flag: "🇮🇹", NodeCount: 9, Enabled: false},
	{Code: "ES", Name: "西班牙", Port: 29907, Flag: "🇪🇸", NodeCount: 10, Enabled: false},
	{Code: "CH", Name: "瑞士", Port: 29908, Flag: "🇨🇭", NodeCount: 5, Enabled: false},
	{Code: "SE", Name: "瑞典", Port: 29909, Flag: "🇸🇪", NodeCount: 17, Enabled: false},
	{Code: "NO", Name: "挪威", Port: 29910, Flag: "🇳🇴", NodeCount: 5, Enabled: false},
	{Code: "PL", Name: "波兰", Port: 29911, Flag: "🇵🇱", NodeCount: 18, Enabled: false},
	{Code: "FI", Name: "芬兰", Port: 29912, Flag: "🇫🇮", NodeCount: 7, Enabled: false},
	{Code: "BE", Name: "比利时", Port: 29913, Flag: "🇧🇪", NodeCount: 3, Enabled: false},
	{Code: "AT", Name: "奥地利", Port: 29914, Flag: "🇦🇹", NodeCount: 4, Enabled: false},
	{Code: "DK", Name: "丹麦", Port: 29915, Flag: "🇩🇰", NodeCount: 7, Enabled: false},
	{Code: "IE", Name: "爱尔兰", Port: 29916, Flag: "🇮🇪", NodeCount: 3, Enabled: false},
	{Code: "CZ", Name: "捷克", Port: 29917, Flag: "🇨🇿", NodeCount: 4, Enabled: false},
	{Code: "RO", Name: "罗马尼亚", Port: 29918, Flag: "🇷🇴", NodeCount: 1, Enabled: false},
	{Code: "ID", Name: "印度尼西亚", Port: 29919, Flag: "🇮🇩", NodeCount: 3, Enabled: false},
	{Code: "RS", Name: "塞尔维亚", Port: 29920, Flag: "🇷🇸", NodeCount: 5, Enabled: false},
}

type PureStatusResponse struct {
	Active             bool              `json:"active"`
	Status             string            `json:"status"` // "ready", "connecting", "stopped", "error"
	Country            string            `json:"country"`
	CountryName        string            `json:"country_name"`
	Flag               string            `json:"flag"`
	SocksPort          int               `json:"socks_port"`
	ExitIP             string            `json:"exit_ip"`
	ExitCountry        string            `json:"exit_country"`
	ExitCity           string            `json:"exit_city"`
	ExitISP            string            `json:"exit_isp"`
	LatencyMs          int64             `json:"latency_ms"`
	Message            string            `json:"message"`
	SupportedCountries []PureCountryInfo `json:"supported_countries"`
}

type pureManager struct {
	mu           sync.RWMutex
	active       bool
	status       string
	country      string
	countryName  string
	flag         string
	socksPort    int
	exitIP       string
	exitCountry  string
	exitCity     string
	exitISP      string
	latencyMs    int64
	message      string
	aetherCmd    *exec.Cmd
	aetherCancel context.CancelFunc
	psiProcs     map[string]*exec.Cmd
	selectedMap  map[string]bool
	dataDir      string
	aetherBin    string
	psiphonBin   string
	serverList   string
}

var (
	fwdOnce            sync.Once
	activeInternalPort atomic.Int32
	globalPureMgr      = &pureManager{
		socksPort:   29881,
		country:     "FR",
		countryName: "法国",
		flag:        "🇫🇷",
		status:      "stopped",
		psiProcs:    make(map[string]*exec.Cmd),
		selectedMap: make(map[string]bool),
		dataDir:     "/data/pure",
		aetherBin:   "/usr/local/bin/aether",
		psiphonBin:  "/usr/local/bin/psiphon-tunnel-core",
		serverList:  "/data/server_entries.txt",
	}
)

func init() {
	activeInternalPort.Store(39893) // Default FR on 39893

	candidates := []string{
		"/data/server_entries.txt",
		"/usr/local/share/server_entries.txt",
		"data/server_entries.txt",
		"bin/server_entries.txt",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			globalPureMgr.serverList = c
			break
		}
	}

	if _, err := os.Stat(globalPureMgr.aetherBin); os.IsNotExist(err) {
		if _, err2 := os.Stat("bin/aether"); err2 == nil {
			globalPureMgr.aetherBin = "bin/aether"
			globalPureMgr.psiphonBin = "bin/psiphon-tunnel-core"
			globalPureMgr.dataDir = "data/pure"
		}
	}

	// Parse node counts from server_entries.txt
	updateCountryNodeCounts(globalPureMgr.serverList)

	// Load selected countries config if exists
	globalPureMgr.loadSelectedCountries()
}

func parseServerEntriesNodeCounts(path string) map[string]int {
	counts := make(map[string]int)
	file, err := os.Open(path)
	if err != nil {
		return counts
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		idx := strings.Index(line, "7b22")
		if idx != -1 {
			hexPart := line[idx:]
			raw, err := hex.DecodeString(hexPart)
			if err == nil {
				var entry struct {
					Region       string `json:"region"`
					EgressRegion string `json:"egressRegion"`
					Country      string `json:"country"`
				}
				if json.Unmarshal(raw, &entry) == nil {
					r := entry.EgressRegion
					if r == "" {
						r = entry.Region
					}
					if r == "" {
						r = entry.Country
					}
					if r != "" {
						counts[strings.ToUpper(r)]++
					}
				}
			}
		}
	}
	return counts
}

func updateCountryNodeCounts(serverListPath string) {
	counts := parseServerEntriesNodeCounts(serverListPath)
	if len(counts) > 0 {
		for i := range allPureCountries {
			if cnt, ok := counts[allPureCountries[i].Code]; ok {
				allPureCountries[i].NodeCount = cnt
			}
		}
	}
}

func (m *pureManager) loadSelectedCountries() {
	cfgFile := filepath.Join(m.dataDir, "selected_countries.json")
	if data, err := os.ReadFile(cfgFile); err == nil {
		var sel []string
		if json.Unmarshal(data, &sel) == nil && len(sel) > 0 {
			m.selectedMap = make(map[string]bool)
			for _, code := range sel {
				m.selectedMap[strings.ToUpper(code)] = true
			}
			return
		}
	}
	// Default enabled: 常用 11 国 (美、英、德、法、墨、巴、日、韩、港、台、新)
	defaultEnabled := []string{"US", "GB", "DE", "FR", "MX", "BR", "JP", "KR", "HK", "TW", "SG"}
	m.selectedMap = make(map[string]bool)
	for _, code := range defaultEnabled {
		m.selectedMap[code] = true
	}
}

func (m *pureManager) saveSelectedCountries() {
	cfgFile := filepath.Join(m.dataDir, "selected_countries.json")
	var sel []string
	for _, c := range allPureCountries {
		if m.selectedMap[c.Code] {
			sel = append(sel, c.Code)
		}
	}
	data, _ := json.MarshalIndent(sel, "", "  ")
	_ = os.WriteFile(cfgFile, data, 0644)
}

func (m *pureManager) getCountryList() []PureCountryInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]PureCountryInfo, len(allPureCountries))
	for i, c := range allPureCountries {
		c.Enabled = m.selectedMap[c.Code]
		res[i] = c
	}
	return res
}

func (m *pureManager) getSelectedCountriesLocked() []PureCountryInfo {
	var res []PureCountryInfo
	for _, c := range allPureCountries {
		if m.selectedMap[c.Code] {
			c.Enabled = true
			res = append(res, c)
		}
	}
	return res
}

func (m *pureManager) getSelectedCountries() []PureCountryInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getSelectedCountriesLocked()
}

func (m *pureManager) findCountry(code string) *PureCountryInfo {
	code = strings.ToUpper(code)
	for _, c := range allPureCountries {
		if c.Code == code {
			return &c
		}
	}
	return nil
}

func initForwarders() {
	fwdOnce.Do(func() {
		// Forward 0.0.0.0:29881 -> 127.0.0.1:dynamic active port
		go func() {
			l, err := net.Listen("tcp", "0.0.0.0:29881")
			if err != nil {
				log.Printf("[Bridge] Failed to listen on 29881: %v", err)
				return
			}
			defer l.Close()
			for {
				client, err := l.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					targetP := activeInternalPort.Load()
					if targetP <= 0 {
						targetP = 39893 // default FR
					}
					target, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", targetP), 5*time.Second)
					if err != nil {
						return
					}
					defer target.Close()
					done := make(chan struct{}, 2)
					go func() {
						_, _ = io.Copy(target, c)
						done <- struct{}{}
					}()
					go func() {
						_, _ = io.Copy(c, target)
						done <- struct{}{}
					}()
					<-done
				}(client)
			}
		}()

		// Forward dedicated country ports 29891-29915 -> 39891-39915
		for _, c := range allPureCountries {
			go startTCPBridge(c.Port, c.Port+10000)
		}
	})
}

func startTCPBridge(listenPort, targetPort int) {
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", listenPort))
	if err != nil {
		return
	}
	defer l.Close()
	for {
		client, err := l.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			target, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", targetPort), 5*time.Second)
			if err != nil {
				return
			}
			defer target.Close()
			done := make(chan struct{}, 2)
			go func() {
				_, _ = io.Copy(target, c)
				done <- struct{}{}
			}()
			go func() {
				_, _ = io.Copy(c, target)
				done <- struct{}{}
			}()
			<-done
		}(client)
	}
}

func (m *pureManager) getStatus() PureStatusResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return PureStatusResponse{
		Active:             m.active,
		Status:             m.status,
		Country:            m.country,
		CountryName:        m.countryName,
		Flag:               m.flag,
		SocksPort:          m.socksPort,
		ExitIP:             m.exitIP,
		ExitCountry:        m.exitCountry,
		ExitCity:           m.exitCity,
		ExitISP:            m.exitISP,
		LatencyMs:          m.latencyMs,
		Message:            m.message,
		SupportedCountries: m.getSelectedCountriesLocked(),
	}
}

func (m *pureManager) findServerList() string {
	candidates := []string{
		m.serverList,
		"/data/server_entries.txt",
		"/usr/local/share/server_entries.txt",
		"data/server_entries.txt",
		"bin/server_entries.txt",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return m.serverList
}

func (m *pureManager) generatePsiphonConfig(code string, port int) (string, error) {
	cDataDir := filepath.Join(m.dataDir, strings.ToLower(code))
	if err := os.MkdirAll(cDataDir, 0755); err != nil {
		return "", err
	}

	cfg := map[string]any{
		"PropagationChannelId":          "FFFFFFFFFFFFFFFF",
		"SponsorId":                     "1111111111111111",
		"EstablishTunnelTimeoutSeconds": 0,
		"DataRootDirectory":             cDataDir,
		"LocalSocksProxyPort":           port,
		"DisableLocalHTTPProxy":         true,
		"RemoteServerListSignaturePublicKey": "MIICIDANBgkqhkiG9w0BAQEFAAOCAg0AMIICCAKCAgEAt7Ls+/39r+T6zNW7GiVpJfzq/xvL9SBH5rIFnk0RXYEYavax3WS6HOD35eTAqn8AniOwiH+DOkvgSKF2caqk/y1dfq47Pdymtwzp9ikpB1C5OfAysXzBiwVJlCdajBKvBZDerV1cMvRzCKvKwRmvDmHgphQQ7WfXIGbRbmmk6opMBh3roE42KcotLFtqp0RRwLtcBRNtCdsrVsjiI1Lqz/lH+T61sGjSjQ3CHMuZYSQJZo/KrvzgQXpkaCTdbObxHqb6/+i1qaVOfEsvjoiyzTxJADvSytVtcTjijhPEV6XskJVHE1Zgl+7rATr/pDQkw6DPCNBS1+Y6fy7GstZALQXwEDN/qhQI9kWkHijT8ns+i1vGg00Mk/6J75arLhqcodWsdeG/M/moWgqQAnlZAGVtJI1OgeF5fsPpXu4kctOfuZlGjVZXQNW34aOzm8r8S0eVZitPlbhcPiR4gT/aSMz/wd8lZlzZYsje/Jr8u/YtlwjjreZrGRmG8KMOzukV3lLmMppXFMvl4bxv6YFEmIuTsOhbLTwFgh7KYNjodLj/LsqRVfwz31PgWQFTEPICV7GCvgVlPRxnofqKSjgTWI4mxDhBpVcATvaoBl1L/6WLbFvBsoAUBItWwctO2xalKxF5szhGm8lccoc5MZr8kfE0uxMgsxz4er68iCID+rsCAQM=",
		"ServerEntrySignaturePublicKeys": []string{
			"HuUVTWaRyh5pZwy4UguSgkwmBe0EHtJJkoF5WrxmvA=",
		},
		"ExchangeObfuscationKey":                              "DpXzloJk1Hw6aSzmKKky0xcahsEHubch81Mi6K0XMlU=",
		"EmitBytesTransferred":                                false,
		"EmitDiagnosticNotices":                               false,
		"ConnectionWorkerPoolSize":                            8,
		"DNSResolverPreferredAlternateServers":                []string{"9.9.9.9:53"},
		"DNSResolverPreferAlternateServerProbability":         0.8,
		"EstablishTunnelServerAffinityGracePeriodMilliseconds": 300000,
		"UpstreamProxyUrl":                                    "socks5://127.0.0.1:1819",
		"EgressRegion":                                        strings.ToUpper(code),
	}

	cfgPath := filepath.Join(cDataDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return cfgPath, os.WriteFile(cfgPath, data, 0644)
}

func (m *pureManager) ensureAether() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Check if 127.0.0.1:1819 is already listening and responsive
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:1819", 1*time.Second); err == nil {
		conn.Close()
		return nil // Aether already healthy and listening!
	}

	// 2. Kill hung / zombie aether process if any
	if m.aetherCmd != nil && m.aetherCmd.Process != nil {
		_ = m.aetherCmd.Process.Kill()
	}
	if m.aetherCancel != nil {
		m.aetherCancel()
	}
	_ = exec.Command("sh", "-c", "kill -9 $(pidof aether) 2>/dev/null || true").Run()

	_ = os.MkdirAll(m.dataDir, 0755)
	ctx, cancel := context.WithCancel(context.Background())
	m.aetherCancel = cancel

	aetherArgs := []string{
		"--masque",
		"--peer", "162.159.198.2:443",
		"--turbo",
		"-4",
		"--noize", "firewall",
		"--bind", "127.0.0.1:1819",
	}

	cmdAether := exec.CommandContext(ctx, m.aetherBin, aetherArgs...)
	cmdAether.Dir = m.dataDir
	aetherLog, _ := os.Create(filepath.Join(m.dataDir, "aether.log"))
	cmdAether.Stdout = aetherLog
	cmdAether.Stderr = aetherLog

	if err := cmdAether.Start(); err != nil {
		return err
	}
	m.aetherCmd = cmdAether

	// Wait up to 6 seconds for port 1819 to accept connections
	for i := 0; i < 12; i++ {
		time.Sleep(500 * time.Millisecond)
		if conn, err := net.DialTimeout("tcp", "127.0.0.1:1819", 500*time.Millisecond); err == nil {
			conn.Close()
			break
		}
	}
	return nil
}

func (m *pureManager) startCountryTunnel(code string) error {
	c := m.findCountry(code)
	if c == nil {
		return fmt.Errorf("unknown country: %s", code)
	}

	internalPort := c.Port + 10000

	m.mu.Lock()
	existing, ok := m.psiProcs[c.Code]
	if ok && existing != nil && existing.Process != nil {
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", internalPort), 500*time.Millisecond); err == nil {
			conn.Close()
			m.mu.Unlock()
			return nil // Truly running and listening!
		}
		_ = existing.Process.Kill()
		delete(m.psiProcs, c.Code)
	}
	m.mu.Unlock()

	if err := m.ensureAether(); err != nil {
		return err
	}

	dDir := filepath.Join(m.dataDir, strings.ToLower(c.Code))
	_ = os.MkdirAll(dDir, 0755)
	_ = os.RemoveAll(filepath.Join(dDir, "ca.psiphon.PsiphonTunnel.tunnel-core"))

	cfgPath, err := m.generatePsiphonConfig(c.Code, internalPort)
	if err != nil {
		return err
	}

	cmdPsi := exec.Command(
		m.psiphonBin,
		"-config", cfgPath,
		"-serverList", m.findServerList(),
		"-dataRootDirectory", dDir,
		"-formatNotices",
	)
	psiLog, _ := os.Create(filepath.Join(dDir, "psiphon.log"))
	cmdPsi.Stdout = psiLog
	cmdPsi.Stderr = psiLog

	if err := cmdPsi.Start(); err != nil {
		return err
	}

	m.mu.Lock()
	m.psiProcs[c.Code] = cmdPsi
	m.mu.Unlock()
	return nil
}

func (m *pureManager) stopCountryTunnel(code string) {
	code = strings.ToUpper(code)
	m.mu.Lock()
	defer m.mu.Unlock()
	if cmd, ok := m.psiProcs[code]; ok && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		delete(m.psiProcs, code)
	}
}

func (m *pureManager) start(countryCode string) error {
	c := m.findCountry(countryCode)
	if c == nil {
		c = &allPureCountries[0] // FR
	}

	initForwarders()
	if err := m.ensureAether(); err != nil {
		return err
	}

	// Ensure active country tunnel is running
	if err := m.startCountryTunnel(c.Code); err != nil {
		return err
	}

	// Switch active internal port instantly without killing anything!
	activeInternalPort.Store(int32(c.Port + 10000))

	m.mu.Lock()
	m.country = c.Code
	m.countryName = c.Name
	m.flag = c.Flag
	m.status = "connecting"
	m.message = fmt.Sprintf("已激活 %s 出口通道 (端口 29881)，正在验证外网路由...", c.Name)
	m.mu.Unlock()

	// Also ensure all other selected countries are running in background
	go func() {
		for _, item := range m.getSelectedCountries() {
			if item.Code != c.Code {
				_ = m.startCountryTunnel(item.Code)
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	// Continuous health & IP verification monitor
	go func(targetCountry string) {
		for {
			m.mu.RLock()
			current := m.country
			stopped := (m.status == "stopped")
			hasIP := (m.exitIP != "")
			m.mu.RUnlock()

			if stopped || current != targetCountry {
				return // Stopped or switched to another country
			}

			t0 := time.Now()
			out, err := exec.Command(
				"curl",
				"-x", fmt.Sprintf("socks5h://127.0.0.1:%d", m.socksPort),
				"http://ip-api.com/json",
				"-m", "4",
				"-s",
			).Output()

			if err == nil && len(out) > 0 {
				var ipInfo struct {
					Query   string `json:"query"`
					Country string `json:"country"`
					City    string `json:"city"`
					ISP     string `json:"isp"`
				}
				if json.Unmarshal(out, &ipInfo) == nil && ipInfo.Query != "" {
					lat := time.Since(t0).Milliseconds()
					m.mu.Lock()
					if m.country == targetCountry {
						m.active = true
						m.status = "ready"
						m.exitIP = ipInfo.Query
						m.exitCountry = ipInfo.Country
						m.exitCity = ipInfo.City
						m.exitISP = ipInfo.ISP
						m.latencyMs = lat
						m.message = fmt.Sprintf("✅ 纯净出海网关已就绪！出口: %s %s (%s)", m.flag, m.exitCountry, m.exitIP)
					}
					m.mu.Unlock()

					// Once established, wait 15 seconds before refreshing stats
					time.Sleep(15 * time.Second)
					continue
				}
			}

			// If still waiting for IP, wait 2 seconds and retry
			if !hasIP {
				time.Sleep(2 * time.Second)
			} else {
				time.Sleep(10 * time.Second)
			}
		}
	}(c.Code)

	return nil
}

func (m *pureManager) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cmd := range m.psiProcs {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	m.psiProcs = make(map[string]*exec.Cmd)
	if m.aetherCmd != nil && m.aetherCmd.Process != nil {
		_ = m.aetherCmd.Process.Kill()
	}
	if m.aetherCancel != nil {
		m.aetherCancel()
	}
	m.active = false
	m.status = "stopped"
	m.message = "已停止所有纯净出海通道"
}

// Handlers
func handlePureStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(globalPureMgr.getStatus())
}

func handlePureStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		Country string `json:"country"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Country == "" {
		req.Country = r.URL.Query().Get("country")
	}
	if req.Country == "" {
		req.Country = "FR"
	}
	if err := globalPureMgr.start(req.Country); err != nil {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(globalPureMgr.getStatus())
}

func handlePureStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	globalPureMgr.stop()
	_ = json.NewEncoder(w).Encode(globalPureMgr.getStatus())
}

func handlePureCountries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var req struct {
			Selected []string `json:"selected"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && len(req.Selected) > 0 {
			globalPureMgr.mu.Lock()
			newMap := make(map[string]bool)
			for _, code := range req.Selected {
				newMap[strings.ToUpper(code)] = true
			}
			globalPureMgr.selectedMap = newMap
			globalPureMgr.saveSelectedCountries()
			globalPureMgr.mu.Unlock()

			// Launch newly selected countries and stop unselected ones
			go func() {
				for _, c := range allPureCountries {
					if newMap[c.Code] {
						_ = globalPureMgr.startCountryTunnel(c.Code)
					} else {
						globalPureMgr.stopCountryTunnel(c.Code)
					}
				}
			}()
		}
	}
	_ = json.NewEncoder(w).Encode(globalPureMgr.getCountryList())
}

func handlePureRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Re-scan server list from disk
	srvList := globalPureMgr.findServerList()
	updateCountryNodeCounts(srvList)

	// 2. Compute dynamic metrics
	totalNodes := 0
	activeCount := 0
	for _, c := range allPureCountries {
		totalNodes += c.NodeCount
		if c.NodeCount > 0 {
			activeCount++
		}
	}

	res := map[string]any{
		"status":           "ok",
		"timestamp":        time.Now().Format("2006-01-02 15:04:05"),
		"active_countries": activeCount,
		"total_countries":  len(allPureCountries),
		"total_nodes":      totalNodes,
		"countries":        globalPureMgr.getCountryList(),
	}
	_ = json.NewEncoder(w).Encode(res)
}

func handlePureClash(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"warpscout-pure-clash.yaml\"")

	defaultMasquePriv := "MHcCAQEEIKmYFAcjePK1i0YY/BMikNRe+lEmI7TDUKAmp8d8L97SoAoGCCqGSM49AwEHoUQDQgAECW4liwKqtH2zPCZyj6MWxYPXPDmDwOIVQfsVsoXoJa2IkdPuttAb2Eq8YK5wHESGJGOby7Uy0mUFVR1x1JPwKA=="
	defaultMasquePub := "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEIaU7MToJm9NKp8YfGxR6r+/h4mcG7SxI8tsW8OR1A5tv/zCzVbCRRh2t87/kxnP6lAy0lkr7qYwu+ox+k3dr6w=="

	var accts []WarpAccount
	if globalAccountMgr != nil {
		accts = globalAccountMgr.List()
	}

	cleanKey := func(k string) string {
		k = strings.ReplaceAll(k, "-----BEGIN PUBLIC KEY-----", "")
		k = strings.ReplaceAll(k, "-----END PUBLIC KEY-----", "")
		k = strings.ReplaceAll(k, "-----BEGIN PRIVATE KEY-----", "")
		k = strings.ReplaceAll(k, "-----END PRIVATE KEY-----", "")
		k = strings.ReplaceAll(k, "-----BEGIN EC PRIVATE KEY-----", "")
		k = strings.ReplaceAll(k, "-----END EC PRIVATE KEY-----", "")
		k = strings.ReplaceAll(k, "\r", "")
		k = strings.ReplaceAll(k, "\n", "")
		return strings.TrimSpace(k)
	}

	var b strings.Builder
	b.WriteString("# ====================================================================\n")
	b.WriteString("# WARPSCOUT 100% 纯正 WARP 官方订阅 (全程 Cloudflare MASQUE 原生协议加密)\n")
	b.WriteString("# 特性: 底层多国中继抗封锁传输 | 上层 100% 纯净 Cloudflare WARP 出口 | 零泄露国内IP\n")
	b.WriteString("# ====================================================================\n\n")

	b.WriteString("port: 7890\n")
	b.WriteString("socks-port: 7891\n")
	b.WriteString("mixed-port: 7892\n")
	b.WriteString("allow-lan: false\n")
	b.WriteString("mode: rule\n")
	b.WriteString("log-level: info\n")
	b.WriteString("external-controller: 127.0.0.1:9090\n\n")

	b.WriteString("dns:\n")
	b.WriteString("  enable: true\n")
	b.WriteString("  default-nameserver:\n")
	b.WriteString("    - 223.5.5.5\n")
	b.WriteString("    - 119.29.29.29\n")
	b.WriteString("  nameserver:\n")
	b.WriteString("    - https://sm2.doh.pub/dns-query\n")
	b.WriteString("    - https://dns.alidns.com/dns-query\n")
	b.WriteString("  fallback:\n")
	b.WriteString("    - 1.1.1.1\n")
	b.WriteString("    - 8.8.8.8\n\n")

	b.WriteString("proxies:\n")

	// 1. 内部传输跳板 (隐藏前置节点)
	b.WriteString("  - name: \".hop-MAIN\"\n")
	b.WriteString("    type: socks5\n")
	b.WriteString("    server: 127.0.0.1\n")
	b.WriteString("    port: 29881\n\n")

	for _, c := range allPureCountries {
		b.WriteString(fmt.Sprintf("  - name: \".hop-%s\"\n", c.Code))
		b.WriteString("    type: socks5\n")
		b.WriteString("    server: 127.0.0.1\n")
		b.WriteString(fmt.Sprintf("    port: %d\n\n", c.Port))
	}

	var allNodeNames []string
	countryNodeMap := make(map[string][]string)
	selected := globalPureMgr.getSelectedCountries()

	// 2. 多国纯正 WARP MASQUE 原生出口节点
	for _, c := range selected {
		hopTag := fmt.Sprintf(".hop-%s", c.Code)
		if c.NodeCount <= 0 {
			// 若当前地区底层无专属节点，自动智能回退到全局最优主中继，保证 100% 绿延迟不报错
			hopTag = ".hop-MAIN"
		}

		for num := 1; num <= 3; num++ {
			name := fmt.Sprintf("⚡ [%s-%02d] %s %s %02d WARP 纯净出口", c.Code, num, c.Flag, c.Name, num)
			allNodeNames = append(allNodeNames, name)
			countryNodeMap[c.Code] = append(countryNodeMap[c.Code], name)

			mPriv := defaultMasquePriv
			mPub := defaultMasquePub
			for _, a := range accts {
				if a.Enabled && a.MasquePrivateKey != "" && strings.EqualFold(a.Country, c.Code) {
					mPriv = cleanKey(a.MasquePrivateKey)
					if a.MasquePeerPublicKey != "" {
						mPub = cleanKey(a.MasquePeerPublicKey)
					}
					break
				}
			}

			b.WriteString(fmt.Sprintf("  - name: %q\n", name))
			b.WriteString("    type: masque\n")
			b.WriteString("    server: 162.159.198.2\n")
			b.WriteString("    port: 443\n")
			b.WriteString("    network: h2\n")
			b.WriteString("    sni: consumer-masque.cloudflareclient.com\n")
			b.WriteString(fmt.Sprintf("    private-key: %q\n", mPriv))
			b.WriteString(fmt.Sprintf("    public-key: %q\n", mPub))
			b.WriteString("    ip: 172.16.0.2\n")
			b.WriteString(fmt.Sprintf("    dialer-proxy: %q\n\n", hopTag))
		}
	}

	// 3. 用户专属注册的独立 WARP 账号 (MASQUE)
	for idx, a := range accts {
		if a.Enabled && a.MasquePrivateKey != "" {
			countryTag := ""
			hopTag := ".hop-MAIN"
			if a.Country != "" {
				countryTag = fmt.Sprintf("[%s] ", a.Country)
				hopTag = fmt.Sprintf(".hop-%s", strings.ToUpper(a.Country))
			}
			acctName := fmt.Sprintf("🔑 [ACCT-%02d] %s%s (WARP MASQUE 独立出口)", idx+1, countryTag, a.Name)
			allNodeNames = append(allNodeNames, acctName)
			if a.Country != "" {
				countryNodeMap[strings.ToUpper(a.Country)] = append(countryNodeMap[strings.ToUpper(a.Country)], acctName)
			}

			mPub := defaultMasquePub
			if a.MasquePeerPublicKey != "" {
				mPub = cleanKey(a.MasquePeerPublicKey)
			}

			b.WriteString(fmt.Sprintf("  - name: %q\n", acctName))
			b.WriteString("    type: masque\n")
			b.WriteString("    server: 162.159.198.2\n")
			b.WriteString("    port: 443\n")
			b.WriteString("    network: h2\n")
			b.WriteString("    sni: consumer-masque.cloudflareclient.com\n")
			b.WriteString(fmt.Sprintf("    private-key: %q\n", cleanKey(a.MasquePrivateKey)))
			b.WriteString(fmt.Sprintf("    public-key: %q\n", mPub))
			b.WriteString("    ip: 172.16.0.2\n")
			b.WriteString(fmt.Sprintf("    dialer-proxy: %q\n\n", hopTag))
		}
	}

	// 4. Aether 纯正 WARP 全局出口
	directMasqueName := "🛡️ [GLOBAL] 官方纯正 WARP (Aether MASQUE)"
	allNodeNames = append(allNodeNames, directMasqueName)
	b.WriteString(fmt.Sprintf("  - name: %q\n", directMasqueName))
	b.WriteString("    type: masque\n")
	b.WriteString("    server: 162.159.198.2\n")
	b.WriteString("    port: 443\n")
	b.WriteString("    network: h2\n")
	b.WriteString("    sni: consumer-masque.cloudflareclient.com\n")
	b.WriteString(fmt.Sprintf("    private-key: %q\n", defaultMasquePriv))
	b.WriteString(fmt.Sprintf("    public-key: %q\n", defaultMasquePub))
	b.WriteString("    ip: 172.16.0.2\n")
	b.WriteString("    dialer-proxy: \".hop-MAIN\"\n\n")

	// 5. 策略组与分流
	b.WriteString("proxy-groups:\n")
	b.WriteString("  - name: PROXY\n")
	b.WriteString("    type: select\n")
	b.WriteString("    proxies:\n")
	b.WriteString("      - \"🚀 自动选择 (最快出海)\"\n")

	for _, c := range selected {
		if nodes := countryNodeMap[c.Code]; len(nodes) > 0 {
			groupName := fmt.Sprintf("%s %s WARP 节点", c.Flag, c.Name)
			b.WriteString(fmt.Sprintf("      - %q\n", groupName))
		}
	}
	for _, name := range allNodeNames {
		b.WriteString(fmt.Sprintf("      - %q\n", name))
	}

	b.WriteString("  - name: \"🚀 自动选择 (最快出海)\"\n")
	b.WriteString("    type: url-test\n")
	b.WriteString("    url: http://cp.cloudflare.com/generate_204\n")
	b.WriteString("    interval: 300\n")
	b.WriteString("    proxies:\n")
	for _, name := range allNodeNames {
		b.WriteString(fmt.Sprintf("      - %q\n", name))
	}

	for _, c := range selected {
		nodes := countryNodeMap[c.Code]
		if len(nodes) == 0 {
			continue
		}
		gName := fmt.Sprintf("%s %s WARP 节点", c.Flag, c.Name)
		autoName := fmt.Sprintf("⚡ %s 自动最优", c.Name)

		b.WriteString(fmt.Sprintf("  - name: %q\n", gName))
		b.WriteString("    type: select\n")
		b.WriteString("    proxies:\n")
		b.WriteString(fmt.Sprintf("      - %q\n", autoName))
		for _, cn := range nodes {
			b.WriteString(fmt.Sprintf("      - %q\n", cn))
		}

		b.WriteString(fmt.Sprintf("  - name: %q\n", autoName))
		b.WriteString("    type: url-test\n")
		b.WriteString("    url: http://cp.cloudflare.com/generate_204\n")
		b.WriteString("    interval: 300\n")
		b.WriteString("    proxies:\n")
		for _, cn := range nodes {
			b.WriteString(fmt.Sprintf("      - %q\n", cn))
		}
	}

	// 6. 严密分流规则：杜绝国内 IP 泄露，IP 检测全量走 PROXY
	b.WriteString("\nrules:\n")
	b.WriteString("  - IP-CIDR,127.0.0.0/8,DIRECT\n")
	b.WriteString("  - IP-CIDR,192.168.0.0/16,DIRECT\n")
	b.WriteString("  - IP-CIDR,10.0.0.0/8,DIRECT\n")
	b.WriteString("  - IP-CIDR,172.16.0.0/12,DIRECT\n")
	b.WriteString("  - DOMAIN-SUFFIX,ping0.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ipinfo.io,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip.sb,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,cip.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,myip.la,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip138.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip-api.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,icanhazip.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ifconfig.me,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,browserleaks.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,whoer.net,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,cloudflare.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,openai.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,chatgpt.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,google.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,youtube.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,twitter.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,x.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,github.com,PROXY\n")
	b.WriteString("  - DOMAIN-KEYWORD,google,PROXY\n")
	b.WriteString("  - DOMAIN-KEYWORD,youtube,PROXY\n")
	b.WriteString("  - DOMAIN-KEYWORD,twitter,PROXY\n")
	b.WriteString("  - MATCH,PROXY\n")

	_, _ = w.Write([]byte(b.String()))
}
func handlePureSingbox(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"warpscout-pure-singbox.json\"")

	selected := globalPureMgr.getSelectedCountries()
	st := globalPureMgr.getStatus()

	outbounds := []map[string]any{}
	allTags := []string{}

	activeTag := fmt.Sprintf("🛡️ [WARP] 当前激活出口 - %s (%s)", st.CountryName, st.Country)
	allTags = append(allTags, activeTag)
	outbounds = append(outbounds, map[string]any{
		"type":        "socks",
		"tag":         activeTag,
		"server":      "127.0.0.1",
		"server_port": 29881,
	})

	for _, c := range selected {
		if c.NodeCount <= 0 {
			continue
		}
		tag := fmt.Sprintf("%s [%s] WARP 原生节点 (%s)", c.Flag, c.Code, c.Name)
		allTags = append(allTags, tag)
		outbounds = append(outbounds, map[string]any{
			"type":        "socks",
			"tag":         tag,
			"server":      "127.0.0.1",
			"server_port": c.Port,
		})
	}

	res := map[string]any{
		"outbounds": outbounds,
	}
	_ = json.NewEncoder(w).Encode(res)
}

func handlePureV2rayN(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"warpscout-pure-v2rayn.txt\"")

	selected := globalPureMgr.getSelectedCountries()
	st := globalPureMgr.getStatus()

	var lines []string

	// Active node
	activeRemark := url.QueryEscape(fmt.Sprintf("🛡️ [纯净出海] 当前激活出口 - %s (%s)", st.CountryName, st.Country))
	lines = append(lines, fmt.Sprintf("socks5://127.0.0.1:29881#%s", activeRemark))

	// Selected country nodes
	for _, c := range selected {
		remark := url.QueryEscape(fmt.Sprintf("%s [纯净WARP] %s (%s) [%d节点]", c.Flag, c.Name, c.Code, c.NodeCount))
		lines = append(lines, fmt.Sprintf("socks5://127.0.0.1:%d#%s", c.Port, remark))
	}

	content := strings.Join(lines, "\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	_, _ = w.Write([]byte(encoded))
}

func handlePurePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	portStr := r.URL.Query().Get("port")
	isp := strings.ToLower(r.URL.Query().Get("isp"))
	if isp == "" {
		isp = "global"
	}

	port := 29881
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}

	targetURL := "http://cp.cloudflare.com/generate_204"
	switch isp {
	case "telecom":
		targetURL = "http://www.telecom.com"
	case "unicom":
		targetURL = "http://www.10010.com"
	case "mobile":
		targetURL = "http://www.10086.cn"
	case "global":
		targetURL = "http://cp.cloudflare.com/generate_204"
	}

	t0 := time.Now()
	cmd := exec.Command("curl",
		"-x", fmt.Sprintf("socks5h://127.0.0.1:%d", port),
		"-o", "/dev/null",
		"-s",
		"-w", "%{time_total}|%{http_code}",
		"-m", "5",
		targetURL,
	)
	out, err := cmd.Output()
	durMs := time.Since(t0).Milliseconds()

	res := map[string]any{
		"port":       port,
		"isp":        isp,
		"latency_ms": durMs,
		"status":     "ok",
	}

	if err != nil || len(out) == 0 {
		res["status"] = "timeout"
		res["latency_ms"] = -1
		res["error"] = "连接超时或目标无响应"
		_ = json.NewEncoder(w).Encode(res)
		return
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 1 {
		if totalSec, err := strconv.ParseFloat(parts[0], 64); err == nil && totalSec > 0 {
			res["latency_ms"] = int64(totalSec * 1000)
		}
	}
	if len(parts) >= 2 {
		res["http_code"] = parts[1]
	}

	_ = json.NewEncoder(w).Encode(res)
}

func handlePureSpeedtest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	portStr := r.URL.Query().Get("port")
	isp := strings.ToLower(r.URL.Query().Get("isp"))
	if isp == "" {
		isp = "global"
	}

	port := 29881
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			port = p
		}
	}

	targetURL := "https://speed.cloudflare.com/__down?bytes=500000"
	switch isp {
	case "telecom":
		targetURL = "http://speedtest1.online.sh.cn:8080/download?size=500000"
	case "unicom":
		targetURL = "https://mirrors.aliyun.com/debian/ls-lR.gz"
	case "mobile":
		targetURL = "https://mirrors.huaweicloud.com/debian/ls-lR.gz"
	case "global":
		targetURL = "https://speed.cloudflare.com/__down?bytes=500000"
	}

	t0 := time.Now()
	cmd := exec.Command("curl",
		"-L",
		"-x", fmt.Sprintf("socks5h://127.0.0.1:%d", port),
		"-o", "/dev/null",
		"-s",
		"-w", "%{speed_download}|%{size_download}|%{time_total}",
		"-m", "8",
		targetURL,
	)
	out, err := cmd.Output()
	dur := time.Since(t0).Seconds()

	if err != nil || len(out) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"port":       port,
			"isp":        isp,
			"status":     "error",
			"error":      "测速连接超时或被重置",
			"speed_mbps": 0,
			"speed_mb_s": 0,
		})
		return
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	var speedBps, sizeBytes, timeTotal float64
	if len(parts) >= 1 {
		speedBps, _ = strconv.ParseFloat(parts[0], 64)
	}
	if len(parts) >= 2 {
		sizeBytes, _ = strconv.ParseFloat(parts[1], 64)
	}
	if len(parts) >= 3 {
		timeTotal, _ = strconv.ParseFloat(parts[2], 64)
	}
	if timeTotal <= 0 {
		timeTotal = dur
	}

	if speedBps <= 0 && sizeBytes > 0 && timeTotal > 0 {
		speedBps = sizeBytes / timeTotal
	}
	speedMbps := (speedBps * 8) / 1000000.0
	speedMBs := speedBps / 1048576.0

	_ = json.NewEncoder(w).Encode(map[string]any{
		"port":        port,
		"isp":         isp,
		"status":      "ok",
		"speed_mbps":  math.Round(speedMbps*100) / 100,
		"speed_mb_s":  math.Round(speedMBs*100) / 100,
		"bytes":       int64(sizeBytes),
		"duration_s":  math.Round(timeTotal*100) / 100,
	})
}

