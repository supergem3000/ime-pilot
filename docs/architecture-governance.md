# IME Pilot 代码架构与交接

## 包与依赖

| 包 | 职责 |
| --- | --- |
| `cmd/ime-pilot` | 系统入口、单实例检查，调用组装入口 |
| `internal/bootstrap` | 注册本机方案、构造并连接核心、UI、HTTP 和系统信号 |
| `internal/core` | 通用配置、规则、插件会话与接管、请求调度、设置用例；按文件分工，不拆成多个业务包 |
| `internal/desktop` | Wails 绑定、窗口与托盘、表单类型、公共前端渲染 |
| `internal/rpc` | 本机 HTTP、鉴权、限流、发现文件；会话与接管处理交给核心 |
| `internal/signals/windows`、`macos` | 将平台应用与前台事件转换为核心类型，核实业务目标身份 |
| `internal/schemes/windowsimm` | IMM 切换实现、配置校验、表单描述 |
| `internal/schemes/windowskey` | 豆包 Shift 方案：只读 IMM 状态、等待按键释放、单次切换与回读；配置为空对象 |
| `internal/schemes/windowslayout` | 布局和输入法配置、表单与切换策略；组合平台的布局请求和 TSF 激活能力 |
| `internal/schemes/macosinputsource` | TIS 输入源方案、配置校验、表单描述 |
| `internal/config` | 配置文件读写与原子保存 |
| `internal/platform/windows` | 窗口与进程、前台钩子、IMM、键盘布局、TSF、按键状态和 SendInput、文件替换与权限、命名互斥量、原生提示框 |
| `internal/platform/darwin` | AppKit 应用事件、Carbon/TIS 输入源桥接、文件锁 |
| `internal/lifecycle` | 选择单实例锁的名称与存储位置，调用平台锁并转换业务错误 |

`core` 不导入上述具体接入或实现包。`desktop`、`rpc`、`signals`、`config` 使用核心定义的契约。具体方案使用 `desktop.Form`，因此方案包确实依赖 desktop；desktop 不反向引用任何具体方案。bootstrap 是连接具体实现的位置。

`platform` 只是分组目录，没有统一平台接口。其两个子包不依赖 core、desktop、schemes 或 signals；对外提供原生数据与操作，业务类型和错误由调用方转换。系统专有 API（Win32、COM、cgo、文件锁等）集中在这里，普通 Go 文件读写仍可留在使用它的包中。

```text
schemes ────────→ platform/windows 或 platform/darwin
signals ────────→ platform/windows 或 platform/darwin
config / rpc ──→ platform/windows（文件替换、ACL）
lifecycle ─────→ platform/windows 或 platform/darwin（锁）
```

平台提供“读取状态、发送消息、选择输入源”等能力；方案决定目标模式、调用顺序和结果验证；signals 将原生事件组装为 `core.Target` / `core.Activation`。平台通过回调通知事件或核实有效性，不接收核心业务对象。

## 接口与实现在哪里

- `core/switcher.go`：`Switcher` 只有 `SwitchEn(ctx)`、`SwitchCN(ctx)`，不接收业务 Target。状态查询 `Inspector` 和回读结果 `Reporter` 是可选能力。
- `core/signals.go`：`SignalSource` 及应用、激活事件类型，由各系统 signals 包实现。
- `core/config.go`：通用配置与 `ConfigStore`，由 config 包实现持久化。
- `desktop/form.go`：Form、Field、Option 等 UI 描述类型；控件支持 select、text、toggle。SchemeInfo 提供方案名称和适用范围描述，列表统一按两行展示。
- 各方案的 `config.go` / `scheme_*.go`：解释和校验方案自己的配置、构造执行实例。
- 各方案的 `form_*.go`：返回自己的说明、字段与动态选项，全部留在方案包内。
- `bootstrap` 的 registration：组装用结构体，关联方案信息、表单函数、校验函数和构造函数，不要求方案实现一个大接口。

`schemes/verification` 是两个现有方案复用的查询、应用、回读工具，承接原 Controller。它依赖 core，不被 core 依赖；新方案不必实现 Snapshot/Apply 或使用这个工具。

