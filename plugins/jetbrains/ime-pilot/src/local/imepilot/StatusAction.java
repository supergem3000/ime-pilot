package local.imepilot;

import com.intellij.openapi.actionSystem.AnActionEvent;
import com.intellij.openapi.project.DumbAwareAction;
import com.intellij.openapi.ui.Messages;

public final class StatusAction extends DumbAwareAction {
    @Override
    public void actionPerformed(AnActionEvent event) {
        Messages.showInfoMessage(
                event.getProject(),
                ClientState.summary() + "\n" + ClientState.detail(),
                "ime-pilot"
        );
    }
}
