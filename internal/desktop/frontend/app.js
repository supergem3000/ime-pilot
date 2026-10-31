import { Call, Events } from "/wails/runtime.js";

const getElement = (id) => document.getElementById(id);
const manualButtons = [...document.querySelectorAll("button[data-mode]")];
const testInput = getElement("test");
const manualStatus = getElement("status");
const modeLabel = getElement("mode");
const manualDetails = getElement("details");

// 已保存配置与编辑草稿分开，后台刷新不能覆盖尚未保存的修改。
let savedConfig = null;
let draftRules = [];
let defaultMode = "";
let runningApps = [];
const appInfoCache = new Map();
let appInfoVersion = 0;
let recentResult = null;
let appsRequest = 0;
let plugins = [];
let dirty = false;
let saving = false;
let editGeneration = 0;
let renderedRulesSignature = "";
let configUnavailable = true;
let pausing = false;
let latestConfig = null;
let statusRequest = 0;
let appliedStatusRequest = 0;
let autoSaveTimer = null;
let saveError = "";
let configConflict = false;
let composingConfigText = false;

let schemeId = "";
let schemes = [];
const schemeDrafts = new Map();
let schemeConfig = {};
let schemeForm = null;
let formGeneration = 0;

let manualBusy = false;
let manualGeneration = 0;
let manualSchemeSignature = "";

function callService(method, ...args) {
  return Call.ByName("ime-pilot/internal/desktop.InputService." + method, ...args);
}

function errorText(error) {
  return String(error?.message ?? error);
}

function createElement(tag, className, text) {
  const element = document.createElement(tag);
  element.className = className;
  if (text !== undefined) {
    element.textContent = text;
  }
  return element;
}

function showPage(page) {
  document.querySelectorAll("[data-page]").forEach((button) => {
    if (button.dataset.page === page) {
      button.setAttribute("aria-current", "page");
    } else {
      button.removeAttribute("aria-current");
    }
  });
  document.querySelectorAll(".page").forEach((section) => {
    section.hidden = section.id !== "page-" + page;
  });
  if (page !== "test") {
    invalidateManualResult();
  }
  if (page === "schemes") {
    // 进入页面时重读动态选项，表单仍使用当前草稿，不提交或重置选择。
    if (!schemes.length) {
      loadSchemes();
    }
    refreshSchemeForm();
  }
  if (page === "rules") {
    refreshApps();
  }
}

function showAddPanel(open) {
  getElement("add-panel").hidden = !open;
  getElement("add-toggle").setAttribute("aria-expanded", String(open));
  if (open) {
    refreshApps();
    getElement("apps").focus();
  } else {
    getElement("add-toggle").focus();
  }
}

// 手动测试：先将焦点移入测试框，再执行切换或查询。
function invalidateManualResult() {
  manualGeneration++;
  if (manualBusy) {
    manualStatus.textContent = "窗口已失焦，本次结果不再展示，请重新查询。";
    modeLabel.textContent = "本次结果未确认";
    manualDetails.textContent = "";
  }
}

async function performManualOperation(mode) {
  if (manualBusy) {
    return;
  }

  manualBusy = true;
  const operationGeneration = ++manualGeneration;
  manualButtons.forEach((button) => {
    button.disabled = true;
  });
  testInput.focus({ preventScroll: true });
  manualStatus.textContent = "正在查询或切换输入法……";

  try {
    // 等待 WebView 焦点更新落到原生窗口，避免操作按钮所在的输入上下文。
    await new Promise((resolve) => {
      requestAnimationFrame(() => requestAnimationFrame(resolve));
    });
    if (!document.hasFocus() || operationGeneration !== manualGeneration) {
      throw new Error("窗口已失焦，请重新操作。");
    }

    const reply = await callService("Perform", mode);
    // 请求期间发生失焦时丢弃旧结果，不能把它显示成本次成功。
    if (operationGeneration !== manualGeneration) {
      return;
    }

    manualStatus.textContent = reply.message;
    if (reply.ok) {
      const state = reply.state;
      const modeNames = { chinese: "中文", english: "英文" };
      modeLabel.textContent = "最近查询：" + (modeNames[state.Mode] ?? "未知");
      manualDetails.textContent = state.Description + "\n" + state.Details;
    } else {
      modeLabel.textContent = "本次状态未确认，请核对下方说明。";
      manualDetails.textContent = "状态未回读确认，请在输入测试区核对实际输入。";
    }
  } catch (error) {
    if (operationGeneration === manualGeneration) {
      manualStatus.textContent = "操作失败：" + String(error);
      modeLabel.textContent = "本次结果未确认";
      manualDetails.textContent = "";
    }
  } finally {
    manualBusy = false;
    manualButtons.forEach((button) => {
      button.disabled = false;
    });
  }
}

