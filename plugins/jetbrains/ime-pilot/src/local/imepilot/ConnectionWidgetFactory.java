package local.imepilot;

import com.intellij.openapi.project.Project;
import com.intellij.openapi.wm.StatusBarWidget;
import com.intellij.openapi.wm.StatusBarWidgetFactory;

public final class ConnectionWidgetFactory implements StatusBarWidgetFactory {
    @Override
    public String getId() {
        return "ime-pilot";
    }

    @Override
    public String getDisplayName() {
        return "IME Pilot connection";
    }

    @Override
    public StatusBarWidget createWidget(Project project) {
        return new Widget();
    }

    private static final class Widget implements StatusBarWidget, StatusBarWidget.TextPresentation {
        @Override
        public String ID() {
            return "ime-pilot";
        }

        @Override
        public WidgetPresentation getPresentation() {
            return this;
        }

        @Override
        public String getText() {
            return "IME: " + ClientState.summary();
        }

        @Override
        public float getAlignment() {
            return 0.5f;
        }

        @Override
        public String getTooltipText() {
            return ClientState.detail();
        }
    }
}
