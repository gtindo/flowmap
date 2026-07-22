const ns = "http://www.w3.org/2000/svg";
const $ = id => document.getElementById(id);
let rootID = "";
let timer;
let searchGeneration = 0;
let graphGeneration = 0;
let focusHistory = [];
let focusHistoryIndex = -1;
let currentGraph;
let currentPositions = new Map();
let currentSize = { width: 255, height: 110 };
let activeDrag;
let activePan;
let suppressClick = false;
const pointerMoveThreshold = 4;
const expansionActivationWindow = 400;
let zoomScale = 1;
let contentBounds = { width: 900, height: 600 };
let viewportState = { marginX: 0, marginY: 0, scale: 1 };
let expandedNodes = new Set();
let expansionRecords = new Map();
let expansionActivationTimes = new Map();
let baseRootNode;
let detailGeneration = 0;
let detailController;
let activeDetailID;
let activeDetailResize;
let gitSnapshot;
let activeProject = "";
let activeLanguage = "";
const reviewedFunctionIDs = new Set();
let reviewedRevision = "";
const projectPreferenceKey = "flowmap-project:v1";
const languagePreferenceKey = "flowmap-language:v1";
const themePreferenceKey = "flowmap-theme:v1";
const themePreferences = new Set(["system", "light", "dark"]);
const systemTheme = window.matchMedia("(prefers-color-scheme: dark)");
const detailMinWidth = 320;
const detailViewportMargin = 48;
let preferredDetailWidth = readDetailWidth();

function projectStorageKey(key) { return key + ":" + (activeProject || "default") + ":" + (activeLanguage || "go"); }
function detailWidthKey() { return projectStorageKey("flowmap-detail-width:v1"); }
function reviewedFunctionsKey() { return projectStorageKey("flowmap-reviewed-functions:v1"); }

function node(tag, cls, text) {
  const value = document.createElement(tag);
  if (cls) value.className = cls;
  if (text !== undefined) value.textContent = text;
  return value;
}

const goKeywords = new Set([
  "break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var"
]);
const goBuiltins = new Set([
  "any", "append", "bool", "byte", "cap", "clear", "close", "comparable", "complex", "complex64", "complex128", "copy", "delete", "error", "false", "float32", "float64", "imag", "int", "int8", "int16", "int32", "int64", "iota", "len", "make", "max", "min", "new", "nil", "panic", "print", "println", "real", "recover", "rune", "string", "true", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr"
]);
const javascriptKeywords = new Set([
  "as", "async", "await", "break", "case", "catch", "class", "const", "continue", "default", "delete", "do", "else", "export", "extends", "finally", "for", "from", "function", "if", "import", "in", "instanceof", "interface", "let", "new", "of", "return", "static", "switch", "throw", "try", "type", "typeof", "var", "while", "yield"
]);
const javascriptBuiltins = new Set(["Array", "Boolean", "Date", "Error", "JSON", "Map", "Math", "Number", "Object", "Promise", "RegExp", "Set", "String", "console", "fetch", "undefined"]);

// highlightGo tokenizes into text nodes so source remains safe without an HTML sanitizer.
function highlightSource(source, language) {
  const javascript = language === "javascript";
  const code = node("code", javascript ? "language-javascript" : "language-go");
  const tokenPattern = /\/\/[^\n]*|\/\*[\s\S]*?\*\/|`[^`]*`|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|(?:0[xX][0-9a-fA-F](?:_?[0-9a-fA-F])*|0[bB][01](?:_?[01])*|0[oO][0-7](?:_?[0-7])*|(?:\d(?:_?\d)*\.\d(?:_?\d)*|\.\d(?:_?\d)*|\d(?:_?\d)*)(?:[eE][+-]?\d(?:_?\d)*)?i?)|[A-Za-z_]\w*|\s+|./g;
  for (const match of source.matchAll(tokenPattern)) {
    const token = match[0];
    let kind = "";
    if (token.startsWith("//") || token.startsWith("/*")) kind = "comment";
    else if (token[0] === '"' || token[0] === "'" || token[0] === "`") kind = "string";
    else if (/^(?:\d|\.\d)/.test(token)) kind = "number";
    else if ((javascript ? javascriptKeywords : goKeywords).has(token)) kind = "keyword";
    else if ((javascript ? javascriptBuiltins : goBuiltins).has(token)) kind = "builtin";
    code.append(kind ? node("span", "token " + kind, token) : document.createTextNode(token));
  }
  return code;
}

function sourceBlock(source, language = "go") {
  const pre = node("pre", "source-code");
  pre.append(highlightSource(source, language));
  return pre;
}

function diffBlock(diff) {
  const pre = node("pre", "diff-code");
  const code = node("code");
  diff.replace(/\n$/, "").split("\n").forEach(line => {
    let kind = "diff-context";
    if (line.startsWith("@@")) kind = "diff-hunk";
    else if (line.startsWith("+++ ") || line.startsWith("--- ")) kind = "diff-meta";
    else if (line.startsWith("+")) kind = "diff-addition";
    else if (line.startsWith("-")) kind = "diff-deletion";
    code.append(node("span", kind, line + "\n"));
  });
  pre.append(code);
  return pre;
}

function readThemePreference() {
  try {
    const preference = localStorage.getItem(themePreferenceKey);
    return themePreferences.has(preference) ? preference : "system";
  } catch (_) { return "system"; }
}

function applyTheme(preference) {
  const resolved = preference === "system" ? (systemTheme.matches ? "dark" : "light") : preference;
  document.documentElement.dataset.theme = resolved;
  document.documentElement.dataset.themePreference = preference;
  document.documentElement.style.colorScheme = resolved;
}

function selectTheme(event) {
  const preference = themePreferences.has(event.target.value) ? event.target.value : "system";
  try { localStorage.setItem(themePreferenceKey, preference); } catch (_) {}
  applyTheme(preference);
}

function readDetailWidth() {
  try {
    const width = Number(localStorage.getItem(detailWidthKey()));
    return Number.isFinite(width) && width > 0 ? width : undefined;
  } catch (_) { return undefined; }
}

