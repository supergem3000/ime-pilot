package local.imepilot;

import com.google.gson.Gson;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.sun.jna.platform.win32.KnownFolders;
import com.sun.jna.platform.win32.Shell32Util;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Set;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

public final class ClientContractTest {
    private static final Gson JSON = new Gson();

    public static void main(String[] args) throws Exception {
        if (System.getProperty("os.name").toLowerCase().contains("win")) {
            Path nativePath = Path.of(
                    Shell32Util.getKnownFolderPath(KnownFolders.FOLDERID_LocalAppData),
                    "ime-pilot",
                    "runtime.json"
            );
            check(RpcClient.discoveryPath().equals(nativePath), "native discovery location");
        }
        Path dir = Files.createTempDirectory("ime-pilot-contract-");
        Path file = dir.resolve("runtime.json");
        try {
            discoveryContract(file);
            httpContract(file);
            System.out.println("Plugin discovery and HTTP contract tests passed.");
        } finally {
            Files.deleteIfExists(file);
            Files.deleteIfExists(dir);
        }
    }

    private static void discoveryContract(Path file) throws Exception {
        String valid = "{\"port\":43210,\"token\":\"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ12345678\"}";
        Files.writeString(file, valid);
        check(RpcClient.readDiscovery(file).get("port").getAsInt() == 43210, "port");
        for (String invalid : new String[]{
                valid.replace("43210", "0"),
                valid.replace("12345678\"}", "!\"}"),
                "{\"port\":43210}",
                valid.replace("\"port\"", "\"unexpected\":1,\"port\""),
                "x".repeat(4097)
        }) {
            Files.writeString(file, invalid);
            boolean rejected = false;
            try {
                RpcClient.readDiscovery(file);
            } catch (Exception expected) {
                rejected = true;
            }
            check(rejected, "invalid discovery accepted");
        }
    }

    private static void httpContract(Path file) throws Exception {
        String token = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ12345678";
        AtomicInteger registrations = new AtomicInteger();
        AtomicInteger switches = new AtomicInteger();
        AtomicInteger renewals = new AtomicInteger();
        AtomicBoolean expireNext = new AtomicBoolean();
        AtomicReference<Throwable> failure = new AtomicReference<>();
        CountDownLatch unregistered = new CountDownLatch(1);
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/api/rpc", exchange -> {
            JsonObject response = new JsonObject();
            try {
                check("POST".equals(exchange.getRequestMethod()), "HTTP method");
                check(("Bearer " + token).equals(exchange.getRequestHeaders().getFirst("Authorization")), "auth");
                JsonObject request = JsonParser.parseString(
                        new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8)
                ).getAsJsonObject();
                check(request.keySet().equals(Set.of("requestId", "method", "params")), "request envelope");
                check(!request.get("requestId").getAsString().isBlank(), "request ID");
                JsonObject params = request.getAsJsonObject("params");
                String method = request.get("method").getAsString();
                JsonObject data = new JsonObject();
                String code = "ok";
                switch (method) {
                    case "registerPlugin" -> {
                        check(params.keySet().equals(Set.of("pluginId", "name", "version", "instanceId", "programs")), "register fields");
                        check("ime-pilot.jetbrains".equals(params.get("pluginId").getAsString()), "plugin ID");
                        JsonObject program = params.getAsJsonArray("programs").get(0).getAsJsonObject();
                        check("windows.executablePath".equals(program.get("kind").getAsString()), "program kind");
                        check(Path.of(program.get("value").getAsString()).isAbsolute(), "program path");
                        data.addProperty("sessionId", "session-" + registrations.incrementAndGet());
                        data.addProperty("paused", false);
                        data.addProperty("leaseMs", 15000);
                        data.addProperty("renewAfterMs", 5000);
                    }
                    case "renewSession" -> {
                        check(params.keySet().equals(Set.of("sessionId")), "renew fields");
                        renewals.incrementAndGet();
                        if (expireNext.getAndSet(false)) {
                            code = "session_expired";
                        } else {
                            data.addProperty("paused", false);
                            data.addProperty("leaseMs", 15000);
                            data.addProperty("renewAfterMs", 5000);
                        }
                    }
                    case "setInputMode" -> {
                        check(params.keySet().equals(Set.of("sessionId", "targetMode", "target")), "switch fields");
                        check(params.getAsJsonObject("target").keySet().equals(Set.of("pid", "windowId")), "target fields");
                        check("123".equals(params.getAsJsonObject("target").get("windowId").getAsString()), "window ID");
                        check("english".equals(params.get("targetMode").getAsString()), "target mode");
                        switches.incrementAndGet();
                        code = "changed";
                        data.addProperty("targetMode", "english");
                        data.addProperty("actualMode", "english");
                    }
                    case "unregisterPlugin" -> {
                        check(params.keySet().equals(Set.of("sessionId")), "unregister fields");
                        unregistered.countDown();
                    }
                    default -> throw new AssertionError("unexpected method " + method);
                }
                response.addProperty("code", code);
                response.add("data", data);
                response.addProperty("message", "test result");
            } catch (Throwable error) {
                failure.compareAndSet(null, error);
                response.addProperty("code", "invalid_request");
                response.add("data", null);
                response.addProperty("message", "contract failure");
            }
            byte[] body = JSON.toJson(response).getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().add("Content-Type", "application/json");
            exchange.sendResponseHeaders(200, body.length);
            exchange.getResponseBody().write(body);
            exchange.close();
        });
        server.start();
        JsonObject discovery = new JsonObject();
        discovery.addProperty("port", server.getAddress().getPort());
        discovery.addProperty("token", token);
        Files.writeString(file, JSON.toJson(discovery));
        try {
            try (RpcClient client = new RpcClient(file)) {
                client.heartbeat();
                check(client.isConnected(), "registered connection");
                client.heartbeat();
                client.setInputMode(123, "english", () -> false);
                check(switches.get() == 0, "invalid local task sent");
                client.setInputMode(123, "english", () -> true);
                check(switches.get() == 1, "switch not sent");
                expireNext.set(true);
                client.heartbeat();
                check(registrations.get() == 2 && renewals.get() == 2, "expired session not re-registered");
                check(switches.get() == 1, "old switch replayed");
            }
            check(unregistered.await(2, TimeUnit.SECONDS), "no unregister on close");
            if (failure.get() != null) {
                throw new AssertionError("HTTP contract failure", failure.get());
            }
        } finally {
            server.stop(0);
        }
    }

    private static void check(boolean condition, String message) {
        if (!condition) {
            throw new AssertionError(message);
        }
    }
}
