package local.imepilot;

import com.google.gson.Gson;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.sun.jna.platform.win32.KnownFolders;
import com.sun.jna.platform.win32.Shell32Util;
import java.io.IOException;
import java.net.Proxy;
import java.net.ProxySelector;
import java.net.SocketAddress;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.Flow;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;

// 由插件工作线程串行调用；UI 线程只读取连接状态，不等待 HTTP。
final class RpcClient implements AutoCloseable {
    private static final Gson JSON = new Gson();
    private final Path runtimePath;
    private final String instance = UUID.randomUUID().toString();
    private final HttpClient http = HttpClient.newBuilder()
            .connectTimeout(Duration.ofMillis(300))
            .followRedirects(HttpClient.Redirect.NEVER)
            .proxy(new ProxySelector() {
                @Override
                public List<Proxy> select(URI uri) {
                    return List.of(Proxy.NO_PROXY);
                }

                @Override
                public void connectFailed(URI uri, SocketAddress address, IOException error) {
                    // 本机直连没有备用代理。
                }
            })
            .build();

    private volatile JsonObject discovery;
    private volatile String session;
    private volatile boolean closed;
    private volatile String stage = "idle";
    private boolean paused;

    RpcClient() {
        this(discoveryPath());
    }

    RpcClient(Path runtimePath) {
        this.runtimePath = runtimePath;
    }

    String diagnostic() {
        return "discovery".equals(stage) ? stage + " (" + runtimePath + ")" : stage;
    }

    boolean isConnected() {
        return !closed && session != null;
    }

    static Path discoveryPath() {
        if (System.getProperty("os.name").toLowerCase(Locale.ROOT).contains("win")) {
            return Path.of(
                    Shell32Util.getKnownFolderPath(KnownFolders.FOLDERID_LocalAppData),
                    "ime-pilot",
                    "runtime.json"
            );
        }
        return Path.of(System.getProperty("user.home"), "Library", "Application Support", "ime-pilot", "runtime.json");
    }

    static JsonObject readDiscovery(Path path) throws IOException {
        byte[] bytes;
        try (var input = Files.newInputStream(path)) {
            bytes = input.readNBytes(4097);
        }
        if (bytes.length > 4096) {
            throw new IOException("discovery limit");
        }
        JsonObject data = JsonParser.parseString(new String(bytes, StandardCharsets.UTF_8)).getAsJsonObject();
        if (!data.keySet().equals(Set.of("port", "token"))) {
            throw new IOException("invalid discovery fields");
        }
        int port = data.get("port").getAsInt();
        String token = data.get("token").getAsString();
        if (port < 1 || port > 65535 || !token.matches("[A-Za-z0-9]{40,128}")) {
            throw new IOException("invalid discovery");
        }
        return data;
    }

    void heartbeat() throws Exception {
        if (closed) {
            return;
        }
        boolean wasConnected = isConnected();
        stage = "discovery";
        JsonObject next = readDiscovery(runtimePath);
        if (discovery == null || !next.equals(discovery)) {
            disconnect();
            discovery = next;
            wasConnected = false;
        }
        JsonObject response;
        if (session == null) {
            response = register();
        } else {
            stage = "renewSession";
            response = send(discovery, "renewSession", params());
            if ("session_expired".equals(response.get("code").getAsString())) {
                // 会话失效只重新注册，不补发之前的切换。
                session = null;
                wasConnected = false;
                response = register();
            }
        }
        requireOK(response);
        boolean nextPaused = response.getAsJsonObject("data").get("paused").getAsBoolean();
        stage = "connected";
        if (!wasConnected || nextPaused != paused) {
            ClientState.set("已连接", nextPaused ? "程序已接管；全局暂停中" : "程序已接管；聚焦及退出 Insert 时切英文");
        }
        paused = nextPaused;
    }

    private JsonObject register() throws Exception {
        stage = "registerPlugin";
        JsonObject program = NativeTarget.program();

        JsonObject params = new JsonObject();
        params.addProperty("pluginId", "ime-pilot.jetbrains");
        params.addProperty("name", "ime-pilot JetBrains");
        params.addProperty("version", "0.4.1");
        params.addProperty("instanceId", instance);
        params.add("programs", JSON.toJsonTree(List.of(program)));

        JsonObject response = send(discovery, "registerPlugin", params);
        requireOK(response);
        session = response.getAsJsonObject("data").get("sessionId").getAsString();
        return response;
    }