function clampDetailWidth(width) {
  const maximum = Math.max(0, window.innerWidth - detailViewportMargin);
  const minimum = Math.min(detailMinWidth, maximum);
  return Math.min(maximum, Math.max(minimum, width));
}

function applyDetailWidth(width) {
  if (!Number.isFinite(width)) return;
  document.documentElement.style.setProperty("--detail-width", clampDetailWidth(width) + "px");
}

function startDetailResize(event) {
  event.preventDefault();
  event.stopPropagation();
  const handle = event.currentTarget;
  activeDetailResize = { pointerID: event.pointerId, handle };
  handle.classList.add("resizing");
  handle.setPointerCapture(event.pointerId);
}

function resizeDetail(event) {
  if (!activeDetailResize || activeDetailResize.pointerID !== event.pointerId) return;
  event.preventDefault();
  event.stopPropagation();
  preferredDetailWidth = clampDetailWidth(window.innerWidth - event.clientX);
  applyDetailWidth(preferredDetailWidth);
}

function finishDetailResize(event) {
  if (!activeDetailResize || activeDetailResize.pointerID !== event.pointerId) return;
  event.stopPropagation();
  activeDetailResize.handle.classList.remove("resizing");
  activeDetailResize = undefined;
  try { localStorage.setItem(detailWidthKey(), String(preferredDetailWidth)); } catch (_) {}
}

async function json(url, options) {
  if (activeProject && url.startsWith("/api/") && !url.includes("project=")) {
    url += (url.includes("?") ? "&" : "?") + "project=" + encodeURIComponent(activeProject);
  }
  if (activeLanguage && url.startsWith("/api/") && !url.includes("language=")) {
    url += (url.includes("?") ? "&" : "?") + "language=" + encodeURIComponent(activeLanguage);
  }
  const response = await fetch(url, options);
  const value = await response.json();
  if (!response.ok) {
    const error = new Error(value.error || response.statusText);
    error.status = response.status;
    throw error;
  }
  return value;
}

const rescanButton = $("rescan");
const rescanLabel = $("rescan-label");
const rescanSpinner = $("rescan-spinner");
const changesButton = $("changes-button");
const themeSelect = $("theme");
const projectSelect = $("project");
const languageSelect = $("language");
const languageIcon = $("language-icon");
const projectStatus = $("project-status");
const languageStatus = $("language-status");
const themePreference = readThemePreference();
themeSelect.value = themePreference;
applyTheme(themePreference);
themeSelect.addEventListener("change", selectTheme);
systemTheme.addEventListener("change", () => {
  if (readThemePreference() === "system") applyTheme("system");
});
rescanButton.addEventListener("click", rescanCodebase);
projectSelect.addEventListener("change", () => selectProject(projectSelect.value));
languageSelect.addEventListener("change", () => selectLanguage(languageSelect.value));
changesButton.addEventListener("click", toggleChangesMenu);
$("search").addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(search, 150); });
$("search").addEventListener("keydown", event => { if (event.key === "Escape") hideResults(); });
$("tests").addEventListener("change", () => {
  renderGitStatus();
  rootID ? loadGraph() : search();
});
$("direction").addEventListener("change", loadGraph);
$("history-back").addEventListener("click", () => navigateHistory(-1));
$("history-forward").addEventListener("click", () => navigateHistory(1));
$("view").addEventListener("change", () => { if (currentGraph) render(currentGraph, true); });
$("reset-layout").addEventListener("click", resetLayout);
$("zoom-in").addEventListener("click", () => zoomGraph(0.8));
$("zoom-out").addEventListener("click", () => zoomGraph(1.25));
$("fit-graph").addEventListener("click", fitGraph);
$("hand-tool").addEventListener("click", toggleHandTool);
$("canvas").addEventListener("pointerdown", startPan);
$("detail-resize").addEventListener("pointerdown", startDetailResize);
$("detail-resize").addEventListener("pointermove", resizeDetail);
$("detail-resize").addEventListener("pointerup", finishDetailResize);
$("detail-resize").addEventListener("pointercancel", finishDetailResize);
$("close").onclick = hideDetail;
document.addEventListener("click", event => {
  if (!event.target.closest(".search-wrap")) hideResults();
  if (!event.target.closest(".git-review")) hideChangesMenu();
});
document.addEventListener("keydown", event => { if (event.key === "Escape") hideChangesMenu(); });
document.addEventListener("pointermove", movePointer);
document.addEventListener("pointerup", finishPointer);
window.addEventListener("resize", () => {
  applyDetailWidth(preferredDetailWidth);
  if (currentGraph) applyViewport();
});
applyDetailWidth(preferredDetailWidth);
initializeProjects();

if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  });
}

async function loadGitStatus() {
  try {
    gitSnapshot = await json("/api/git-status");
    loadReviewedFunctions(gitSnapshot.revision);
    renderGitStatus();
  } catch (_) {
    gitSnapshot = undefined;
    renderGitStatus();
  }
}

function loadReviewedFunctions(revision) {
  reviewedFunctionIDs.clear();
  reviewedRevision = revision || "";
  if (!reviewedRevision) {
    try { localStorage.removeItem(reviewedFunctionsKey()); } catch (_) {}
    return;
  }

  try {
    const stored = JSON.parse(localStorage.getItem(reviewedFunctionsKey()) || "null");
    if (stored?.revision === reviewedRevision && Array.isArray(stored.function_ids)) {
      stored.function_ids.filter(id => typeof id === "string").forEach(id => reviewedFunctionIDs.add(id));
      return;
    }
  } catch (_) {}

  saveReviewedFunctions();
}

function saveReviewedFunctions() {
  if (!reviewedRevision) return;
  try {
    localStorage.setItem(reviewedFunctionsKey(), JSON.stringify({
      revision: reviewedRevision,
      function_ids: Array.from(reviewedFunctionIDs),
    }));
  } catch (_) {}
}

