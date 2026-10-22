# pcsclone 使用、迁移与构建指南

`pcsclone` 是本仓库新增的独立文件操作入口。它复用 `internal/rc` 的显式端点、计划/执行引擎和隔离传输 worker，并复用 `baidupcs` 的网盘能力，但不进入 `BaiduPCS-Go` 的旧交互界面，也不依赖旧的当前账号或工作目录。

- 命令契约：[pcsclone-command-contract.md](pcsclone-command-contract.md)
- 验证记录：[pcsclone-validation.md](pcsclone-validation.md)
- 旧的统一命令入口（仍保留）：[rc.md](rc.md)

## 1. 构建

只需要 Go 工具链：

```powershell
go build -o build/pcsclone.exe ./cmd/pcsclone
& .\build\pcsclone.exe --version
& .\build\pcsclone.exe help
```

发布时建议关闭 CGO：

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags "-s -w -X main.Version=v4.0.2-dev" -o build/pcsclone.exe ./cmd/pcsclone
```

`go build ./...` 仍然会构建旧的 `BaiduPCS-Go` 入口；两个入口共享代码，但产物独立。CI 的构建矩阵会同时输出 `BaiduPCS-Go` 和 `pcsclone_<os>_<arch>` 两种产物。`pcsclone.exe` 不需要旁边的 `BaiduPCS-Go.exe`：传输 worker 通过 `os.Executable()` 重新执行自身。

## 2. 配置目录

默认目录：

- Windows：`%APPDATA%\pcsclone`
- Linux/macOS：`$HOME/.config/pcsclone`

也可以显式指定：

```powershell
& .\build\pcsclone.exe --config D:\pcsclone-config\pcs_config.json listremotes
$env:PCSCLONE_CONFIG_FILE = 'D:\pcsclone-config\pcs_config.json'
& .\build\pcsclone.exe listremotes
```

配置选择优先级：

1. `--config` / `-c`
2. `PCSCLONE_CONFIG_FILE`
3. `PCSCLONE_CONFIG_DIR` 下的 `pcs_config.json`
4. 平台默认目录

pcsclone 的账号、别名和 `rc-state/` 都写在自己的目录。正常命令不会读写旧 `BaiduPCS-Go` 配置；唯一例外是显式的 `config import-pcsgo`，它只读旧文件。

代理、User-Agent、缓存、限速等高级设置仍保存在 `pcs_config.json` 中；本轮没有为这些设置提供逐项命令，可直接编辑该文件。`config file` 会打印实际路径。

## 3. 账号

### 3.1 交互登录并创建别名

```powershell
& .\build\pcsclone.exe config create bd
```

按提示输入用户名、密码以及可能需要的验证码/手机或邮箱验证。创建后 `bd` 就是远端名。

### 3.2 使用 BDUSS / Cookie

适合脚本或无法交互的环境：

```powershell
& .\build\pcsclone.exe config create bd --bduss "<BDUSS>" --stoken "<STOKEN>"
& .\build\pcsclone.exe config create bd --cookies "BDUSS=...; STOKEN=...;"
```

`--cookies` 必须包含完整的 `BDUSS=...;` 字段，不能和其他凭据参数混用。注意：`--bduss`、`--stoken`、`--cookies` 会出现在 shell 历史和进程列表中；在可信终端中优先使用交互登录，或从受保护的环境变量/脚本中传入。

### 3.3 查看和整理

```powershell
& .\build\pcsclone.exe listremotes
& .\build\pcsclone.exe config show
& .\build\pcsclone.exe config show bd --json
& .\build\pcsclone.exe config file
& .\build\pcsclone.exe config rename bd backup
& .\build\pcsclone.exe config reconnect backup
& .\build\pcsclone.exe config delete backup --keep-account
```

`config show` 只显示凭据“是否存在”，不会输出 BDUSS、STOKEN、Cookie 或 AccessToken 的值。`config delete` 默认在没有其他别名引用该 UID 时删除存储的账号；`--keep-account` 只删别名。

### 3.4 从旧 BaiduPCS-Go 导入

旧配置会在以下位置查找（也可用 `--from` 指定文件或目录）：

- `BAIDUPCS_GO_CONFIG_DIR/pcs_config.json`
- 可执行文件目录下的 `pcs_config.json`
- Windows：`%APPDATA%\BaiduPCS-Go\pcs_config.json`
- 其他：`$HOME/.config/BaiduPCS-Go/pcs_config.json`

```powershell
& .\build\pcsclone.exe config import-pcsgo --dry-run
& .\build\pcsclone.exe config import-pcsgo
& .\build\pcsclone.exe config import-pcsgo --from D:\old-config\pcs_config.json --json
```

导入规则：

- 优先沿用旧目录 `rc_remotes.json` 中的别名。
- 没有可用别名时从用户名生成；中文名等无法生成合法 ASCII 别名时回退到 `u<uid>`。
- 已存在的 UID 不重复添加；没有别名的账号会补一个别名。
- 旧配置文件只读，导入前后字节不变。
- 旧账号的 `workdir` 不导入为隐式语义；pcsclone 所有网盘路径都从 `NAME:/` 开始。

导入**不会固定创建 `bd` 别名**：别名优先沿用旧 `rc_remotes.json`；没有旧别名时由用户名生成（例如 `Lapluis`）。导入命令现在会在 `Remotes:` 行列出可用别名。先查看实际名称，再使用：

```powershell
& .\build\pcsclone.exe listremotes
# 假设输出 Lapluis:
& .\build\pcsclone.exe ls Lapluis:/
& .\build\pcsclone.exe about Lapluis:
# 如果希望使用 bd，可以重命名：
& .\build\pcsclone.exe config rename Lapluis bd
```

本指南后续示例使用 `bd` 作为别名；它需要先通过 `config create bd` 创建，或通过 `config rename` 改名，导入本身不保证生成这个名字。

## 4. 核心文件操作

### 4.1 路径

- 本地：`D:\Backup`、`.\Backup`、`\\server\share`
- 网盘：`bd:/Backup`、`bd:`（账号根）
- 相对本地路径以**启动命令时所在目录**为基准。

没有隐式的当前账号、网盘工作目录或 `rc` 前缀。

### 4.2 复制、同步、移动

```powershell
# 目录内容复制；保留目标额外文件
& .\build\pcsclone.exe copy .\Backup bd:/Backup --checksum -P

