# pcsclone 验证记录

- 日期：2026-10-23
- 平台：Windows 11 Pro（10.0.26300），Go 1.27.1 windows/amd64
- 范围：`cmd/pcsclone`、独立配置/导入、本地文件操作、命令解析与退出码

本记录区分“本轮已执行的验证”和“未执行的真实网盘验证”。未执行的项目不会因为本地测试通过而被视为已验证。

## 1. 已执行

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 全仓构建 | `go build ./...` | 通过 |
| 全仓单元测试 | `go test ./... -timeout 180s` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| 独立入口构建 | `go build -o build/pcsclone.exe ./cmd/pcsclone` | 通过 |
| 交叉编译冒烟 | `GOOS/GOARCH` 为 linux/amd64、windows/386、darwin/arm64，`CGO_ENABLED=0` | 通过 |
| pcsclone 本地回归 | `scripts/test-pcsclone-local.ps1 -Binary build/pcsclone.exe` | PASS |
| 旧 rc 本地回归 | `scripts/test-rc-local.ps1 -Binary <legacy-build>` | PASS |
| 独立分发冒烟 | 把 `pcsclone.exe` 复制到仓库外临时目录，执行 `ls`/`copy`/`stat` | 通过 |
| worker 配置继承 | 用损坏的 `PCSCLONE_CONFIG_FILE` 启动 `__rc_transfer` | 按预期读取该路径并返回 1 |

### 1.1 pcsclone 本地回归覆盖

- `mkdir`、`copy`、`copyto`、`sync`、`move`、`moveto`、`delete`、`deletefile`、`purge`、`rmdir`、`rmdirs`。
- 目录内容复制规则、尾斜线不改变含义、空目录、筛选、排除优先、`--files-from` 选择（本地回归与 `internal/rc` 引擎测试）。
- `--dry-run` 不创建目标；`--checksum` 传输后校验。
- `sync` 删除上限 `--max-delete`、目标多余文件删除、`check` 差异返回 1。
- `ls` 默认递归；`lsf`/`lsd`/`lsjson` 默认一层；`-R` 递归。
- `lsjson` 数组、`stat` 单对象与 `--hash`、`size` 文本/JSON。
- 参数错误返回 2；源目标重叠、check 差异返回 1；未知命令返回 2。
- `completion bash|zsh|powershell` 生成脚本，包含顶层命令、嵌套子命令和长选项；不支持 `fish` 时返回 2。脚本内容经过单元测试检查，未在真实交互 shell 中逐键验证补全行为。
- 上传/下载 worker 的自调用代码路径通过源码检查和配置继承冒烟覆盖；**未执行真实传输**。

### 1.2 配置与导入覆盖

- `config import-pcsgo` 的 dry-run、正式导入、重复导入。
- 旧 `rc_remotes.json` 别名保留（`bd`、`cjk`）。
- 非 ASCII 用户名回退为 `u<uid>`。
- 缺少凭据的旧账号计入 unusable，不导入。
- `config show` 文本和 JSON 均不输出 BDUSS/STOKEN/Cookie/AccessToken 值。
- `config rename`、`config delete --keep-account`。
- 旧配置文件导入前后字节一致。
- `PCSCLONE_CONFIG_FILE` 指向的配置文件、`rc_remotes.json` 和 `rc-state/` 都位于独立配置目录。
- `--config` 优先级、`PCSCLONE_CONFIG_FILE` 与 `PCSCLONE_CONFIG_DIR` 的解析有单元测试。

### 1.3 模拟后端

`internal/rc` 既有测试覆盖了扫描失败、比较/传输失败、源或目标变化、删除阻断、取消、账号隔离和跨账号中转（模拟两个账号的传输边界）等。本轮没有修改这些引擎语义，只复用了它们。新增 `baidu ...` 子命令主要是对 `baidupcs` 方法的薄封装，本轮没有为这些网络方法编写模拟服务器测试；分页异常与认证失效也没有在本轮新增模拟用例。

## 2. 未执行（发布前仍需验证）

以下项目需要真实百度账号和网盘环境，本轮环境没有可用测试凭据，因此**未执行**：

- `about` 对真实账号的容量查询。
- 真实网盘 `ls`/`lsf`/`lsjson`/`stat`/`size`。
- 真实上传、下载、同账号及跨账号复制/移动。
- `baidu share create/list/cancel`。
- `baidu transfer`（含提取码、`--collect`、目标目录创建）。
- `baidu link`（locate 与 `--direct`）。
- `baidu search`。
- `baidu offline add/list/query/cancel/delete/clear`。
- `baidu recycle list/restore/delete/clear`。
- 真实双账号跨账号中转。
- 真实中断（Ctrl-C）期间 worker 终止与源文件保留。
- `go test -race`（当前环境未运行；CGO 环境另行验证）。

建议的真实网盘验证方式：

1. 在专用测试账号下创建 `/pcsclone-test-<随机ID>`。
2. 先用 `copy`/`ls`/`stat`/`size`/`about` 验证基本读写。
3. 再验证 `sync`（删除上限先设为 0/1）、`move`、`check`。
4. 分享/转存/直链/搜索/离线/回收站分别使用测试数据，结束后清理。
5. 双账号验证时，两个别名分别指向独立账号，跨账号复制只使用临时中转文件。

测试凭据、Cookie、分享密码和测试数据不得提交到 Git。

## 3. 结论

- 独立入口、独立配置、导入、脱敏、核心本地文件操作、帮助和退出码已在本机验证。
- `pcsclone.exe` 可脱离 `BaiduPCS-Go.exe` 和仓库文件运行。
- 真实网盘和百度专有功能仍需在具备测试账号的环境中完成发布前验证。

## 4. 合并审查回归（2026-10-23）

审查发现并修复以下问题，新增 `internal/pcsclone/review_test.go`：

- 独立入口继承 `BAIDUPCS_GO_VERBOSE=1`，导致旧请求日志绕过新日志设置；使用模拟配额请求验证旧日志已禁用。
- 导入时新旧配置文件或目录重叠，可能改写旧账号/别名文件；现在初始化前拒绝共用存储，并验证旧文件字节不变，包括空旧配置。
- 批量离线添加、取消及删除任务收到取消后仍继续请求；使用本地模拟 HTTP 服务触发取消，验证只发送首个请求并返回取消错误。

上述验证不读取真实凭据、不连接真实百度服务，不能替代第 2 节列出的发布前网盘验证。