async function initializeProjects() {
  try {
    const projects = await json("/api/projects");
    if (projects.length > 1) $("project-control").classList.remove("hidden");
    projectSelect.replaceChildren();
    projects.forEach(project => {
      const option = node("option", "", project.name);
      option.value = project.name;
      option.dataset.status = project.status;
      projectSelect.append(option);
    });
    const saved = (() => { try { return localStorage.getItem(projectPreferenceKey); } catch (_) { return ""; } })();
    const selected = projects.find(project => project.name === saved) || projects[0];
    if (selected) await selectProject(selected.name, projects);
  } catch (error) {
    alert(error.message);
  }
}

async function selectProject(name, knownProjects) {
  if (!name) return;
  const projects = knownProjects || await json("/api/projects");
  const selected = projects.find(project => project.name === name);
  if (!selected) return;
  const changed = name !== activeProject;
  activeProject = name;
  projectSelect.value = name;

  setStatusIndicator(projectStatus, selected.status, "Project");
  try { localStorage.setItem(projectPreferenceKey, name); } catch (_) {}
  if (changed) resetProjectView();
  preferredDetailWidth = readDetailWidth();
  applyDetailWidth(preferredDetailWidth);
  const languages = selected.languages || [{ language: "go", status: selected.status }];
  languageSelect.replaceChildren();
  languages.forEach(item => {
    const option = node("option", "", item.language);
    option.value = item.language;
    option.dataset.status = item.status;
    languageSelect.append(option);
  });
  if (languages.length > 1) $("language-control").classList.remove("hidden");
  else $("language-control").classList.add("hidden");
  const savedLanguage = (() => { try { return localStorage.getItem(languagePreferenceKey + ":" + name); } catch (_) { return ""; } })();
  const language = languages.find(item => item.language === savedLanguage) || languages[0];
  if (language) await selectLanguage(language.language, languages);
}

async function selectLanguage(language, knownLanguages) {
  if (!language) return;
  const languages = knownLanguages || Array.from(languageSelect.options).map(option => ({ language: option.value, status: option.dataset.status }));
  const selected = languages.find(item => item.language === language);
  if (!selected) return;
  const changed = language !== activeLanguage;
  activeLanguage = language;
  languageSelect.value = language;
  updateLanguageIcon(language);
  setStatusIndicator(languageStatus, selected.status, "Language");
  try { localStorage.setItem(languagePreferenceKey + ":" + activeProject, language); } catch (_) {}
  if (changed) resetProjectView();
  preferredDetailWidth = readDetailWidth();
  applyDetailWidth(preferredDetailWidth);
  if (selected.status !== "ready") {
    setScanLoading(true);
    try {
      const result = await json("/api/projects/" + encodeURIComponent(activeProject) + "/languages/" + encodeURIComponent(language) + "/scan", { method: "POST" });
      selected.status = "ready";
      languageSelect.selectedOptions[0].dataset.status = "ready";
      projectSelect.selectedOptions[0].dataset.status = "ready";
      setStatusIndicator(languageStatus, "ready", "Language");
      setStatusIndicator(projectStatus, "ready", "Project");
      renderGitStatus(result.git_status);
    } catch (error) {
      selected.status = "failed";
      setStatusIndicator(languageStatus, "failed", "Language");
      alert(error.message);
    } finally {
      setScanLoading(false);
    }
  }
  await loadGitStatus();
}

function resetProjectView() {
  graphGeneration++;
  searchGeneration++;
  rootID = "";
  currentGraph = undefined;
  currentPositions = new Map();
  expansionRecords = new Map();
  expandedNodes = new Set();
  focusHistory = [];
  focusHistoryIndex = -1;
  reviewedFunctionIDs.clear();
  reviewedRevision = "";
  hideDetail();
  hideResults();
  $("search").value = "";
  $("workspace").classList.add("hidden");
  $("empty").classList.remove("hidden");
  $("empty").querySelector("h1").textContent = "Begin with a function.";
  $("empty").querySelector("p").textContent = "Search by package, receiver, or name. Flowmap reveals a focused typed neighborhood—not the whole-repository hairball.";
  updateHistoryButtons();
}

function resetReviewedFunctions() {
  if (!window.confirm("Reset all reviewed function markers for this revision?")) return;
  reviewedFunctionIDs.clear();
  saveReviewedFunctions();
  if (currentGraph) drawGraph();
  renderGitStatus();
  if (activeDetailID) showDetail(activeDetailID);
}

function visibleChangedFunctions() {
  if (!gitSnapshot) return [];
  const includeTests = $("tests").checked;
  return (gitSnapshot.changed_functions || []).filter(item => includeTests || !item.test);
}

function renderGitStatus(status) {
  if (status) gitSnapshot = status;
  const review = $("git-review");
  if (!gitSnapshot || !gitSnapshot.available) {
    review.classList.add("hidden");
    hideChangesMenu();
    return;
  }
  const revision = (gitSnapshot.revision || "").slice(0, 7);
  $("git-branch").textContent = gitSnapshot.detached ? `detached @ ${revision}` : (gitSnapshot.branch || "unborn branch");
  const changes = visibleChangedFunctions();
  changesButton.textContent = `Changes ${changes.length}`;
  const menu = $("changes-menu");
  menu.replaceChildren();
  if (changes.length === 0) {
    menu.append(node("p", "changes-empty", "No changed functions in this view."));
  } else {
    changes.forEach(item => {
      const row = node("button", "change-item");
      row.type = "button";
      row.setAttribute("role", "menuitem");
      const title = node("span", "change-name", item.qualified_name);
      if (reviewedFunctionIDs.has(item.id)) title.append(node("span", "reviewed-list-badge", "✓ Reviewed"));
      const leafCount = Number.isInteger(item.leaf_descendant_count) ? item.leaf_descendant_count : 0;
      const leafBadge = node("span", "change-leaf-badge", `${leafCount} ${leafCount === 1 ? "leaf" : "leaves"}`);
      leafBadge.setAttribute("aria-label", `${leafCount} changed leaf descendants`);
      title.append(leafBadge);
      const metadata = node("span", "change-metadata");
      metadata.append(node("span", "change-kind " + item.kind, item.kind), document.createTextNode(" " + item.package + " · " + item.file.split(/[\\/]/).pop() + ":" + item.line));
      row.append(title, metadata);
      row.onclick = async () => {
        hideChangesMenu();
        await focusGraph(item.id, item.qualified_name);
        showDetail(item.id);
      };
      menu.append(row);
    });
  }
  if (reviewedFunctionIDs.size > 0) {
    const reset = node("button", "reset-reviewed", "Reset reviewed markers");
    reset.type = "button";
    reset.setAttribute("role", "menuitem");
    reset.onclick = resetReviewedFunctions;
    menu.append(reset);
  }
  review.classList.remove("hidden");
}

