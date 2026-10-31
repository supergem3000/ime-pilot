# IME Pilot 图标素材

| 用途 | 文件 |
| --- | --- |
| Windows 应用图标 | `windows/ime-pilot.ico` |
| Windows 大图 | `windows/app-1024.png` |
| macOS 应用图标 | `macos/ime-pilot.icns` |
| macOS 大图 | `macos/app-1024.png` |
| macOS 菜单栏 1× / 2× | `macos/menubar-template.png`、`macos/menubar-template@2x.png` |

Windows 采用选定的浅绿光标、深绿箭头和柔和立体质感，无底板。
macOS 采用最初的墨绿圆角矩形应用图标，菜单栏使用黑色透明模板图标。

菜单栏 PNG 必须作为模板图标加载（Wails `SetTemplateIcon`），由 macOS 根据背景着色；普通 `SetIcon` 不会自动获得该行为。

`sources/approved-*.png` 是用户选定的原始设计稿；其余源 PNG 是通过内置 image_gen 从选定稿制作的独立素材，生成提示词保存在 `sources/prompts.json`。

`scripts/export-icons.ps1` 仅按透明边界统一画布、缩放和封装格式，不重画图形。无需额外安装工具。ICO 包含 16、20、24、32、48、64、128、256 像素；ICNS 包含 16 至 1024 像素。

`icons_windows.go` / `icons_darwin.go` 按平台嵌入运行时应用和托盘 PNG，供 desktop 使用。macOS 菜单栏加载 2× 模板图标。

Windows 构建脚本通过已安装的 `wails3 generate syso` 将 ICO 和 manifest 链接进 EXE，构建结束后清理临时资源对象。macOS 构建脚本将 ICNS 放入 `.app/Contents/Resources`，并在 Info.plist 中声明。更新图标源素材后，先运行导出脚本，再重新构建应用。
