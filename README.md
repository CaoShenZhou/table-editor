# 表格与备忘录（table-editor）

一个用 Go 写的本地小工具：浏览器里编辑多张表格、写备忘录，支持 CSV 导出和锁屏防误触。单文件、零第三方依赖、纯标准库，数据存在一个 `data.json` 里。

![Go](https://img.shields.io/badge/Go-1.25-00ADD8) ![License](https://img.shields.io/badge/license-MIT-green)

## 功能一览

- **多表格**：标签页管理任意多张表，新建 / 切换 / 双击重命名 / 删除
- **表格编辑**：表头直接改、单元格点击即填、单元格内回车换行、粘贴纯文本
- **行列操作**：随意加行加列、悬停删行删列、**拖表头左右换列顺序**
- **键盘导航**：`Alt/Option + 方向键` 在单元格间移动，末行按 `Alt+↓` 自动加行
- **导出 CSV**：UTF-8 BOM + CRLF，Excel / WPS 打开中文不乱码、换行保留
- **备忘录**：左侧列表 + 右侧编辑，停止输入自动保存，支持多行
- **锁屏**：一键锁屏 / `⌘+Shift+L`，可设 4-6 位数字密码，防止误触；忘了密码有命令行找回

## 快速开始

两种启动方式，二选一（**不要同时跑**，数据文件和端口都会冲突）。

### 方式一：本地直接使用

```bash
cd table-editor
./run.sh              # 默认 8080 端口，自动编译 + 打开浏览器
./run.sh 9000         # 指定端口
```

不想用脚本：

```bash
go run main.go 8080
```

### 方式二：Docker 使用

```bash
cd table-editor
./docker-run.sh       # 构建镜像 + 启动容器 + 打开浏览器
./docker-run.sh 9000  # 指定宿主机端口
```

子命令：`stop`（停止，数据保留）/ `logs` / `status` / `reset-pin` / `rebuild`。

等价手写命令：

```bash
DOCKER_BUILDKIT=0 docker build -t table-editor:latest .
docker run -d --name table-editor -p 8080:8080 \
  -v "$(pwd)/data:/app/data" --restart unless-stopped table-editor:latest
# 或
docker compose up -d --build
```

打开浏览器访问 <http://localhost:8080> 即可。两种方式的差异、数据位置、常见问题见 [启动方式.md](./启动方式.md)。

## 文档

- [启动方式.md](./启动方式.md) —— 本地 / Docker 两种启动方式对照，常用命令与排错
- [使用文档.md](./使用文档.md) —— 界面、操作、快捷键、数据文件说明
- [部署文档.md](./部署文档.md) —— 本地 / 二进制 / Docker / 开机自启 / 反向代理 / 备份与升级

## 目录结构

```
table-editor/
├── main.go              # 全部后端 + 前端页面（单文件）
├── go.mod
├── run.sh               # 方式一一键启动（也支持 --reset-pin）
├── docker-run.sh        # 方式二一键部署（build/stop/logs/status/reset-pin/rebuild）
├── Dockerfile           # 多阶段构建，静态二进制 + alpine
├── docker-compose.yml   # 一键起容器，数据挂到 ./data
├── .dockerignore
├── data.json            # 本地运行时生成的数据文件（表格 + 备忘录 + 密码哈希）
├── data/                # Docker 方式的数据目录（data/data.json）
├── 启动方式.md
├── 使用文档.md
├── 部署文档.md
└── README.md
```

## 技术说明

- 后端：Go 标准库 `net/http`，无框架、无第三方依赖
- 前端：单页 HTML + 原生 JS（内嵌在 `main.go` 里，无需构建）
- 存储：`data.json`（表格、备忘录、锁屏密码的 SHA-256 哈希）
- 并发：内存数据 + `sync.Mutex` 保护，每次写操作落盘

## 许可

MIT