# 单文件到精确目标名
& .\build\pcsclone.exe copyto .\a.txt bd:/Backup/renamed.txt

# 单向镜像；删除目标额外文件
& .\build\pcsclone.exe sync .\Backup bd:/Backup --exclude "*.tmp" --max-delete 100

# 移动；校验后删除源
& .\build\pcsclone.exe move bd:/Old .\Old --delete-empty-src-dirs

# 比较，不修改文件；差异时退出 1
& .\build\pcsclone.exe check .\Backup bd:/Backup
```

`copy`/`move`/`sync` 对目录操作时复制目录**内容**，尾斜线不改变含义。`sync` 只有在扫描、比较和传输都成功后才删除目标端多余文件，并在删除前重新检查源清单。

### 4.3 列表与信息

```powershell
& .\build\pcsclone.exe ls bd:/Backup                  # 默认递归
& .\build\pcsclone.exe ls bd:/Backup --recursive=false
& .\build\pcsclone.exe lsf bd:/Backup                 # 默认一层
& .\build\pcsclone.exe lsf bd:/Backup -R              # 递归
& .\build\pcsclone.exe lsd bd:/Backup
& .\build\pcsclone.exe lsjson bd:/Backup -R
& .\build\pcsclone.exe stat bd:/Backup/a.txt
& .\build\pcsclone.exe stat .\a.txt --hash
& .\build\pcsclone.exe size bd:/Backup --json
& .\build\pcsclone.exe about bd:
```

列表命令的区别：

| 命令 | 默认深度 | 输出内容 |
| --- | --- | --- |
| `ls` | 递归 | 文件和目录 |
| `ls --recursive=false` / `lsf` | 一层 | 文件和目录 |
| `lsd` | 一层 | 仅目录 |
| `lsjson` | 一层 | 文件和目录，JSON 数组 |

`lsf`/`lsd`/`lsjson` 加 `-R` 递归；`lsf` 还可用 `--files-only`、`--dirs-only`。如果某个目录下恰好只有子目录，`lsf` 和 `lsd` 的文本输出会看起来相同；在包含文件的目录下（或使用 `-R`）就能看到区别。

`ls` 的递归默认是本工具的固定契约，不等同于 Unix `ls` 的一层默认行为；需要一层输出时请使用 `lsf` 或 `ls --recursive=false`。

`stat` 输出单个 JSON 对象；`lsjson` 输出数组。`size`/`about` 默认文本，`--json` 输出对象。`stat --hash` 对远端文件会下载内容后计算 MD5。

### 4.4 删除

```powershell
& .\build\pcsclone.exe delete bd:/Backup --include "*.tmp" --dry-run
& .\build\pcsclone.exe delete bd:/Backup --include "*.tmp"
& .\build\pcsclone.exe deletefile bd:/Backup/a.txt
& .\build\pcsclone.exe purge bd:/Backup/old
& .\build\pcsclone.exe rmdirs bd:/Backup
& .\build\pcsclone.exe rmdir bd:/Backup/empty
```

删除不会弹交互确认；先运行 `--dry-run`。`purge` 拒绝文件系统根和网盘根，且不接受筛选参数。

### 4.5 过滤与日志

```powershell
& .\build\pcsclone.exe copy .\src bd:/dst --include "*.jpg" --exclude "*thumbnail*" --files-from .\list.txt
& .\build\pcsclone.exe copy .\src bd:/dst --log-file .\copy.jsonl --use-json-log --log-level DEBUG
```

日志写 stderr 或 `--log-file`；列表和 JSON 数据写 stdout。

## 5. 百度专有功能

所有命令通过远端路径选择账号。例如 `bd:` 表示账号根，`bd:/Backup` 表示该账号的目录。

### 5.1 分享

```powershell
& .\build\pcsclone.exe baidu share create bd:/Backup --period 7 --combined
& .\build\pcsclone.exe baidu share list bd: --page 1
& .\build\pcsclone.exe baidu share cancel bd: 123456789
```

拒绝分享网盘根。`--pwd` 为空时由百度生成四字符提取码。

### 5.2 转存

```powershell
& .\build\pcsclone.exe baidu transfer "https://pan.baidu.com/s/xxxx?pwd=abcd" bd:/Inbox
& .\build\pcsclone.exe baidu transfer "https://pan.baidu.com/s/xxxx" bd:/Inbox --code abcd --collect
```

目标是网盘目录，不存在时创建。账号由目标远端决定。转存不会自动下载；需要下载时使用 `copy`/`copyto` 或旧下载命令。

### 5.3 直链、搜索

```powershell
& .\build\pcsclone.exe baidu link bd:/Backup/a.txt
& .\build\pcsclone.exe baidu link bd:/Backup/a.txt --direct --json
& .\build\pcsclone.exe baidu search bd:/Backup report --recursive
```

默认直链通过 locate 接口获取临时 URL；`--direct` 输出 PCS 下载地址，实际下载时仍需要账号 Cookie。

### 5.4 离线任务

```powershell
& .\build\pcsclone.exe baidu offline add bd:/Offline "https://example.com/a.iso"
& .\build\pcsclone.exe baidu offline list bd:
& .\build\pcsclone.exe baidu offline query bd: 123456
& .\build\pcsclone.exe baidu offline cancel bd: 123456
& .\build\pcsclone.exe baidu offline delete bd: 123456
& .\build\pcsclone.exe baidu offline clear bd:
```

保存目录不存在时自动创建。

### 5.5 回收站

```powershell
& .\build\pcsclone.exe baidu recycle list bd: --page 1
& .\build\pcsclone.exe baidu recycle restore bd: 123456789
& .\build\pcsclone.exe baidu recycle delete bd: 123456789
& .\build\pcsclone.exe baidu recycle clear bd:
```

回收站 ID 是 `fs_id`，从 `recycle list` 获取。`recycle delete` 和 `recycle clear` 是永久操作。

## 6. 退出码与自动化

| 码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 操作失败、远端错误、`check` 有差异 |
| 2 | 命令/参数/选项错误 |
| 130 | Ctrl-C 取消 |

脚本示例：

```powershell
& .\build\pcsclone.exe sync .\Backup bd:/Backup --exclude "*.tmp" --max-delete 100
if ($LASTEXITCODE -ne 0) { throw "sync failed: $LASTEXITCODE" }
```

## 7. 与 BaiduPCS-Go 的关系

- 两者共享 `baidupcs`、`pcsconfig` 配置结构和 `internal/rc` 执行引擎。
- 旧入口及旧 `rc` 命令继续存在，行为不被 pcsclone 改写。
- pcsclone 使用独立配置目录；导入是单向、显式、只读旧文件的。
- 传输 worker 是 pcsclone 自身，不使用旧可执行文件；两个入口的 worker 互不依赖。
- 旧的 `rc_remotes.json` 别名可通过 `config import-pcsgo` 导入；导入后由 pcsclone 管理自己的副本。

### 7.1 `upload` / `download` 参数对照

pcsclone **没有** `upload` / `download` 子命令。上传和下载分别用本地/网盘端点之间的 `copy`、`copyto`、`sync`、`move` 表达；传输 worker 内部仍复用原来的 `pcsupload` / `pcsdownload` 实现。

先注意三条语义差异：

- `copy` 对目录复制的是**目录内容**；旧 `upload`/`download` 对目录默认会把源目录名也放进目标路径。
- `sync` 会删除目标端多余文件；`move` 会删除源；旧 `upload`/`download` 不删除任何源或额外文件。
- pcsclone 默认比较内容；旧 `download` 默认“目标已存在就跳过”，旧 `upload` 默认策略是 `skip`。

#### 下载参数

| 旧 `download` | pcsclone |
| --- | --- |
| `download REMOTE...` | `copy REMOTE LOCAL_DIR`；单文件精确目标用 `copyto`；镜像删除用 `sync` |
| `--saveto DIR` | 本地 `DEST` 参数 |
| `--save` | `DEST` 写 `.` |
| 默认 `SaveDir/uid_name/...` | 没有隐式保存目录；必须显式给 `DEST` |
| 默认跳过已存在文件 | `--ignore-existing` 最接近；pcsclone 默认跳过相同内容、覆盖不同内容 |
| `--ow` | pcsclone 默认覆盖不同内容；没有强制重下相同内容的开关 |
| `--mode pcs/stream/locate` | 只使用等价于 `locate` 的下载模式；`pcs`/`stream` 未暴露 |
| `-p N` 单文件线程数 | worker 固定 `MaxParallel: 1`，没有对应参数 |
| `-l N` 同时下载文件数 | `--transfers N`；注意 pcsclone 默认 4，旧配置默认 1 |
| `--retry N` | `--retries N`，按整个文件重试 worker；默认 2 次额外重试 |
| `--nocheck` | worker 固定不校验；需要校验用 `--checksum` |
| `--mtime` | worker 固定设置下载文件 mtime |
| `--status` | `--progress` / `-P`（周期进度，不是逐线程状态） |
| `--x`、`--dindex`、`--fullpath`、`--test` | 未提供对应参数 |
| 远端通配符 `*` | 不展开远端通配符；使用 `--include`/`--exclude`/`--files-from` |
| `.BaiduPCS-Go-downloading` 续传 | 下载先写临时文件；不提供跨命令下载续传 |

旧下载的目录名布局与 pcsclone 的对应写法：

```powershell
# 旧：D:\Downloads\我的资源\...
BaiduPCS-Go download --saveto D:\Downloads /我的资源
# pcsclone：显式把源目录名放进目标
& .\build\pcsclone.exe copy bd:/我的资源 D:\Downloads\我的资源

