# pcsclone 命令契约（v1）

本文件是 `cmd/pcsclone` 的可审查命令规范。实现以本文件为准；`BaiduPCS-Go` 旧命令和旧 `rc` 入口不受本规范约束。

- 制定日期：2026-10-23
- 适用范围：`pcsclone` 独立可执行文件
- 命令解析、帮助和账号命令位于 `internal/pcsclone`；文件操作执行引擎复用 `internal/rc`。

## 1. 入口

```text
pcsclone [global options] <command> [arguments]
```

- 无参数、`--help`、`help`：输出全局帮助，退出码 0。
- `--version`、`-V`、`version`：输出版本，退出码 0。
- `help <command...>`：输出逐命令帮助；支持 `config`、`baidu` 多层子命令。
- `completion bash|zsh|powershell`：输出命令、子命令和长选项的补全脚本；不补全文件路径。PowerShell 脚本要求 PowerShell 7+。
- 全局选项必须写在命令之前：
  - `--config FILE` / `-c FILE`
  - 配置路径优先级：显式 `--config` > `PCSCLONE_CONFIG_FILE` > `PCSCLONE_CONFIG_DIR/pcs_config.json` > 平台默认目录。
  - Windows 默认目录：`%APPDATA%\pcsclone`；其他平台：`$HOME/.config/pcsclone`。
- 正常命令不会读取或修改旧 `BaiduPCS-Go` 配置。只有显式的 `config import-pcsgo` 会读取旧配置，且只读。
- 传输 worker 由当前可执行文件自身启动（`__rc_transfer` 隐藏入口），不依赖 `BaiduPCS-Go.exe`。父进程通过 `PCSCLONE_CONFIG_FILE` 把同一配置路径传给 worker。

## 2. 路径

- 本地路径：
  - 绝对路径：`D:\Backup`、`/home/user/backup`。
  - 相对路径：相对**启动 pcsclone 时的当前目录**，不受旧交互式工作目录影响。
  - UNC：`\\server\share`。
  - 拒绝 `D:relative` 这类盘符相对路径。
  - 本地文件名本身含冒号时，加 `./` 前缀，避免被当作远端名。
- 网盘路径：
  - `NAME:/path`、`NAME:`（账号根目录）。
  - `NAME` 由 `config create` 或 `config import-pcsgo` 建立，至少两个字符，首字符为字母。
  - 只使用 `/` 分隔；不使用远端工作目录。
- 每条命令通过路径参数显式选择账号；不存在“当前账号”或“当前工作目录”。
- 列表输出中的 `Path` 是相对请求根目录的路径；`stat` 的 `Path` 对本地是绝对路径，对网盘保留用户输入的远端参数。
- 以 `-` 开头的路径或参数需要放在 `--` 之后。
- 源和目标相同或互为子目录时拒绝执行。

## 3. 退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 操作失败；`check` 发现差异；远端错误；账号或路径不存在 |
| 2 | 命令、参数、选项或互斥选项错误 |
| 130 | 收到中断信号（Ctrl-C）；已启动的 worker 被取消 |

规则：

- 覆盖目标、删除文件和清空回收站都**不进行交互确认**；需要预演时使用 `--dry-run`。
- 部分文件失败时，已完成文件不回滚；命令返回 1，并保留失败信息。
- `--help`、无参数帮助、成功帮助请求均返回 0。
- 未知命令、未知选项、参数数量错误、无效整数/布尔值返回 2。
- 账号别名不存在、远端账号失效、文件不存在、API 错误返回 1。

## 4. 核心文件命令

