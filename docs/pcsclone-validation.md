# pcsclone 验证记录

## 2026-11-06：进度、取消与下载续传回归

后续复查新增回归：模拟目标文件刚发布就被其他程序改写，旧实现会把修改后的摘要与原远端 ETag 绑定并错误跳过。修复后在发布前计算下载临时文件的摘要；完成记录升级为版本 2，旧版本记录回退到完整内容比较。有效的部分下载断点不受记录升级影响。

错误响应回归：初始探测的 403/429/500 响应不得发布文件实体信息或触发实体变化重置，已有部分文件和 Range 状态保持不变；分片的 403/404/429/509/500 保留各自 HTTP 错误分类，不写入响应体、不推进区间，也不误报实体变化。成功响应中的 ETag/Last-Modified 真正变化时仍拒绝旧断点。

弱 ETag 回归：模拟服务端在收到弱 `If-Range` 时返回完整响应，旧实现因分片区间不符失败。修复后关闭前缀探测响应，使用不带 Range/If-Range 的完整单流响应；有无 Last-Modified 均逐字节一致。旧弱 ETag 断点不再被复用，即使同时带有 Last-Modified。

默认 URL 探测取消回归：本地服务器分别阻塞初始探测、备用服务器探测的响应头；取消 `ExecuteWithContext` 后执行及时返回 `context.Canceled`，服务器确认请求上下文已取消。备用探测使用客户端副本设置超时，不再临时改写调用方共享客户端。旧式自定义 `DURLCheckFunc` 继续保持原接口，其回调内部的网络请求需要自行使用上下文；pcsclone 的下载回调已这样处理。

恢复并发回归：旧断点包含 4 个未完成区间、当前并发改为 1 时，旧实现仍建立 4 个连接。修复后多余区间排队，保存断点时包含排队区间，完成判断也等待队列清空。JSON/Protobuf、平均分片/按块分片四种组合均验证“恢复→再次取消→再恢复”，最大分片连接数始终为 1；最终内容逐字节一致，实际下载量只包含原来未完成的字节，含尚未分配的块。

EOF 完整性回归：模拟 8192 字节探测前缀恰好填满缓冲区后才单独返回 EOF；旧实现不请求剩余 8192 字节便报告成功，随后扩展文件造成尾部为零。修复后仅在整个区间写入完成时报告成功，探测前缀结束会继续请求剩余区间。未知长度单流的 `io.ErrUnexpectedEOF` 返回错误、不触发成功事件，也不从已写偏移重复请求完整流。完成记录升级为版本 3，版本 1/2 均回退到完整内容比较，避免继续信任可能由旧 EOF 行为产生的记录；有效部分下载断点仍可使用。

写盘回归：注入数据全部写入后 `Sync` 失败，确认下载返回该错误、不触发成功事件、不删除断点；恢复正常后重试成功并删除断点。另注入短写且返回 nil 错误的 WriterAt，确认转为 `io.ErrShortWrite`，不推进范围或已下载字节数。文件输出先完成截断及落盘，再报告成功。

断点保存错误回归：使用只读断点文件分别触发定期保存、取消收尾保存失败，确认下载停止、HTTP 请求结束，底层文件错误仍可识别，取消原因同时保留。断点先写临时文件并落盘，再替换旧记录；Windows 实际禁止替换的文件句柄验证替换失败时旧记录原样保留、临时文件清理，解除占用后重试可保存并重新读取。子进程协议只传递固定的保存失败标记，主程序保留该错误、计入 failed，并在取消提示外说明断点未能保存；普通取消仍单独计入 canceled。

数据先于断点落盘回归：在写入 1024 字节后注入数据文件 Sync 失败，定期保存与取消收尾均返回保存失败并保留旧断点，不把未落盘字节标为已完成。保存期间使用下载写锁协调数据落盘与区间快照，复制区间值后再序列化；另以受控交错验证，数据落盘完成后的新写入不会被较早的断点错误认证。

断点读取回归：执行器通过可返回错误的 Load 读取断点，拒绝损坏格式、未知模式、越界生成位置、非正块大小和重叠的未完成区间；JSON/Protobuf 均在修改输出前返回失配错误。合法的零长度完成标记仍可读取。断点读取限制为 16 MiB，文件读取错误原样返回而非 panic，也不把 I/O 故障误判为需删除的损坏状态；兼容 Get 接口同样不再 panic。pcsclone 对格式失配仍走已有的部分下载重置与重试流程。