function toggleChangesMenu() {
  const menu = $("changes-menu");
  const open = menu.classList.contains("hidden");
  menu.classList.toggle("hidden", !open);
  changesButton.setAttribute("aria-expanded", String(open));
}

function hideChangesMenu() {
  $("changes-menu").classList.add("hidden");
  changesButton.setAttribute("aria-expanded", "false");
}

function hideResults() {
  searchGeneration++;
  $("results").replaceChildren();
  $("results").classList.add("hidden");
}

async function search() {
  const query = $("search").value.trim();
  if (query === "") { hideResults(); return; }
  const generation = ++searchGeneration;
  const results = await json("/api/search?q=" + encodeURIComponent(query) + "&tests=" + $("tests").checked);
  if (generation !== searchGeneration) return;
  const box = $("results");
  box.replaceChildren();
  results.forEach(item => {
    const row = node("div", "result");
    row.append(node("b", "", item.qualified_name), node("small", "", item.package + " · " + item.classification));
    row.onclick = () => focusGraph(item.id, item.qualified_name);
    box.append(row);
  });
  box.classList.toggle("hidden", results.length === 0);
}

async function loadGraph() {
  if (!rootID) return;
  const generation = ++graphGeneration;
  const url = graphURL(rootID);
  try {
    const graph = await json(url);
    if (generation !== graphGeneration) return;
    showLoadedGraph(graph);
  } catch (error) {
    if (generation === graphGeneration) alert(error.message);
  }
}

function showLoadedGraph(graph) {
  baseRootNode = graph.nodes.find(item => item.id === graph.root);
  expansionRecords = new Map([[graph.root, graph]]);
  expandedNodes = new Set([graph.root]);
  $("empty").classList.add("hidden");
  $("workspace").classList.remove("hidden");
  render(composeGraph(), true);
}

function showEmptyAfterRescan() {
  graphGeneration++;
  rootID = "";
  currentGraph = undefined;
  baseRootNode = undefined;
  expansionRecords = new Map();
  expandedNodes = new Set();
  $("workspace").classList.add("hidden");
  $("reset-layout").classList.add("hidden");
  $("empty").classList.remove("hidden");
  $("empty").querySelector("h1").textContent = "That function is no longer in the codebase.";
  $("empty").querySelector("p").textContent = "Search for another function to begin a refreshed graph.";
  $("search").focus();
  updateHistoryButtons();
}

async function rescanCodebase() {
  const previousLabel = rescanLabel.textContent;
  setScanLoading(true);
  try {
    const result = await json("/api/rescan", { method: "POST" });
    if ((result.git_status.revision || "") !== reviewedRevision) loadReviewedFunctions(result.git_status.revision);
    renderGitStatus(result.git_status);
    hideDetail();
    hideResults();
    if (rootID) {
      try {
        showLoadedGraph(await json(graphURL(rootID)));
      } catch (error) {
        if (error.status !== 404) throw error;
        showEmptyAfterRescan();
      }
    } else if ($("search").value.trim()) {
      await search();
    }
    const failures = result.load_report.failed_units || result.load_report.failed_package_variants || result.load_report.FailedPackageVariants || 0;
    rescanLabel.textContent = failures ? `Rescanned (${failures} warnings)` : `Rescanned ${result.function_count}`;
    setTimeout(() => { if (!rescanButton.disabled) rescanLabel.textContent = previousLabel; }, 1800);
  } catch (error) {
    alert(error.message);
    rescanLabel.textContent = previousLabel;
  } finally {
    setScanLoading(false);
  }
}

function setStatusIndicator(indicator, status, scope) {
  const normalized = ["ready", "unscanned", "loading", "failed"].includes(status) ? status : "unscanned";
  indicator.className = "status-indicator " + normalized;
  indicator.title = `${scope}: ${normalized}`;
}

function updateLanguageIcon(language) {
  languageIcon.querySelectorAll("svg").forEach(icon => icon.classList.toggle("active", icon.dataset.languageIcon === language));
  languageIcon.title = language === "go" ? "Go" : "JavaScript and TypeScript";
}

function setScanLoading(loading) {
	// The former “Scanning…” label is intentionally replaced by the spinner.
	rescanButton.disabled = loading;
  rescanSpinner.classList.toggle("hidden", !loading);
}

function graphURL(id) {
  return "/api/graph?root=" + encodeURIComponent(id) + "&direction=" + $("direction").value + "&depth=1&tests=" + $("tests").checked;
}

async function focusGraph(id, qualifiedName, options = {}) {
  if (!options.historyIndex && options.historyIndex !== 0 && id === rootID) {
    if (qualifiedName) $("search").value = qualifiedName;
    hideResults();
    return;
  }
  hideResults();
  const generation = ++graphGeneration;
  try {
    const graph = await json(graphURL(id));
    if (generation !== graphGeneration) return;
    rootID = id;
    if (qualifiedName) $("search").value = qualifiedName;
    hideDetail();
    expandedNodes = new Set();
    expansionRecords = new Map();
    baseRootNode = undefined;
    showLoadedGraph(graph);
    if (options.historyIndex || options.historyIndex === 0) {
      focusHistoryIndex = options.historyIndex;
    } else {
      focusHistory = focusHistory.slice(0, focusHistoryIndex + 1);
      focusHistory.push({ id, qualifiedName: qualifiedName || graph.nodes.find(item => item.id === id)?.qualified_name || id });
      focusHistoryIndex = focusHistory.length - 1;
    }
    updateHistoryButtons();
  } catch (error) {
    if (generation === graphGeneration) alert(error.message);
  }
}