// 插件接管信息只用于展示；是否执行普通规则由后台决定。
function programMatches(rule, program) {
  const path = (rule.appPath || "").replaceAll("/", "\\").toLowerCase();
  const executable = path.split("\\").at(-1);
  switch (program.kind) {
    case "macos.bundleId":
      return program.value === rule.bundleId;
    case "windows.executablePath":
      return !rule.bundleId && program.value.replaceAll("/", "\\").toLowerCase() === path;
    case "windows.executableName":
      return !rule.bundleId && program.value.toLowerCase() === executable;
    default:
      return false;
  }
}

function takeoverNames(rule) {
  const owners = plugins.filter((plugin) => {
    return plugin.programs.some((program) => programMatches(rule, program));
  });
  return [...new Set(owners.map((plugin) => plugin.name))];
}

function appIdentity(name, identity) {
  const container = createElement("div", "app-identity");
  const path = createElement("div", "path", identity);
  // 完整路径直接显示，长路径自动换行，便于区分同名应用。
  path.title = identity;
  container.append(createElement("div", "app-name", name), path);
  return container;
}

function programApp(program) {
  return runningApps.find((app) => programMatches(
    { appPath: app.path, bundleId: app.bundleId }, program,
  ));
}

// 名称和图标只缓存于当前界面会话，绝不写进规则配置。
// 按安装位置读取，不要求应用正在运行；读取期间先显示标识和占位图标。
function applicationDetails(path = "", bundleId = "") {
  const identifier = bundleId || path;
  const key = bundleId ? "bundle:" + bundleId : "path:" + path.toLowerCase();
  const cached = appInfoCache.get(key);
  if (cached) {
    return cached;
  }

  const info = { name: identifier.replaceAll("\\", "/").split("/").at(-1), icon: "" };
  appInfoCache.set(key, info);
  callService("GetAppInfo", path, bundleId).then((resolved) => {
    // 手动刷新可能已清空缓存，不让上一轮请求覆盖新结果。
    if (appInfoCache.get(key) !== info) {
      return;
    }
    info.name = resolved.name || info.name;
    info.icon = resolved.icon || "";
    appInfoVersion++;
    drawRules();
    displayRecentResult(recentResult);
  }).catch(() => {
    // 元数据读取失败只影响展示，规则仍按程序标识执行。
  });
  return info;
}

function ruleDetails(rule) {
  return applicationDetails(rule.appPath, rule.bundleId);
}

function ruleAppName(rule) {
  return ruleDetails(rule).name;
}

function programDetails(program) {
  const app = programApp(program);
  if (app) {
    return applicationDetails(app.path, app.bundleId);
  }
  if (program.kind === "windows.executablePath") {
    return applicationDetails(program.value);
  }
  if (program.kind === "macos.bundleId") {
    return applicationDetails("", program.value);
  }
  return { name: program.value, icon: "" };
}

function appIcon(info) {
  const placeholder = () => createElement("span", "app-icon", info.name.slice(0, 2).toUpperCase());
  if (!info.icon.startsWith("data:image/png;base64,")) {
    return placeholder();
  }
  const image = createElement("img", "app-icon app-icon-image");
  image.alt = "";
  image.src = info.icon;
  image.onerror = () => image.replaceWith(placeholder());
  return image;
}