    private JsonObject params() {
        JsonObject params = new JsonObject();
        params.addProperty("sessionId", session);
        return params;
    }

    void setInputMode(JsonObject target, String mode, BooleanSupplier valid) throws Exception {
        if (!isConnected() || !valid.getAsBoolean()) {
            return;
        }
        JsonObject params = params();
        params.addProperty("targetMode", mode);
        params.add("target", target);

        // 发出前再次检查本地代次。请求一旦发出就不重试，超时结果可能未知。
        if (!valid.getAsBoolean() || closed) {
            return;
        }
        stage = "setInputMode";
        JsonObject response = send(discovery, "setInputMode", params);
        String code = response.get("code").getAsString();
        if ("session_expired".equals(code)) {
            disconnect();
            ClientState.set("未连接", "会话已失效，等待重新注册");
        } else {
            ClientState.set("已连接", code + "：" + response.get("message").getAsString());
        }
    }

    void disconnect() {
        session = null;
        discovery = null;
    }

    private JsonObject send(JsonObject endpoint, String method, JsonObject params) throws Exception {
        if (endpoint == null) {
            throw new IOException("not connected");
        }
        JsonObject request = new JsonObject();
        request.addProperty("requestId", UUID.randomUUID().toString());
        request.addProperty("method", method);
        request.add("params", params);

        HttpRequest httpRequest = HttpRequest.newBuilder(URI.create(
                        "http://127.0.0.1:" + endpoint.get("port").getAsInt() + "/api/rpc"))
                .timeout(Duration.ofMillis(1000))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + endpoint.get("token").getAsString())
                .POST(HttpRequest.BodyPublishers.ofString(JSON.toJson(request)))
                .build();
        var future = http.sendAsync(httpRequest, info -> new LimitedBody());
        HttpResponse<byte[]> reply;
        try {
            reply = future.get(1100, TimeUnit.MILLISECONDS);
        } catch (Exception error) {
            future.cancel(true);
            throw error;
        }
        if (reply.statusCode() != 200) {
            throw new IOException("HTTP " + reply.statusCode());
        }
        JsonObject response = JsonParser.parseString(new String(reply.body(), StandardCharsets.UTF_8)).getAsJsonObject();
        if (!response.keySet().equals(Set.of("code", "data", "message"))
                || !response.get("code").isJsonPrimitive()
                || !response.get("message").isJsonPrimitive()) {
            throw new IOException("invalid response");
        }
        return response;
    }

    private static void requireOK(JsonObject response) throws IOException {
        String code = response.get("code").getAsString();
        if (!"ok".equals(code)) {
            throw new IOException("RPC " + code + ": " + response.get("message").getAsString());
        }
    }

    @Override
    public void close() {
        closed = true;
        JsonObject endpoint = discovery;
        String sessionId = session;
        session = null;
        // 卸载线程不等待 HTTP；后台尽力注销，主体也会在租约到期后清理。
        Thread cleanup = new Thread(() -> {
            try {
                if (endpoint != null && sessionId != null) {
                    JsonObject params = new JsonObject();
                    params.addProperty("sessionId", sessionId);
                    send(endpoint, "unregisterPlugin", params);
                }
            } catch (Exception ignored) {
                // 主体可能已经退出。
            } finally {
                http.shutdownNow();
            }
        }, "ime-pilot-close");
        cleanup.setDaemon(true);
        cleanup.start();
    }

    private static final class LimitedBody implements HttpResponse.BodySubscriber<byte[]> {
        private final HttpResponse.BodySubscriber<byte[]> delegate = HttpResponse.BodySubscribers.ofByteArray();
        private Flow.Subscription subscription;
        private int count;

        @Override
        public CompletionStage<byte[]> getBody() {
            return delegate.getBody();
        }

        @Override
        public void onSubscribe(Flow.Subscription subscription) {
            this.subscription = subscription;
            delegate.onSubscribe(subscription);
        }

        @Override
        public void onNext(List<ByteBuffer> items) {
            for (ByteBuffer buffer : items) {
                count += buffer.remaining();
                if (count > 65536) {
                    subscription.cancel();
                    delegate.onError(new IOException("response limit"));
                    return;
                }
            }
            delegate.onNext(items);
        }

        @Override
        public void onError(Throwable error) {
            delegate.onError(error);
        }

        @Override
        public void onComplete() {
            delegate.onComplete();
        }
    }
}
