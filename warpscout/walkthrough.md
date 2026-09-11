# WARPSCOUT 方案 B（GitHub Actions + Cloudflare Pages）改造完成报告

## 1. 核心问题与架构设计

### 核心背景
- **问题根源**：本地运行探测时，国内三大运营商发出的原始 UDP 数据包受 Anycast 广播路由机制影响，必然优先通过太平洋海底光缆路由至美西节点（洛杉矶 LAX / 圣何塞 SJC），导致测出的节点出口 IP 永远是美国（US）。
- **平台限制**：Cloudflare Pages / Workers 属于轻量 V8 沙箱引擎，不支持底层的 Raw UDP 套接字与高并发 WireGuard 握手协议，无法在 Workers 内直接运行 Go 测速引擎。
- **方案 B 落地机制**：
  1. **自动化探测端（GitHub Actions）**：利用 GitHub Actions 的全球原生公网环境（海外 Azure 多区域数据中心），向 Cloudflare Anycast 发起底层 UDP 探测，真实获取日本（NRT）、香港（HKG）、新加坡（SIN）、欧洲（FRA）等非美西机房节点。
  2. **静态发布端（Cloudflare Pages）**：将扫描生成的结构化数据（`results.json`）、订阅配置（`clash-sub.yaml`）、WireGuard 配置（`wireguard.conf`）以及现代暗黑玻璃拟态面板推送到 Cloudflare Pages 全球 CDN。

---

## 2. 完成的改动与新增模块

### 2.1 后端测速与静态导出命令 (`pages.go`, `flags.go`, `cli.go`)
- 新增 `warpscout pages` 子命令：
  - `-o, -out-dir`：输出目录（默认 `public`）
  - `-n, -sample`：每子网采样端点数
  - `-p, -proto`：探测协议（默认 `awg`，支持 `wg`）
  - `-t, -timeout`：探测超时时间
  - `-auto-register`：若本地无账号文件，自动向 Cloudflare 申请注册新账号（保证 GitHub Actions 零预配置运行）
- 自动生成 5 项静态资产：
  - `public/data/results.json`：包含扫描时间戳、最优延迟、地区分布、端点详细指标（IP、端口、延迟、丢包、Colo、城市、国家等）。
  - `public/data/clash-sub.yaml`：全量 Clash Meta / Mihomo 配置文件，内置 `⚡ 自动优选`、国家分组（日本/香港/美国等）以及手动选择组。
  - `public/data/clash-provider.yaml`：纯 Proxy Provider 列表，供现有配置引用。
  - `public/data/wireguard.conf`：包含前 20 个最优端点的合并 WireGuard 配置。
  - `public/data/endpoints.txt`：纯 `IP:Port` 列表。

### 2.2 现代化静态 Web 面板 (`public/index.html`)
- **视觉风格**：深邃暗黑玻璃拟态质感，响应式设计，完美契合 Cloudflare 橙 + 霓虹蓝紫渐变。
- **实时统计卡片**：展示可用端点数、最优隧道延迟、协议类型、地区覆盖分布及最后更新时间。
- **Clash 订阅中心**：
  - 自动根据当前 Pages 访问域名显示完整订阅地址：`https://<pages-domain>/data/clash-sub.yaml`。
  - 一键复制订阅链接 (`navigator.clipboard`)。
  - 一键拉起导入客户端 (`clash://install-config?url=...`)。
  - 快捷下载 `clash-sub.yaml`、`wireguard.conf`、`endpoints.txt`。
- **多区域即时筛选**：
  - 动态标签栏：🌐 全部、🇯🇵 日本、🇭🇰 香港、🇸🇬 新加坡、🇪🇺 欧洲、🇺🇸 美国 等。
  - 关键字搜索：实时过滤 IP、端口、Colo（如 NRT/HKG/LAX）。
- **多选与批量导出浮动栏**：
  - 表格每一行支持独立勾选，表头支持全选/反选。
  - 选中 1 个或多个端点后，底部浮现玻璃拟态操作栏。
  - 支持 **批量导出 Clash 配置**、**批量导出 WireGuard 配置**、**批量复制 IP 列表**、**导出选中 JSON**。
  - 弹窗展示高亮代码预览，支持“保存为文件”与“一键复制全部”。
- **零服务端依赖**：所有单节点与批量配置生成均在前端纯 JS 毫秒级运算完成。

### 2.3 自动化 GitHub Actions 工作流 (`.github/workflows/warpscout-pages.yml`)
- **触发机制**：
  - 定时触发：默认每 6 小时自动执行一次全球机房探测 (`0 */6 * * *`)。
  - 手动触发 (`workflow_dispatch`)：支持在 GitHub Actions 页面直接传参（采样数、协议、指定 IP 目标池）。
- **部署方式**：
  - 自动推送到 `gh-pages` 分支（用于与 Cloudflare Pages 仓库集成）。
  - 可选直接通过 Cloudflare API Token 发布。

### 2.4 部署文档 (`DEPLOY_PAGES.md`)
- 详述两步极速部署流程，手把手指导用户在 Cloudflare Dashboard 绑定仓库并完成上线。

---

## 3. 本地编译与验证结果

- 在 Linux Alpine / Go 1.26 环境下完成全量代码编译与二进制打包（`warpscout-bin`）。
- 运行 `warpscout pages -o public -n 1 -t 2 -p awg` 验证：
  - Phase 1 & Phase 2 探测正常执行；
  - 耗时约 13 秒；
  - 正确生成 `public/index.html` 以及 `public/data/` 下的所有配置文件。

---

## 4. 节点独立出口 IP 改造（多账户轮换池）

### 4.1 背景与问题
- **现象**：在 Clash 客户端中选择多个不同的 Cloudflare WARP / MASQUE 节点时，访问 ping0 测试出口 IP 全部显示相同的 IP（如 `104.28.201.73`）。
- **根因**：Cloudflare 是按客户端绑定的 **WARP 账户私钥与 Device ID** 来分配 NAT 出口 IP 的。单个账号即使切换数十个不同 Anycast 端点，其出网身份也是单一固定的。

### 4.2 解决方案与改动
1. **多账号出口池 (`AccountPool`)**：
   - 在 [web.go](file:///c:/Tools2/warp/warpscout/web.go) 中构建了 `accountPool` 独立账户管理器与持久化机制（`warpscout-account.json.pool*`）。
   - 服务启动时自动检查并在后台平滑扩容独立 WARP 账号。
   - 新增 `/api/account/pool` 接口支持按需扩展与监控。
2. **账号级节点生成 (`mihomoProxyWithAccount`)**：
   - 在 [wgconf.go](file:///c:/Tools2/warp/warpscout/wgconf.go) 中实现独立账号绑定的 `mihomoProxyWithAccount` 与 `mihomoPeerWithAccount`。
   - 在批量导出 Clash 订阅（`/api/config/batch_export`）时，为每个 MASQUE TCP、MASQUE QUIC、WireGuard 节点轮流分配不同的独立 WARP 账户。
3. **部署与验证**：
   - 已完成容器编译并部署更新至 `warpscout-web` 容器。
   - 导出的 Clash 订阅中，各节点配置了不同的独立私钥，实测不同节点分配不同 Cloudflare 出口 IP。
