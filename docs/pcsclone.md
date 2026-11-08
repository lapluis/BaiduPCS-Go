# pcsclone 使用、迁移与构建指南

`pcsclone` 是本仓库新增的独立文件操作入口。它复用 `internal/rc` 的显式端点、计划/执行引擎和隔离传输 worker，并复用 `baidupcs` 的网盘能力，但不进入 `BaiduPCS-Go` 的旧交互界面，也不依赖旧的当前账号或工作目录。

- 命令契约：[pcsclone-command-contract.md](pcsclone-command-contract.md)
- 验证记录：[pcsclone-validation.md](pcsclone-validation.md)
- 旧的统一命令入口（仍保留）：[rc.md](rc.md)

## 1. 构建

安装 Go 后，使用固定工具链的构建脚本：

```powershell
& .\scripts\build-pcsclone.ps1
& .\build\pcsclone.exe --version
& .\build\pcsclone.exe help
```

脚本从 `.go-version` 选择发布工具链（当前为 Go 1.27.3），通过 Go 自动工具链机制下载并校验，关闭 CGO，退出时恢复调用者的环境变量和工作目录。系统 Go 安装不被替换。构建使用 `-w` 去除 DWARF 调试信息，保留 Go 符号以供 `govulncheck -mode binary` 检查实际链接的函数；使用 `-s` 剥离符号后，扫描器可能退回模块级匹配，报告未链接进程序的函数漏洞。该扫描仍不等于证明整个程序没有安全问题。指定版本字符串或输出路径：

```powershell
& .\scripts\build-pcsclone.ps1 -Version 'v4.0.2-dev' -Output 'build/pcsclone.exe'
```