function navigateHistory(offset) {
  const targetIndex = focusHistoryIndex + offset;
  if (targetIndex < 0 || targetIndex >= focusHistory.length) return;
  const target = focusHistory[targetIndex];
  focusGraph(target.id, target.qualifiedName, { historyIndex: targetIndex });
}

function updateHistoryButtons() {
  $("history-back").disabled = focusHistoryIndex <= 0;
  $("history-forward").disabled = focusHistoryIndex < 0 || focusHistoryIndex >= focusHistory.length - 1;
}

function edgeKey(edge) {
  return edge.caller_id + "|" + edge.callee_id + "|" + (edge.kind || "call") + "|" + edge.dynamic + "|" + (edge.call_site || "");
}

function composeGraph() {
  const nodes = new Map();
  const edges = new Map();
  if (baseRootNode) nodes.set(baseRootNode.id, baseRootNode);
  const pending = new Map(expansionRecords);
  let changed = true;
  while (changed) {
    changed = false;
    for (const [ownerID, expansion] of pending) {
      if (!nodes.has(ownerID)) continue;
      expansion.nodes.forEach(item => nodes.set(item.id, item));
      expansion.edges.forEach(edge => edges.set(edgeKey(edge), edge));
      pending.delete(ownerID);
      changed = true;
    }
  }
  return { root: rootID, nodes: Array.from(nodes.values()), edges: Array.from(edges.values()) };
}

function pruneOrphanedExpansions() {
  let changed = true;
  while (changed) {
    changed = false;
    const visible = new Set(composeGraph().nodes.map(item => item.id));
    for (const ownerID of expansionRecords.keys()) {
      if (visible.has(ownerID)) continue;
      expansionRecords.delete(ownerID);
      expandedNodes.delete(ownerID);
      changed = true;
    }
  }
}

function render(graph, resetViewport = false) {
  currentGraph = graph;
  const simple = $("view").value === "simple";
  $("reset-layout").classList.remove("hidden");
  currentSize = simple ? { width: 185, height: 48 } : { width: 255, height: 110 };
  const gaps = graphGaps();
  const direction = $("direction").value;
  const levels = signedLevels(graph, direction);
  const buckets = new Map();
  graph.nodes.forEach(item => { const level = levels.get(item.id); if (!buckets.has(level)) buckets.set(level, []); buckets.get(level).push(item); });
  const wrap = $("canvas-wrap");
  const minimumLevel = Math.min(...levels.values());
  const rootX = Math.max(wrap.clientWidth / 2 - currentSize.width / 2, 40 - minimumLevel * gaps.x);
  const rootY = Math.max(45, wrap.clientHeight / 2 - currentSize.height / 2);
  currentPositions = new Map();
  for (const [level, items] of buckets) {
    items.sort((left, right) => left.package.localeCompare(right.package) || left.qualified_name.localeCompare(right.qualified_name));
    items.forEach((item, index) => currentPositions.set(item.id, {
      x: rootX + level * gaps.x,
      y: rootY + (index - (items.length - 1) / 2) * gaps.y,
    }));
  }
  normalizeLayout();
  const saved = readSavedPositions();
  for (const [id, position] of Object.entries(saved)) if (currentPositions.has(id)) currentPositions.set(id, position);
  resizeCanvas(resetViewport);
  drawGraph();
  if (resetViewport) centerRootInViewport();
}

function signedLevels(graph, direction) {
  const byID = new Set(graph.nodes.map(item => item.id));
  const levels = new Map([[graph.root, 0]]);
  const queue = [graph.root];
  while (queue.length) {
    const current = queue.shift();
    graph.edges.forEach(edge => {
      let next;
      let step;
      if ((direction === "downstream" || direction === "both") && edge.caller_id === current) {
        next = edge.callee_id;
        step = 1;
      }
      if ((direction === "upstream" || direction === "both") && edge.callee_id === current) {
        next = edge.caller_id;
        step = -1;
      }
      if (next && byID.has(next) && !levels.has(next)) {
        levels.set(next, levels.get(current) + step);
        queue.push(next);
      }
    });
  }
  graph.nodes.forEach(item => { if (!levels.has(item.id)) levels.set(item.id, 0); });
  return levels;
}

function graphGaps() {
  return $("view").value === "simple" ? { x: 230, y: 70 } : { x: 320, y: 155 };
}

function resizeCanvas(resetViewport = false) {
  let maxX = 900;
  let maxY = 600;
  for (const position of currentPositions.values()) {
    maxX = Math.max(maxX, position.x + currentSize.width + 80);
    maxY = Math.max(maxY, position.y + currentSize.height + 80);
  }
  contentBounds = { width: maxX, height: maxY };
  if (resetViewport) {
    zoomScale = 1;
    applyViewport();
  } else {
    applyViewport();
  }
}

function centerRootInViewport() {
  const position = currentGraph && currentPositions.get(currentGraph.root);
  if (!position) return;
  scrollViewportTo({
    x: position.x + currentSize.width / 2,
    y: position.y + currentSize.height / 2,
  });
}

function normalizeLayout() {
  let minimumX = 40;
  let minimumY = 10;
  for (const position of currentPositions.values()) {
    minimumX = Math.min(minimumX, position.x);
    minimumY = Math.min(minimumY, position.y);
  }
  const deltaX = Math.max(0, 40 - minimumX);
  const deltaY = Math.max(0, 10 - minimumY);
  if (!deltaX && !deltaY) return;
  for (const position of currentPositions.values()) {
    position.x += deltaX;
    position.y += deltaY;
  }
}

function drawGraph() {
  $("nodes").replaceChildren();
  drawEdges();
  const simple = $("view").value === "simple";
  currentGraph.nodes.forEach(item => drawNode(item, currentPositions.get(item.id), item.id === currentGraph.root, simple));
}

