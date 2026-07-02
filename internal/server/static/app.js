const ns = "http://www.w3.org/2000/svg";
const $ = id => document.getElementById(id);
let rootID = "";
let timer;
let searchGeneration = 0;
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
const detailWidthKey = "flowmap-detail-width:v1";
const detailMinWidth = 320;
const detailViewportMargin = 48;
let preferredDetailWidth = readDetailWidth();

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

// highlightGo tokenizes into text nodes so source remains safe without an HTML sanitizer.
function highlightGo(source) {
  const code = node("code", "language-go");
  const tokenPattern = /\/\/[^\n]*|\/\*[\s\S]*?\*\/|`[^`]*`|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|(?:0[xX][0-9a-fA-F](?:_?[0-9a-fA-F])*|0[bB][01](?:_?[01])*|0[oO][0-7](?:_?[0-7])*|(?:\d(?:_?\d)*\.\d(?:_?\d)*|\.\d(?:_?\d)*|\d(?:_?\d)*)(?:[eE][+-]?\d(?:_?\d)*)?i?)|[A-Za-z_]\w*|\s+|./g;
  for (const match of source.matchAll(tokenPattern)) {
    const token = match[0];
    let kind = "";
    if (token.startsWith("//") || token.startsWith("/*")) kind = "comment";
    else if (token[0] === '"' || token[0] === "'" || token[0] === "`") kind = "string";
    else if (/^(?:\d|\.\d)/.test(token)) kind = "number";
    else if (goKeywords.has(token)) kind = "keyword";
    else if (goBuiltins.has(token)) kind = "builtin";
    code.append(kind ? node("span", "token " + kind, token) : document.createTextNode(token));
  }
  return code;
}

function sourceBlock(source) {
  const pre = node("pre", "source-code");
  pre.append(highlightGo(source));
  return pre;
}

function readDetailWidth() {
  try {
    const width = Number(localStorage.getItem(detailWidthKey));
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
  try { localStorage.setItem(detailWidthKey, String(preferredDetailWidth)); } catch (_) {}
}

async function json(url, options) {
  const response = await fetch(url, options);
  const value = await response.json();
  if (!response.ok) {
    const error = new Error(value.error || response.statusText);
    error.status = response.status;
    throw error;
  }
  return value;
}

const rescanButton = node("button", "", "Rescan codebase");
rescanButton.id = "rescan";
rescanButton.title = "Rebuild the codebase scan";
document.querySelector(".controls").prepend(rescanButton);
rescanButton.addEventListener("click", rescanCodebase);
$("search").addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(search, 150); });
$("search").addEventListener("keydown", event => { if (event.key === "Escape") hideResults(); });
$("tests").addEventListener("change", () => rootID ? loadGraph() : search());
$("direction").addEventListener("change", loadGraph);
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
document.addEventListener("click", event => { if (!event.target.closest(".search-wrap")) hideResults(); });
document.addEventListener("pointermove", movePointer);
document.addEventListener("pointerup", finishPointer);
window.addEventListener("resize", () => {
  applyDetailWidth(preferredDetailWidth);
  if (currentGraph) applyViewport();
});
applyDetailWidth(preferredDetailWidth);

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
  const url = graphURL(rootID);
  try {
    const graph = await json(url);
    showLoadedGraph(graph);
  } catch (error) { alert(error.message); }
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
}

async function rescanCodebase() {
  const previousLabel = rescanButton.textContent;
  rescanButton.disabled = true;
  rescanButton.textContent = "Scanning…";
  try {
    const result = await json("/api/rescan", { method: "POST" });
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
    const failures = result.load_report.failed_package_variants || result.load_report.FailedPackageVariants || 0;
    rescanButton.textContent = failures ? `Rescanned (${failures} warnings)` : `Rescanned ${result.function_count}`;
    setTimeout(() => { if (!rescanButton.disabled) rescanButton.textContent = previousLabel; }, 1800);
  } catch (error) {
    alert(error.message);
    rescanButton.textContent = previousLabel;
  } finally {
    rescanButton.disabled = false;
  }
}

