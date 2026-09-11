package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type WarpAccount struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Token               string    `json:"token,omitempty"`
	PrivateKey          string    `json:"private_key"`
	PeerPublicKey       string    `json:"peer_public_key"`
	IPv4                string    `json:"ipv4"`
	IPv6                string    `json:"ipv6"`
	MasquePrivateKey    string    `json:"masque_private_key,omitempty"`
	MasquePeerPublicKey string    `json:"masque_peer_public_key,omitempty"`
	Country             string    `json:"country,omitempty"`
	BoundNodeID         string    `json:"bound_node_id,omitempty"`
	BoundNodeName       string    `json:"bound_node_name,omitempty"`
	RegisteredVia       string    `json:"registered_via,omitempty"`
	Enabled             bool      `json:"enabled"`
	CreatedAt           time.Time `json:"created_at"`
	Status              string    `json:"status"` // "active", "rate_limited", "disabled"
}

type AccountManager struct {
	mu        sync.RWMutex
	filePath  string
	accounts  []WarpAccount
	scheduler struct {
		running      bool
		intervalMin  int
		targetCount  int
		createdCount int
		relayMode    string
		customProxy  string
		lastRun      time.Time
		nextRun      time.Time
		lastError    string
		stopChan     chan struct{}
	}
}

var globalAccountMgr *AccountManager

func initAccountManager(baseDir string) {
	fp := filepath.Join(baseDir, "warp_accounts.json")
	if baseDir == "" {
		fp = "data/warp_accounts.json"
	}
	globalAccountMgr = &AccountManager{
		filePath: fp,
		accounts: make([]WarpAccount, 0),
	}
	_ = globalAccountMgr.load()

	// If empty, import existing warpscout-account.json if present
	if len(globalAccountMgr.accounts) == 0 {
		globalAccountMgr.importLegacyAccount(filepath.Join(baseDir, defaultAccount))
	}
}

func (m *AccountManager) importLegacyAccount(path string) {
	if a, err := loadAccount(path); err == nil && a.PrivateKey != "" {
		masquePriv := ""
		masquePub := ""
		if a.Masque != nil {
			masquePriv = a.Masque.PrivateKey
			masquePub = a.Masque.PeerPublicKey
		}
		item := WarpAccount{
			ID:                  a.ID,
			Name:                "WARP 默认初始账号",
			Token:               a.Token,
			PrivateKey:          a.PrivateKey,
			PeerPublicKey:       a.PeerPublicKey,
			IPv4:                a.IPv4,
			IPv6:                a.IPv6,
			MasquePrivateKey:    masquePriv,
			MasquePeerPublicKey: masquePub,
			Enabled:             true,
			CreatedAt:           time.Now(),
			Status:              "active",
		}
		if item.ID == "" {
			item.ID = fmt.Sprintf("acct_%d", time.Now().Unix())
		}
		m.accounts = append(m.accounts, item)
		_ = m.save()
	}
}

func (m *AccountManager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return err
	}
	var list []WarpAccount
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	m.accounts = list
	return nil
}

func (m *AccountManager) save() error {
	_ = os.MkdirAll(filepath.Dir(m.filePath), 0755)
	data, err := json.MarshalIndent(m.accounts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.filePath, data, 0644)
}

func (m *AccountManager) List() []WarpAccount {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]WarpAccount, len(m.accounts))
	copy(res, m.accounts)
	return res
}

