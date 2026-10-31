import { Call, Events } from "/wails/runtime.js";
const buttons = [...document.querySelectorAll("button[data-mode]")];
const test = document.querySelector("#test");
const status = document.querySelector("#status");
const modeLabel = document.querySelector("#mode");
const details = document.querySelector("#details");
let busy = false;
let generation = 0;
window.addEventListener("blur", () => { generation++; if(busy) { status.textContent="窗口已失焦，本次结果不再展示，请重新查询。"; modeLabel.textContent="本次结果未确认"; details.textContent=""; } });
for (const button of buttons) button.addEventListener("click", async () => {
 if(busy) return;
 busy=true; const current=++generation;
 buttons.forEach(b=>b.disabled=true);
 test.focus({preventScroll:true});
 status.textContent="正在查询或切换输入法……";
 try {
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  if(!document.hasFocus() || current!==generation) throw new Error("窗口已失焦，请重新操作。");
  const reply=await Call.ByName("ime-pilot/internal/desktop.InputService.Perform",button.dataset.mode);
  if(current!==generation) return;
  status.textContent=reply.message;
  if(reply.ok){const s=reply.state;modeLabel.textContent="最近查询："+({chinese:"中文",english:"英文"}[s.Mode]??(platform==="darwin"?"未配置的":"未知"))+(platform==="darwin"?"输入源":"模式");details.textContent=s.Description+"\n"+s.Details;}
  else {modeLabel.textContent="本次结果未确认，请重新查询。";details.textContent="没有将上一次查询结果作为本次成功状态。";}
 } catch(error){if(current===generation){status.textContent="操作失败："+String(error);modeLabel.textContent="本次结果未确认";details.textContent="";}}
 finally {busy=false;buttons.forEach(b=>b.disabled=false);}
});

const api = (method,...args) => Call.ByName("ime-pilot/internal/desktop.InputService."+method,...args);
const $ = id => document.getElementById(id);
let config = null;
let draft = [];
let apps = [];
let dirty = false;
let editGeneration = 0;
let saving = false;
let plugins = [];
let macos = {};
let inputSources = [];
let platform = "";

function takeoverNames(rule) {
 const path = (rule.appPath || "").replaceAll("/", "\\").toLowerCase();
 const executable = path.split("\\").at(-1);
 return [...new Set(plugins.filter(plugin => plugin.programs.some(program =>
  (program.kind === "macos.bundleId" && program.value === rule.bundleId) ||
  (!rule.bundleId && program.kind === "windows.executablePath" && program.value.replaceAll("/", "\\").toLowerCase() === path) ||
  (!rule.bundleId && program.kind === "windows.executableName" && program.value.toLowerCase() === executable)
 )).map(plugin => plugin.name))];
}