function graphURL(id) {
  return "/api/graph?root=" + encodeURIComponent(id) + "&direction=" + $("direction").value + "&depth=1&tests=" + $("tests").checked;
}

function focusGraph(id, qualifiedName) {
  rootID = id;
  if (qualifiedName) $("search").value = qualifiedName;
  hideResults();
  hideDetail();
  expandedNodes = new Set();
  expansionRecords = new Map();
  baseRootNode = undefined;
  loadGraph();
}

function edgeKey(edge) {
  return edge.caller_id + "|" + edge.callee_id + "|" + edge.dynamic + "|" + (edge.call_site || "");
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
    if (edge.dynamic) path.setAttribute("class", "dynamic");
    const title = document.createElementNS(ns, "title");
    title.textContent = edge.dynamic ? "Dynamic dispatch candidate" : "Static call";
    path.append(title);
    $("edges").append(path);
  });
}

function drawNode(item, position, isRoot, simple) {
  const group = document.createElementNS(ns, "g");
  group.setAttribute("class", "node " + item.classification.kind + (isRoot ? " root" : "") + (simple ? " simple" : "") + (item.id === activeDetailID ? " detail-selected" : ""));
  group.setAttribute("transform", "translate(" + position.x + " " + position.y + ")");
  group.dataset.id = item.id;
  const title = document.createElementNS(ns, "title");
  title.textContent = item.qualified_name;
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
  if (simple) {
    addText(group, item.name, 10, 29, "name", 26);
  } else {
    addText(group, item.qualified_name, 12, 23, "name", 34);
    addText(group, item.package, 12, 42, "pkg", 39);
    addText(group, item.signature, 12, 63, "sig", 40);
    addText(group, item.intent || "No authored intent", 12, 86, "intent", 38);
  }
  group.addEventListener("pointerdown", event => startDrag(event, item.id, group));
  group.onclick = () => { if (!suppressClick) showDetail(item.id); };
  group.ondblclick = event => {
    event.stopPropagation();
    if (event.target.closest(".expand-control")) return;
    if (!$("hand-tool").classList.contains("active")) focusGraph(item.id, item.qualified_name);
  };
  addExpansionControl(group, item.id);
  $("nodes").append(group);
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
  return "flowmap-layout:v2:" + $("view").value + ":" + rootID + ":" + $("direction").value + ":" + $("tests").checked;
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
  const actions = node("div", "detail-actions");
  const focusButton = node("button", "", "Focus graph here");
  focusButton.onclick = () => focusGraph(item.id, item.qualified_name);
  const expandButton = node("button", "", expandedNodes.has(item.id) ? "Collapse one hop" : "Expand one hop");
  expandButton.onclick = async () => {
    if (expandedNodes.has(item.id)) collapseNode(item.id); else await expandNode(item.id);
    showDetail(item.id);
  };
  actions.append(focusButton, expandButton);
  content.append(badges, node("p", "muted", item.file + ":" + item.line), actions, node("h3", "", "Intent"), node("p", "intent", item.intent || "No authored intent."));
  const button = node("button", "generate", "Generate fallback intent");
  button.onclick = async () => { button.disabled = true; try { const result = await json("/api/functions/" + encodeURIComponent(item.id) + "/summary", { method: "POST" }); button.before(node("p", "intent", result.summary)); button.remove(); } catch (error) { button.textContent = error.message; button.disabled = false; } };
  content.append(button, node("h3", "", "Contract"), node("pre", "", item.signature));
  (item.contracts || []).forEach(contract => { const card = node("div", "contract"); card.append(node("b", "", contract.name + " · " + contract.kind)); (contract.fields || []).forEach(field => card.append(node("div", "muted", field.name + " " + field.type))); (contract.methods || []).forEach(method => card.append(node("div", "muted", method))); content.append(card); });
  content.append(node("h3", "", "Classification evidence"));
  (item.classification.evidence || []).forEach(evidence => content.append(node("div", "muted", "• " + evidence)));
  content.append(node("h3", "", "Source"), sourceBlock(item.source));
  $("detail").classList.remove("hidden");
}
