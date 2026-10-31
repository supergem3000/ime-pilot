package local.imepilot;

import com.google.gson.JsonObject;
import com.intellij.openapi.util.SystemInfo;
import com.intellij.ui.mac.foundation.Foundation;
import com.intellij.ui.mac.foundation.ID;
import com.sun.jna.Native;
import java.awt.Window;
import java.io.IOException;

final class NativeTarget {
    static boolean supported() {
        return SystemInfo.isWindows || SystemInfo.isMac;
    }

    static JsonObject program() throws IOException {
        JsonObject program = new JsonObject();
        if (SystemInfo.isWindows) {
            program.addProperty("kind", "windows.executablePath");
            program.addProperty("value", ProcessHandle.current().info().command()
                    .orElseThrow(() -> new IOException("host executable unavailable")));
        } else if (SystemInfo.isMac) {
            ID bundle = Foundation.invoke("NSBundle", "mainBundle");
            String id = Foundation.toStringViaUTF8(Foundation.invoke(bundle, "bundleIdentifier"));
            if (id == null || id.isBlank()) {
                throw new IOException("host Bundle ID unavailable");
            }
            program.addProperty("kind", "macos.bundleId");
            program.addProperty("value", id);
        } else {
            throw new IOException("native window adapter unavailable");
        }
        return program;
    }

    static JsonObject target(Window window) throws IOException {
        JsonObject target = new JsonObject();
        target.addProperty("pid", ProcessHandle.current().pid());
        if (SystemInfo.isWindows) {
            long windowId = Native.getWindowID(window);
            if (windowId == 0) {
                throw new IOException("native window ID unavailable");
            }
            target.addProperty("windowId", Long.toUnsignedString(windowId));
        }
        return target;
    }
}