# 单文件：两边都是 D:\Downloads\1.mp4
BaiduPCS-Go download --saveto D:\Downloads /我的资源/1.mp4
& .\build\pcsclone.exe copy bd:/我的资源/1.mp4 D:\Downloads
```

#### 上传参数

| 旧 `upload` | pcsclone |
| --- | --- |
| `upload LOCAL... REMOTE_DIR` | `copy LOCAL REMOTE_DIR`；精确远端文件名用 `copyto` |
| 目录上传保留源目录名 | `copy` 复制目录内容；用 `copy LOCAL REMOTE_DIR/源目录名` 模拟旧布局 |
| `--policy skip` | `--ignore-existing` |
| `--policy overwrite` | pcsclone 默认覆盖不同内容；相同内容跳过 |
| `--policy rsync` | `--size-only`（同大小跳过；默认内容比较更强） |
| `-p N` 单文件线程数 | worker 固定 `Parallel: 1`，没有对应参数 |
| `-l N` 同时上传文件数 | `--transfers N`（默认 4） |
| `--retry N` | `--retries N`，按整个文件重试 worker；默认 2 次额外重试 |
| `--norapid` | 未提供；worker 仍会尝试秒传 |
| 零字节文件 | 旧 `upload` 跳过；pcsclone 会上传零字节文件 |
| 空目录 | 旧 `upload` 跳过；pcsclone 默认也跳过，可用 `--create-empty-src-dirs` 创建 |
| 符号链接 | 旧 `upload` 跟随；pcsclone 拒绝符号链接 |
| 上传续传数据库 | worker 使用独立 `rc-state/`，按账号/源/目标隔离；不与旧数据库共用 |
| 多个本地源 | pcsclone 每个 `copy` 只接受一个源；用脚本循环或先整理到一个目录 |

旧上传目录布局与 pcsclone 的对应写法：

```powershell
# 旧：/视频/Desktop/...
BaiduPCS-Go upload D:\Desktop /视频
# pcsclone：显式把源目录名放进目标
& .\build\pcsclone.exe copy D:\Desktop bd:/视频/Desktop