## 执行与配置

系统激活 → core 规则判断 → 调度；插件 HTTP → core 会话/接管判断 → 同一调度。手动操作由 core 服务执行，与自动切换共享操作锁，保持手动操作不受全局自动暂停影响的行为。

核心持有业务 Target，检查接管、前台有效性、取消和过期。方案在执行前创建原生上下文，通过注入的有效性回调继续核实核心保护；回调不暴露业务 Target。前台检查不能持有核心锁等待 GUI 线程。

支持回读的方案在内部确认结果。没有回读能力的方案只报告调用完成，不能伪称状态已确认。macOS 输入源回读只代表选中该输入源，不代表输入法内部中文状态。

通用配置保存 `schemeId` 和 `schemeConfig`，全局使用一个方案。表单由方案提供，公共前端不按方案 ID 判断字段。保存先校验，在操作锁内持久化并使旧请求失效；执行时按已保存配置构造操作实例。不自动回退方案。

前端修改后串行自动保存，文本输入合并 300 ms 内的修改。方案必填项完整后整体提交，未填完整时规则使用已生效的方案独立保存；失败保留待保存内容并提供重试，外部配置冲突要求重新加载。后台刷新和保存响应不能覆盖请求期间的新编辑。

配置只解析已定义的字段，忽略未知字段，不做迁移。应用规则显式保存 `enabled` 和 `targetMode`；新规则默认目标为英文。仅在配置文件不存在时创建平台默认配置；JSON 格式或已定义字段的值无效时，报错且不覆盖原文件。

`defaultMode` 配置其他应用的行为：`chinese`、`english`，空字符串表示不切换（默认）。激活时先判断插件接管，再匹配单独规则，仅未匹配的应用使用默认行为；停用的单独规则不落入默认行为。默认行为与应用规则共享暂停、取消、方案执行和接管检查。界面修改自动保存，下次应用激活时生效。

应用规则只保存程序标识、启用状态和目标模式，不保存名称或图标。desktop 的 `AppInfo` 是展示数据，由 bootstrap 接入平台读取能力；前端异步读取并在内存缓存，手动刷新应用列表时清空缓存。Windows 按 exe 路径读取 FileDescription / ProductName 和图标资源；macOS 按 Bundle ID 查找已安装应用并读取名称、图标，不要求应用正在运行。缺少元数据时显示文件名或 Bundle ID 和占位图标。名称和图标不参与匹配，不读取窗口标题。

新增方案：添加一个方案 package，再在 bootstrap 对应系统注册一次，不修改核心规则和中央 UI 的方案分支。

`windowskey` 的方案 ID 为 `windows.shift`，配置为 `{}`，界面名称为“模拟 Shift 按键”。用户先在目标应用选中豆包并保持 Shift 中英文切换设置。方案只读取 IMM conversion/open；已符合目标时不按键，否则在请求时限内等待所有按键松开，重新读状态，发送一次 Shift down/up 后仅轮询回读。失焦、超时或发送失败均停止，不盲目重按，不写 IMM 状态，不做组字检测。根窗口焦点和属于该窗口的子控件焦点均可使用，但请求期间焦点发生变化就作废。

`platform/windows/keyboard_windows.go` 封装按键状态读取和 `SendInput`，INPUT 布局兼容 32/64 位；不记录按键历史。部分发送只补发必要的 key-up 清理，仍报告失败。SendInput 作用于前台输入流，前台检查与发送不是原子操作；它受 Windows 完整性级别限制，主体不能向权限更高的应用发送按键，见 [SendInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput)。此方案不识别目标线程的具体 TSF 输入法，不宣称适用于其他输入法；回读仅确认 IMM 兼容状态。

Shift 独立实验已在豆包 0.9.3.20 / 资源管理器验证双向实际输入，包括切回窗口、尚未点击搜索框时切换。产品接入已通过 Windows 离线构建与静态检查。2026-10-31 新版主程序测试框实测：切英文后按 n 直接上屏且无候选；切回中文后按 n 出现候选，空格上屏“你”，双向切换通过。测试文字已清理，原方案已恢复。本次仅验证手动切换，没有测试应用规则或 IDEA 插件链路。