function drawPlugins() {
 const list = $("plugin-list");
 list.replaceChildren();
 if (!plugins.length) {
  list.textContent = "暂无插件接管程序。";
  return;
 }
 const seen = new Set();
 for (const plugin of plugins) {
  for (const program of plugin.programs) {
   const key = plugin.pluginId + ":" + program.kind + ":" + program.value;
   if (seen.has(key)) continue;
   seen.add(key);
   const item = document.createElement("div");
   item.textContent = plugin.name + "：" + program.value;
   list.append(item);
  }
 }
}
function errorText(error) {return String(error?.message ?? error);}
function drawRules() {
 const body=$("rules"); body.replaceChildren();
 if(!draft.length){const row=body.insertRow();const cell=row.insertCell();cell.colSpan=4;cell.textContent="尚无应用规则。插件接管不需要添加规则。";return;}
 draft.forEach((rule,index)=>{
  const row=body.insertRow();const toggle=document.createElement("input");toggle.type="checkbox";toggle.checked=rule.enabled;toggle.setAttribute("aria-label","启用 "+rule.name);
  toggle.onchange=()=>{rule.enabled=toggle.checked;markDirty();};row.insertCell().append(toggle);
  const cell=row.insertCell();const name=document.createElement("strong");name.textContent=rule.name;const path=document.createElement("div");path.className="path";path.textContent=rule.bundleId || rule.appPath;cell.append(name,path);
  const owners = takeoverNames(rule);
  if (owners.length) {
   const notice = document.createElement("div");
   notice.className = "takeover";
   notice.textContent = "被 " + owners.join("、") + " 插件接管，应用规则暂不执行";
   cell.append(notice);
  }
  const activation = document.createElement("select");
  activation.setAttribute("aria-label", rule.name + "：进入应用时的输入模式");
  activation.add(new Option("切英文", "english"));
  activation.add(new Option("切中文", "chinese"));
  activation.add(new Option("不切换", "none"));
  activation.value = rule.switchOnActivation === false ? "none" : (rule.targetMode || "english");
  activation.onchange = () => {
   rule.switchOnActivation = activation.value !== "none";
   // 暂停进入应用切换时保留目标模式，避免丢失已保存的选择。
   if (rule.switchOnActivation) rule.targetMode = activation.value;
   markDirty();
  };
  row.insertCell().append(activation);
  const remove=document.createElement("button");remove.textContent="删除";remove.className="secondary";remove.setAttribute("aria-label","删除 "+rule.name);remove.onclick=()=>{draft.splice(index,1);markDirty();drawRules();};row.insertCell().append(remove);
 });
}
function markDirty(){editGeneration++;dirty=true;$("saved").textContent="有未保存的修改";}
function displayStatus(data,force=false){
 platform = data.platform;
 $("macos-settings").hidden = platform !== "darwin";
 if (platform === "darwin") $("manual-help").textContent = "先保存上方输入源设置，再点击按钮，在测试框核对实际输入。这里只确认本窗口选中的系统输入源，不读取输入法内部中英文状态。";
 plugins = data.rpc?.plugins ?? [];
 drawPlugins();
 $("config-path").textContent=data.configPath;
 $("rpc-state").textContent=data.rpcError?"插件接口不可用："+data.rpcError:data.rpc?.listening?"本机接口运行中 · "+data.rpc.clients+" 个插件已连接":"插件接口未启动";
 if(data.rpc?.lastEvent)$("rpc-state").textContent+=" · 最近事件："+data.rpc.lastEvent;
 $("agent-state").textContent=data.error||data.monitorError?"自动功能不可用":data.config.paused?"已暂停":"自动功能运行中";
 $("pause").textContent=data.config.paused?"恢复自动切换":"暂停自动切换";
 $("pause").dataset.paused=String(data.config.paused);
 $("pause").disabled=!!data.error || saving;
 $("config-error").textContent=data.error||data.monitorError||"";
 $("save").disabled=!!data.error || saving;
 if(force || !dirty){
  const changed = force || !config || config.revision !== data.config.revision;
  config=data.config;draft=structuredClone(config.rules);macos=structuredClone(config.macos ?? {});dirty=false;
  if(changed) drawSources();
  drawRules();$("saved").textContent="配置已加载";
 }
 else {
  drawRules();
  if(config && data.config.revision!==config.revision){$("saved").textContent="配置已在其他位置更新；请重新加载后编辑";}
 }
 const r=data.recent;
 if(r.status){const labels={changed:"已切换",already_selected:"已是目标模式",failed:"切换失败",skipped:"已跳过"};$("auto-status").textContent=(labels[r.status]??r.status)+" · "+r.app;$("auto-details").textContent=new Date(r.at).toLocaleTimeString()+" · "+r.message;}
}
async function refreshStatus(force=false){try{displayStatus(await api("GetStatus"),force);}catch(e){$("config-error").textContent="读取状态失败："+errorText(e);}}
async function refreshApps(){
 $("refresh").disabled=true;
 try{apps=await api("ListApps");const select=$("apps");select.replaceChildren(new Option("选择正在运行的应用",""));apps.forEach((a,i)=>select.add(new Option(a.name+" — "+(a.bundleId || a.path),String(i))));}
 catch(e){$("config-error").textContent="读取应用失败："+errorText(e);}
 finally{$("refresh").disabled=false;}
}
$("refresh").onclick=refreshApps;
$("add").onclick=()=>{
 const value=$("apps").value;if(value===""){$("saved").textContent="请先选择应用";return;}
 const app=apps[Number(value)];if(!app)return;
 const duplicate = draft.some(rule => app.bundleId
  ? rule.bundleId === app.bundleId
  : !rule.bundleId && rule.appPath.toLowerCase() === app.path.toLowerCase());
 if(duplicate){$("saved").textContent="该应用已在规则中";return;}
 const identity = app.bundleId ? {bundleId: app.bundleId} : {appPath: app.path};
 draft.push({...identity,name:app.name,enabled:true});markDirty();drawRules();
};
$("save").onclick=async()=>{
 if(!config||saving)return;const savedGeneration=editGeneration;saving=true;$("save").disabled=true;
 try{const data=await api("SaveConfig",{...config,macos:structuredClone(macos),rules:structuredClone(draft)});if(editGeneration===savedGeneration){displayStatus(data,true);$("saved").textContent="已保存；未被插件接管时，下次进入应用生效";}else{config=data.config;displayStatus(data);$("saved").textContent="已保存；还有新的修改尚未保存";}}
 catch(e){$("config-error").textContent="保存失败："+errorText(e);}
 finally{saving=false;$("save").disabled=false;$("pause").disabled=false;}
};
$("reload").onclick=()=>refreshStatus(true);
$("pause").onclick=async()=>{
 $("pause").disabled=true;
 try{displayStatus(await api("SetPaused",$("pause").dataset.paused!=="true"));}
 catch(e){$("config-error").textContent="暂停状态保存失败："+errorText(e);}
 finally{$("pause").disabled=false;}
};
Events.On("agent:changed",()=>refreshStatus());
window.addEventListener("focus",()=>refreshStatus());
await refreshStatus(true);
await refreshApps();

function drawSources() {
 for (const [element, key, capability] of [["chinese-source", "chineseSourceId", "chinese"], ["english-source", "englishSourceId", "english"]]) {
  const select = $(element);
  select.replaceChildren(new Option("请选择输入源", ""));
  for (const source of inputSources.filter(s => s[capability])) select.add(new Option(source.name + " — " + source.id, source.id));
  const selected = macos[key] || "";
  if (selected && ![...select.options].some(o => o.value === selected)) select.add(new Option("未启用或不可用 — " + selected, selected));
  select.value = selected;
  select.onchange = () => { macos[key] = select.value; markDirty(); };
 }
}
async function refreshSources() {
 $("refresh-sources").disabled = true;
 try { inputSources = await api("ListInputSources"); drawSources(); $("source-status").textContent = "输入源列表已更新；修改后请点击下方保存规则。"; }
 catch (e) { $("source-status").textContent = "读取输入源失败：" + errorText(e); }
 finally { $("refresh-sources").disabled = false; }
}
$("refresh-sources").onclick = refreshSources;
if (platform === "darwin") await refreshSources();
