import os
import sys
import json
import time
import subprocess
import ctypes
import signal

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

BASE_DIR = r"C:\Tools2\warp\Aether-Desktop\Aether"
ENGINE_DIR = os.path.join(BASE_DIR, "engine")
AETHER_BIN = os.path.join(ENGINE_DIR, "aether.exe")
PSIPHON_BIN = os.path.join(ENGINE_DIR, "psiphon-tunnel-core.exe")
SERVER_LIST = os.path.join(ENGINE_DIR, "server_entries.txt")
DATA_DIR = os.path.join(BASE_DIR, "data")

SUPPORTED_COUNTRIES = {
    "US": ("美国", 29891),
    "JP": ("日本", 29892),
    "FR": ("法国", 29893),
    "DE": ("德国", 29894),
    "GB": ("英国", 29895),
    "SG": ("新加坡", 29896),
    "CA": ("加拿大", 29897),
    "NL": ("荷兰", 29898)
}

PROCESS_TERMINATE = 1

def kill_by_name(name):
    try:
        out = subprocess.run(["tasklist", "/FO", "CSV", "/NH"], capture_output=True, text=True).stdout
        for line in out.splitlines():
            parts = [p.strip('"') for p in line.split(',')]
            if len(parts) >= 2 and parts[0].lower() == name.lower():
                try:
                    pid = int(parts[1])
                    h = ctypes.windll.kernel32.OpenProcess(PROCESS_TERMINATE, False, pid)
                    if h:
                        ctypes.windll.kernel32.TerminateProcess(h, 0)
                        ctypes.windll.kernel32.CloseHandle(h)
                except Exception:
                    pass
    except Exception:
        pass

def stop_all():
    print("[*] 正在清理后台核心进程...")
    kill_by_name("psiphon-tunnel-core.exe")
    kill_by_name("aether.exe")
    time.sleep(1)
    print("[+] 旧进程清理完毕。")

def generate_psiphon_config(country_code, port):
    country_data_dir = os.path.join(DATA_DIR, f"psiphon_{country_code.lower()}")
    os.makedirs(country_data_dir, exist_ok=True)
    cfg = {
        "PropagationChannelId": "FFFFFFFFFFFFFFFF",
        "SponsorId": "1111111111111111",
        "EstablishTunnelTimeoutSeconds": 0,  # 0 = 不断重试，永不超时退出
        "DataRootDirectory": country_data_dir,
        "LocalSocksProxyPort": port,
        "DisableLocalHTTPProxy": True,
        "RemoteServerListSignaturePublicKey": "MIICIDANBgkqhkiG9w0BAQEFAAOCAg0AMIICCAKCAgEAt7Ls+/39r+T6zNW7GiVpJfzq/xvL9SBH5rIFnk0RXYEYavax3WS6HOD35eTAqn8AniOwiH+DOkvgSKF2caqk/y1dfq47Pdymtwzp9ikpB1C5OfAysXzBiwVJlCdajBKvBZDerV1cMvRzCKvKwRmvDmHgphQQ7WfXIGbRbmmk6opMBh3roE42KcotLFtqp0RRwLtcBRNtCdsrVsjiI1Lqz/lH+T61sGjSjQ3CHMuZYSQJZo/KrvzgQXpkaCTdbObxHqb6/+i1qaVOfEsvjoiyzTxJADvSytVtcTjijhPEV6XskJVHE1Zgl+7rATr/pDQkw6DPCNBS1+Y6fy7GstZALQXwEDN/qhQI9kWkHijT8ns+i1vGg00Mk/6J75arLhqcodWsdeG/M/moWgqQAnlZAGVtJI1OgeF5fsPpXu4kctOfuZlGjVZXQNW34aOzm8r8S0eVZitPlbhcPiR4gT/aSMz/wd8lZlzZYsje/Jr8u/YtlwjjreZrGRmG8KMOzukV3lLmMppXFMvl4bxv6YFEmIuTsOhbLTwFgh7KYNjodLj/LsqRVfwz31PgWQFTEPICV7GCvgVlPRxnofqKSjgTWI4mxDhBpVcATvaoBl1L/6WLbFvBsoAUBItWwctO2xalKxF5szhGm8lccoc5MZr8kfE0uxMgsxz4er68iCID+rsCAQM=",
        "ServerEntrySignaturePublicKeys": [
            "HuUVTWaRyh5pZwy4UguSgkwmBe0EHtJJkoF5WrxmvA="
        ],
        "ExchangeObfuscationKey": "DpXzloJk1Hw6aSzmKKky0xcahsEHubch81Mi6K0XMlU=",
        "EmitBytesTransferred": False,
        "EmitDiagnosticNotices": False,
        "ConnectionWorkerPoolSize": 8,
        "DNSResolverPreferredAlternateServers": ["9.9.9.9:53"],
        "DNSResolverPreferAlternateServerProbability": 0.8,
        "EstablishTunnelServerAffinityGracePeriodMilliseconds": 300000,
        "UpstreamProxyUrl": "socks5://127.0.0.1:1819",
        "EgressRegion": country_code.upper()
    }
    cfg_path = os.path.join(country_data_dir, "config.json")
    with open(cfg_path, "w", encoding="utf-8") as f:
        json.dump(cfg, f, indent=2)
    return cfg_path, country_data_dir