`windowslayout` 的配置为 `chineseLayoutId` / `englishLayoutId`。纯布局标识为 8 位 HKL，使用 `WM_INPUTLANGCHANGEREQUEST` 并回读目标线程 HKL；具体输入法标识为 `tsf:语言ID:{CLSID}:{ProfileGUID}`，由 TSF 枚举已启用的键盘输入法，避免多个输入法共用 HKL 时混淆。标识失效时明确报错，不改输入法内部状态。

选定具体输入法时，`platform/windows/tsf_com_windows.go` 用 Go 直接调用系统 COM，锁定 OS 线程完成初始化、枚举、激活和释放；没有自建 DLL、线程注入或 C++ 构建步骤。执行时先向目标输入窗口发送有超时的语言布局请求，再调用 TSF `ActivateProfile(TF_IPPMF_FORSESSION)` 选择输入源。TSF 激活覆盖当前桌面，会影响其他应用；前台、焦点与超时检查不能将这个系统接口限制成单窗口作用域。COM 调用无法中途取消，返回后再次检查有效性。接口依据见 [ActivateProfile](https://learn.microsoft.com/en-us/windows/win32/api/msctf/nf-msctf-itfinputprocessorprofilemgr-activateprofile)。

这条路径只实现 `core.Switcher`，不实现 `Inspector` / `Reporter`：主程序自己的 TSF 状态不能证明目标应用已切换，HKL 也不能区分豆包和微软拼音。成功调用记录为 `applied`，界面提示核对实际输入；仅选择 HKL 的配置仍使用原有回读工具。方案不启用输入法、不修改默认输入法、不模拟按键。

## 验证与后续

平台层重构已通过 Windows 主程序和诊断工具的离线构建、`go vet ./...`、macOS ARM64/AMD64 公共包及文件锁/单实例包编译、macOS 桌面依赖路径解析。没有新增或运行单元测试；已有前台监听测试随实现迁到平台包。未启动程序做实机联调，macOS 原生桌面完整构建与运行仍需在 Mac 验证。

2026-10-31 Windows + 微软拼音实机验证：手动中英文切换与真实输入、普通规则切中文/英文、IDEA 未勾选普通规则时退出 Insert 切英文、停用插件后规则执行、重新启用后接管均通过。中文/英文通过系统只读探针回读确认；使用已安装的 IDEA 插件 0.3.0 / IdeaVim 2.47.3，未验证 0.4.1 的 Windows 安装包。修复状态刷新重建规则控件、打断下拉选择的问题，并验证编辑保存；原规则和插件启用状态已恢复。

本轮未覆盖插件异常退出后的租约超时、暂停、快速失焦取消和托盘退出。macOS 原生桌面编译与实机验证需回到 Mac 完成，不能用公共包编译代替。

Windows 键盘布局方案已完成生产构建和静态检查，未运行单测。2026-10-31 实测 `08040804` 与 `04090409` 双向手动切换、实际字母/拼音候选输入、普通规则双向切换、IDEA 插件独立切英文及停用后普通规则执行通过，系统探针回读布局一致。原 IMM 方案、英文规则和插件启用状态已恢复；其他布局/应用、跨登录会话标识及快速失焦边界尚未实测。

不修改 IdeaVim 配置，不做组字检测，不引入 macOS 辅助功能权限。只有显式选择 Shift 方案才模拟按键，默认 IMM 方案不变。未经用户要求，不新增或运行单元测试；缺少环境或下载依赖先确认，环境变量仅临时设置。

2026-10-31 纯 Go TSF 实机验证：删除自建 DLL 后，豆包与微软拼音独立枚举及配置保存、豆包与美国英语布局手动互切、资源管理器规则双向切换、豆包与微软拼音互切通过；核对真实按键、候选窗口与中文上屏，未回读目标线程 TSF 身份。原配置和规则已恢复。未覆盖 32 位/ARM 应用、提权应用、其他第三方输入法及快速失焦压力场景；本次未重新验证 IDEA 插件链路。