func (m *AccountManager) Get(id string) (*WarpAccount, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range m.accounts {
		if a.ID == id {
			cp := a
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("账号不存在: %s", id)
}

func (m *AccountManager) Add(a WarpAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if a.ID == "" {
		a.ID = fmt.Sprintf("acct_%d", time.Now().UnixNano()/1e6)
	}
	if a.Name == "" {
		a.Name = fmt.Sprintf("WARP 账号 #%d", len(m.accounts)+1)
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.Status = "active"
	a.Enabled = true

	// Check duplicates
	for i, existing := range m.accounts {
		if existing.ID == a.ID || (existing.PrivateKey == a.PrivateKey && a.PrivateKey != "") {
			m.accounts[i] = a
			return m.save()
		}
	}

	m.accounts = append(m.accounts, a)
	return m.save()
}

func (m *AccountManager) Update(a WarpAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	for i, existing := range m.accounts {
		if existing.ID == a.ID {
			if a.Name != "" {
				existing.Name = a.Name
			}
			existing.BoundNodeID = a.BoundNodeID
			existing.BoundNodeName = a.BoundNodeName
			existing.Enabled = a.Enabled
			if a.Country != "" {
				existing.Country = a.Country
			}
			if a.RegisteredVia != "" {
				existing.RegisteredVia = a.RegisteredVia
			}
			if a.PrivateKey != "" {
				existing.PrivateKey = a.PrivateKey
			}
			if a.PeerPublicKey != "" {
				existing.PeerPublicKey = a.PeerPublicKey
			}
			if a.MasquePrivateKey != "" {
				existing.MasquePrivateKey = a.MasquePrivateKey
			}
			if a.MasquePeerPublicKey != "" {
				existing.MasquePeerPublicKey = a.MasquePeerPublicKey
			}
			m.accounts[i] = existing
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("未找到待更新账号: %s", a.ID)
	}
	return m.save()
}

func (m *AccountManager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var updated []WarpAccount
	found := false
	for _, a := range m.accounts {
		if a.ID == id {
			found = true
			continue
		}
		updated = append(updated, a)
	}
	if !found {
		return fmt.Errorf("账号不存在: %s", id)
	}
	m.accounts = updated
	return m.save()
}

func (m *AccountManager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts = make([]WarpAccount, 0)
	return m.save()
}

func (m *AccountManager) BindNode(acctID, nodeID, nodeName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, a := range m.accounts {
		if a.ID == acctID {
			m.accounts[i].BoundNodeID = nodeID
			m.accounts[i].BoundNodeName = nodeName
			return m.save()
		}
	}
	return fmt.Errorf("账号不存在: %s", acctID)
}

// RegisterFreshWARP creates a real device registration via Cloudflare API, optionally routed through a proxy
func (m *AccountManager) RegisterFreshWARP(ctx context.Context, proxyURL string, viaNote string) (*WarpAccount, error) {
	var client *http.Client
	if proxyURL != "" {
		if pURL, err := url.Parse(proxyURL); err == nil {
			client = &http.Client{
				Timeout: registerTimeout,
				Transport: &http.Transport{
					Proxy: http.ProxyURL(pURL),
				},
			}
		}
	}
	if client == nil {
		client = &http.Client{Timeout: registerTimeout}
	}

	rawAcct, err := mintAccount(ctx, client, account{})
	if err != nil {
		relayClient := &http.Client{Timeout: relayTimeout}
		rawAcct, err = mintAccount(ctx, relayClient, account{})
		if err != nil {
			return nil, fmt.Errorf("注册 Cloudflare WARP 账号失败 (可能触发限流): %w", err)
		}
	}

	masquePriv := ""
	masquePub := ""
	if rawAcct.Masque != nil {
		masquePriv = rawAcct.Masque.PrivateKey
		masquePub = rawAcct.Masque.PeerPublicKey
	}

	count := len(m.List()) + 1
	if viaNote == "" {
		viaNote = "直连 Cloudflare 官方"
	}
	item := WarpAccount{
		ID:                  rawAcct.ID,
		Name:                fmt.Sprintf("WARP 独立账号 #%02d", count),
		Token:               rawAcct.Token,
		PrivateKey:          rawAcct.PrivateKey,
		PeerPublicKey:       rawAcct.PeerPublicKey,
		IPv4:                rawAcct.IPv4,
		IPv6:                rawAcct.IPv6,
		MasquePrivateKey:    masquePriv,
		MasquePeerPublicKey: masquePub,
		RegisteredVia:       viaNote,
		Enabled:             true,
		CreatedAt:           time.Now(),
		Status:              "active",
	}

	if err := m.Add(item); err != nil {
		return nil, err
	}
	return &item, nil
}

// RegisterWARPViaCountry routes WARP registration through a specific country node from globalSubMgr
func (m *AccountManager) RegisterWARPViaCountry(ctx context.Context, country string) (*WarpAccount, error) {
	if globalSubMgr == nil {
		return m.RegisterFreshWARP(ctx, "", "直连 Cloudflare 官方 API")
	}

	node := globalSubMgr.GetBestNodeForCountry(country)
	if node == nil {
		return m.RegisterFreshWARP(ctx, "", "直连 Cloudflare 官方 API")
	}

	proxyURL, err := startSingBoxNode(node)
	if err != nil {
		// Fallback to direct registration if singbox tunnel couldn't start
		return m.RegisterFreshWARP(ctx, "", fmt.Sprintf("中继启动失败，降级直连 [%s]", node.Country))
	}

	viaNote := fmt.Sprintf("[%s] %s", node.Country, node.Name)
	acct, err := m.RegisterFreshWARP(ctx, proxyURL, viaNote)
	if err != nil {
		return nil, err
	}
	acct.Country = node.Country
	acct.Name = fmt.Sprintf("WARP [%s] 账号 #%02d", node.Country, len(m.List()))
	_ = m.Update(*acct)
	return acct, nil
}

func (m *AccountManager) StartScheduler(intervalMin, targetCount int, relayMode, customProxy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.scheduler.running {
		return fmt.Errorf("定时注册任务已在运行中")
	}
	if intervalMin < 1 {
		intervalMin = 5
	}
	if targetCount <= 0 {
		targetCount = 20
	}
	if relayMode == "" {
		relayMode = "auto"
	}

	m.scheduler.running = true
	m.scheduler.intervalMin = intervalMin
	m.scheduler.targetCount = targetCount
	m.scheduler.relayMode = relayMode
	m.scheduler.customProxy = customProxy
	m.scheduler.createdCount = 0
	m.scheduler.lastError = ""
	m.scheduler.lastRun = time.Now()
	m.scheduler.nextRun = time.Now().Add(time.Duration(intervalMin) * time.Minute)
	m.scheduler.stopChan = make(chan struct{})

	go m.runSchedulerLoop(m.scheduler.stopChan, intervalMin, targetCount)
	return nil
}

func (m *AccountManager) StopScheduler() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.scheduler.running {
		return
	}
	close(m.scheduler.stopChan)
	m.scheduler.running = false
	m.scheduler.nextRun = time.Time{}
}

func (m *AccountManager) GetSchedulerStatus() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	countdownSec := 0
	if m.scheduler.running && !m.scheduler.nextRun.IsZero() {
		rem := time.Until(m.scheduler.nextRun)
		if rem > 0 {
			countdownSec = int(rem.Seconds())
		}
	}

	return map[string]any{
		"running":        m.scheduler.running,
		"interval_min":   m.scheduler.intervalMin,
		"target_count":   m.scheduler.targetCount,
		"created_count":  m.scheduler.createdCount,
		"total_accounts": len(m.accounts),
		"last_run":       m.scheduler.lastRun.Format("2006-01-02 15:04:05"),
		"next_run":       m.scheduler.nextRun.Format("2006-01-02 15:04:05"),
		"countdown_sec":  countdownSec,
		"relay_mode":     m.scheduler.relayMode,
		"custom_proxy":   m.scheduler.customProxy,
		"last_error":     m.scheduler.lastError,
	}
}

func (m *AccountManager) runSchedulerLoop(stopChan chan struct{}, intervalMin, targetCount int) {
	ticker := time.NewTicker(time.Duration(intervalMin) * time.Minute)
	defer ticker.Stop()

	time.Sleep(2 * time.Second)
	m.executeScheduledOne(targetCount)

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			m.executeScheduledOne(targetCount)
		}
	}
}

