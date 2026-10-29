package local.imepilot;

import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.startup.StartupActivity;

public final class VimStartup implements StartupActivity.DumbAware {
    @Override
    public void runActivity(Project project) {
        // 各项目共用应用级服务，由服务内部防止重复注册监听器。
        ApplicationManager.getApplication().getService(VimIntegration.class).start();
    }
}