| 命令 | 参数 | 行为 |
| --- | --- | --- |
| `copy` | `SOURCE DEST [options]` | 目录：复制内容到 DEST 根；文件：放入 DEST/源文件名；保留目标额外文件 |
| `copyto` | `FILE TARGET [options]` | 单文件复制；TARGET 是精确文件名 |
| `sync` | `SOURCE DEST [options]` | 目录单向镜像；删除目标额外文件和符合条件的空目录 |
| `move` | `SOURCE DEST [options]` | 目录内容移动；每个文件复制并校验后再删除源 |
| `moveto` | `FILE TARGET [options]` | 单文件移动；校验后删除源 |
| `check` | `SOURCE DEST [options]` | 比较目录，不修改文件；有差异返回 1 |
| `ls` | `PATH [options]` | 默认递归列出路径 |
| `lsf` | `PATH [options]` | 默认只列一层；`-R`/`--recursive` 递归 |
| `lsd` | `PATH [options]` | 只列目录；默认一层；`-R` 递归 |
| `lsjson` | `PATH [options]` | JSON 数组；默认一层；`-R` 递归 |
| `stat` | `PATH [options]` | 单个对象的 JSON 对象 |
| `size` | `PATH [options]` | 递归统计文件数和字节数；文本或 JSON |
| `about` | `REMOTE: [options]` | 账号容量；文本或 JSON |
| `delete` | `PATH [options]` | 删除筛选后的文件，保留目录 |
| `deletefile` | `FILE [options]` | 删除一个文件 |
| `purge` | `PATH [options]` | 删除目录树；拒绝文件系统根；不接受筛选 |
| `rmdir` | `PATH [options]` | 删除一个空目录 |
| `rmdirs` | `PATH [options]` | 自底向上删除空子目录，保留根 |
| `mkdir` | `PATH [options]` | 创建目录及缺失父目录 |
| `tree` | `PATH [options]` | 目录树；`--depth N` |

### 4.1 目录语义

- `copy`、`sync`、`move` 对目录操作时处理**目录内容**，不额外生成源目录名。
- 尾斜线不改变含义：`copy D:\Backup bd:/Backup` 生成 `bd:/Backup/...`。
- `copyto`、`moveto` 的目标始终按文件名解释，不按目录解释。
- 空目录默认不复制；`copy`/`sync`/`move` 使用 `--create-empty-src-dirs` 复制空源目录。
- `move` 使用 `--delete-empty-src-dirs` 在完成后清理空源子目录，保留源根。

### 4.2 列表

- `ls`：默认递归；`--recursive=false` 可改为一层。
- `lsf`、`lsjson`、`lsd`：默认一层；`-R`/`--recursive` 递归。
- 文本列表每行一个路径；目录追加 `/`。`lsjson` 的 `Path` 不带尾斜线，目录由 `IsDir` 表示。
- `lsd` 只列目录；`lsf`/`lsjson` 支持 `--files-only`、`--dirs-only`，两者互斥。
- `tree` 的 `--depth 1` 表示直接子项，默认无限；`--depth` 接受 `-1` 或正整数，不接受 0。
- 列表命令要求 PATH 是目录；单文件信息使用 `stat`。

### 4.3 JSON 输出

`lsjson`：数组，元素字段：

```json
{"Path":"nested/a.txt","Name":"a.txt","Size":6,"ModTime":"2026-10-23T00:00:00+08:00","IsDir":false}
```

`stat`：单个对象：

```json
{"Path":"bd:/a.txt","Name":"a.txt","Size":6,"ModTime":"...","IsDir":false,"Hash":"<md5>","Remote":true,"UID":123}
```

- `Hash` 仅在 `--hash` 时出现；远端文件会下载内容计算 MD5。
- `Remote` 明确标识本地/网盘；`UID` 仅网盘出现。
- `size --json`：`{"count":N,"bytes":N}`。
- `size` 文本：`Count: N`、`Bytes: N` 两行。
- `about --json`：`{"total":N,"used":N,"free":N}`。
- `about` 文本：`Total: N`、`Used: N`、`Free: N` 三行。
- `size`/`about` 的数值单位均为字节。

### 4.4 筛选

- `--include PATTERN`、`--exclude PATTERN` 可重复；排除优先。
- `--files-from FILE` 从 UTF-8 文本读取相对路径（使用 `/` 分隔）；空行和 `#` 注释忽略。
- 模式以源根目录的相对路径为基准；无斜线模式匹配任意层级名称；`/` 开头从根匹配；`**` 跨目录；支持 `*`、`?`、字符组。
- 目录排除作用于全部后代。
- 文件路径本身不展开通配符。

### 4.5 比较、覆盖与校验