function drawPlugins() {
  const list = getElement("plugin-list");
  const expanded = new Set([...list.querySelectorAll("details[open]")].map((item) => item.dataset.key));
  list.replaceChildren();
  if (!plugins.length) {
    list.append(createElement("p", "empty-state", "暂无插件接管。插件连接后会自动显示在这里，无需添加应用规则。"));
  }

  const seen = new Set();
  const renderedRules = new Set();
  for (const plugin of plugins) {
    for (const program of plugin.programs) {
      const key = JSON.stringify([plugin.pluginId, program.kind, program.value]);
      if (seen.has(key)) {
        continue;
      }
      seen.add(key);

      const matching = draftRules.filter((rule) => programMatches(rule, program));
      const info = matching[0] ? ruleDetails(matching[0]) : programDetails(program);
      const item = createElement("details", "plugin-item");
      item.dataset.key = key;
      item.open = expanded.has(key);
      const summary = document.createElement("summary");
      summary.append(
        appIcon(info),
        appIdentity(info.name, programApp(program)?.path || program.value),
        createElement("span", "takeover", plugin.name + " 已接管"),
      );
      const detail = createElement("div", "plugin-detail");
      detail.append(
        createElement("p", "caption", "插件已连接；窗口聚焦、编辑器模式等行为由插件决定。"),
        createElement("p", "path", "接管范围：" + program.value),
      );
      const retained = matching.filter((rule) => !renderedRules.has(rule));
      if (retained.length) {
        detail.append(createElement("p", "caption", "保留的应用规则（接管期间不执行）。断开后，下次进入应用时按以下设置执行："));
        retained.forEach((rule) => {
          renderedRules.add(rule);
          detail.append(drawRuleRow(rule));
        });
      } else {
        const message = matching.length
          ? "保留规则已显示在该插件的另一条接管范围中。"
          : "未配置普通应用规则。插件断开后，下次进入应用时使用“其他应用”的设置。";
        detail.append(createElement("p", "caption", message));
      }
      item.append(summary, detail);
      list.append(item);
    }
  }
  getElement("plugin-count").textContent = seen.size ? seen.size + " 个接管范围" : "";
}

// 应用规则：修改立即排队保存；草稿保留到后台确认成功。
function rulesSignature() {
  return JSON.stringify([draftRules, plugins, runningApps, appInfoVersion]);
}

function drawRules() {
  const signature = rulesSignature();

  // 续期和聚焦都会刷新状态。内容没变时保留控件，避免关闭正在编辑的下拉框。
  if (signature === renderedRulesSignature) {
    return;
  }
  renderedRulesSignature = signature;

  drawPlugins();
  const body = getElement("rules");
  body.replaceChildren();
  const ordinaryRules = draftRules.filter((rule) => !takeoverNames(rule).length);
  if (!ordinaryRules.length) {
    body.append(createElement("p", "empty-state", "暂无普通应用规则。添加一个常用应用开始配置。"));
    return;
  }

  ordinaryRules.forEach((rule) => {
    body.append(drawRuleRow(rule));
  });
}

