package local.imepilot;

import com.intellij.ide.plugins.PluginManagerCore;
import com.intellij.openapi.Disposable;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.editor.Editor;
import com.intellij.openapi.extensions.PluginId;
import com.maddyhome.idea.vim.VimPlugin;
import com.maddyhome.idea.vim.api.VimInjectorKt;
import com.maddyhome.idea.vim.common.ModeChangeListener;
import com.maddyhome.idea.vim.common.VimListenersNotifier;
import com.maddyhome.idea.vim.newapi.IjVimEditor;
import com.maddyhome.idea.vim.state.mode.Mode;
import java.awt.Component;
import java.awt.KeyboardFocusManager;
import java.awt.Window;
import javax.swing.SwingUtilities;

// 仅适配 IdeaVim 事件，不向主体发送模式或编辑器标识。
public final class VimIntegration implements Disposable {
    private boolean disposed;
    private boolean registered;
    private VimListenersNotifier notifier;
    private ModeChangeListener listener;

    public VimIntegration() {
        start();
    }

    public void start() {
        var descriptor = PluginManagerCore.getPlugin(PluginId.getId("IdeaVIM"));
        if (descriptor == null || !("2.47.3".equals(descriptor.getVersion()) || "2.48.0".equals(descriptor.getVersion()))) {
            ClientState.set(ClientState.summary(), "IdeaVim 适配支持 2.47.3 / 2.48.0；当前仅处理窗口聚焦");
            return;
        }
        ApplicationManager.getApplication().invokeLater(this::register);
    }

    private void register() {
        if (disposed || registered) {
            return;
        }
        try {
            PluginService service = ApplicationManager.getApplication().getService(PluginService.class);
            service.start();
            notifier = VimInjectorKt.getInjector().getListenersNotifier();
            listener = (editor, oldMode) -> {
                // 包括进入 Insert 等不需要切换的变化，都使本地旧任务失效。
                service.invalidate();
                if (disposed || !VimPlugin.isEnabled()
                        || !(oldMode instanceof Mode.INSERT)
                        || !(editor instanceof IjVimEditor ij)) {
                    return;
                }
                Mode mode = editor.getMode();
                if (!(mode instanceof Mode.NORMAL normal)
                        || normal.isInsertPending() || normal.isReplacePending()) {
                    return;
                }

                // 只对当前获得键盘焦点的编辑器响应；Ctrl+O 临时 Normal 不触发。
                Editor nativeEditor = ij.getEditor();
                Component focus = KeyboardFocusManager.getCurrentKeyboardFocusManager().getFocusOwner();
                if (nativeEditor.isDisposed() || focus == null
                        || !SwingUtilities.isDescendingFrom(focus, nativeEditor.getContentComponent())) {
                    return;
                }
                Window window = SwingUtilities.getWindowAncestor(nativeEditor.getContentComponent());
                service.requestEnglish(window);
            };
            notifier.getModeChangeListeners().add(listener);
            registered = true;
        } catch (LinkageError | RuntimeException error) {
            // 模式适配失败不撤销程序接管，窗口聚焦仍由插件负责。
            ClientState.set(ClientState.summary(), "IdeaVim 事件适配不可用；窗口聚焦行为仍由插件负责");
        }
    }

    @Override
    public void dispose() {
        disposed = true;
        if (notifier != null && listener != null) {
            notifier.getModeChangeListeners().remove(listener);
        }
        ApplicationManager.getApplication().getService(PluginService.class).invalidate();
    }
}