- 默认对同大小文件读取内容并比较 MD5；**远端比较和 `check` 会下载文件内容**，不信任列表接口的 MD5。
- `--checksum`：传输完成后再比较源和目标内容 MD5。
- `--size-only`：差异判断只比较大小；与 `--checksum` 互斥。即使使用该选项，跨后端 `move` 删除源前仍比较真实内容。
- `--update`：目标修改时间晚于源时跳过；使用接口提供的修改时间，不保证等于本地原始时间。
- `--ignore-existing`：目标文件已存在就跳过。
- 目标文件存在且内容不同：覆盖；没有交互确认。
- 本地目标先写同目录临时文件，完成后重命名。
- 同账号且目标不存在时优先服务端复制/移动；覆盖已有远端目标通过下载/上传完成。
- 跨账号复制通过本地临时文件中转；失败保留源，结束清理临时文件。
- `move`/`moveto` 删除源前必须完成内容校验，并再次确认源未变化。

### 4.6 删除与同步安全

- `sync` 在完成源扫描、目标扫描、比较和传输后才执行删除。
- 删除前重新扫描源清单；源清单变化则阻止删除。
- 所有删除候选先复核，再执行第一个删除。
- `--max-delete N` 在计划阶段检查；超限直接失败。默认 `-1` 表示不限制。
- `delete` 只删除筛选后的文件；目录保留。
- `purge` 拒绝删除本地文件系统根和网盘根；不接受 `--include`/`--exclude`。
- `--dry-run` 不写入、不删除，但可能为了比较而读取或下载内容。

### 4.7 重试与取消

- `--retries N`：失败后的额外传输尝试次数，默认 2。
- 传输失败返回 1；已成功文件保留。
- Ctrl-C 取消上下文；worker 被终止；`move` 不会在未完成校验时删除源；退出码 130。
- `--progress`/`-P` 输出周期进度和结束汇总；不改变输出数据通道。

### 4.8 公共传输选项

`--dry-run`/`-n`、`--include`、`--exclude`、`--files-from`、`--checksum`、`--size-only`、`--ignore-existing`、`--update`、`--transfers N`、`--checkers N`、`--retries N`、`--bwlimit RATE`、`--max-delete N`、`--delete-excluded`、`--create-empty-src-dirs`、`--delete-empty-src-dirs`、`--progress`/`-P`。

日志：`--log-level DEBUG|INFO|ERROR|OFF`、`--log-file FILE`、`--use-json-log`。日志写 stderr 或指定文件；列表和 JSON 数据写 stdout。

## 5. 账号配置命令

| 命令 | 行为 |
| --- | --- |
| `config create NAME [options]` | 登录并创建别名；默认交互登录；支持 `--bduss`、`--stoken`、`--ptoken`、`--cookies`、`--username`、`--password` |
| `config reconnect NAME [options]` | 重新登录并更新凭据；返回 UID 必须与别名当前 UID 相同 |
| `config rename OLD NEW` | 只改别名 |
| `config delete NAME [--keep-account]` | 删除别名；没有其他别名引用时默认删除凭据，`--keep-account` 保留 |
| `config show [NAME] [--json]` | 输出 UID、用户名、凭据是否存在；**永不输出凭据值** |
| `config file [--json]` | 输出配置、别名和状态文件路径 |
| `config import-pcsgo [--from PATH] [--dry-run] [--json]` | 从旧配置导入账号和别名；旧文件只读 |
| `listremotes` | 每个别名一行，格式 `NAME:` |

- `--cookies` 必须包含完整 `BDUSS=...;` 字段，不能与 `--bduss`/`--ptoken`/`--stoken` 同时使用。
- `config create` 拒绝为同一 UID 再创建一个别名，重复创建返回 1；导入时如果旧配置本来就有同一 UID 的多个别名，会保留这些别名。
- `config show` 的 JSON 字段：`name`、`uid`、`username`、`bduss`、`stoken`、`cookies`、`access_token`、`broken`；除 `name`/`uid`/`username`/`broken` 外均为布尔值。存在指向缺失账号的别名时仍输出全部条目，但返回 1。
- `config import-pcsgo`：
  - 优先沿用旧 `rc_remotes.json` 的别名。
  - 没有可用别名时，从用户名生成；非 ASCII 或不合法的名称回退为 `u<uid>`。
  - 已存在的 UID 不重复添加，也不会被旧凭据覆盖；需要刷新凭据时使用 `config reconnect`。没有别名的账号会补一个别名。
  - 不复制旧 `workdir` 语义；导入账号的工作目录统一为 `/`。
  - 旧配置文件在导入前后字节不变。
  - `--dry-run` 不写入导入的账号或别名；pcsclone 自身的配置目录仍可能按正常运行规则初始化。