function drawRuleRow(rule) {
  const info = ruleDetails(rule);
  const name = info.name;
  const row = createElement("div", "rule-row");
  row.append(
    appIcon(info),
    appIdentity(name, rule.bundleId || rule.appPath),
  );
  const toggleLabel = createElement("label", "enable-control");
  const toggle = document.createElement("input");
  toggle.type = "checkbox";
  toggle.checked = rule.enabled;
  toggle.setAttribute("aria-label", "启用 " + name);
  const toggleText = createElement("span", "", rule.enabled ? "启用" : "停用");
  toggle.onchange = () => {
    rule.enabled = toggle.checked;
    toggleText.textContent = rule.enabled ? "启用" : "停用";
    activation.disabled = !rule.enabled;
    markDirty();
    renderedRulesSignature = rulesSignature();
  };
  toggleLabel.append(toggle, toggleText);
  row.append(toggleLabel);

  const activation = document.createElement("select");
  activation.setAttribute("aria-label", name + "：进入应用时的输入模式");
  activation.add(new Option("切英文", "english"));
  activation.add(new Option("切中文", "chinese"));
  activation.value = rule.targetMode;
  activation.disabled = !rule.enabled;
  activation.onchange = () => {
    rule.targetMode = activation.value;
    markDirty();
    renderedRulesSignature = rulesSignature();
  };

  const remove = document.createElement("button");
  remove.textContent = "删除";
  remove.className = "text-button";
  remove.setAttribute("aria-label", "删除 " + name);
  remove.onclick = () => {
    draftRules = draftRules.filter((item) => item !== rule);
    markDirty();
    drawRules();
  };
  const actions = createElement("div", "rule-actions");
  actions.append(activation, remove);
  row.append(actions);
  return row;
}

function markDirty(delay = 0) {
  editGeneration++;
  dirty = true;
  saveError = "";
  scheduleAutoSave(delay);
  updateActions();
}

function updateActions() {
  getElement("default-mode").disabled = configUnavailable;
  getElement("pause").disabled = configUnavailable || saving || pausing;
  getElement("reload").disabled = saving || pausing;
  getElement("retry-save").hidden = !saveError || configConflict;
  getElement("retry-save").disabled = saving || pausing || configUnavailable;
  displaySaveStatus();
}

function displaySaveStatus() {
  const status = getElement("saved");
  status.dataset.state = "";
  if (saving) {
    status.textContent = "正在自动保存…";
  } else if (configUnavailable) {
    status.textContent = "配置不可用，暂时无法自动保存";
    status.dataset.state = "error";
  } else if (configConflict) {
    status.textContent = "配置已在其他位置修改。请到“测试与诊断”重新加载后编辑。";
    status.dataset.state = "error";
  } else if (saveError) {
    status.textContent = "自动保存失败，未生效：" + saveError;
    status.dataset.state = "error";
  } else if (dirty && !schemeReady()) {
    status.textContent = "方案尚未生效：请补全有效配置。应用规则仍会自动保存。";
    status.dataset.state = "pending";
  } else {
    status.textContent = dirty ? "等待自动保存…" : "所有更改已自动保存";
  }
}

function schemeReady() {
  if (!schemeId || !schemeForm || composingConfigText) {
    return false;
  }
  return (schemeForm.fields || []).every((field) => {
    const value = schemeConfig[field.key];
    if (field.type === "toggle") {
      return !field.required || typeof value === "boolean";
    }
    if (field.type !== "text" && field.type !== "select") {
      return false;
    }
    if (value === undefined || value === "") {
      return !field.required;
    }
    return field.type !== "select" || (field.options || []).some((option) => option.value === value);
  });
}

function draftConfig() {
  return { ...savedConfig, schemeId, schemeConfig, rules: draftRules, defaultMode };
}

function pendingConfig() {
  if (!savedConfig) {
    return null;
  }
  const next = structuredClone(draftConfig());
  // 多个必填项作为一个整体应用。方案未填完时，规则仍可独立保存，后台继续用原方案。
  if (!schemeReady()) {
    next.schemeId = savedConfig.schemeId;
    next.schemeConfig = structuredClone(savedConfig.schemeConfig ?? {});
  }
  return editableConfig(next) === editableConfig(savedConfig) ? null : next;
}

function scheduleAutoSave(delay = 0) {
  clearTimeout(autoSaveTimer);
  autoSaveTimer = null;
  if (!dirty || saveError || configConflict || composingConfigText) {
    return;
  }
  // 文本输入短暂合并；选择、开关、添加和删除直接进入串行保存队列。
  autoSaveTimer = setTimeout(() => {
    autoSaveTimer = null;
    saveConfig();
  }, delay);
}