已完成断点收尾回归：非空文件的断点已无剩余区间时，旧实现返回 no workers。现在仅在已下载字节等于总大小、生成器结束且没有排队区间时允许收尾。JSON/Protobuf、两种分片模式验证无需额外数据请求、内容不变、最终落盘成功后清理断点；最终落盘失败仍保留断点、不报告成功。缺少字节、仍有未分配块或排队区间时不能误报完成。

收尾事件回归：成功事件改为数据落盘及断点清理成功后同步执行，结束事件在返回前同步执行；关闭或删除断点的错误不再被忽略，断点已不存在则按清理完成处理。Windows 实际阻止删除断点时，执行返回文件错误、不触发成功事件、仍触发结束事件，下载数据保持完整；解除占用后重试成功。正常成功回调可确认断点已经移除，且严格先于结束回调。

进度回调生命周期回归：分别在正常结束、取消时阻塞已开始的进度回调，验证执行器等待其退出后才触发最终事件并返回。回调绑定本次任务的完成通道，完成与 tick 同时就绪时不启动新回调；移除旧下载任务中用于抑制迟到输出的共享 isComplete 标志，最终状态不再与遗留进度回调并发打印。

启动控制回归：Cancel 的执行级上下文在探测前建立，取消动作同步发出并保护注册/读取；默认探测与新增上下文探测接口，在初始/备用服务器两阶段均验证手动取消结束请求且不取消调用方的父上下文。百度下载探测使用新接口。开始回调同步完成后才启动 worker，回调内取消不会发送数据请求；开始事件不再被成功/结束事件越过。

备用服务器探测回归：并发探测共享结果列表时曾实际遗漏已校验服务器（513 条只保留 511 条）；追加结果使用互斥保护，20 轮、每轮 512 个备用地址均完整保留且无重复。另以本地 HTTP 403/500 验证，错误响应体在解析器读取前保持打开，并在探测结束时关闭；错误文档不会进入下载服务器列表。

镜像身份回归：默认探测比较补齐 ETag，缺少 ETag 时比较 Last-Modified；同 ETag 的镜像不因日期差异被错误拒绝。响应元数据构造函数保留 ETag、Last-Modified 和已有 MD5/CRC 字段，初始信息比较同样检查实体版本。实际多服务器分片测试验证，同大小但 ETag 不同的镜像没有收到数据区间请求，健康镜像仍参与传输，最终内容逐字节一致。

本轮针对用户上传、下载日志改进 `pcsclone`，保留下方历史验证记录。本轮没有访问真实网盘或修改用户测试目录。

- `go test ./... -count=1 -timeout 180s`、`go build ./...`、`go vet ./...` 通过。
- 新增取消队列测试：2 个运行任务取消，4 个未启动任务保留为 pending，failed 为 0；子进程通过 stdin 收到取消并保存检查点后退出。
- 本地 HTTP Range 服务器验证：中断保存断点，重新打开后继续剩余区间，最终逐字节一致且传输量小于整文件重下；忽略 Range 时退回单流；ETag 变化拒绝混合内容。
- 稳定临时路径、原子发布、远端/本地身份变化重置、缺少实体校验信息重置、同一断点独占锁及释放、用户同名状态目录保护、内部状态不参与本地扫描均有回归覆盖。
- 完成记录校验本地 SHA-256，并实时探测远端 ETag；相同元数据但 ETag 改变时不信任完成记录。探测不读取完整响应体、不创建目标文件，错误状态/大小变化不被接受。
- 终端原地刷新、Unicode 路径截断、控制字符处理、重定向/JSON 日志节流和无 ANSI 控制码、并发参数边界均有回归覆盖。
- 独立可执行文件通过 `scripts/test-pcsclone-local.ps1`：本地文件操作、帮助、退出码、配置导入和敏感值隐藏均 PASS。

真实账号下载速度、真实控制台 Ctrl-C 和断点后重试仍需用户复测；本地模拟通过不代表网盘限速已解除。本轮未运行 `go test -race`。

## 2026-10-23：独立入口初始验证

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
- `ls` 默认递归；`lsf`/`lsd`/`lsjson` 默认一层；`-R` 递归。混合目录下验证 `lsf` 输出文件+目录、`lsd` 只输出目录；只有子目录的目录两者文本相同。
- `lsjson` 数组、`stat` 单对象与 `--hash`、`size` 文本/JSON。
- 参数错误返回 2；源目标重叠、check 差异返回 1；未知命令返回 2。
- `completion bash|zsh|powershell` 生成脚本，包含顶层命令、嵌套子命令和长选项；不支持 `fish` 时返回 2。脚本内容经过单元测试检查，未在真实交互 shell 中逐键验证补全行为。
- 上传/下载 worker 的自调用代码路径通过源码检查和配置继承冒烟覆盖；**未执行真实传输**。