# 单文件：/视频/1.mp4
& .\build\pcsclone.exe copy D:\Desktop\1.mp4 bd:/视频
```

#### 公共并发、限速和校验

| 项目 | pcsclone |
| --- | --- |
| 同时传输文件数 | `--transfers N`，默认 4 |
| 比较并发数 | `--checkers N`，默认 4 |
| 失败重试 | `--retries N`，默认 2 次额外重试 |
| 限速 | `--bwlimit RATE`；**旧配置的 `max_download_rate`/`max_upload_rate` 不会用于 pcsclone 传输** |
| 传输后校验 | `--checksum`；远端会下载内容计算 MD5 |
| 仅比较大小 | `--size-only` |
| 跳过已存在/较新目标 | `--ignore-existing` / `--update` |
| 预演 | `--dry-run`；不写目标，但比较时可能读取/下载内容 |
| 缓存大小、代理、UA | 仍从 pcsclone 的 `pcs_config.json` 读取；可用 `config file` 定位后编辑 |

## 8. Shell 补全

```bash
# bash
source <(pcsclone completion bash)

# zsh
pcsclone completion zsh > "${fpath[1]}/_pcsclone"

# PowerShell 7+
pcsclone completion powershell | Out-String | Invoke-Expression
```

补全覆盖顶层命令、`config`/`baidu` 子命令和长选项，不补全文件路径；脚本由命令注册表生成，新增命令会自动出现。PowerShell 5.1 不支持 `Register-ArgumentCompleter -Native`，请使用 PowerShell 7+。

## 9. 当前限制

- 不提供 `mount`、`serve`、`bisync` 或 rclone Remote Control API。
- 执行引擎仍通过 `pcsconfig.Config` 全局值读取账号列表；pcsclone 在启动时把它指向独立配置文件。完整的依赖注入式账号/配置访问接口尚未重构。
- 不提供跨命令下载续传；上传续传状态按账号、源、目标隔离。
- 不跟随符号链接，不复制 ACL/所有者。
- 远端比较、`check`、`stat --hash`、`--checksum` 可能需要下载内容，产生流量和临时磁盘占用。
- 真实网盘验证需要在有测试账号的环境中另行执行；当前验证范围见 [pcsclone-validation.md](pcsclone-validation.md)。