async function refreshApps() {
  const request = ++appsRequest;
  getElement("refresh").disabled = true;
  getElement("apps-status").textContent = "正在读取应用…";
  try {
    const apps = await callService("ListApps");
    if (request !== appsRequest) {
      return;
    }
    const select = getElement("apps");
    const selected = select.value === "" ? null : runningApps[Number(select.value)];
    runningApps = apps || [];
    select.replaceChildren(new Option("选择正在运行的应用", ""));
    runningApps.forEach((app, index) => {
      const label = app.name + " — " + (app.bundleId || app.path);
      select.add(new Option(label, String(index)));
      if (selected && app.path === selected.path && app.bundleId === selected.bundleId) {
        select.value = String(index);
      }
    });
    getElement("apps-status").textContent = runningApps.length ? "" : "没有可添加的应用，请先打开目标应用再刷新。";
    drawRules();
  } catch (error) {
    if (request === appsRequest) {
      getElement("apps-status").textContent = "读取应用失败：" + errorText(error);
    }
  } finally {
    if (request === appsRequest) {
      getElement("refresh").disabled = false;
    }
  }
}

function addRule() {
  const value = getElement("apps").value;
  if (value === "") {
    getElement("apps-status").textContent = "请先选择应用";
    return;
  }
  const app = runningApps[Number(value)];
  if (!app) {
    return;
  }

  const duplicate = draftRules.some((rule) => {
    return app.bundleId
      ? rule.bundleId === app.bundleId
      : !rule.bundleId && rule.appPath.toLowerCase() === app.path.toLowerCase();
  });
  if (duplicate) {
    getElement("apps-status").textContent = "该应用已在规则中；被接管的规则可在插件详情中编辑。";
    return;
  }

  const identity = app.bundleId ? { bundleId: app.bundleId } : { appPath: app.path };
  draftRules.push({ ...identity, enabled: true, targetMode: "english" });
  markDirty();
  drawRules();
  showAddPanel(false);
}

// 后台状态：连接、暂停和执行结果始终刷新，配置草稿按是否有编辑决定是否同步。
function displayStatus(data, force = false) {
  // 保存、续期及窗口聚焦可能同时刷新；旧版本响应不能回滚较新的配置。
  if (latestConfig && data.config.revision < latestConfig.revision) {
    return;
  }
  latestConfig = data.config;
  configUnavailable = !!data.error;
  const nextPlugins = data.rpc?.plugins ?? [];
  const pluginsChanged = JSON.stringify(plugins) !== JSON.stringify(nextPlugins);
  plugins = nextPlugins;
  if (pluginsChanged) {
    refreshApps();
  }
  displayConnectionStatus(data);
  getElement("config-path").textContent = data.configPath;

  if (data.error || data.monitorError) {
    getElement("agent-state").textContent = "自动功能不可用";
  } else {
    getElement("agent-state").textContent = data.config.paused ? "自动切换已暂停" : "自动切换运行中";
  }
  document.querySelector(".agent-indicator").dataset.state = data.error || data.monitorError
    ? "error"
    : (data.config.paused ? "paused" : "running");
  getElement("paused-notice").hidden = !data.config.paused;

  const pause = getElement("pause");
  pause.textContent = data.config.paused ? "恢复自动切换" : "暂停自动切换";
  pause.dataset.paused = String(data.config.paused);
  getElement("config-error").textContent = data.error || data.monitorError || "";

  syncConfigDraft(data.config, force);
  displayRecentResult(data.recent);
  updateActions();
}

function displayConnectionStatus(data) {
  const status = getElement("rpc-state");
  if (data.rpcError) {
    status.textContent = "插件接口不可用：" + data.rpcError;
  } else if (data.rpc?.listening) {
    status.textContent = "本机接口运行中 · " + data.rpc.clients + " 个插件已连接";
  } else {
    status.textContent = "插件接口未启动";
  }
  if (data.rpc?.lastEvent) {
    status.textContent += " · 最近事件：" + data.rpc.lastEvent;
  }
}