function drawEdges() {
  $("edges").replaceChildren();
  if (!currentGraph) return;
  currentGraph.edges.forEach(edge => {
    const from = currentPositions.get(edge.caller_id);
    const to = currentPositions.get(edge.callee_id);
    if (!from || !to) return;
    const path = document.createElementNS(ns, "path");
    const x1 = from.x + currentSize.width;
    const y1 = from.y + currentSize.height / 2;
    const x2 = to.x;
    const y2 = to.y + currentSize.height / 2;
    const curve = Math.max(45, Math.abs(x2 - x1) / 2);
    path.setAttribute("d", "M" + x1 + " " + y1 + " C" + (x1 + curve) + " " + y1 + " " + (x2 - curve) + " " + y2 + " " + x2 + " " + y2);
    if (edge.kind === "dependency") path.setAttribute("class", "dependency");
    else if (edge.dynamic) path.setAttribute("class", "dynamic");
    const title = document.createElementNS(ns, "title");
    title.textContent = edge.kind === "dependency" ? "Function dependency" : edge.dynamic ? "Dynamic dispatch candidate" : "Static call";
    path.append(title);
    $("edges").append(path);
  });
}

function drawNode(item, position, isRoot, simple) {
  const group = document.createElementNS(ns, "g");
  const changeKind = item.change?.kind;
  const reviewed = reviewedFunctionIDs.has(item.id);
  const nameClass = "name" + (changeKind ? " " + changeKind : "");
  group.setAttribute("class", "node " + item.classification.kind + (isRoot ? " root" : "") + (simple ? " simple" : "") + (item.id === activeDetailID ? " detail-selected" : ""));
  group.setAttribute("transform", "translate(" + position.x + " " + position.y + ")");
  group.dataset.id = item.id;
  const title = document.createElementNS(ns, "title");
  title.textContent = item.qualified_name + (changeKind ? " (" + changeKind + " in Git diff)" : "");
  const focusRing = document.createElementNS(ns, "rect");
  focusRing.setAttribute("class", "detail-focus-ring");
  focusRing.setAttribute("x", -5);
  focusRing.setAttribute("y", -5);
  focusRing.setAttribute("width", currentSize.width + 10);
  focusRing.setAttribute("height", currentSize.height + 10);
  const rect = document.createElementNS(ns, "rect");
  rect.setAttribute("width", currentSize.width);
  rect.setAttribute("height", currentSize.height);
  group.append(title, focusRing, rect);
  if (reviewed) addReviewedNodeBadge(group);
  if (simple) {
    addText(group, item.name, 10, 29, nameClass, reviewed ? 8 : 26);
  } else {
    addText(group, item.qualified_name, 12, 23, nameClass, reviewed ? 21 : 34);
    addText(group, item.package, 12, 42, "pkg", 39);
    addText(group, item.signature, 12, 63, "sig", 40);
    addText(group, item.intent || "No authored intent", 12, 86, "intent", 38);
  }
  group.addEventListener("pointerdown", event => startDrag(event, item.id, group));
  group.onclick = () => { if (!suppressClick) showDetail(item.id); };
  addExpansionControl(group, item.id);
  $("nodes").append(group);
}

function addReviewedNodeBadge(group) {
  const badgeX = currentSize.width - 96;
  const marker = document.createElementNS(ns, "g");
  marker.setAttribute("class", "reviewed-node-badge");
  marker.setAttribute("role", "img");
  marker.setAttribute("aria-label", "Reviewed");
  const title = document.createElementNS(ns, "title");
  title.textContent = "Reviewed";
  const background = document.createElementNS(ns, "rect");
  background.setAttribute("x", badgeX);
  background.setAttribute("y", 8);
  background.setAttribute("width", 68);
  background.setAttribute("height", 19);
  const label = document.createElementNS(ns, "text");
  label.setAttribute("x", badgeX + 6);
  label.setAttribute("y", 21);
  label.textContent = "✓ Reviewed";
  marker.append(title, background, label);
  group.append(marker);
}

function addExpansionControl(group, id) {
  const isExpanded = expandedNodes.has(id);
  const control = document.createElementNS(ns, "g");
  control.setAttribute("class", "expand-control");
  control.setAttribute("transform", "translate(" + (currentSize.width - 13) + " 13)");
  const circle = document.createElementNS(ns, "circle");
  circle.setAttribute("r", 10);
  const plus = document.createElementNS(ns, "text");
  plus.setAttribute("x", -4); plus.setAttribute("y", 5); plus.textContent = isExpanded ? "−" : "+";
  const title = document.createElementNS(ns, "title");
  title.textContent = isExpanded ? "Collapse this expansion" : "Expand this function by one hop";
  control.append(title, circle, plus);
  control.addEventListener("pointerdown", event => event.stopPropagation());
  control.addEventListener("dblclick", event => event.stopPropagation());
  control.addEventListener("click", event => {
    event.stopPropagation();
    const now = Date.now();
    if (now - (expansionActivationTimes.get(id) || 0) < expansionActivationWindow) return;
    expansionActivationTimes.set(id, now);
    isExpanded ? collapseNode(id) : expandNode(id);
  });
  group.append(control);
}

function addText(group, text, x, y, cls, max) {
  const value = document.createElementNS(ns, "text");
  value.setAttribute("x", x); value.setAttribute("y", y); value.setAttribute("class", cls);
  value.textContent = text.length > max ? text.slice(0, max - 1) + "…" : text;
  group.append(value);
}

