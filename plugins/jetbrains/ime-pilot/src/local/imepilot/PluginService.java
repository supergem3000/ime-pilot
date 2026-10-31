package local.imepilot;

import com.intellij.openapi.Disposable;
import com.intellij.openapi.application.ApplicationManager;
import java.awt.AWTEvent;
import java.awt.Toolkit;
import java.awt.Window;
import java.awt.event.AWTEventListener;
import java.awt.event.WindowEvent;
import java.io.IOException;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;

// 应用级连接独立于 IdeaVim：插件负责聚焦行为，Vim 适配只提供内部事件。
public final class PluginService implements Disposable {
    private final ScheduledExecutorService worker = Executors.newSingleThreadScheduledExecutor(runnable -> {
        Thread thread = new Thread(runnable, "ime-pilot-client");
        thread.setDaemon(true);
        return thread;
    });
    private final RpcClient client = new RpcClient();
    private final AtomicLong generation = new AtomicLong();
    private final AtomicReference<Runnable> latest = new AtomicReference<>();
    private volatile boolean disposed;
    private boolean started;
    private AWTEventListener focusListener;
    private long backoffUntil;
    private int failures;

    public PluginService() {
        ApplicationManager.getApplication().invokeLater(this::start);
    }

    public void start() {
        if (disposed || started) {
            return;
        }
        if (!NativeTarget.supported()) {
            ClientState.set("平台待支持", "本机窗口及输入法适配支持 Windows 和 macOS");
            return;
        }
        started = true;
        focusListener = event -> {
            invalidate();
            if (event instanceof WindowEvent windowEvent
                    && windowEvent.getID() == WindowEvent.WINDOW_GAINED_FOCUS) {
                Window window = windowEvent.getWindow();
                // 等当前焦点事件分发完成，再记录新的前台窗口任务。
                ApplicationManager.getApplication().invokeLater(() -> requestEnglish(window));
            }
        };
        Toolkit.getDefaultToolkit().addAWTEventListener(
                focusListener,
                AWTEvent.FOCUS_EVENT_MASK | AWTEvent.WINDOW_FOCUS_EVENT_MASK
        );

        // 连接续期与切换共用一个后台线程，切换请求不并发、不补发旧事件。
        worker.scheduleWithFixedDelay(() -> {
            if (disposed || System.nanoTime() < backoffUntil) {
                return;
            }
            try {
                client.heartbeat();
                failures = 0;
            } catch (Exception | LinkageError error) {
                failed(error);
            }
        }, 0, 5, TimeUnit.SECONDS);
        worker.scheduleWithFixedDelay(() -> {
            Runnable task = latest.getAndSet(null);
            if (task != null) {
                task.run();
            }
        }, 0, 10, TimeUnit.MILLISECONDS);
    }

    void invalidate() {
        generation.incrementAndGet();
        latest.set(null);
    }

    void requestEnglish(Window window) {
        if (disposed || !client.isConnected() || window == null || !window.isFocused() || !window.isDisplayable()) {
            return;
        }
        long ticket = generation.incrementAndGet();
        long created = System.nanoTime();
        try {
            var target = NativeTarget.target(window);
            latest.set(() -> {
                try {
                    client.setInputMode(target, "english", () -> current(ticket, created));
                } catch (Exception failure) {
                    failed(failure);
                }
            });
        } catch (IOException | RuntimeException | LinkageError error) {
            invalidate();
            ClientState.set("目标不可用", "无法读取原生目标：" + error.getClass().getSimpleName());
        }
    }

    private boolean current(long ticket, long created) {
        return !disposed && generation.get() == ticket
                && System.nanoTime() - created < TimeUnit.MILLISECONDS.toNanos(300);
    }

    private void failed(Throwable error) {
        String stage = client.diagnostic();
        invalidate();
        client.disconnect();
        failures = Math.min(failures + 1, 4);
        backoffUntil = System.nanoTime() + TimeUnit.SECONDS.toNanos(1L << failures);
        Throwable cause = error;
        for (int i = 0; i < 5 && cause.getCause() != null; i++) {
            cause = cause.getCause();
        }
        ClientState.set("未连接", "阶段：" + stage + " / " + cause.getClass().getSimpleName()
                + "：" + cause.getMessage() + "；恢复后等待新事件");
    }

    @Override
    public void dispose() {
        disposed = true;
        invalidate();
        if (focusListener != null) {
            Toolkit.getDefaultToolkit().removeAWTEventListener(focusListener);
        }
        worker.shutdownNow();
        client.close();
    }
}