### 1.2 配置与导入覆盖

- `config import-pcsgo` 的 dry-run、正式导入、重复导入；文本输出列出 `Remotes:`，JSON 输出包含 `remotes` 数组。
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

取消与调度交错的新增回归：`TestCanceledPendingRangeCannotBeReassigned` 验证已取消但仍有待下载字节的 worker 不会被新区间覆盖，断点仍保留缺失区间，下载计数不会虚增；`TestAvailableWorkerRequiresNoRemainingBytes` 验证成功和取消状态都必须没有剩余字节才能复用。完成记录升级至版本 4，版本 1–3 必须重新进行完整内容比较，避免沿用旧调度逻辑可能产生的错误完成记录。

动态分片与写入交错的新增回归：`TestQueuedWriteRechecksSplitBoundary` 在 worker 排队写盘时缩小区间，验证实际写入与进度只覆盖新的边界；`TestDynamicSplitUsesCommittedWriteProgress` 模拟正在提交的写入，验证分片使用提交后的偏移，不会产生结束位置小于起始位置的断点区间。边界调整、写入前裁剪、写盘与进度提交使用同一把锁。

未知长度响应的收尾回归：`TestUnknownLengthDownloadFinalizesActualSize` 使用真实本地 HTTP chunked 响应，验证空内容和短内容都在成功回调前清除旧文件尾部；`TestUnknownLengthDownloadReturnsFinalSizeFailure` 验证设置最终大小失败会返回错误且不触发成功回调。未知长度流仅在成功读到 EOF 后按实际下载字节数设置文件大小。

部分响应与完整流的新增回归：`TestUnknownLengthPartialResponseCannotCompleteFile` 验证未知长度的 206 响应不能被当作完整文件，错误响应不修改已有输出；`TestUnknownLengthPrefixProbeFetchesFullStream` 验证未知总长度的探测前缀会被丢弃并重新请求完整流；`TestFullStreamWorkerRejectsPartialResponseBeforeWriting` 验证完整流 worker 在写入前拒绝部分响应，包括响应长度恰好等于预期长度的情况。

单 worker 限流重试回归：`TestSingleWorkerRetriesThrottledResponse` 分别验证分片与完整流模式收到 429/509 后能够重试并得到完整文件，避免只有一个 worker 时一直停滞；`TestThrottledWorkerHonorsPauseAndRetryBudget` 验证重试仍受暂停状态与现有连接重置预算控制。

暂停与调度交错的新增回归：`TestPauseSerializesWithPendingRangeAssignment` 在调度已通过初始暂停检查、等待连接预算时调用暂停，验证暂停返回后新分配的区间也被覆盖；`TestRangeWorkerCallbackCanPauseMonitor` 验证 worker 遍历回调仍能调用暂停而不死锁。暂停、恢复、区间分配、重试与动态分片共用调度锁，回调遍历使用锁内取得的 worker 列表快照。

结束与恢复交错的新增回归：`TestResumeCannotRestartWorkerAfterCanceledMonitorReturns` 验证已取消并返回的任务不会被恢复操作再次启动或改写最终状态；`TestCanceledMonitorCannotAssignPendingRangesAfterReturn` 验证取消入口提前返回后也不能继续分配区间。监控器在等待 worker 和保存最终断点之前关闭调度入口，恢复、重试、分片和区间分配都检查终止状态。

启动回调暂停恢复的新增回归：`TestStartupPauseResumeKeepsWorkersUnderTaskContext` 验证启动回调中的暂停/恢复不会提前发出请求，正式启动后的请求仍受任务取消控制；`TestStartupPauseResumeDownloadsExactlyOnce` 验证重复切换后只请求一次、内容完整且下载计数准确。监控器接管前的恢复只清除暂停状态，不启动 worker。

上传启动回调回归：`TestUploadStartupCallbackFinishesBeforeNetworkAndFinalEvents` 分别验证正常继续、取消和优雅停止。多分片上传会先等待开始回调返回，再预创建或发送分片；回调内取消/停止不会发送任何网络请求，最终事件也不会早于开始回调结束。正常继续仍完成分片与合并。

上传分片摘要回归：`TestUploadTmpFileRejectsMalformedDigest` 验证分片成功响应的 MD5 必须为 32 位十六进制字符串，拒绝短值、长值、非十六进制和空白值，避免将异常响应作为完成摘要交给断点记录。大小写十六进制均接受，缺失摘要保留原有错误类型，所有响应体都会关闭。