async function expandNode(id) {
  if (expandedNodes.has(id)) return;
  try {
    const expansion = await json(graphURL(id));
    const existingNodes = new Set(currentGraph.nodes.map(item => item.id));
    const newItems = expansion.nodes.filter(item => !existingNodes.has(item.id));
    expansionRecords.set(id, expansion);
    expandedNodes.add(id);
    currentGraph = composeGraph();
    const anchor = currentPositions.get(id) || { x: 40, y: 45 };
    const gaps = graphGaps();
    const saved = readSavedPositions();
    const sides = new Map([[-1, []], [1, []]]);
    newItems.forEach(item => sides.get(expansionSide(expansion, id, item.id)).push(item));
    for (const [side, items] of sides) {
      items.forEach((item, index) => {
        const automatic = {
          x: anchor.x + side * gaps.x,
          y: anchor.y + (index - (items.length - 1) / 2) * gaps.y,
        };
        currentPositions.set(item.id, saved[item.id] || automatic);
      });
    }
    normalizeLayout();
    savePositions();
    resizeCanvas(false);
    drawGraph();
  } catch (error) { alert(error.message); }
}

function expansionSide(expansion, anchorID, itemID) {
  if (expansion.edges.some(edge => edge.callee_id === anchorID && edge.caller_id === itemID)) return -1;
  if (expansion.edges.some(edge => edge.caller_id === anchorID && edge.callee_id === itemID)) return 1;
  return $("direction").value === "upstream" ? -1 : 1;
}

function collapseNode(id) {
  if (!expandedNodes.has(id)) return;
  expansionRecords.delete(id);
  expandedNodes.delete(id);
  pruneOrphanedExpansions();
  currentGraph = composeGraph();
  if (activeDetailID && !currentGraph.nodes.some(item => item.id === activeDetailID)) hideDetail();
  const visible = new Set(currentGraph.nodes.map(item => item.id));
  for (const nodeID of currentPositions.keys()) if (!visible.has(nodeID)) currentPositions.delete(nodeID);
  resizeCanvas(false);
  drawGraph();
}

function startDrag(event, id, group) {
  if ($("hand-tool").classList.contains("active")) return;
  event.preventDefault();
  const position = currentPositions.get(id);
  activeDrag = { id, group, startX: event.clientX, startY: event.clientY, originX: position.x, originY: position.y, moved: false };
  group.classList.add("dragging");
}

function dragNode(event) {
  if (!activeDrag) return;
  const deltaX = event.clientX - activeDrag.startX;
  const deltaY = event.clientY - activeDrag.startY;
  if (!activeDrag.moved && Math.hypot(deltaX, deltaY) < pointerMoveThreshold) return;
  const x = Math.max(10, activeDrag.originX + deltaX / zoomScale);
  const y = Math.max(10, activeDrag.originY + deltaY / zoomScale);
  activeDrag.moved = true;
  currentPositions.set(activeDrag.id, { x, y });
  activeDrag.group.setAttribute("transform", "translate(" + x + " " + y + ")");
  resizeCanvas();
  drawEdges();
}

function movePointer(event) {
  if (activeDrag) dragNode(event);
  if (activePan) panGraph(event);
}

function finishDrag() {
  if (!activeDrag) return;
  activeDrag.group.classList.remove("dragging");
  if (activeDrag.moved) {
    suppressClick = true;
    savePositions();
    setTimeout(() => { suppressClick = false; }, 0);
  }
  activeDrag = undefined;
}

function finishPointer() {
  finishDrag();
  if (!activePan) return;
  if (activePan.moved) {
    suppressClick = true;
    setTimeout(() => { suppressClick = false; }, 0);
  }
  activePan = undefined;
  $("canvas").classList.remove("panning");
}

function toggleHandTool() {
  const button = $("hand-tool");
  const active = !button.classList.contains("active");
  button.classList.toggle("active", active);
  button.setAttribute("aria-pressed", String(active));
  $("canvas").classList.toggle("hand", active);
}

function startPan(event) {
  if (!currentGraph || !$("hand-tool").classList.contains("active")) return;
  event.preventDefault();
  const wrap = $("canvas-wrap");
  activePan = { startX: event.clientX, startY: event.clientY, originX: wrap.scrollLeft, originY: wrap.scrollTop, moved: false };
  $("canvas").classList.add("panning");
}

function panGraph(event) {
  const wrap = $("canvas-wrap");
  const deltaX = event.clientX - activePan.startX;
  const deltaY = event.clientY - activePan.startY;
  if (!activePan.moved && Math.hypot(deltaX, deltaY) < pointerMoveThreshold) return;
  activePan.moved = true;
  wrap.scrollLeft = activePan.originX - deltaX;
  wrap.scrollTop = activePan.originY - deltaY;
}

function zoomGraph(factor) {
  if (!currentGraph) return;
  zoomScale = Math.min(4, Math.max(minimumZoom(), zoomScale / factor));
  applyViewport();
}

function fitGraph() {
  if (!currentGraph) return;
  const wrap = $("canvas-wrap");
  zoomScale = Math.min(1, wrap.clientWidth / contentBounds.width, wrap.clientHeight / contentBounds.height);
  applyViewport(false);
  scrollViewportTo({ x: contentBounds.width / 2, y: contentBounds.height / 2 });
}

function minimumZoom() {
  const wrap = $("canvas-wrap");
  return Math.min(0.1, wrap.clientWidth / contentBounds.width, wrap.clientHeight / contentBounds.height);
}

function viewportCenter() {
  const wrap = $("canvas-wrap");
  return {
    x: (wrap.scrollLeft + wrap.clientWidth / 2 - viewportState.marginX) / viewportState.scale,
    y: (wrap.scrollTop + wrap.clientHeight / 2 - viewportState.marginY) / viewportState.scale,
  };
}

function scrollViewportTo(center) {
  const wrap = $("canvas-wrap");
  wrap.scrollTo(
    viewportState.marginX + center.x * viewportState.scale - wrap.clientWidth / 2,
    viewportState.marginY + center.y * viewportState.scale - wrap.clientHeight / 2,
  );
}