func (m *AccountManager) executeScheduledOne(targetCount int) {
	m.mu.Lock()
	if !m.scheduler.running {
		m.mu.Unlock()
		return
	}
	currentTotal := len(m.accounts)
	if currentTotal >= targetCount {
		m.scheduler.running = false
		m.scheduler.lastError = fmt.Sprintf("已达到目标账号数 %d，自动完成", targetCount)
		m.mu.Unlock()
		return
	}
	intervalMin := m.scheduler.intervalMin
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Determine relay proxy & country strategy
	m.mu.RLock()
	rm := m.scheduler.relayMode
	cp := m.scheduler.customProxy
	created := m.scheduler.createdCount
	m.mu.RUnlock()

	var err error
	if rm == "direct" {
		_, err = m.RegisterFreshWARP(ctx, "", "直连 Cloudflare 官方 API")
	} else if cp != "" {
		_, err = m.RegisterFreshWARP(ctx, cp, "自定义代理 "+cp)
	} else if rm == "active" {
		proxyURL := "socks5://127.0.0.1:29891"
		viaNote := "活跃底座"
		if globalSubMgr != nil {
			if act := globalSubMgr.GetActiveNode(); act != nil {
				viaNote = fmt.Sprintf("活跃底座: [%s] %s", act.Country, act.Name)
			}
		}
		_, err = m.RegisterFreshWARP(ctx, proxyURL, viaNote)
	} else {
		// Multi-country rotation (default / pool / rotate)
		countryCycle := []string{"US", "HK", "JP", "SG", "DE", "GB", "CA"}
		targetCountry := countryCycle[created%len(countryCycle)]
		if rm != "auto" && rm != "pool" && rm != "rotate" && rm != "" {
			// If a specific country is requested, e.g. "US", "HK"
			targetCountry = strings.ToUpper(rm)
		}
		_, err = m.RegisterWARPViaCountry(ctx, targetCountry)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduler.lastRun = time.Now()
	m.scheduler.nextRun = time.Now().Add(time.Duration(intervalMin) * time.Minute)

	if err != nil {
		m.scheduler.lastError = fmt.Sprintf("[%s] %v", time.Now().Format("15:04:05"), err)
	} else {
		m.scheduler.createdCount++
		m.scheduler.lastError = ""
	}
}
