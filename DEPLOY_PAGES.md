# WARPSCOUT Cloudflare Pages 静态面板与自动化订阅部署指南

本项目已实现 **GitHub Actions 全球机房探测 + Cloudflare Pages 静态托管** 的全自动化架构（方案 B）。

---

## 💡 为什么需要本方案？

1. **解决本地测不出非美西（US）出口的问题**：
   - 国内三大运营商（电信/联通/移动）连接 Cloudflare WARP 的 Anycast 广播 IP 时，底层国际物理路由会直接走跨太平洋海缆抵达美西（洛杉矶 LAX / 圣何塞 SJC）。因此在境内直连无论如何优选，出口地区永远是美国。
   - GitHub Actions 运行在海外全球原生公网环境（Azure 海外多区域数据中心），向 Cloudflare WARP 发起的原始 UDP 握手将就近命中香港（HKG）、东京（NRT）、新加坡（SIN）、法兰克福（FRA）等非美西机房，从而获取真实的亚太及欧洲低延迟原生节点。

2. **解决 Serverless 环境无法运行探测引擎的限制**：
   - Cloudflare Pages / Workers 属于 V8 沙箱引擎，不支持底层的 Raw UDP 套接字与高并发 WireGuard 协议栈。
   - 本方案将**探测计算**与**前端展示**完美解耦：
     - **计算端**：由 GitHub Actions 定时执行底层 UDP 探测、丢包率测试与出口元数据解析；
     - **展示端**：将生成的 `results.json`、`clash-sub.yaml`、`wireguard.conf` 和现代极简 Web 面板推送到 Cloudflare Pages 全球 CDN。

---

## 🚀 两步极速部署指南

### 第一步：推送到你的 GitHub 仓库

将当前项目代码推送到你的 GitHub 仓库（或 Fork 到你的账号下）：
```bash
git add .
git commit -m "feat: add cloudflare pages static dashboard and github actions workflow"
git push origin main
```

> **提示**：仓库根目录的 `.github/workflows/warpscout-pages.yml` 已经内置了每 6 小时自动运行一次的 Cron 定时任务，也支持随时在 GitHub 页面手动一键触发。

---

### 第二步：在 Cloudflare Pages 绑定仓库（零密钥，推荐）

1. 登录 [Cloudflare Dashboard 控制台](https://dash.cloudflare.com/)。
2. 在左侧导航栏点击 **Workers 和 Pages (Workers & Pages)**。
3. 点击 **创建应用程序 (Create Application)** -> 切换到 **Pages** 标签页。
4. 点击 **连接到 Git (Connect to Git)**，授权并选择你的仓库。
5. 配置构建与部署设置：
   - **项目名称**：自定义（如 `warpscout`，对应的访问地址将是 `https://warpscout.pages.dev`）
   - **生产分支 (Production branch)**：选择 `gh-pages`
   - **框架预设 (Framework preset)**：选择 `无 (None)`
   - **构建命令 (Build command)**：留空（无需构建）
   - **构建输出目录 (Build output directory)**：填 `.` 或留空
6. 点击 **保存并部署 (Save and Deploy)**。

🎉 **大功告成！**
当 GitHub Actions 完成首次探测后，它会自动将面板和配置推送到 `gh-pages` 分支，Cloudflare Pages 会在 1 秒内自动同步发布全球 CDN！

---

## 📡 如何在 Clash / Mihomo 中一键订阅

部署完成后，你将获得属于自己的专属 Pages 域名（例如 `https://warpscout.pages.dev`）：

### 1. 订阅链接地址
```text
https://<你的项目名>.pages.dev/data/clash-sub.yaml
```

### 2. 导入到客户端
- **Clash Verge Rev / Clash Nyanpasu / Mihomo Party / Flclash**：
  1. 打开客户端，进入 **订阅 / 配置 (Profiles)**。
  2. 点击 **新建 / 从 URL 导入**。
  3. 粘贴上述订阅链接，点击 **保存并更新**。
  4. 订阅内已预设：
     - `⚡ 自动优选`（URL-Test 自动选择延迟最低节点）
     - 各国家分组（`🇯🇵 日本节点`、`🇭🇰 香港节点`、`🇸🇬 新加坡节点`、`🇺🇸 美国节点`）
     - 全部节点的独立手动选择列表

---

## 🖥️ 网页面板功能说明

访问 `https://<你的项目名>.pages.dev` 即可查看美观的高级暗黑玻璃拟态面板：

- **统计看板**：展示当前可用端点总数、最低隧道延迟、协议类型与更新时间。
- **国家/地区快捷筛选**：按 🌐 全部、🇯🇵 日本、🇭🇰 香港、🇸🇬 新加坡、🇪🇺 欧洲、🇺🇸 美国 快速切换。
- **关键字即时搜索**：支持输入 IP、端口、Colo 机场三字码（如 NRT、HKG）实时过滤。
- **端点多选与批量导出**：
  - 勾选任意多个端点，屏幕底部浮现批量操作栏。
  - 支持 **一键批量导出 Clash YAML**。
  - 支持 **一键批量导出 WireGuard .conf**。
  - 支持 **批量复制 IP 列表** 或 **导出 JSON**。
- **纯静态前端运算**：所有批量导出和单节点配置均在浏览器本地毫秒级生成，不依赖任何后端服务器。

---

## ⚙️ 高级配置与自定义

### 1. 手动触发即时探测
在 GitHub 仓库页面点击 **Actions** -> 选择左侧的 **WARPSCOUT Pages - Scan & Deploy** -> 点击右侧的 **Run workflow**：
- `sample`：每个子网探测采样的 IP 数（默认 12）
- `proto`：探测协议（默认 `awg`，可选 `wg`）
- `timeout`：探测超时时间（默认 3 秒）
- `target`：自定义目标 IP 或 CIDR 网段（留空使用官方池）

### 2. 本地调试命令
如果你想在本地生成静态文件预览：
```bash
# 进入 warpscout 目录执行扫描并导出到 public/ 目录
cd warpscout
./warpscout pages -o public -n 5 -p awg

# 启动本地静态服务器预览（例如使用 Python）
cd public && python -m http.server 8080
```
浏览器打开 `http://localhost:8080` 即可预览面板。