def export_configs():
    proxies = []
    proxy_names = []
    for c_code, (c_name, port) in SUPPORTED_COUNTRIES.items():
        node_name = f"[纯净WARP] {c_name} ({c_code})"
        proxy_names.append(node_name)
        proxies.append(f"""  - name: "{node_name}"
    type: socks5
    server: 127.0.0.1
    port: {port}
    udp: true""")

    proxy_list_str = "\n".join(f'      - "{name}"' for name in proxy_names)
    proxies_str = "\n".join(proxies)
    clash_content = f"""port: 7890
socks-port: 7891
mixed-port: 7892
allow-lan: false
mode: rule
log-level: info

proxies:
{proxies_str}

proxy-groups:
  - name: "节点选择"
    type: select
    proxies:
{proxy_list_str}
      - DIRECT

  - name: "自动优选"
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    tolerance: 50
    proxies:
{proxy_list_str}

rules:
  - MATCH,节点选择
"""
    clash_path = r"C:\Tools2\warp\warpscout-pure-clash.yaml"
    with open(clash_path, "w", encoding="utf-8") as f:
        f.write(clash_content)
    print(f"[+] 纯净 Clash 配置已生成: {clash_path}")

    outbounds = []
    outbound_tags = []
    for c_code, (c_name, port) in SUPPORTED_COUNTRIES.items():
        tag = f"[纯净WARP] {c_name} ({c_code})"
        outbound_tags.append(tag)
        outbounds.append({
            "type": "socks",
            "tag": tag,
            "server": "127.0.0.1",
            "server_port": port
        })
    
    sb_cfg = {
        "log": {"level": "info"},
        "inbounds": [
            {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080}
        ],
        "outbounds": [
            {
                "type": "selector",
                "tag": "select",
                "outbounds": outbound_tags + ["direct"]
            },
            {
                "type": "urltest",
                "tag": "auto",
                "outbounds": outbound_tags,
                "url": "http://www.gstatic.com/generate_204",
                "interval": "5m"
            }
        ] + outbounds + [{"type": "direct", "tag": "direct"}],
        "route": {
            "rules": [
                {"outbound": "select"}
            ]
        }
    }
    singbox_path = r"C:\Tools2\warp\warpscout-pure-singbox.json"
    with open(singbox_path, "w", encoding="utf-8") as f:
        json.dump(sb_cfg, f, ensure_ascii=False, indent=2)
    print(f"[+] 纯净 Sing-box 配置已生成: {singbox_path}")

def test_socks_proxy(port):
    try:
        res = subprocess.run(
            ["curl.exe", "-x", f"socks5h://127.0.0.1:{port}", "http://ip-api.com/json", "-m", "4", "-s"],
            capture_output=True,
            text=True
        )
        if res.returncode == 0 and "query" in res.stdout:
            data = json.loads(res.stdout)
            return True, data.get("query", ""), data.get("country", "")
    except Exception:
        pass
    return False, "", ""

def launch_psiphon_country(c_code, c_name, port):
    cfg_path, c_data_dir = generate_psiphon_config(c_code, port)
    psi_cmd = [
        PSIPHON_BIN,
        "-config", cfg_path,
        "-serverList", SERVER_LIST,
        "-dataRootDirectory", c_data_dir,
        "-formatNotices"
    ]
    log_f = open(os.path.join(c_data_dir, "psiphon.log"), "w", encoding="utf-8")
    proc = subprocess.Popen(psi_cmd, stdout=log_f, stderr=subprocess.STDOUT, cwd=BASE_DIR)
    return proc