function syncConfigDraft(config, force) {
  const activeSchemeSignature = JSON.stringify([config.schemeId, config.schemeConfig]);
  if (manualSchemeSignature && activeSchemeSignature !== manualSchemeSignature) {
    invalidateManualResult();
    modeLabel.textContent = "方案已更改，请重新测试";
    manualStatus.textContent = "";
    manualDetails.textContent = "";
  }
  manualSchemeSignature = activeSchemeSignature;

  if (force || (!dirty && !saving)) {
    const changed = force || !savedConfig
      || savedConfig.schemeId !== config.schemeId
      || JSON.stringify(savedConfig.schemeConfig) !== JSON.stringify(config.schemeConfig);
    savedConfig = config;
    defaultMode = config.defaultMode || "";
    getElement("default-mode").value = defaultMode;

    // 保留控件的回调引用了规则对象；内容不变时也必须保留这些对象。
    if (JSON.stringify(draftRules) !== JSON.stringify(config.rules ?? [])) {
      draftRules = structuredClone(config.rules ?? []);
    }
    schemeId = config.schemeId || "";
    schemeConfig = structuredClone(config.schemeConfig ?? {});
    dirty = false;
    saveError = "";
    configConflict = false;
    clearTimeout(autoSaveTimer);

    if (changed) {
      schemeDrafts.clear();
      selectScheme();
      refreshSchemeForm();
    }
    drawRules();
  } else {
    drawRules();
    if (savedConfig && config.revision !== savedConfig.revision) {
      // 暂停是立即保存的独立操作。仅暂停发生变化时更新版本基线，保留规则草稿。
      if (editableConfig(config) === editableConfig(savedConfig)) {
        savedConfig = config;
      } else if (!saving) {
        configConflict = true;
        clearTimeout(autoSaveTimer);
      }
    }
  }
  const name = schemes.find((scheme) => scheme.id === config.schemeId)?.name || config.schemeId || "未配置";
  getElement("manual-scheme").textContent = "当前生效方案：" + name;
}

function editableConfig(config) {
  return JSON.stringify([config.rules ?? [], config.schemeId, config.schemeConfig ?? {}, config.defaultMode || ""]);
}

function displayRecentResult(result) {
  recentResult = result;
  if (!result?.status) {
    return;
  }

  const labels = {
    changed: "已切换",
    applied: "调用已完成",
    already_selected: "已是目标模式",
    failed: "切换失败",
    skipped: "已跳过",
  };
  const rule = draftRules.find((rule) => rule.bundleId
    ? rule.bundleId === result.app
    : rule.appPath?.toLowerCase() === result.app?.toLowerCase());
  const appName = rule ? ruleAppName(rule) : result.app.replaceAll("\\", "/").split("/").at(-1);
  getElement("auto-status").textContent =
    (labels[result.status] ?? result.status) + " · " + appName;
  getElement("auto-details").textContent =
    new Date(result.at).toLocaleTimeString() + " · " + result.message;
  document.querySelector(".recent-result").dataset.failed = String(result.status === "failed");
}

async function refreshStatus(force = false) {
  if (force) {
    clearTimeout(autoSaveTimer);
  }
  const request = ++statusRequest;
  const requestedGeneration = editGeneration;
  try {
    const data = await callService("GetStatus");
    if (request < appliedStatusRequest) {
      return;
    }
    appliedStatusRequest = request;
    displayStatus(data, force && requestedGeneration === editGeneration);
  } catch (error) {
    getElement("config-error").textContent = "读取状态失败：" + errorText(error);
  }
}

