# 文档目录

## 本 fork 文档

### 独立入口 `pcsclone`（推荐）

| 文档 | 说明 |
| --- | --- |
| [pcsclone.md](pcsclone.md) | `pcsclone` 构建、独立配置、账号迁移、常用文件操作与旧命令参数对照 |
| [pcsclone-command-contract.md](pcsclone-command-contract.md) | `pcsclone` 命令行规范、路径语义、退出码与输出格式契约 |
| [pcsclone-validation.md](pcsclone-validation.md) | 当前验证总览、实盘测试结果与发布前待办 |
| [pcsclone-validation-archive.md](pcsclone-validation-archive.md) | 2026-10-23 至 2026-11-15 逐轮审计与回归测试完整归档 |

### 旧入口 `rc` 与维护流程

| 文档 | 说明 |
| --- | --- |
| [rc.md](rc.md) | `BaiduPCS-Go` 内置 rclone 风格命令使用指南（旧入口） |
| [rc-implementation.md](rc-implementation.md) | `internal/rc` 补丁边界、来源与旧命令兼容性说明 |
| [rc-validation.md](rc-validation.md) | 旧 `rc` 入口初始基线验证与历史参考记录 |
| [patch-series.md](patch-series.md) | 补丁分组、上游同步与补丁导出流程 |

## 上游百度 PCS API 参考

### 文件 API

- [综述](overview.md)
- [文件 API 列表](file_data_apis_list.md)
- [文件 API 错误码列表](file_data_apis_error.md)

### 结构化数据 API

- [综述](structured_data_apis_overview.md)
- [结构化数据 API 列表](structured_data_api_list.md)
- [结构化数据 API 错误码](structured_data_apis_error.md)