def main():
    stop_all()
    os.makedirs(DATA_DIR, exist_ok=True)
    export_configs()

    # 1. 启动底层 Aether (MASQUE QUIC 破墙跳板)
    print("\n[Step 1/3] 正在启动底层 Aether MASQUE 破墙引擎 (端口 1819)...")
    aether_cmd = [
        AETHER_BIN,
        "--masque",
        "--turbo",
        "-4",
        "--noize", "firewall",
        "--bind", "127.0.0.1:1819"
    ]
    proc_aether = subprocess.Popen(
        aether_cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        cwd=BASE_DIR
    )

    aether_ready = False
    start_t = time.time()
    while time.time() - start_t < 15:
        line = proc_aether.stdout.readline()
        if not line:
            time.sleep(0.5)
            continue
        line_s = line.strip()
        if "socks5 server listening" in line_s or "socks5 listening" in line_s:
            aether_ready = True
            break
        if "selected MASQUE gateway" in line_s or "using cloudflare edge" in line_s:
            print(f"  -> {line_s}")

    if not aether_ready:
        print("[!] 正在等待 Aether 探测最佳 Cloudflare 边缘...")
        time.sleep(3)

    print("[+] Aether MASQUE 破墙底座就绪！(本地跳板: 127.0.0.1:1819)")

    # 2. 依次拉起各国家 Psiphon 核心
    print("\n[Step 2/3] 正在启动各国家专属出口通道...")
    psi_procs = {}
    for c_code, (c_name, port) in SUPPORTED_COUNTRIES.items():
        proc = launch_psiphon_country(c_code, c_name, port)
        psi_procs[c_code] = proc
        print(f"  [*] 已拉起出口: {c_name} ({c_code}) -> 本地端口 {port}")
        time.sleep(0.8)

    # 3. 实时健康探测与保活监控
    print("\n[Step 3/3] 正在探测各国家出口链路连通性 (预计需要 10-20 秒)...")
    connected_status = {}
    
    probe_start = time.time()
    while time.time() - probe_start < 45:
        for c_code, (c_name, port) in SUPPORTED_COUNTRIES.items():
            if c_code in connected_status:
                continue
            ok, ip, country = test_socks_proxy(port)
            if ok:
                connected_status[c_code] = (ip, country)
                print(f"  [OK] [{c_name} {c_code}] 连通成功! 真实出口 IP: {ip} ({country}) [端口: {port}]")
        if len(connected_status) == len(SUPPORTED_COUNTRIES):
            break
        time.sleep(2)

    print("\n" + "=" * 65)
    print(" 纯净 WARP 多国出口核心已全线就绪！")
    print(f" 当前已建立出口: {len(connected_status)}/{len(SUPPORTED_COUNTRIES)} 个国家")
    for code, (ip, country) in connected_status.items():
        cname, port = SUPPORTED_COUNTRIES[code]
        print(f"   - {cname} ({code}) [Port {port}]: {ip} ({country})")
    print("\n 本地配置文件：")
    print(r"  - Clash:    C:\Tools2\warp\warpscout-pure-clash.yaml")
    print(r"  - Sing-box: C:\Tools2\warp\warpscout-pure-singbox.json")
    print("\n 请保持当前控制台窗口开启！按 Ctrl+C 即可安全退出并停止所有后台通道。")
    print("=" * 65)

    try:
        while True:
            # 持续保活监控：如果某个子进程挂了，自动重启
            for c_code, (c_name, port) in SUPPORTED_COUNTRIES.items():
                p = psi_procs.get(c_code)
                if p and p.poll() is not None:
                    # 意外退出，重新拉起
                    print(f"[*] 检测到 {c_name} ({c_code}) 进程已停止，正在自动重启...")
                    psi_procs[c_code] = launch_psiphon_country(c_code, c_name, port)
            time.sleep(5)
    except KeyboardInterrupt:
        print("\n[*] 正在退出并停止服务...")
    finally:
        for _, p in psi_procs.items():
            try:
                p.terminate()
            except Exception:
                pass
        try:
            proc_aether.terminate()
        except Exception:
            pass
        stop_all()

if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "stop":
        stop_all()
    elif len(sys.argv) > 1 and sys.argv[1] == "export":
        export_configs()
    else:
        main()