async function saveConfig() {
  if (!savedConfig || saving || pausing || configUnavailable || !dirty || configConflict || saveError) {
    return;
  }
  const next = pendingConfig();
  if (!next) {
    dirty = editableConfig(draftConfig()) !== editableConfig(savedConfig);
    updateActions();
    return;
  }

  const savedGeneration = editGeneration;
  saving = true;
  updateActions();

  try {
    const data = await callService("SaveConfig", next);
    // 只推进已确认的版本，不用响应重建控件或覆盖正在编辑的新值。
    savedConfig = data.config;
    if (latestConfig && latestConfig.revision > data.config.revision) {
      if (editableConfig(latestConfig) === editableConfig(data.config)) {
        savedConfig = latestConfig;
      } else {
        configConflict = true;
      }
    }
    dirty = editableConfig(draftConfig()) !== editableConfig(savedConfig);
    displayStatus(data);
  } catch (error) {
    saveError = errorText(error);
    // 重读版本以识别托盘暂停或其他位置更新。错误仍保留，不能用续期刷新掩盖失败。
    await refreshStatus();
  } finally {
    saving = false;
    if (latestConfig) {
      syncConfigDraft(latestConfig, false);
    }
    // 同一时刻最多一个保存；请求期间的修改在成功后接着提交。
    // 失败不无限重试；新编辑可以重试一次，或由错误提示旁的“重试”触发。
    if (editGeneration !== savedGeneration && !configConflict) {
      saveError = "";
    }
    updateActions();
    if (dirty && pendingConfig()) {
      scheduleAutoSave();
    }
  }
}

async function retryAutoSave() {
  if (saving || pausing) {
    return;
  }
  await refreshStatus();
  saveError = "";
  updateActions();
  scheduleAutoSave();
}

async function togglePaused() {
  if (saving || pausing || configUnavailable) {
    return;
  }
  const pause = getElement("pause");
  pausing = true;
  updateActions();
  try {
    const data = await callService("SetPaused", pause.dataset.paused !== "true");
    displayStatus(data);
  } catch (error) {
    getElement("config-error").textContent = "暂停状态保存失败：" + errorText(error);
  } finally {
    pausing = false;
    updateActions();
    scheduleAutoSave();
  }
}

// 切换方案：只解释通用表单描述，不在前端判断具体方案 ID。
async function loadSchemes() {
  try {
    schemes = await callService("ListSchemes") || [];
    selectScheme();
  } catch (error) {
    getElement("scheme-list").textContent = "读取方案列表失败：" + errorText(error);
  }
}

function selectScheme() {
  const list = getElement("scheme-list");
  list.replaceChildren();
  const available = [...schemes];
  if (schemeId && !available.some((scheme) => scheme.id === schemeId)) {
    available.push({ id: schemeId, name: "当前系统不可用 — " + schemeId });
  }
  for (const scheme of available) {
    const label = createElement("label", "scheme-option");
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "scheme";
    radio.value = scheme.id;
    radio.checked = scheme.id === schemeId;
    radio.onchange = () => changeScheme(scheme.id);
    // 名称描述切换方式，次行说明适用输入法；内容由方案提供。
    const copy = createElement("span", "scheme-copy");
    const name = createElement("span", "scheme-name", scheme.name);
    name.id = "scheme-name-" + scheme.id;
    radio.setAttribute("aria-labelledby", name.id);
    copy.append(name);
    if (scheme.description) {
      const description = createElement("span", "scheme-summary", scheme.description);
      description.id = "scheme-summary-" + scheme.id;
      radio.setAttribute("aria-describedby", description.id);
      copy.append(description);
    }
    label.append(radio, copy);
    list.append(label);
  }
  if (!available.length) {
    list.append(createElement("p", "empty-state", "当前没有可用的切换方案。"));
  }
}

function changeScheme(nextId) {
  // 切到其他方案再回来时保留该方案尚未填完的配置。
  schemeDrafts.set(schemeId, structuredClone(schemeConfig));
  schemeId = nextId;
  const config = schemeDrafts.get(schemeId)
    ?? (schemeId === savedConfig?.schemeId ? savedConfig.schemeConfig : {});
  schemeConfig = structuredClone(config || {});
  markDirty();
  refreshSchemeForm();
}

