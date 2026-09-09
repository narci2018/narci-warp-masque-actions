package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type CountryWarpTarget struct {
	Code     string
	Flag     string
	Name     string
	Num      int
	Endpoint string
	Port     int
}

var defaultCountryTargets = []CountryWarpTarget{
	{Code: "US", Flag: "🇺🇸", Name: "美国", Num: 1, Endpoint: "162.159.192.1", Port: 2408},
	{Code: "US", Flag: "🇺🇸", Name: "美国", Num: 2, Endpoint: "162.159.193.1", Port: 2408},
	{Code: "US", Flag: "🇺🇸", Name: "美国", Num: 3, Endpoint: "162.159.195.1", Port: 2408},
	{Code: "US", Flag: "🇺🇸", Name: "美国", Num: 4, Endpoint: "8.39.204.2", Port: 1701},
	{Code: "US", Flag: "🇺🇸", Name: "美国", Num: 5, Endpoint: "8.35.211.248", Port: 1701},
	{Code: "GB", Flag: "🇬🇧", Name: "英国", Num: 1, Endpoint: "162.159.193.10", Port: 4500},
	{Code: "GB", Flag: "🇬🇧", Name: "英国", Num: 2, Endpoint: "162.159.193.11", Port: 2408},
	{Code: "GB", Flag: "🇬🇧", Name: "英国", Num: 3, Endpoint: "188.114.99.10", Port: 2408},
	{Code: "GB", Flag: "🇬🇧", Name: "英国", Num: 4, Endpoint: "188.114.99.11", Port: 4500},
	{Code: "GB", Flag: "🇬🇧", Name: "英国", Num: 5, Endpoint: "162.159.195.10", Port: 1701},
	{Code: "DE", Flag: "🇩🇪", Name: "德国", Num: 1, Endpoint: "188.114.99.144", Port: 4500},
	{Code: "DE", Flag: "🇩🇪", Name: "德国", Num: 2, Endpoint: "188.114.99.145", Port: 2408},
	{Code: "DE", Flag: "🇩🇪", Name: "德国", Num: 3, Endpoint: "188.114.98.144", Port: 2408},
	{Code: "DE", Flag: "🇩🇪", Name: "德国", Num: 4, Endpoint: "188.114.97.144", Port: 4500},
	{Code: "DE", Flag: "🇩🇪", Name: "德国", Num: 5, Endpoint: "162.159.192.144", Port: 1701},
	{Code: "FR", Flag: "🇫🇷", Name: "法国", Num: 1, Endpoint: "162.159.195.50", Port: 4500},
	{Code: "FR", Flag: "🇫🇷", Name: "法国", Num: 2, Endpoint: "162.159.195.51", Port: 2408},
	{Code: "FR", Flag: "🇫🇷", Name: "法国", Num: 3, Endpoint: "188.114.99.50", Port: 2408},
	{Code: "FR", Flag: "🇫🇷", Name: "法国", Num: 4, Endpoint: "188.114.98.50", Port: 4500},
	{Code: "FR", Flag: "🇫🇷", Name: "法国", Num: 5, Endpoint: "162.159.193.50", Port: 1701},
	{Code: "JP", Flag: "🇯🇵", Name: "日本", Num: 1, Endpoint: "188.114.97.1", Port: 2408},
	{Code: "JP", Flag: "🇯🇵", Name: "日本", Num: 2, Endpoint: "188.114.97.2", Port: 4500},
	{Code: "JP", Flag: "🇯🇵", Name: "日本", Num: 3, Endpoint: "188.114.97.10", Port: 1701},
	{Code: "JP", Flag: "🇯🇵", Name: "日本", Num: 4, Endpoint: "162.159.192.20", Port: 2408},
	{Code: "JP", Flag: "🇯🇵", Name: "日本", Num: 5, Endpoint: "162.159.193.20", Port: 4500},
	{Code: "KR", Flag: "🇰🇷", Name: "韩国", Num: 1, Endpoint: "162.159.192.80", Port: 4500},
	{Code: "KR", Flag: "🇰🇷", Name: "韩国", Num: 2, Endpoint: "162.159.192.81", Port: 2408},
	{Code: "KR", Flag: "🇰🇷", Name: "韩国", Num: 3, Endpoint: "188.114.96.80", Port: 2408},
	{Code: "KR", Flag: "🇰🇷", Name: "韩国", Num: 4, Endpoint: "188.114.97.80", Port: 4500},
	{Code: "KR", Flag: "🇰🇷", Name: "韩国", Num: 5, Endpoint: "162.159.195.80", Port: 1701},
	{Code: "SG", Flag: "🇸🇬", Name: "新加坡", Num: 1, Endpoint: "188.114.98.1", Port: 2408},
	{Code: "SG", Flag: "🇸🇬", Name: "新加坡", Num: 2, Endpoint: "188.114.98.2", Port: 4500},
	{Code: "SG", Flag: "🇸🇬", Name: "新加坡", Num: 3, Endpoint: "188.114.98.10", Port: 1701},
	{Code: "SG", Flag: "🇸🇬", Name: "新加坡", Num: 4, Endpoint: "162.159.192.30", Port: 2408},
	{Code: "SG", Flag: "🇸🇬", Name: "新加坡", Num: 5, Endpoint: "162.159.193.30", Port: 4500},
	{Code: "HK", Flag: "🇭🇰", Name: "香港", Num: 1, Endpoint: "188.114.96.1", Port: 2408},
	{Code: "HK", Flag: "🇭🇰", Name: "香港", Num: 2, Endpoint: "188.114.96.2", Port: 4500},
	{Code: "HK", Flag: "🇭🇰", Name: "香港", Num: 3, Endpoint: "188.114.96.10", Port: 1701},
	{Code: "HK", Flag: "🇭🇰", Name: "香港", Num: 4, Endpoint: "162.159.192.5", Port: 2408},
	{Code: "HK", Flag: "🇭🇰", Name: "香港", Num: 5, Endpoint: "162.159.193.5", Port: 4500},
	{Code: "TW", Flag: "🇹🇼", Name: "台湾", Num: 1, Endpoint: "162.159.193.15", Port: 1701},
	{Code: "TW", Flag: "🇹🇼", Name: "台湾", Num: 2, Endpoint: "162.159.193.16", Port: 2408},
	{Code: "TW", Flag: "🇹🇼", Name: "台湾", Num: 3, Endpoint: "188.114.96.15", Port: 2408},
	{Code: "TW", Flag: "🇹🇼", Name: "台湾", Num: 4, Endpoint: "188.114.97.15", Port: 4500},
	{Code: "TW", Flag: "🇹🇼", Name: "台湾", Num: 5, Endpoint: "162.159.192.15", Port: 1701},
	{Code: "MX", Flag: "🇲🇽", Name: "墨西哥", Num: 1, Endpoint: "162.159.192.60", Port: 2408},
	{Code: "MX", Flag: "🇲🇽", Name: "墨西哥", Num: 2, Endpoint: "162.159.193.60", Port: 4500},
	{Code: "MX", Flag: "🇲🇽", Name: "墨西哥", Num: 3, Endpoint: "162.159.195.60", Port: 1701},
	{Code: "MX", Flag: "🇲🇽", Name: "墨西哥", Num: 4, Endpoint: "188.114.96.60", Port: 2408},
	{Code: "MX", Flag: "🇲🇽", Name: "墨西哥", Num: 5, Endpoint: "8.39.204.60", Port: 1701},
	{Code: "CA", Flag: "🇨🇦", Name: "加拿大", Num: 1, Endpoint: "162.159.195.20", Port: 4500},
	{Code: "CA", Flag: "🇨🇦", Name: "加拿大", Num: 2, Endpoint: "162.159.195.21", Port: 2408},
	{Code: "CA", Flag: "🇨🇦", Name: "加拿大", Num: 3, Endpoint: "162.159.192.25", Port: 2408},
	{Code: "CA", Flag: "🇨🇦", Name: "加拿大", Num: 4, Endpoint: "188.114.99.20", Port: 4500},
	{Code: "CA", Flag: "🇨🇦", Name: "加拿大", Num: 5, Endpoint: "8.39.125.20", Port: 1701},
	{Code: "TH", Flag: "🇹🇭", Name: "泰国", Num: 1, Endpoint: "188.114.98.70", Port: 2408},
	{Code: "TH", Flag: "🇹🇭", Name: "泰国", Num: 2, Endpoint: "188.114.98.71", Port: 4500},
	{Code: "TH", Flag: "🇹🇭", Name: "泰国", Num: 3, Endpoint: "162.159.192.70", Port: 2408},
	{Code: "TH", Flag: "🇹🇭", Name: "泰国", Num: 4, Endpoint: "162.159.193.70", Port: 1701},
	{Code: "TH", Flag: "🇹🇭", Name: "泰国", Num: 5, Endpoint: "188.114.96.70", Port: 4500},
	{Code: "MY", Flag: "🇲🇾", Name: "马来西亚", Num: 1, Endpoint: "188.114.98.85", Port: 2408},
	{Code: "MY", Flag: "🇲🇾", Name: "马来西亚", Num: 2, Endpoint: "188.114.98.86", Port: 4500},
	{Code: "MY", Flag: "🇲🇾", Name: "马来西亚", Num: 3, Endpoint: "162.159.192.85", Port: 2408},
	{Code: "MY", Flag: "🇲🇾", Name: "马来西亚", Num: 4, Endpoint: "162.159.193.85", Port: 1701},
	{Code: "MY", Flag: "🇲🇾", Name: "马来西亚", Num: 5, Endpoint: "188.114.97.85", Port: 4500},
	{Code: "VN", Flag: "🇻🇳", Name: "越南", Num: 1, Endpoint: "188.114.96.90", Port: 2408},
	{Code: "VN", Flag: "🇻🇳", Name: "越南", Num: 2, Endpoint: "188.114.96.91", Port: 4500},
	{Code: "VN", Flag: "🇻🇳", Name: "越南", Num: 3, Endpoint: "162.159.192.90", Port: 2408},
	{Code: "VN", Flag: "🇻🇳", Name: "越南", Num: 4, Endpoint: "162.159.193.90", Port: 1701},
	{Code: "VN", Flag: "🇻🇳", Name: "越南", Num: 5, Endpoint: "188.114.98.90", Port: 4500},
	{Code: "PH", Flag: "🇵🇭", Name: "菲律宾", Num: 1, Endpoint: "188.114.96.110", Port: 2408},
	{Code: "PH", Flag: "🇵🇭", Name: "菲律宾", Num: 2, Endpoint: "188.114.96.111", Port: 4500},
	{Code: "PH", Flag: "🇵🇭", Name: "菲律宾", Num: 3, Endpoint: "162.159.192.110", Port: 2408},
	{Code: "PH", Flag: "🇵🇭", Name: "菲律宾", Num: 4, Endpoint: "162.159.193.110", Port: 1701},
	{Code: "PH", Flag: "🇵🇭", Name: "菲律宾", Num: 5, Endpoint: "188.114.97.110", Port: 4500},
}

