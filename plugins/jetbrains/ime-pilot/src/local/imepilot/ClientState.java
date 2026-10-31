package local.imepilot;

import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.project.ProjectManager;
import com.intellij.openapi.wm.WindowManager;

public final class ClientState {
    private static volatile String summary = "未连接";
    private static volatile String detail = "等待注册程序接管；IdeaVim 适配版本为 2.47.3 / 2.48.0";

    public static String summary() {
        return summary;
    }

    public static String detail() {
        return detail;
    }

    public static void set(String state, String message) {
        summary = state;
        detail = message;
        var app = ApplicationManager.getApplication();
        if (app == null || app.isDisposed()) {
            return;
        }

        // 连接状态可在后台更新；状态栏刷新必须交给 IDE 的 UI 线程。
        app.invokeLater(() -> {
            for (var project : ProjectManager.getInstance().getOpenProjects()) {
                if (project.isDisposed()) {
                    continue;
                }
                var bar = WindowManager.getInstance().getStatusBar(project);
                if (bar != null) {
                    bar.updateWidget("ime-pilot");
                }
            }
        });
    }
}