function createFieldControl(field) {
  let control;
  if (field.type === "select") {
    control = document.createElement("select");
    control.add(new Option("请选择", ""));
    for (const option of field.options || []) {
      control.add(new Option(option.label, option.value));
    }

    // 已保存但当前不可用的值仍显示出来，让用户明确看到需要重新选择。
    const selected = schemeConfig[field.key] || "";
    const available = [...control.options].some((option) => option.value === selected);
    if (selected && !available) {
      control.add(new Option("未启用或不可用 — " + selected, selected));
    }
    control.value = selected;
  } else if (field.type === "text" || field.type === "toggle") {
    control = document.createElement("input");
    control.type = field.type === "toggle" ? "checkbox" : "text";
    if (field.type === "toggle") {
      control.checked = schemeConfig[field.key] === true;
    } else {
      control.value = schemeConfig[field.key] || "";
    }
  } else {
    getElement("scheme-status").textContent = "不支持的配置控件：" + field.type;
    return null;
  }

  control.required = !!field.required;
  control.setAttribute("aria-label", field.label);
  const updateValue = () => {
    schemeConfig[field.key] = field.type === "toggle" ? control.checked : control.value;
    markDirty(field.type === "text" ? 300 : 0);
  };
  if (field.type === "text") {
    control.oninput = updateValue;
    // 仅处理配置文本框的浏览器输入事件，避免把拼音输入过程中的中间文本写盘。
    control.addEventListener("compositionstart", () => {
      composingConfigText = true;
      clearTimeout(autoSaveTimer);
    });
    control.addEventListener("compositionend", () => {
      composingConfigText = false;
      updateValue();
    });
  } else {
    control.onchange = updateValue;
  }
  return control;
}

function drawForm() {
  const container = getElement("scheme-fields");
  container.replaceChildren();
  getElement("scheme-description").textContent = schemeForm?.description || "";

  for (const field of schemeForm?.fields || []) {
    const control = createFieldControl(field);
    if (!control) {
      continue;
    }

    const label = document.createElement("label");
    label.textContent = field.label;
    label.append(control);
    if (field.help) {
      const help = document.createElement("span");
      help.textContent = field.help;
      label.append(help);
    }
    container.append(label);
  }
}

async function refreshSchemeForm() {
  const requestedGeneration = ++formGeneration;
  schemeForm = null;
  composingConfigText = false;
  drawForm();
  updateActions();
  getElement("scheme-status").textContent = "";
  if (!schemeId) {
    return;
  }

  try {
    const form = await callService("GetSchemeForm", schemeId);
    // 快速切换方案时，旧请求可能晚到，不能用它覆盖当前表单。
    if (requestedGeneration !== formGeneration) {
      return;
    }
    schemeForm = form;
    drawForm();
    updateActions();
    scheduleAutoSave();
  } catch (error) {
    if (requestedGeneration === formGeneration) {
      getElement("scheme-status").textContent = "读取方案配置失败：" + errorText(error) + "；请重新进入此页重试。";
    }
  }
}

// 集中绑定事件，再依次加载方案、已保存配置和应用列表。
function bindEvents() {
  for (const button of manualButtons) {
    button.addEventListener("click", () => performManualOperation(button.dataset.mode));
  }
  window.addEventListener("blur", invalidateManualResult);
  window.addEventListener("focus", () => refreshStatus());
  Events.On("agent:changed", () => refreshStatus());

  getElement("refresh").onclick = () => {
    appInfoCache.clear();
    appInfoVersion++;
    refreshApps();
  };
  getElement("add").onclick = addRule;
  getElement("default-mode").onchange = (event) => {
    defaultMode = event.target.value;
    markDirty();
  };
  getElement("reload").onclick = () => refreshStatus(true);
  getElement("retry-save").onclick = retryAutoSave;
  getElement("pause").onclick = togglePaused;
  getElement("add-toggle").onclick = () => showAddPanel(getElement("add-panel").hidden);
  getElement("add-cancel").onclick = () => showAddPanel(false);
  document.querySelectorAll("[data-page]").forEach((button) => {
    button.onclick = () => showPage(button.dataset.page);
  });
}

bindEvents();
await loadSchemes();
await refreshStatus(true);
await refreshApps();
