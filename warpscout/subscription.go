package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SubNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`     // socks5, http, ss, vmess, trojan, vless
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Country  string `json:"country"`  // 识别出的地区（如 HK, JP, US, SG 等）
	ProxyURL string `json:"proxy_url"` // 可直接使用的代理 URL
	RawLink  string `json:"raw_link"`  // 原始协议链接
}

type SubState struct {
	URL        string    `json:"url"`
	UpdateTime time.Time `json:"update_time"`
	Nodes      []SubNode `json:"nodes"`
	ActiveNode *SubNode  `json:"active_node"`
}

var (
	globalSubState = &SubState{
		Nodes: make([]SubNode, 0),
	}
	singBoxCmd *exec.Cmd
	singBoxMu  sync.Mutex
)

const singBoxLocalPort = 29882

func stopSingBoxNode() {
	singBoxMu.Lock()
	defer singBoxMu.Unlock()
	if singBoxCmd != nil && singBoxCmd.Process != nil {
		_ = singBoxCmd.Process.Kill()
		_ = singBoxCmd.Wait()
		singBoxCmd = nil
	}
}

func startSingBoxNode(node *SubNode) (string, error) {
	stopSingBoxNode()

	if node.RawLink == "" {
		if node.ProxyURL != "" {
			return node.ProxyURL, nil
		}
		return "", fmt.Errorf("节点无可用协议链接或代理地址")
	}

	u, err := url.Parse(node.RawLink)
	if err != nil {
		return "", fmt.Errorf("解析节点 URL 失败: %w", err)
	}

	config := map[string]any{
		"log": map[string]any{
			"level": "warn",
		},
		"inbounds": []map[string]any{
			{
				"type": "mixed",
				"tag":  "mixed-in",
				"listen": "127.0.0.1",
				"listen_port": singBoxLocalPort,
			},
		},
	}

	var outbound map[string]any

	switch strings.ToLower(u.Scheme) {
	case "vless":
		uuid := u.User.Username()
		q := u.Query()
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 443
		}

		vlessOut := map[string]any{
			"type": "vless",
			"tag": "proxy",
			"server": u.Hostname(),
			"server_port": port,
			"uuid": uuid,
		}

		if q.Get("security") == "tls" || q.Get("tls") == "true" {
			tlsConf := map[string]any{
				"enabled": true,
			}
			if sni := q.Get("sni"); sni != "" {
				tlsConf["server_name"] = sni
			} else if host := q.Get("host"); host != "" {
				tlsConf["server_name"] = host
			}
			if fp := q.Get("fp"); fp != "" {
				tlsConf["utls"] = map[string]any{
					"enabled": true,
					"fingerprint": fp,
				}
			}
			vlessOut["tls"] = tlsConf
		}

		if q.Get("type") == "ws" {
			wsConf := map[string]any{
				"type": "ws",
			}
			if path := q.Get("path"); path != "" {
				wsConf["path"] = path
			}
			if host := q.Get("host"); host != "" {
				wsConf["headers"] = map[string]string{"Host": host}
			}
			vlessOut["transport"] = wsConf
		}
		outbound = vlessOut

	case "socks5", "socks", "http":
		port, _ := strconv.Atoi(u.Port())
		outbound = map[string]any{
			"type": u.Scheme,
			"tag": "proxy",
			"server": u.Hostname(),
			"server_port": port,
		}

	default:
		if node.ProxyURL != "" {
			return node.ProxyURL, nil
		}
		return "", fmt.Errorf("暂不支持该协议 (%s) 的自动 sing-box 转换", u.Scheme)
	}

	config["outbounds"] = []map[string]any{outbound}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成 sing-box 配置失败: %w", err)
	}

	tmpFile := "/tmp/sing-box-exit.json"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return "", fmt.Errorf("写入 sing-box 配置失败: %w", err)
	}

	singBoxMu.Lock()
	cmd := exec.Command("sing-box", "run", "-c", tmpFile)
	if err := cmd.Start(); err != nil {
		singBoxMu.Unlock()
		return "", fmt.Errorf("启动 sing-box 失败: %w", err)
	}
	singBoxCmd = cmd
	singBoxMu.Unlock()

	time.Sleep(500 * time.Millisecond)

	localProxy := fmt.Sprintf("socks5://127.0.0.1:%d", singBoxLocalPort)
	return localProxy, nil
}

func detectCountryFromText(name string) string {
	upper := strings.ToUpper(name)
	switch {
	case strings.Contains(upper, "香港") || strings.Contains(upper, "HK") || strings.Contains(upper, "HONG KONG"):
		return "HK"
	case strings.Contains(upper, "日本") || strings.Contains(upper, "JP") || strings.Contains(upper, "JAPAN") || strings.Contains(upper, "东京") || strings.Contains(upper, "大阪") || strings.Contains(upper, "NRT"):
		return "JP"
	case strings.Contains(upper, "新加坡") || strings.Contains(upper, "SG") || strings.Contains(upper, "SINGAPORE") || strings.Contains(upper, "狮城") || strings.Contains(upper, "SIN"):
		return "SG"
	case strings.Contains(upper, "美国") || strings.Contains(upper, "US") || strings.Contains(upper, "UNITED STATES") || strings.Contains(upper, "美") || strings.Contains(upper, "洛杉矶") || strings.Contains(upper, "LAX") || strings.Contains(upper, "SJC"):
		return "US"
	case strings.Contains(upper, "台湾") || strings.Contains(upper, "TW") || strings.Contains(upper, "TAIWAN"):
		return "TW"
	case strings.Contains(upper, "韩国") || strings.Contains(upper, "KR") || strings.Contains(upper, "KOREA") || strings.Contains(upper, "首尔") || strings.Contains(upper, "ICN"):
		return "KR"
	case strings.Contains(upper, "英国") || strings.Contains(upper, "UK") || strings.Contains(upper, "GB") || strings.Contains(upper, "LONDON") || strings.Contains(upper, "LHR"):
		return "UK"
	case strings.Contains(upper, "德国") || strings.Contains(upper, "DE") || strings.Contains(upper, "GERMANY") || strings.Contains(upper, "法兰克福") || strings.Contains(upper, "FRA"):
		return "DE"
	default:
		return "OTHER"
	}
}

func fetchSubscription(ctx context.Context, subURL string) ([]SubNode, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "ClashMeta/v1.18.0 warpscout/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取订阅失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("订阅服务器返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取订阅内容失败: %w", err)
	}

	content := string(body)
	nodes := parseSubscriptionContent(content)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("未能从订阅内容中解析出有效节点（支持 Base64 / Clash YAML / 协议直链）")
	}

	return nodes, nil
}

func parseSubscriptionContent(content string) []SubNode {
	content = strings.TrimSpace(content)
	cleanB64 := strings.ReplaceAll(strings.ReplaceAll(content, "\r", ""), "\n", "")
	if decoded, err := base64.StdEncoding.DecodeString(cleanB64); err == nil && len(decoded) > 0 {
		if nodes := parseURLLinks(string(decoded)); len(nodes) > 0 {
			return nodes
		}
	}
	if decoded, err := base64.URLEncoding.DecodeString(cleanB64); err == nil && len(decoded) > 0 {
		if nodes := parseURLLinks(string(decoded)); len(nodes) > 0 {
			return nodes
		}
	}

	if nodes := parseURLLinks(content); len(nodes) > 0 {
		return nodes
	}

	return parseClashProxies(content)
}

func parseURLLinks(text string) []SubNode {
	var nodes []SubNode
	scanner := bufio.NewScanner(strings.NewReader(text))
	idx := 1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		u, err := url.Parse(line)
		if err != nil {
			continue
		}

		node := SubNode{
			ID:      fmt.Sprintf("node_%d", idx),
			Type:    strings.ToLower(u.Scheme),
			RawLink: line,
		}

		switch u.Scheme {
		case "socks5", "socks", "http", "https":
			node.Server = u.Hostname()
			if p, err := strconv.Atoi(u.Port()); err == nil {
				node.Port = p
			}
			name := u.Fragment
			if name == "" {
				name = fmt.Sprintf("%s-%s:%s", strings.ToUpper(u.Scheme), node.Server, u.Port())
			} else if decoded, err := url.QueryUnescape(name); err == nil {
				name = decoded
			}
			node.Name = name
			node.Country = detectCountryFromText(node.Name)
			node.ProxyURL = fmt.Sprintf("%s://%s:%d", u.Scheme, node.Server, node.Port)
			nodes = append(nodes, node)
			idx++

		case "vless", "trojan", "ss":
			name := u.Fragment
			if decoded, err := url.QueryUnescape(name); err == nil && decoded != "" {
				name = decoded
			} else {
				name = fmt.Sprintf("%s-%s", strings.ToUpper(u.Scheme), u.Host)
			}
			node.Name = name
			node.Server = u.Hostname()
			if p, err := strconv.Atoi(u.Port()); err == nil {
				node.Port = p
			}
			node.Country = detectCountryFromText(node.Name)
			nodes = append(nodes, node)
			idx++

		case "vmess":
			b64Str := strings.TrimPrefix(line, "vmess://")
			if decoded, err := base64.StdEncoding.DecodeString(b64Str); err == nil {
				var v struct {
					PS   string      `json:"ps"`
					Add  string      `json:"add"`
					Port interface{} `json:"port"`
				}
				if json.Unmarshal(decoded, &v) == nil {
					node.Name = v.PS
					node.Server = v.Add
					switch p := v.Port.(type) {
					case float64:
						node.Port = int(p)
					case string:
						node.Port, _ = strconv.Atoi(p)
					}
					node.Country = detectCountryFromText(node.Name)
					nodes = append(nodes, node)
					idx++
				}
			}
		}
	}

	return nodes
}

func parseClashProxies(yamlText string) []SubNode {
	var nodes []SubNode
	scanner := bufio.NewScanner(strings.NewReader(yamlText))
	inProxies := false
	idx := 1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "proxies:") {
			inProxies = true
			continue
		}
		if inProxies && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && line != "" {
			break
		}

		if inProxies && strings.HasPrefix(line, "-") {
			// 单行 JSON/YAML 字典风格: - {name: ..., server: ..., port: ...}
			name := extractYAMLVal(line, "name:")
			server := extractYAMLVal(line, "server:")
			portStr := extractYAMLVal(line, "port:")
			protoType := extractYAMLVal(line, "type:")
			uuid := extractYAMLVal(line, "uuid:")
			tls := extractYAMLVal(line, "tls:")
			sni := extractYAMLVal(line, "servername:")
			if sni == "" {
				sni = extractYAMLVal(line, "sni:")
			}
			fp := extractYAMLVal(line, "client-fingerprint:")
			netType := extractYAMLVal(line, "network:")
			wsPath := extractYAMLVal(line, "path:")
			wsHost := extractYAMLVal(line, "Host:")

			if name == "" || server == "" || protoType == "" {
				continue
			}

			port, _ := strconv.Atoi(portStr)
			if port == 0 {
				port = 443
			}

			var rawLink string
			if strings.ToLower(protoType) == "vless" && uuid != "" {
				rawLink = fmt.Sprintf("vless://%s@%s:%d?security=%s&type=%s&host=%s&fp=%s&sni=%s&path=%s#%s",
					uuid, server, port,
					map[bool]string{true: "tls", false: "none"}[tls == "true"],
					netType,
					url.QueryEscape(wsHost),
					fp,
					url.QueryEscape(sni),
					url.QueryEscape(wsPath),
					url.QueryEscape(name),
				)
			} else if strings.ToLower(protoType) == "socks5" || strings.ToLower(protoType) == "http" {
				rawLink = fmt.Sprintf("%s://%s:%d#%s", protoType, server, port, url.QueryEscape(name))
			}

			node := SubNode{
				ID:       fmt.Sprintf("node_%d", idx),
				Name:     name,
				Type:     protoType,
				Server:   server,
				Port:     port,
				Country:  detectCountryFromText(name),
				RawLink:  rawLink,
			}
			nodes = append(nodes, node)
			idx++
		}
	}

	return nodes
}

func extractYAMLVal(line, key string) string {
	idx := strings.Index(line, key)
	if idx == -1 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+len(key):])
	end := strings.IndexAny(rest, ",}\n#")
	if end != -1 {
		rest = rest[:end]
	}
	return strings.Trim(strings.TrimSpace(rest), "\"'")
}