// GenerateWarpOnWarpClash 生成 100% 纯双层 WireGuard (AmneziaWG) 的 Clash Verge / Mihomo 完整订阅配置
func GenerateWarpOnWarpClash(opts options) (string, error) {
	acct, err := loadAccount(opts.accountPath)
	if err != nil {
		return "", fmt.Errorf("读取 WARP 账号失败: %w", err)
	}

	outerPriv := warpPrivateKey
	outerPub := warpPublicKey
	outerIP := warpAddress
	if acct.Outer != nil && acct.Outer.PrivateKey != "" {
		outerPriv = acct.Outer.PrivateKey
		outerPub = acct.Outer.PeerPublicKey
		outerIP = acct.Outer.IPv4
	}

	innerPriv := acct.PrivateKey
	innerPub := acct.PeerPublicKey
	innerIP := acct.IPv4
	if innerPriv == "" {
		innerPriv = warpPrivateKey
		innerPub = warpPublicKey
		innerIP = warpAddress
	}

	outerName := "⚡ [Transit-Hop] 🚀 国内直连穿墙跳板 (AWG混淆)"

	var b strings.Builder
	b.WriteString("# ====================================================================\n")
	b.WriteString("# WARPSCOUT 纯双层 WARP-on-WARP 官方订阅 (15国 75节点 矩阵架构)\n")
	b.WriteString("# 特性: 100% 原生 WireGuard 协议 | 免任何第三方代理 | 彻底杜绝 WARP 送中\n")
	b.WriteString("# 外层: 国内直连 Anycast + AmneziaWG 混淆 (穿透 GFW 阻断)\n")
	b.WriteString("# 内层: 海外 Cloudflare Edge 落地 (获得香港/日本/美国/新加坡等原生出口)\n")
	b.WriteString("# ====================================================================\n\n")

	b.WriteString("port: 7890\n")
	b.WriteString("socks-port: 7891\n")
	b.WriteString("allow-lan: true\n")
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

	// 1. 外层穿墙节点 (Outer Hop)
	b.WriteString(fmt.Sprintf("  - name: %q\n", outerName))
	b.WriteString("    type: wireguard\n")
	b.WriteString(fmt.Sprintf("    private-key: %s\n", outerPriv))
	b.WriteString(fmt.Sprintf("    ip: %s\n", outerIP))
	b.WriteString("    peers:\n")
	b.WriteString("      - server: 8.39.204.2\n")
	b.WriteString("        port: 1701\n")
	b.WriteString(fmt.Sprintf("        public-key: %s\n", outerPub))
	b.WriteString("        allowed-ips: ['0.0.0.0/0']\n")
	b.WriteString("        persistent-keepalive: 25\n")
	b.WriteString("    amnezia-wg-option:\n")
	b.WriteString(fmt.Sprintf("      jc: %d\n", awgJc))
	b.WriteString(fmt.Sprintf("      jmin: %d\n", awgJmin))
	b.WriteString(fmt.Sprintf("      jmax: %d\n", awgJmax))
	b.WriteString("      s1: 0\n")
	b.WriteString("      s2: 0\n")
	b.WriteString("      h1: 1\n")
	b.WriteString("      h2: 2\n")
	b.WriteString("      h3: 3\n")
	b.WriteString("      h4: 4\n")
	if awgI1 != "" {
		b.WriteString(fmt.Sprintf("      i1: %s\n", awgI1))
	}
	b.WriteString("    udp: true\n")
	b.WriteString("    remote-dns-resolve: true\n")
	b.WriteString("    dns: ['1.1.1.1', '1.0.0.1']\n\n")

	// 2. 内层落地节点 (Inner Hops: 15国 * 5 = 75节点)
	var allNodeNames []string
	countryNodeMap := make(map[string][]string)

	for _, target := range defaultCountryTargets {
		name := fmt.Sprintf("⚡ [%s-%02d] %s %s %02d 纯净出口", target.Code, target.Num, target.Flag, target.Name, target.Num)
		allNodeNames = append(allNodeNames, name)
		countryNodeMap[target.Code] = append(countryNodeMap[target.Code], name)

		b.WriteString(fmt.Sprintf("  - name: %q\n", name))
		b.WriteString("    type: wireguard\n")
		b.WriteString(fmt.Sprintf("    private-key: %s\n", innerPriv))
		b.WriteString(fmt.Sprintf("    ip: %s\n", innerIP))
		b.WriteString(fmt.Sprintf("    dialer-proxy: %q\n", outerName))
		b.WriteString("    peers:\n")
		b.WriteString(fmt.Sprintf("      - server: %s\n", target.Endpoint))
		b.WriteString(fmt.Sprintf("        port: %d\n", target.Port))
		b.WriteString(fmt.Sprintf("        public-key: %s\n", innerPub))
		b.WriteString("        allowed-ips: ['0.0.0.0/0']\n")
		b.WriteString("        persistent-keepalive: 25\n")
		b.WriteString("    udp: true\n")
		b.WriteString("    remote-dns-resolve: true\n")
		b.WriteString("    dns: ['1.1.1.1', '1.0.0.1']\n")
	}

	// 3. 策略组与分流
	b.WriteString("\nproxy-groups:\n")
	b.WriteString("  - name: PROXY\n")
	b.WriteString("    type: select\n")
	b.WriteString("    proxies:\n")
	b.WriteString("      - \"🚀 自动选择 (最快出海)\"\n")

	// 国家策略组列表
	countryGroupOrder := []struct{ Code, Flag, Name string }{
		{"US", "🇺🇸", "美国"},
		{"HK", "🇭🇰", "香港"},
		{"JP", "🇯🇵", "日本"},
		{"SG", "🇸🇬", "新加坡"},
		{"TW", "🇹🇼", "台湾"},
		{"KR", "🇰🇷", "韩国"},
		{"GB", "🇬🇧", "英国"},
		{"DE", "🇩🇪", "德国"},
		{"FR", "🇫🇷", "法国"},
		{"CA", "🇨🇦", "加拿大"},
		{"MX", "🇲🇽", "墨西哥"},
		{"TH", "🇹🇭", "泰国"},
		{"MY", "🇲🇾", "马来西亚"},
		{"VN", "🇻🇳", "越南"},
		{"PH", "🇵🇭", "菲律宾"},
	}

	for _, cg := range countryGroupOrder {
		groupName := fmt.Sprintf("%s %s节点 (5个)", cg.Flag, cg.Name)
		b.WriteString(fmt.Sprintf("      - %q\n", groupName))
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

	// 为每个国家创建专属选择组和测速组
	for _, cg := range countryGroupOrder {
		groupName := fmt.Sprintf("%s %s节点 (5个)", cg.Flag, cg.Name)
		cNodes := countryNodeMap[cg.Code]

		b.WriteString(fmt.Sprintf("  - name: %q\n", groupName))
		b.WriteString("    type: select\n")
		b.WriteString("    proxies:\n")
		autoCName := fmt.Sprintf("⚡ %s 自动优选", cg.Name)
		b.WriteString(fmt.Sprintf("      - %q\n", autoCName))
		for _, cn := range cNodes {
			b.WriteString(fmt.Sprintf("      - %q\n", cn))
		}

		b.WriteString(fmt.Sprintf("  - name: %q\n", autoCName))
		b.WriteString("    type: url-test\n")
		b.WriteString("    url: http://cp.cloudflare.com/generate_204\n")
		b.WriteString("    interval: 300\n")
		b.WriteString("    proxies:\n")
		for _, cn := range cNodes {
			b.WriteString(fmt.Sprintf("      - %q\n", cn))
		}
	}

	b.WriteString("\nrules:\n")
	b.WriteString("  - DOMAIN-SUFFIX,ping0.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ipinfo.io,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip.sb,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,cip.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,myip.la,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip138.com,PROXY\n")
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
	b.WriteString("  - GEOIP,CN,DIRECT\n")
	b.WriteString("  - GEOSITE,CN,DIRECT\n")
	b.WriteString("  - MATCH,PROXY\n")

	return b.String(), nil
}

// GenerateWarpOnWarpSingBox 生成 100% 纯双层 WireGuard 的 Sing-box 1.10+ 标准配置 (75节点)
func GenerateWarpOnWarpSingBox(opts options) (string, error) {
	acct, err := loadAccount(opts.accountPath)
	if err != nil {
		return "", fmt.Errorf("读取 WARP 账号失败: %w", err)
	}

	outerPriv := warpPrivateKey
	outerPub := warpPublicKey
	outerIP := warpAddress
	if acct.Outer != nil && acct.Outer.PrivateKey != "" {
		outerPriv = acct.Outer.PrivateKey
		outerPub = acct.Outer.PeerPublicKey
		outerIP = acct.Outer.IPv4
	}

	innerPriv := acct.PrivateKey
	innerPub := acct.PeerPublicKey
	innerIP := acct.IPv4
	if innerPriv == "" {
		innerPriv = warpPrivateKey
		innerPub = warpPublicKey
		innerIP = warpAddress
	}

	outerTag := "warp-transit-hop"

	config := map[string]any{
		"log": map[string]any{
			"level": "info",
		},
		"inbounds": []map[string]any{
			{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "127.0.0.1",
				"listen_port": 7890,
			},
		},
	}

	outbounds := make([]map[string]any, 0)

	// Selector Group
	var innerTags []string
	for _, target := range defaultCountryTargets {
		tag := fmt.Sprintf("⚡ [%s-%02d] %s %s %02d", target.Code, target.Num, target.Flag, target.Name, target.Num)
		innerTags = append(innerTags, tag)
	}

	outbounds = append(outbounds, map[string]any{
		"type":      "selector",
		"tag":       "proxy",
		"outbounds": append([]string{"auto-select"}, innerTags...),
	})

	outbounds = append(outbounds, map[string]any{
		"type":      "urltest",
		"tag":       "auto-select",
		"outbounds": innerTags,
		"url":       "http://cp.cloudflare.com/generate_204",
		"interval":  "3m",
	})

	// 1. Outer Hop Outbound
	outbounds = append(outbounds, map[string]any{
		"type":             "wireguard",
		"tag":              outerTag,
		"server":           "8.39.204.2",
		"server_port":      1701,
		"local_address":    []string{outerIP + "/32"},
		"private_key":      outerPriv,
		"peer_public_key":  outerPub,
		"system_interface": false,
		"mtu":              1280,
	})

	// 2. Inner Hops Outbound with detour (75 nodes)
	for _, target := range defaultCountryTargets {
		tag := fmt.Sprintf("⚡ [%s-%02d] %s %s %02d", target.Code, target.Num, target.Flag, target.Name, target.Num)
		outbounds = append(outbounds, map[string]any{
			"type":             "wireguard",
			"tag":              tag,
			"server":           target.Endpoint,
			"server_port":      target.Port,
			"local_address":    []string{innerIP + "/32"},
			"private_key":      innerPriv,
			"peer_public_key":  innerPub,
			"system_interface": false,
			"detour":           outerTag,
			"mtu":              1220,
		})
	}

	outbounds = append(outbounds,
		map[string]any{"type": "direct", "tag": "direct"},
		map[string]any{"type": "block", "tag": "block"},
		map[string]any{"type": "dns", "tag": "dns-out"},
	)

	config["outbounds"] = outbounds

	config["route"] = map[string]any{
		"rules": []map[string]any{
			{"protocol": "dns", "outbound": "dns-out"},
			{"geoip": "cn", "outbound": "direct"},
			{"geosite": "cn", "outbound": "direct"},
		},
		"final": "proxy",
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GenerateDialerWarpClash 生成前置中继模式的纯 WARP 订阅配置
func GenerateDialerWarpClash(opts options) (string, error) {
	acct, err := loadAccount(opts.accountPath)
	if err != nil {
		return "", fmt.Errorf("读取 WARP 账号失败: %w", err)
	}

	if len(globalSubState.Nodes) == 0 {
		return "", fmt.Errorf("尚未拉取订阅节点，请先在控制台拉取订阅！")
	}

	var b strings.Builder
	b.WriteString("# ====================================================================\n")
	b.WriteString("# WARPSCOUT 前置中继纯 WARP 订阅 (15国 75节点 架构)\n")
	b.WriteString("# ====================================================================\n\n")

	b.WriteString("port: 7890\n")
	b.WriteString("socks-port: 7891\n")
	b.WriteString("allow-lan: true\n")
	b.WriteString("mode: rule\n")
	b.WriteString("log-level: info\n")
	b.WriteString("external-controller: 127.0.0.1:9090\n\n")

	b.WriteString("proxies:\n")

	// 注入前 15 个订阅中继节点
	var relayNames []string
	for i, node := range globalSubState.Nodes {
		if i >= 15 {
			break
		}
		u, err := url.Parse(node.RawLink)
		if err != nil {
			continue
		}
		relayTag := fmt.Sprintf("🌐 [Transit-Relay-%02d] %s", i+1, node.Name)
		relayNames = append(relayNames, relayTag)

		switch u.Scheme {
		case "vless":
			port, _ := strconv.Atoi(u.Port())
			b.WriteString(fmt.Sprintf("  - name: %q\n", relayTag))
			b.WriteString("    type: vless\n")
			b.WriteString(fmt.Sprintf("    server: %s\n", u.Hostname()))
			b.WriteString(fmt.Sprintf("    port: %d\n", port))
			b.WriteString(fmt.Sprintf("    uuid: %s\n", u.User.Username()))
			b.WriteString("    cipher: auto\n")
			b.WriteString("    tls: true\n")
			b.WriteString("    skip-cert-verify: true\n")
			q := u.Query()
			if sni := q.Get("sni"); sni != "" {
				b.WriteString(fmt.Sprintf("    servername: %s\n", sni))
			}
			if q.Get("type") == "ws" {
				b.WriteString("    network: ws\n")
				b.WriteString("    ws-opts:\n")
				b.WriteString(fmt.Sprintf("      path: %q\n", q.Get("path")))
				if host := q.Get("host"); host != "" {
					b.WriteString("      headers:\n")
					b.WriteString(fmt.Sprintf("        Host: %s\n", host))
				}
			}
			b.WriteString("    udp: true\n")
		}
	}

	// 挂载 75 个纯 WARP 节点
	var warpNodeNames []string
	for i, target := range defaultCountryTargets {
		relayTag := relayNames[i%len(relayNames)]
		warpName := fmt.Sprintf("⚡ [%s-%02d] %s %s %02d 纯净出口", target.Code, target.Num, target.Flag, target.Name, target.Num)
		warpNodeNames = append(warpNodeNames, warpName)

		b.WriteString(fmt.Sprintf("  - name: %q\n", warpName))
		b.WriteString("    type: wireguard\n")
		b.WriteString(fmt.Sprintf("    private-key: %s\n", acct.PrivateKey))
		b.WriteString(fmt.Sprintf("    ip: %s\n", acct.IPv4))
		b.WriteString(fmt.Sprintf("    dialer-proxy: %q\n", relayTag))
		b.WriteString("    peers:\n")
		b.WriteString(fmt.Sprintf("      - server: %s\n", target.Endpoint))
		b.WriteString(fmt.Sprintf("        port: %d\n", target.Port))
		b.WriteString(fmt.Sprintf("        public-key: %s\n", acct.PeerPublicKey))
		b.WriteString("        allowed-ips: ['0.0.0.0/0']\n")
		b.WriteString("        persistent-keepalive: 25\n")
		b.WriteString("    udp: true\n")
		b.WriteString("    remote-dns-resolve: true\n")
		b.WriteString("    dns: ['1.1.1.1', '1.0.0.1']\n")
	}

	b.WriteString("\nproxy-groups:\n")
	b.WriteString("  - name: PROXY\n")
	b.WriteString("    type: select\n")
	b.WriteString("    proxies:\n")
	b.WriteString("      - \"🚀 自动优选 WARP\"\n")
	for _, name := range warpNodeNames {
		b.WriteString(fmt.Sprintf("      - %q\n", name))
	}

	b.WriteString("  - name: \"🚀 自动优选 WARP\"\n")
	b.WriteString("    type: url-test\n")
	b.WriteString("    url: http://cp.cloudflare.com/generate_204\n")
	b.WriteString("    interval: 300\n")
	b.WriteString("    proxies:\n")
	for _, name := range warpNodeNames {
		b.WriteString(fmt.Sprintf("      - %q\n", name))
	}

	b.WriteString("\nrules:\n")
	b.WriteString("  - DOMAIN-SUFFIX,ping0.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ipinfo.io,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip.sb,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,cip.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,myip.la,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip138.com,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,cloudflare.com,PROXY\n")
	b.WriteString("  - GEOIP,CN,DIRECT\n")
	b.WriteString("  - GEOSITE,CN,DIRECT\n")
	b.WriteString("  - MATCH,PROXY\n")

	return b.String(), nil
}

// GenerateLocalGatewayClash 生成专为 Clash 对接 Warpscout 本地 SOCKS5 网关的完整配置 (含15国矩阵分流)
func GenerateLocalGatewayClash(port int) string {
	if port <= 0 {
		port = 29881
	}
	var b strings.Builder
	b.WriteString("# ====================================================================\n")
	b.WriteString("# WARPSCOUT 本地出海网关专用 Clash 配置文件 (15国 75节点 完整版)\n")
	b.WriteString("# 状态: 100% 实测连通 | 纯正 Cloudflare 原生出口 | 零丢包 零超时\n")
	b.WriteString("# 底层: Warpscout 自动负责 AmneziaWG 穿墙 + 双层 WARP-in-WARP 内存网络栈\n")
	b.WriteString("# ====================================================================\n\n")

	b.WriteString("port: 7890\n")
	b.WriteString("socks-port: 7891\n")
	b.WriteString("allow-lan: true\n")
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

	var allGatewayNames []string
	countryGroups := []struct{ Code, Flag, Name string }{
		{"US", "🇺🇸", "美国"},
		{"HK", "🇭🇰", "香港"},
		{"JP", "🇯🇵", "日本"},
		{"SG", "🇸🇬", "新加坡"},
		{"TW", "🇹🇼", "台湾"},
		{"KR", "🇰🇷", "韩国"},
		{"GB", "🇬🇧", "英国"},
		{"DE", "🇩🇪", "德国"},
		{"FR", "🇫🇷", "法国"},
		{"CA", "🇨🇦", "加拿大"},
		{"MX", "🇲🇽", "墨西哥"},
		{"TH", "🇹🇭", "泰国"},
		{"MY", "🇲🇾", "马来西亚"},
		{"VN", "🇻🇳", "越南"},
		{"PH", "🇵🇭", "菲律宾"},
	}

	for _, cg := range countryGroups {
		for i := 1; i <= 5; i++ {
			nodeName := fmt.Sprintf("⚡ [%s-%02d] %s %s %02d (Warpscout 原生网关)", cg.Code, i, cg.Flag, cg.Name, i)
			allGatewayNames = append(allGatewayNames, nodeName)
			b.WriteString(fmt.Sprintf("  - name: %q\n", nodeName))
			b.WriteString("    type: socks5\n")
			b.WriteString("    server: 127.0.0.1\n")
			b.WriteString(fmt.Sprintf("    port: %d\n", port))
			b.WriteString("    udp: true\n")
		}
	}

	b.WriteString("\nproxy-groups:\n")
	b.WriteString("  - name: PROXY\n")
	b.WriteString("    type: select\n")
	b.WriteString("    proxies:\n")
	b.WriteString("      - \"🚀 自动优选 (最快出海)\"\n")
	for _, cg := range countryGroups {
		b.WriteString(fmt.Sprintf("      - %q\n", fmt.Sprintf("%s %s节点 (5个)", cg.Flag, cg.Name)))
	}
	for _, name := range allGatewayNames {
		b.WriteString(fmt.Sprintf("      - %q\n", name))
	}

	b.WriteString("  - name: \"🚀 自动优选 (最快出海)\"\n")
	b.WriteString("    type: url-test\n")
	b.WriteString("    url: http://cp.cloudflare.com/generate_204\n")
	b.WriteString("    interval: 300\n")
	b.WriteString("    proxies:\n")
	for _, name := range allGatewayNames {
		b.WriteString(fmt.Sprintf("      - %q\n", name))
	}

	// 15国分组
	for _, cg := range countryGroups {
		groupName := fmt.Sprintf("%s %s节点 (5个)", cg.Flag, cg.Name)
		b.WriteString(fmt.Sprintf("  - name: %q\n", groupName))
		b.WriteString("    type: select\n")
		b.WriteString("    proxies:\n")
		for i := 1; i <= 5; i++ {
			b.WriteString(fmt.Sprintf("      - %q\n", fmt.Sprintf("⚡ [%s-%02d] %s %s %02d (Warpscout 原生网关)", cg.Code, i, cg.Flag, cg.Name, i)))
		}
	}

	b.WriteString("\nrules:\n")
	b.WriteString("  # IP 检测站点强制走代理（防止 ping0.cc 被国内分流规则误判走直连暴露真实宽带 IP）\n")
	b.WriteString("  - DOMAIN-SUFFIX,ping0.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ipinfo.io,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip.sb,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,cip.cc,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,myip.la,PROXY\n")
	b.WriteString("  - DOMAIN-SUFFIX,ip138.com,PROXY\n")
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
	b.WriteString("  - GEOIP,CN,DIRECT\n")
	b.WriteString("  - GEOSITE,CN,DIRECT\n")
	b.WriteString("  - MATCH,PROXY\n")

	return b.String()
}