function applyViewport(preserveCenter = true) {
  const wrap = $("canvas-wrap");
  const canvas = $("canvas");
  const center = preserveCenter ? viewportCenter() : undefined;
  // Force overflow first so the final margins use the viewport after scrollbars appear.
  canvas.style.width = contentBounds.width * zoomScale + wrap.clientWidth * 2 + "px";
  canvas.style.height = contentBounds.height * zoomScale + wrap.clientHeight * 2 + "px";
  const marginX = wrap.clientWidth;
  const marginY = wrap.clientHeight;
  const width = contentBounds.width * zoomScale + marginX * 2;
  const height = contentBounds.height * zoomScale + marginY * 2;
  canvas.style.width = width + "px";
  canvas.style.height = height + "px";
  canvas.setAttribute("viewBox", [
    -marginX / zoomScale,
    -marginY / zoomScale,
    width / zoomScale,
    height / zoomScale,
  ].join(" "));
  viewportState = { marginX, marginY, scale: zoomScale };
  if (center) scrollViewportTo(center);
}

function layoutKey() {
  return "flowmap-layout:v2:" + (activeProject || "default") + ":" + $("view").value + ":" + rootID + ":" + $("direction").value + ":" + $("tests").checked;
}

function readSavedPositions() {
  try { return JSON.parse(localStorage.getItem(layoutKey()) || "{}"); } catch (_) { return {}; }
}

function savePositions() {
  const positions = readSavedPositions();
  for (const [id, position] of currentPositions) positions[id] = position;
  try { localStorage.setItem(layoutKey(), JSON.stringify(positions)); } catch (_) {}
}

function resetLayout() {
  try { localStorage.removeItem(layoutKey()); } catch (_) {}
  if (currentGraph) render(currentGraph, true);
}

function hideDetail() {
  detailGeneration++;
  if (detailController) detailController.abort();
  detailController = undefined;
  setActiveDetail(undefined);
  $("detail").classList.add("hidden");
}

async function showDetail(id) {
  if (detailController) detailController.abort();
  setActiveDetail(id);
  const generation = ++detailGeneration;
  const controller = new AbortController();
  detailController = controller;
  const content = $("detail-content");
  content.replaceChildren(node("p", "muted", "Loading details…"));
  $("detail").classList.remove("hidden");
  try {
    const item = await json("/api/functions/" + encodeURIComponent(id), { signal: controller.signal });
    if (generation !== detailGeneration) return;
    renderDetail(item);
  } catch (error) {
    if (generation !== detailGeneration || error.name === "AbortError") return;
    content.replaceChildren(node("h2", "", "Unable to load details"), node("p", "muted", error.message));
  } finally {
    if (detailController === controller) detailController = undefined;
  }
}

function setActiveDetail(id) {
  activeDetailID = id;
  document.querySelectorAll("#nodes .node").forEach(group => {
    group.classList.toggle("detail-selected", group.dataset.id === activeDetailID);
  });
}

function renderDetail(item) {
  const content = $("detail-content");
  content.replaceChildren();
  content.append(node("h2", "", item.qualified_name));
  const badges = node("div");
  badges.append(node("span", "badge " + item.classification.kind, item.classification.kind), node("span", "muted", " " + item.classification.provenance));
  if (item.change) badges.append(node("span", "badge change-badge " + item.change.kind, item.change.kind));
  const actions = node("div", "detail-actions");
  const focusButton = node("button", "", "Focus graph here");
  focusButton.onclick = () => focusGraph(item.id, item.qualified_name);
  const expandButton = node("button", "", expandedNodes.has(item.id) ? "Collapse one hop" : "Expand one hop");
  expandButton.onclick = async () => {
    if (expandedNodes.has(item.id)) collapseNode(item.id); else await expandNode(item.id);
    showDetail(item.id);
  };
  actions.append(focusButton, expandButton);
  if (item.change) {
    const reviewed = reviewedFunctionIDs.has(item.id);
    const reviewButton = node("button", reviewed ? "reviewed-action" : "", reviewed ? "Mark unreviewed" : "Mark reviewed");
    reviewButton.type = "button";
    reviewButton.setAttribute("aria-pressed", String(reviewed));
    reviewButton.onclick = () => {
      if (reviewedFunctionIDs.has(item.id)) reviewedFunctionIDs.delete(item.id);
      else reviewedFunctionIDs.add(item.id);
      saveReviewedFunctions();
      drawGraph();
      renderGitStatus();
      renderDetail(item);
    };
    actions.append(reviewButton);
  }
  content.append(badges, node("p", "muted", item.file + ":" + item.line), actions, node("h3", "", "Intent"), node("p", "intent", item.intent || "No authored intent."));
  const button = node("button", "generate", "Generate fallback intent");
  button.onclick = async () => { button.disabled = true; try { const result = await json("/api/functions/" + encodeURIComponent(item.id) + "/summary", { method: "POST" }); button.before(node("p", "intent", result.summary)); button.remove(); } catch (error) { button.textContent = error.message; button.disabled = false; } };
  content.append(button, node("h3", "", "Contract"), node("pre", "", item.signature));
  (item.contracts || []).forEach(contract => { const card = node("div", "contract"); card.append(node("b", "", contract.name + " · " + contract.kind)); (contract.fields || []).forEach(field => card.append(node("div", "muted", field.name + " " + field.type))); (contract.methods || []).forEach(method => card.append(node("div", "muted", method))); content.append(card); });
  content.append(node("h3", "", "Classification evidence"));
  (item.classification.evidence || []).forEach(evidence => content.append(node("div", "muted", "• " + evidence)));
  const sourceHeading = node("div", "source-heading");
  sourceHeading.append(node("h3", "", "Source"));
  const source = sourceBlock(item.source, item.language);
  content.append(sourceHeading, source);
  if (item.change) {
    const toggle = node("button", "diff-toggle", "Show diff");
    toggle.type = "button";
    toggle.setAttribute("aria-pressed", "false");
    sourceHeading.append(toggle);
    const diff = diffBlock(item.change.diff);
    diff.classList.add("hidden");
    content.append(diff);
    toggle.onclick = () => {
      const showingDiff = toggle.getAttribute("aria-pressed") !== "true";
      toggle.setAttribute("aria-pressed", String(showingDiff));
      toggle.textContent = showingDiff ? "Show source" : "Show diff";
      source.classList.toggle("hidden", showingDiff);
      diff.classList.toggle("hidden", !showingDiff);
    };
  }
  $("detail").classList.remove("hidden");
}
