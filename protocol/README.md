# 插件通信协议

定义插件注册、接管程序和请求切换输入法的接口。

Windows 和 macOS 主体均提供本接口。macOS 插件请求只校验前台应用 PID，不需要辅助功能权限；Windows 仍校验 PID 与 HWND。

## 1. 怎么连接

插件读取当前用户的 runtime.json，取得端口 port 和认证令牌 token。

- Windows：%LOCALAPPDATA%/ime-pilot/runtime.json
- macOS：~/Library/Application Support/ime-pilot/runtime.json

发现文件包含 port、token。

所有请求使用 POST http://127.0.0.1:<port>/api/rpc，JSON 编码为 UTF-8，带 Authorization: Bearer <token>。主体只监听本机回环地址；插件不走代理、不跟随重定向。文件存在不代表主体在线。

## 2. 请求和响应

四个接口共用下面的外壳，只替换 method 和 params：

```json
{
  "requestId": "request-001",
  "method": "renewSession",
  "params": {"sessionId": "current-session"}
}
```

requestId 每次新请求生成 UUID；重试同一请求沿用原 ID。注册不需要 sessionId；续期和切换通过 sessionId 校验有效会话。

响应体统一包含三个字段：

| 字段 | 含义 |
| --- | --- |
| code | 字符串结果码，见第 4 节 |
| data | 接口返回数据，无数据时为 null |
| message | 人类可读的说明文字；程序只根据 code 判断结果 |

```json
{
  "code": "changed",
  "data": {"targetMode": "english", "actualMode": "english"},
  "message": "已切换到英文"
}
```

响应通过对应的 HTTP 请求关联，不额外回传请求身份字段。HTTP 200 只表示收到了业务响应；只有 code 为 changed 或 already_selected 表示切换成功。

## 3. 四个接口

### 注册：registerPlugin

params 示例：

```json
{
  "pluginId": "ime-pilot.jetbrains",
  "name": "ime-pilot JetBrains",
  "version": "0.4.1",
  "instanceId": "74e872f0-ae15-4f59-9dab-155cce418d58",
  "programs": [
    {"kind": "windows.executableName", "value": "idea64.exe"}
  ]
}
```

- pluginId 是插件的固定产品 ID；name、version 用于展示。
- instanceId 是本次插件运行的 UUID，重连期间不变，插件重新加载后更换。
- programs 为非空列表，只声明要接管哪些程序，不声明观察能力或 Vim 状态。

程序支持三种写法：

| kind | value 示例 | 覆盖范围 |
| --- | --- | --- |
| windows.executableName | idea64.exe | 所有同名可执行程序，忽略大小写 |
| windows.executablePath | C:\Apps\IDEA\bin\idea64.exe | 规范化完整路径对应的程序 |
| macos.bundleId | com.jetbrains.intellij | 系统识别出的相同 Bundle ID 应用 |

精确匹配，不支持通配符，全部覆盖匹配程序的所有实例和窗口。注册整批成功或整批失败，不留下部分接管。不同插件的范围重叠则整个注册失败；名称范围包含某个路径时也算重叠，无法核实时拒绝。注册成功后立即屏蔽对应普通应用规则，并取消这些规则尚未执行的切换；注册本身不切输入法。

成功 code=ok，data 为 {sessionId,leaseMs:15000,renewAfterMs:5000,paused}。同一 pluginId + instanceId、相同注册内容返回原会话，不产生重复接管；内容不同则 registration_conflict。修改接管列表需要先注销再注册。

每次建立会话生成全新的随机 sessionId，不复用、不持久化恢复。主体重启时清空会话、接管关系和请求缓存。

### 续期：renewSession

params 为 {sessionId}。成功 code=ok，data 为 {leaseMs:15000,renewAfterMs:5000,paused}。

插件每 5 秒续期，主体从成功处理续期时起保留会话 15 秒。注册重试不替代续期。单次请求失败不立即解除接管；sessionId 不存在或租约已到期时，续期和切换统一返回 session_expired，插件须重新注册。

### 切换：setInputMode

Windows params 示例（macOS 省略 windowId）：

```json
{
  "sessionId": "current-session",
  "targetMode": "english",
  "target": {
    "pid": 12345,
    "windowId": "123456"
  }
}
```