旧上传断点摘要修复回归：`TestLegacyInvalidUploadDigestIsReuploaded` 从磁盘读取包含异常摘要的旧数据库，经真实分片调度和模拟合并验证只重新上传异常分片，保留上传会话与合法分片；覆盖短值、长值、非十六进制、空白、待上传、合法大小写摘要。读取数据库仅修复内存中的待上传标记，不主动改写原文件。

上传断点安全写入回归：`TestUploadDatabaseReplacementFailurePreservesPreviousRecord` 在 Windows 阻止文件替换时验证保存报错、旧记录字节不变、临时文件清理，解除占用后重试可以保存并重新读取新进度；`TestUploadDatabaseReadOnlySavePreservesPreviousRecord` 验证只读句柄不会被临时文件替换绕过。保存过程先完整写入并同步临时文件，再替换正式记录，不再先截断旧记录。

上传断点保存错误上报回归：`TestCanceledUploadReportsCheckpointFailure` 验证取消时保存失败保留原始 I/O 错误并上报可识别标记；`TestPeriodicUploadCheckpointFailureStopsBeforeMerge` 验证周期保存失败会取消在途分片、阻止合并且保留旧记录；`TestCompletedUploadReportsCheckpointCleanupFailure` 验证远端合并后清理断点失败不再报告整个任务成功或自动重传。worker IPC 回归同时覆盖上传和下载的取消与断点失败分类。

首次上传会话落盘回归：`TestNoRapidUploadPersistsInitialSessionBeforePartsStart` 使用模拟元信息查询和预创建响应，验证 `--norapid` 在分片启动前将新会话加入数据库并保存；关闭、重新打开数据库后仍可恢复会话，重启任务不再发起预创建握手。

`--norapid` 准备阶段取消回归：`TestNoRapidPreparationCancellationReachesHeadersAndBody` 分别阻塞目标元信息查询和预创建握手的响应头/响应体，验证任务取消会停止请求、关闭响应体并直接结束准备阶段，不再回退到秒传；`TestCanceledNoRapidHandshakeDoesNotSendRequests` 验证已取消的上下文不会发出请求。原有无上下文接口保留，新增接口将上下文贯穿目标检查、元信息请求和预创建响应解析。

普通上传准备取消回归：`TestRapidPreparationCancellationCoversEveryRequest` 覆盖目录列表、用户 UK、目标元信息和秒传握手的响应头/响应体阻塞；准备阶段取消不会继续上传或自动重试。`TestNoRapidPreparationCancellationReachesHeadersAndBody` 追加大文件容量查询的两种阻塞场景。`TestPreparationCacheWaitCanBeCanceled` 验证缓存等待可取消、不会中断缓存拥有者，且热缓存不会绕过取消。`TestRapidUploadContextPreservesTargetChecksAndHandshake` 验证新增上下文接口保留目标目录保护和握手参数。

本地摘要取消回归：`TestCanceledChecksumPreservesPreviousSnapshot` 验证空文件和非空文件的提前取消不改写已有 MD5、切片摘要、CRC32 或分块结果；`TestSumCancellationDuringReadPreservesDigestsAndResetsPosition` 在读取大文件期间取消并验证摘要未发布、读取位置复位；`TestChunkedSumCancellationPreservesPreviousBlocks` 验证分块计算取消后保留旧结果且文件句柄仍可使用。`TestUploadPreparationCancelsLocalChecksumBeforePublishing` 验证上传准备已接入可取消摘要接口，取消后不再发起后续请求。

预创建响应校验回归：`TestUploadPrecreateRequiresSupportedModeAndUsableSession` 覆盖十种成功/异常响应，拒绝未知模式或无法取得有效会话的分片上传响应；保留普通秒传命中及恢复已有会话时省略 ID 的兼容行为，`--norapid` 只接受可继续分片上传的会话。`TestRapidPreparationRetainsPrecreateFailure` 验证异常会话、远端容量错误和 JSON 解析错误都保留在任务结果中，不再返回空错误或继续分片阶段。

恢复上传会话更新回归：`TestRenewedUploadSessionReachesCheckpointPartsAndMerge` 经模拟目录查询、握手、真实上传适配器和合并路径，验证服务端返回新会话 ID 后，内存状态、落盘断点、分片请求和合并请求均使用新 ID；同时覆盖返回相同 ID 和省略返回 ID 的兼容情况。

该轮全仓复查未全绿：构建、`go vet` 和上传会话回归通过；随机全仓测试遇到本机 TCP `connectex` 端口占用错误。等待连接回收后按包串行复跑，其余包通过，下载包单独按同一 shuffle 种子 `1793986091576393300` 复跑仍遇到相同端口错误。未修改测试断言或系统端口设置；本轮不能据此声称全仓测试通过。

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