手动构建（包括其他 Shell）时，使用 `.go-version` 对应的已修补工具链。`go.mod` 的 Go 1.23 是源码兼容性下限，不表示旧标准库仍有安全更新；发布版本应另行核对 [Go 安全公告](https://go.dev/security/) 和 [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) 结果。

`go build ./...` 仍然会构建旧的 `BaiduPCS-Go` 入口；两个入口共享代码，但产物独立。CI 的构建矩阵会同时输出 `BaiduPCS-Go` 和 `pcsclone_<os>_<arch>` 两种产物。`pcsclone.exe` 不需要旁边的 `BaiduPCS-Go.exe`：传输 worker 通过 `os.Executable()` 重新执行自身。

CI 先在 Windows 上用 `go.mod` 的 Go 1.23 运行全仓兼容性测试，再构建各平台。八个发布目标使用 `.go-version` 工具链、保留 Go 符号，并在两个入口均通过二进制漏洞扫描后上传；扫描器安装或扫描失败会阻止上传。Windows ARM32 保留旧工具链编译检查，但不上传其产物；PR 也不上传产物。当前发布目标为 Linux 386/amd64/arm/arm64、Windows 386/amd64、macOS amd64/arm64。[Go 1.27 的 macOS 产物要求 macOS 13 或更高版本](https://go.dev/doc/go1.27#darwin)。

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

`--cookies` 必须包含名称恰为 `BDUSS` 的非空字段，末尾分号可省略，不能和其他凭据参数混用。注意：`--bduss`、`--stoken`、`--cookies` 会出现在 shell 历史和进程列表中；在可信终端中优先使用交互登录，或从受保护的环境变量/脚本中传入。

显式传入 `--bduss` 或 `--cookies` 时，值不能为空或仅含空白，否则返回用法错误。仅提供 `--ptoken`/`--stoken` 时，即使值为空也会报错，不会转入交互登录；与有效 `--bduss` 搭配时，这些可选 token 仍可为空。省略这些凭据选项时仍可使用交互登录。

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
- Windows 中，`\Backup` 或 `/Backup` 从启动目录所在的卷根解析：在 `D:\work` 启动时均指向 `D:\Backup`；端点、`--files-from` 和 `--log-file` 使用同一规则。`D:Backup` 这类盘符相对路径会报错，请写成 `D:\Backup`。
- Windows 文件名冲突和本地路径重叠按系统的序号比较规则判断，不按 Unicode 小写文本归一；系统可区分的 `k.txt` 与 `K.txt` 可以分别传输。

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

比较完成后及执行跳过前，也会复核源和目标的身份、大小与修改时间。发现变化会报错，`sync` 不会进入删除阶段；重新运行命令可建立新计划。

`move --delete-empty-src-dirs` 只清理原计划中身份未变的源子目录，保留移动期间新出现的目录；移动单个文件时跳过目录清理。配合 `--dry-run` 会列出预计清理的源空目录，不执行删除。`sync`、`purge` 和空目录删除也会在删除目录前复核其身份，发现同名替换目录时停止。

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

`size` 和传输计划按过滤后的文件计算字节合计，上限为 9223372036854775807 字节。超出时返回错误；传输计划会在执行前拒绝。可用过滤规则分批处理，`lsjson` 仍可列出各文件的大小。

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

`purge --dry-run` 会模拟文件和子目录删除，再检查目录是否为空及身份是否改变。含工具断点状态的本地目录会返回目录非空，保留断点。实际 `purge` 在失败前可能已经删除普通文件，请先查看预演结果。

### 4.5 过滤与日志

```powershell
& .\build\pcsclone.exe copy .\src bd:/dst --include "*.jpg" --exclude "*thumbnail*" --files-from .\list.txt
& .\build\pcsclone.exe copy .\src bd:/dst --log-file .\copy.jsonl --use-json-log --log-level DEBUG
```

日志写 stderr 或 `--log-file`；列表和 JSON 数据写 stdout。

筛选字符类支持范围、否定、转义字符和 POSIX 字符类，例如 `[a-z]`、`[!0-9]`、`[\]]`（字面的 `]`）、`[[:digit:]]`。字符类始终只匹配一个路径段内的字符，不匹配 `/`。

不含字符类外分隔符的规则在任意层级按文件名匹配；前导 `/` 或字符类外的 `/` 将规则定位到传输根的相对路径。例如 `a[/Y]b` 在各层匹配 `aYb`，`/a[/Y]b` 只匹配根下的 `aYb`，`dir/a[/Y]b` 只匹配根下的 `dir/aYb`。

字符串或筛选选项缺少值时会立即报错，例如 `--exclude --dry-run` 不会执行传输。值本身以 `-` 开头时使用等号形式，例如 `--exclude=-draft*`、`--log-file=-transfer.log`；凭据选项同样适用。负整数可以分开书写，例如 `--max-delete -1`。以 `-` 开头的位置参数放在 `--` 之后。

### 4.6 下载并发、进度与取消后重试

```powershell
& .\build\pcsclone.exe copy bd:/Backup D:\Downloads -P --transfers 4 --download-threads 4 --max-connections 16
```

`--transfers` 是同时传输的文件数上限（默认 4）；`--download-threads` 是每个文件的 Range 并发上限（默认 4，范围 1–32）；`--max-connections` 是文件传输连接预算（默认 16，范围 1–256，不能小于 `--transfers`）。本次计划中需要复制或移动内容校验的文件数较少时，按这些文件分配带宽和连接额度，纯元数据跳过不占内容传输额度。单文件可使用完整的 `--bwlimit` 额度及最多 `min(download-threads, max-connections)` 个 Range 线程；文件数充足时，每文件线程数仍不超过 `floor(max-connections / transfers)`。小文件可能使用更少连接。服务端忽略 Range 时自动使用单流；预算不包含账号、目录、直链等控制接口请求。增加并发能否提速需要用实际账号和线路验证。

目录计划中的比较并发由 `--checkers` 控制，还受连接预算和待比较文件数限制。远端哈希下载同样使用 `--download-threads`，各比较槽位共同遵守 `--max-connections`；并行比较按槽位分摊带宽，单文件比较使用完整的 `--bwlimit` 额度。串行 `check` 按一个比较槽位分配默认下载资源，移动删除源前的远端哈希校验也遵守传输线程预算。

带宽以整数 B/s 分配；极低的正值 `--bwlimit` 会进一步减少内容任务并发，避免每任务最低 1 B/s 的配额合计超过设置值。换算单位后不足 1 B/s 的正值会报错，`--bwlimit 0` 表示不限速。

返回弱 ETag（`W/"…"`）的资源使用完整单流下载，不复用分片断点；初始探测若只返回文件前缀，会关闭该响应并重新请求完整文件。[RFC 9110 §13.1.5](https://httpwg.org/specs/rfc9110#field.if-range) 禁止把弱 ETag 用于 `If-Range`，它也不能证明不同分片具有逐字节一致的版本。

恢复旧断点时同样遵守当前并发限制。调低 `--download-threads` 或连接预算后，多余未完成区间会排队执行；再次取消仍会保存这些区间，无需丢弃断点重下。

`-P` 在终端原地刷新总字节、实时速度、已用时间、ETA 和正在传输的文件。速度停止更新时显示为零，无法估计的 ETA 显示 `--`。重定向、`--log-file` 和 JSON 日志不包含终端控制码，进度最多每 5 秒一条，结束时仍输出摘要。

计划总字节数为零时，百分比按已完成文件数计算；成功结束的空计划显示 `100.0%`。失败和取消的文件不计入已完成文件数。

Ctrl-C 会停止调度新文件，通知正在运行的 worker 保存断点并等待退出；无法退出的 worker 最多等待 5 秒后终止。摘要分别显示 `failed`（实际错误）、`canceled`（已开始但取消）、`pending`（尚未开始），取消退出码为 130。

文件内容传输的 worker 收到 `429`、`503` 或兼容的 `509` 响应且包含有效 `Retry-After` 时，相应请求会等待指定秒数或 HTTP 日期后再重试，等待期间仍可用 Ctrl-C 取消。缺失、无效或已过期的等待值沿用原有重试节奏。

`--retries` 对普通文件传输错误执行额外重试；断点保存失败、worker 协议错误及进度/日志回调错误会直接结束当前文件并保留错误，避免后续成功掩盖这些故障。排除故障后可重新运行命令。

远端到本地下载先写在目标文件旁的 `.pcsclone-partials/` 目录，完整下载并检查后才发布为目标文件。断点按账号、源路径、目标绝对路径隔离，并检查远端文件 ID、大小、时间、HTTP ETag/Last-Modified、本地部分文件及 Range 状态；不满足条件时安全重下。相同命令可继续有效断点，两个进程不能同时使用同一断点。工具拥有的状态目录不会进入本地扫描或反向上传；遇到用户创建的同名目录会报错并保留其内容。旧版本已经删掉的临时文件无法恢复。

新版本完成下载后记录本地 SHA-256；后续默认比较会重新校验本地内容、远端身份和当前 HTTP ETag，符合时无需再下载整个远端文件。缺少记录/ETag、身份变化或探测失败时回退到内容比较；`--checksum` 仍执行完整内容校验。移动删除源前仍完整校验内容。旧版本下载的文件没有记录；在专用测试目录中明确接受“存在即跳过”时，可加 `--ignore-existing` 避免重新比较这些文件，该选项不会检查它们是否完整。

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
| `-p N` 单文件线程数 | `--download-threads N`，默认 4；受 `--max-connections` 预算限制 |
| `-l N` 同时下载文件数 | `--transfers N`；注意 pcsclone 默认 4，旧配置默认 1 |
| `--retry N` | `--retries N`，按整个文件重试 worker；默认 2 次额外重试 |
| `--nocheck` | worker 固定不校验；需要校验用 `--checksum` |
| `--mtime` | worker 固定设置下载文件 mtime |
| `--status` | `--progress` / `-P`：终端原地刷新总进度和文件进度 |
| `--x`、`--dindex`、`--fullpath`、`--test` | 未提供对应参数 |
| 远端通配符 `*` | 不展开远端通配符；使用 `--include`/`--exclude`/`--files-from` |
| `.BaiduPCS-Go-downloading` 续传 | `.pcsclone-partials/` 保存经过身份检查的下载断点；重复相同命令续传 |

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
| 单文件下载并发 / 传输连接预算 | `--download-threads N` 默认 4；`--max-connections N` 默认 16 |
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

PowerShell 在命令行中间补全时，只解析光标前的命令和参数。升级后请重新执行上面的命令生成并加载补全脚本，使当前终端使用新版规则。

## 9. 当前限制

- 不提供 `mount`、`serve`、`bisync` 或 rclone Remote Control API。
- 执行引擎仍通过 `pcsconfig.Config` 全局值读取账号列表；pcsclone 在启动时把它指向独立配置文件。完整的依赖注入式账号/配置访问接口尚未重构。
- 下载跨命令续传需要服务端支持 Range 并提供有效的实体校验信息；否则重下。上传续传状态按账号、源、目标隔离。
- 不跟随符号链接或 Windows 目录联接，路径的父目录也受此限制；不复制 ACL/所有者。
- 远端比较、`check`、`stat --hash`、`--checksum` 可能需要下载内容，产生流量和临时磁盘占用。
- 真实网盘验证需要在有测试账号的环境中另行执行；当前验证范围见 [pcsclone-validation.md](pcsclone-validation.md)。