- targetMode 只接受 english 或 chinese，不提供“反转当前状态”。
- pid 是目标应用进程，不是独立的插件通信进程。
- windowId 在 Windows 上必填，为 HWND 的十进制字符串。macOS 不需要该字段，旧客户端携带时忽略，主体通过 NSWorkspace 校验前台应用 PID，不区分同一进程内的窗口。
- 每个插件实例串行发送切换请求，不积压旧事件；发送前确认任务仍有效，窗口失焦或内部状态变化后作废本地旧任务。
- 主体核实目标在会话接管范围内，Windows 核对前台 PID 与 HWND，macOS 核对前台应用 PID，并在实际执行前再次检查；从主体收到请求开始计时，排队超过 300ms 则丢弃。

主体无法判断请求到达前已耽搁多久，也无法判断同一窗口内的插件业务状态是否变化；这些由插件在发送前检查。macOS 上请求发出后若切到同一 PID 的另一个窗口，主体仍可能执行该请求；切到其他进程则拒绝执行。

主体只处理目标模式，不理解插件为何切换，不查该程序普通规则的开关或策略。全局暂停仍生效。Windows 控制当前输入法内部中英文；macOS 选择用户配置的中文输入源或英文键盘布局，不控制第三方输入法的内部中英文开关。不支持就返回错误。macOS 的 actualMode 表示已核实选中对应配置的系统输入源，不表示已读到输入法内部状态。

成功 data 为 {targetMode,actualMode}，两者必须一致；已经符合则 code=already_selected，切换并验证符合则 code=changed。失败或跳过 data=null，message 说明原因。不把系统调用返回或输入法名称不变当作验证成功。

### 注销：unregisterPlugin

params 为 {sessionId}。成功 code=ok，data=null，重复注销也返回 ok。

立即取消该会话任务并移除其接管贡献。同插件其他有效会话仍覆盖的程序继续接管；最后覆盖消失后恢复原应用规则资格，等下次窗口切换触发，不立刻补切。

## 4. 结果与错误

| code | 含义 |
| --- | --- |
| ok | 注册、续期或注销成功 |
| changed / already_selected | 已验证达到目标输入模式 |
| skipped_paused / skipped_stale / skipped_busy | 暂停、主体排队超时或繁忙，未执行切换 |
| scope_conflict / scope_unverifiable | 其他插件已接管重叠范围，或范围无法核实 |
| registration_conflict / scope_not_owned | 重复注册内容不同，或目标不属于该会话接管范围 |
| session_expired | sessionId 不存在或租约已到期，需要重新注册 |
| focus_changed / unsupported_context | 目标已失焦或无法可靠识别前台目标 |
| unsupported_mode / verification_failed | 不支持目标模式或无法确认切换结果 |
| timeout / native_error | 超时或系统调用失败 |
| invalid_request / rate_limited | 参数或调用频率不符合要求 |

失败和跳过同样返回 code、data、message；未知 code 一律不当作成功。例如：

```json
{
  "code": "focus_changed",
  "data": null,
  "message": "目标窗口已失焦，未执行切换"
}
```

HTTP 错误响应也使用这三个字段：401 / unauthorized、403 / forbidden（非法 Host / Origin）、400 / invalid_request、415 / unsupported_media_type、413 / message_too_large、429 / rate_limited，data=null。请求和响应上限均为 64KiB。令牌不进入 URL 或日志；接口不接收输入文字、按键和任意执行命令。

## 5. 重试和断开

注册、切换和注销按 requestId 去重：同 ID 同内容复用已有结果，不同内容返回 invalid_request。完成结果保留 60 秒，最多 1024 条（含进行中），满时拒绝新请求，不淘汰进行中的操作。去重保证限于记录保留期间；插件不得在记录过期后补发旧切换。过期会话不能通过旧缓存恢复权限；注销仍按幂等处理。

切换请求客户端等待上限初定 1 秒，主体总执行预算 800ms。超时可能已经切换，插件显示“结果未知”，不自动重发旧切换。焦点变化可取消未执行任务，但不能保证撤回已经提交系统的操作。

收到 session_expired 时，插件丢弃旧会话及旧事件，重新读取发现文件并注册。连接失败或收到 HTTP 401 时也重新读取发现文件，获取有效端口和令牌后重新注册，不补发旧切换。暂停不解除接管；插件崩溃则等剩余租约到期交还。同插件多实例按有效会话共同覆盖，一个实例退出不能解除其他实例的接管。