- 配置目录隔离：pcsclone 的配置、别名、`rc-state/` 都在自己的目录中；worker 使用同一目录。

## 6. 百度专有命令

所有子命令都通过远端路径选择账号，例如 `bd:` 或 `bd:/backup`；不使用旧“当前账号”或工作目录。

| 命令 | 行为 |
| --- | --- |
| `baidu share create PATH... [--pwd CODE] [--period DAYS] [--combined] [--json]` | 分享同一账号下的一个或多个路径；拒绝分享网盘根 |
| `baidu share list REMOTE [--page N] [--json]` | 列出分享记录 |
| `baidu share cancel REMOTE SHAREID... [--json]` | 按 shareid 取消分享 |
| `baidu transfer SHARE_URL DEST_REMOTE [--code CODE] [--collect] [--json]` | 转存到 DEST_REMOTE 目录；目录不存在时创建；账号取自目标远端 |
| `baidu link REMOTE_FILE [--direct] [--json]` | 获取临时下载链接；`--direct` 使用 PCS 下载地址，下载时仍需账号 Cookie |
| `baidu search REMOTE_DIR KEYWORD [--recursive] [--json]` | 搜索文件名；默认一层，`-R` 递归 |
| `baidu offline add SAVE_REMOTE URL... [--json]` | 添加离线任务；保存目录不存在时创建 |
| `baidu offline list REMOTE [--json]` | 列出离线任务 |
| `baidu offline query REMOTE ID... [--json]` | 按任务 ID 查询 |
| `baidu offline cancel REMOTE ID... [--json]` | 取消任务 |
| `baidu offline delete REMOTE ID... [--json]` | 删除任务记录 |
| `baidu offline clear REMOTE [--json]` | 清空任务记录 |
| `baidu recycle list REMOTE [--page N] [--json]` | 列出回收站 |
| `baidu recycle restore REMOTE FSID... [--json]` | 还原回收站项 |
| `baidu recycle delete REMOTE FSID... [--json]` | 永久删除回收站项 |
| `baidu recycle clear REMOTE [--json]` | 清空回收站 |

规则：

- 数字 ID 必须为正整数；无效 ID 返回 2。
- 多 ID 操作逐项执行；部分失败时已完成项保留，最终返回 1。
- `baidu transfer` 的目标按目录处理并创建；提取码长度为 4。
- `baidu share create` 的 `--pwd` 为空时由百度生成；非空时必须为 4 个字符。
- 远端 API 错误、账号失效、链接/提取码非法返回 1。
- 所有命令的文本结果写 stdout；错误和日志写 stderr。

## 7. 输出与日志约定

- stdout 只放数据：列表、路径、JSON、命令结果摘要。
- stderr 放事件日志（`copy:`、`delete:`、`error:` 等）、进度和错误。
- `--use-json-log` 时事件日志为 JSON Lines。
- `--log-file` 相对路径以启动目录为基准。
- JSON 输出为单个 JSON 值（数组或对象），单行紧凑格式，末尾换行。
- `lsjson`/`stat` 沿用 rc 的 Go 风格字段名（`Path`、`Name`、`Size`、`ModTime`、`IsDir`）；`size`、`about`、`config` 和 `baidu ...` 的 JSON 字段使用小写或 snake_case。

## 8. 明确不包含

本轮不提供：

- `mount`、`serve`、`bisync`、rclone Remote Control API。
- 独立仓库拆分。
- 跨命令下载续传（上传续传状态仍按账号/源/目标隔离）。
- 交互式覆盖确认、ACL/所有者/完整文件系统元数据同步。
- 符号链接和特殊文件复制。
- 旧 `rc` 前缀和隐式账号/工作目录。
