#!/usr/bin/env python3
"""Verify an agent-authored flow view against a running Flowmap server and render it.

Every node must exist in the Flowmap index. Every `call` edge must be a real
graph edge, and every `path` edge must be reachable downstream within its hop
budget. `inferred` edges bridge what static analysis cannot see: they must
carry evidence, a confidence, a gap class, and the basis of the claim, and they
are rejected when the graph already connects their endpoints. `external` nodes
stand for systems outside the index, such as a browser or a database.
`unresolved` entries record where a flow continues but the agent could not tell
where. The result is printed as a report and rendered to `<view>.html`.

Usage: check_view.py VIEW.json [--server URL] [--project NAME] [--root PATH]

The flowmap-views skill calls this through `fm check`, which supplies the
server, project, and repository root of the running Flowmap instance.
"""

import argparse
import html
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

DEFAULT_SERVER = "http://127.0.0.1:7878"
DEFAULT_PROJECT = "default"
DEFAULT_PATH_HOPS = 4
MAX_PATH_HOPS = 8
NODE_BUDGET = 25
EDGE_KINDS = ("call", "path", "inferred")
CONFIDENCES = ("high", "medium", "low")
BASES = ("code", "map", "overview", "graph")
GAP_CLASSES = (
    "cross-language",
    "framework-callback",
    "http-route",
    "event-stream",
    "channel-handoff",
    "registry-lookup",
    "unresolved-dispatch",
    "config-selection",
    "external-system",
    "other",
)


class Flowmap:
    """Side Effect (Edge): cached reads from the Flowmap HTTP API."""

    def __init__(self, server, project):
        self.server = server.rstrip("/")
        self.project = project
        self.cache = {}

    def get(self, path, **params):
        params["project"] = self.project
        url = f"{self.server}{path}?{urllib.parse.urlencode(params)}"
        if url in self.cache:
            return self.cache[url]

        try:
            with urllib.request.urlopen(url, timeout=60) as response:
                payload = json.load(response)
        except urllib.error.HTTPError as error:
            payload = None if error.code == 404 else _raise(error, url)

        self.cache[url] = payload
        return payload

    def function(self, language, symbol_id):
        return self.get(f"/api/functions/{symbol_id}", language=language)

    def downstream(self, language, root, depth):
        graph = self.get("/api/graph", language=language, root=root, direction="downstream", depth=depth, tests="false")
        if graph is None:
            raise RuntimeError(f"no {language} graph for {root}; is the language view scanned?")
        return graph


def _raise(error, url):
    raise RuntimeError(f"{url}: HTTP {error.code}") from error


def check(view, flowmap):
    """Return (node facts, edge results, problems) for a view."""
    problems = []
    nodes = {}

    for node in view.get("nodes", []):
        if node.get("external"):
            nodes[node["id"]] = {**node, "fact": None}
            continue

        fact = flowmap.function(node["language"], node["id"])
        if fact is None:
            problems.append(f"node {node['id']} ({node.get('label', '?')}) does not exist in the {node['language']} index (or the view is not scanned)")
        nodes[node["id"]] = {**node, "fact": fact}

    if len(nodes) > NODE_BUDGET:
        problems.append(f"view has {len(nodes)} nodes; budget is {NODE_BUDGET}")

    results = []
    for edge in view.get("edges", []):
        results.append(check_edge(edge, nodes, flowmap))

    problems.extend(f"edge {r['from_label']} -> {r['to_label']} ({r['kind']}): {r['problem']}" for r in results if r["problem"])

    for item in view.get("unresolved", []):
        if item.get("from") not in nodes:
            problems.append(f"unresolved entry starts at undeclared node {item.get('from')}")
        if item.get("gap") not in GAP_CLASSES:
            problems.append(f"unresolved entry from {item.get('from')} needs a gap class")
    return nodes, results, problems


def check_edge(edge, nodes, flowmap):
    kind = edge.get("kind")
    source = nodes.get(edge.get("from"))
    target = nodes.get(edge.get("to"))
    result = {
        **edge,
        "from_label": source["label"] if source else edge.get("from"),
        "to_label": target["label"] if target else edge.get("to"),
        "problem": None,
        "call_site": None,
    }

    if kind not in EDGE_KINDS:
        result["problem"] = f"unknown kind; use one of {', '.join(EDGE_KINDS)}"
        return result
    if source is None or target is None:
        result["problem"] = "endpoint is not declared in nodes"
        return result

    if kind == "inferred":
        result["problem"] = inferred_problem(edge, source, target, flowmap)
        return result

    if source.get("external") or target.get("external"):
        result["problem"] = "edges touching external nodes must be inferred"
        return result
    if source["language"] != target["language"]:
        result["problem"] = "cross-language edges must be inferred"
        return result

    if kind == "call":
        graph = flowmap.downstream(source["language"], source["id"], 1)
        match = next((e for e in graph["edges"] if e["caller_id"] == source["id"] and e["callee_id"] == target["id"]), None)
        if match is None:
            result["problem"] = "no direct call/dependency edge in the graph"
        else:
            result["call_site"] = match.get("call_site")
        return result

    hops = min(int(edge.get("hops", DEFAULT_PATH_HOPS)), MAX_PATH_HOPS)
    graph = flowmap.downstream(source["language"], source["id"], hops)
    if not any(n["id"] == target["id"] for n in graph["nodes"]):
        result["problem"] = f"target is not reachable downstream within {hops} hops"
    return result


def inferred_problem(edge, source, target, flowmap):
    """Return why an inferred edge is malformed or unnecessary, or None."""
    if not edge.get("evidence") or edge.get("confidence") not in CONFIDENCES:
        return "inferred edges need evidence and a high/medium/low confidence"
    if edge.get("gap") not in GAP_CLASSES:
        return f"inferred edges need a gap class: {', '.join(GAP_CLASSES)}"
    if edge.get("basis") not in BASES:
        return f"inferred edges need a basis: {', '.join(BASES)}"

    comparable = not source.get("external") and not target.get("external") and source["language"] == target["language"]
    if not comparable:
        return None

    # A gap is only real when the graph cannot already connect the endpoints.
    graph = flowmap.downstream(source["language"], source["id"], MAX_PATH_HOPS)
    if any(n["id"] == target["id"] for n in graph["nodes"]):
        return f"the graph already reaches the target within {MAX_PATH_HOPS} hops; use a call or path edge"
    return None


CLASS_DEFS = (
    "  classDef edge fill:#fff1e5,stroke:#bc4c00",
    "  classDef pure fill:#e6f6ea,stroke:#1a7f37",
    "  classDef unknown fill:#f0f1f3,stroke:#6e7781",
    "  classDef external fill:#ddf4ff,stroke:#0969da,stroke-dasharray:4 3",
    "  classDef missing fill:#ffebe9,stroke:#d1242f,stroke-width:3px",
    "  classDef stepbox fill:#f6f8fa,stroke:#57606a,stroke-width:1.5px",
    "  classDef ghost fill:#ffffff,stroke:#afb8c1,stroke-dasharray:3 3,color:#57606a",
)
PROBLEM_STYLE = "stroke:#d1242f,stroke-width:3px"
INFERRED_STYLE = "stroke:#bf8700,stroke-width:2px"
UNRESOLVED_STYLE = "stroke:#8250df,stroke-width:2px"
FAN_OUT_FOR_LR = 3


def render(view, nodes, results, problems, root):
    """Operations (Pure): build the HTML report: step overview, one diagram per step, and the full graph."""
    aliases = {node_id: f"n{index}" for index, node_id in enumerate(nodes)}
    drawable = [r for r in results if r.get("from") in aliases and r.get("to") in aliases]
    steps = _steps(view, nodes)
    step_of = {node_id: index for index, (_, ids) in enumerate(steps) for node_id in ids}

    sections = "".join(
        _step_section(index, title, ids, steps, step_of, nodes, aliases, drawable, view)
        for index, (title, ids) in enumerate(steps)
    )

    rows = []
    for node in nodes.values():
        fact = node["fact"] or {}
        location = "external" if node.get("external") else f"{_relative(fact.get('file', 'missing'), root)}:{fact.get('line', '')}"
        rows.append(
            "<tr>"
            f"<td>{html.escape(node.get('label', ''))}</td>"
            f"<td><code>{html.escape(fact.get('qualified_name', node['id']))}</code></td>"
            f"<td><code>{html.escape(location)}</code></td>"
            f"<td>{html.escape(node.get('note', ''))}</td>"
            "</tr>"
        )

    collapsed = "".join(
        f"<li><code>{html.escape(item.get('name', item.get('id', '')))}</code> — {html.escape(item.get('reason', ''))}</li>"
        for item in view.get("collapsed", [])
    )
    problem_items = "".join(f"<li>{html.escape(p)}</li>" for p in problems) or "<li>None — every graph claim verified.</li>"
    summary = "".join(f"<p>{html.escape(paragraph)}</p>" for paragraph in view.get("summary", "").split("\n\n") if paragraph)

    return TEMPLATE.format(
        title=html.escape(view.get("question", "Flow view")),
        summary=summary,
        overview=html.escape(_overview_chart(steps, step_of, drawable, view)),
        sections=sections,
        diagram=html.escape(_full_chart(steps, nodes, aliases, drawable, view)),
        rows="".join(rows),
        collapsed=collapsed or "<li>None</li>",
        problems=problem_items,
        stats=html.escape(_stats(nodes, results)),
        gaps="".join(f"<li>{html.escape(line)}</li>" for line in _gap_breakdown(results, view)) or "<li>None</li>",
        process=html.escape(json.dumps(view.get("process", {}), indent=2)),
    )


def _steps(view, nodes):
    """Operations (Pure): ordered (title, node ids) pairs; ungrouped nodes form a final step."""
    steps, grouped = [], set()
    for step in view.get("steps", []):
        ids = [node_id for node_id in step.get("nodes", []) if node_id in nodes and node_id not in grouped]
        grouped.update(ids)
        if ids:
            steps.append((step["title"], ids))

    rest = [node_id for node_id in nodes if node_id not in grouped]
    if rest:
        steps.append(("Other" if steps else "Flow", rest))
    return steps


def _overview_chart(steps, step_of, drawable, view):
    """Operations (Pure): one box per step, with cross-step edges aggregated."""
    lines = ['%%{init: {"flowchart": {"useMaxWidth": true}}}%%', "flowchart LR"]
    unresolved = {}
    for item in view.get("unresolved", []):
        if item.get("from") in step_of:
            unresolved[step_of[item["from"]]] = unresolved.get(step_of[item["from"]], 0) + 1

    for index, (title, ids) in enumerate(steps):
        detail = f"{len(ids)} node{'s' if len(ids) != 1 else ''}"
        if unresolved.get(index):
            detail += f" · {unresolved[index]} unresolved"
        lines.append(f'  s{index}["<b>{index + 1}. {_mermaid(title)}</b><br/><small>{detail}</small>"]:::stepbox')

    pairs = {}
    for result in drawable:
        source, target = step_of[result["from"]], step_of[result["to"]]
        if source != target:
            pairs.setdefault((source, target), []).append(result)

    styles = []
    for index, ((source, target), edges) in enumerate(sorted(pairs.items())):
        inferred = all(edge["kind"] == "inferred" for edge in edges)
        label = f"{len(edges)} inferred" if inferred else f"{len(edges)} edge{'s' if len(edges) != 1 else ''}"
        lines.append(f'  s{source} {"-.->" if inferred else "-->"}|"{label}"| s{target}')
        if any(edge["problem"] for edge in edges):
            styles.append(f"  linkStyle {index} {PROBLEM_STYLE}")
        elif inferred:
            styles.append(f"  linkStyle {index} {INFERRED_STYLE}")

    return "\n".join(lines + styles + list(CLASS_DEFS))


def _step_section(index, title, ids, steps, step_of, nodes, aliases, drawable, view):
    """Operations (Pure): an HTML section with a small diagram of one step and its neighbours."""
    members = set(ids)
    lines = ["flowchart {direction}"]
    for node_id in ids:
        lines.append(f"  {_node(aliases[node_id], nodes[node_id])}")

    styles, link_index, ghosts, out_degree, ghost_edges = [], 0, set(), {}, set()
    for result in drawable:
        source_in, target_in = result["from"] in members, result["to"] in members
        if not (source_in or target_in):
            continue

        if source_in and target_in:
            source_alias, target_alias = aliases[result["from"]], aliases[result["to"]]
        elif source_in:
            other = step_of[result["to"]]
            source_alias, target_alias = aliases[result["from"]], f"out{other}"
            if target_alias not in ghosts:
                lines.append(f'  {target_alias}(["→ {other + 1}. {_mermaid(steps[other][0])}"]):::ghost')
                ghosts.add(target_alias)
        else:
            other = step_of[result["from"]]
            source_alias, target_alias = f"in{other}", aliases[result["to"]]
            if source_alias not in ghosts:
                lines.append(f'  {source_alias}(["{other + 1}. {_mermaid(steps[other][0])} →"]):::ghost')
                ghosts.add(source_alias)

        # Several edges between one node and the same neighbouring step add noise, not information.
        is_ghost_edge = not (source_in and target_in)
        if is_ghost_edge and (source_alias, target_alias) in ghost_edges:
            continue
        ghost_edges.add((source_alias, target_alias))

        line, style = _edge_line(result, source_alias, target_alias)
        lines.append(line)
        out_degree[source_alias] = out_degree.get(source_alias, 0) + 1
        if style:
            styles.append(f"  linkStyle {link_index} {style}")
        link_index += 1

    # Chains read best top-to-bottom; wide fan-outs read best left-to-right, where siblings stack vertically.
    lines[0] = lines[0].format(direction="LR" if max(out_degree.values(), default=0) > FAN_OUT_FOR_LR else "TD")

    for item_index, item in enumerate(i for i in view.get("unresolved", []) if i.get("from") in members):
        lines.append(f'  u{item_index}(("?"))')
        lines.append(f'  {aliases[item["from"]]} -.-|"{_mermaid(item.get("gap", "gap?"))}: {_mermaid(item.get("note", ""))}"| u{item_index}')
        styles.append(f"  linkStyle {link_index} {UNRESOLVED_STYLE}")
        link_index += 1

    notes = "".join(
        f"<li><b>{html.escape(nodes[node_id].get('label', ''))}</b>: {html.escape(nodes[node_id]['note'])}</li>"
        for node_id in ids
        if nodes[node_id].get("note")
    )
    chart = html.escape("\n".join(lines + styles + list(CLASS_DEFS)))
    return (
        f'<section class="flow-step"><h3>{index + 1}. {html.escape(title)}</h3>'
        f'<div class="diagram"><pre class="mermaid now">{chart}</pre></div>'
        f'{f"<ul class=notes>{notes}</ul>" if notes else ""}</section>'
    )


def _full_chart(steps, nodes, aliases, drawable, view):
    """Operations (Pure): every node and edge, grouped into step subgraphs."""
    lines = ["flowchart LR"]
    for step_index, (title, ids) in enumerate(steps):
        lines.append(f'  subgraph s{step_index}["{step_index + 1}. {_mermaid(title)}"]')
        lines.extend(f"    {_node(aliases[node_id], nodes[node_id])}" for node_id in ids)
        lines.append("  end")

    styles = []
    for index, result in enumerate(drawable):
        line, style = _edge_line(result, aliases[result["from"]], aliases[result["to"]])
        lines.append(line)
        if style:
            styles.append(f"  linkStyle {index} {style}")

    for item_index, item in enumerate(i for i in view.get("unresolved", []) if i.get("from") in aliases):
        lines.append(f'  u{item_index}(("?"))')
        lines.append(f'  {aliases[item["from"]]} -.-|"{_mermaid(item.get("gap", "gap?"))}: {_mermaid(item.get("note", ""))}"| u{item_index}')
        styles.append(f"  linkStyle {len(drawable) + item_index} {UNRESOLVED_STYLE}")

    return "\n".join(lines + styles + list(CLASS_DEFS))


def _edge_line(result, source_alias, target_alias):
    """Operations (Pure): one Mermaid edge and its optional link style."""
    arrow = "-.->" if result["kind"] in ("path", "inferred") else "-->"
    label = result.get("label") or ""
    if result["kind"] == "path":
        label = f"{label} ⋯".strip()
    if result["kind"] == "inferred":
        label = f"{label} [{result.get('gap', 'gap?')}, {result.get('basis', '?')}, {result.get('confidence', '?')}]".strip()
    if result["problem"]:
        label = f"UNVERIFIED {label}".strip()

    text = f'|"{_mermaid(label)}"|' if label else ""
    style = PROBLEM_STYLE if result["problem"] else INFERRED_STYLE if result["kind"] == "inferred" else None
    return f"  {source_alias} {arrow}{text} {target_alias}", style


def _node(alias, node):
    if node.get("external"):
        return f'{alias}(["{_mermaid(node.get("label", node["id"]))}"]):::external'

    fact = node["fact"]
    css = "missing" if fact is None else fact["classification"]["kind"]
    name = node["id"] if fact is None else _short_name(fact["qualified_name"])
    return f'{alias}["{_mermaid(node.get("label", name))}<br/><small>{_mermaid(name)}</small>"]:::{css}'


def _short_name(qualified_name):
    """Operations (Pure): drop receiver pointer noise, e.g. sql.(*sql.builder).build -> sql.builder.build."""
    package, _, rest = qualified_name.partition(".(*")
    if not rest:
        return qualified_name
    receiver, _, method = rest.partition(").")
    return f"{package}.{receiver.rpartition('.')[2]}.{method}"


def _mermaid(text):
    return html.escape(str(text)).replace("&quot;", "#quot;")


def _relative(path, root):
    return path[len(root) + 1:] if root and path.startswith(root + "/") else path


def _gap_breakdown(results, view):
    """Operations (Pure): count bridged gaps by class and basis, plus unresolved gaps."""
    counts = {}
    for result in results:
        if result["kind"] == "inferred":
            key = (result.get("gap", "?"), f"bridged via {result.get('basis', '?')}")
            counts[key] = counts.get(key, 0) + 1
    for item in view.get("unresolved", []):
        key = (item.get("gap", "?"), "unresolved")
        counts[key] = counts.get(key, 0) + 1
    return [f"{gap} {how}: {count}" for (gap, how), count in sorted(counts.items())]


def _stats(nodes, results):
    counts = {kind: sum(1 for r in results if r["kind"] == kind) for kind in EDGE_KINDS}
    failed = sum(1 for r in results if r["problem"])
    return (
        f"{len(nodes)} nodes · {counts['call']} call · {counts['path']} path · "
        f"{counts['inferred']} inferred edges · {failed} unverified"
    )


TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title}</title>
<style>
  body {{ font: 15px/1.5 system-ui, sans-serif; margin: 0 auto; padding: 24px 16px; max-width: 1200px; color: #1f2328; background: #fff; }}
  h1 {{ font-size: 22px; }}
  .stats {{ color: #59636e; }}
  .diagram {{ overflow: auto; max-height: 85vh; border: 1px solid #d1d9e0; border-radius: 8px; padding: 16px; }}
  .diagram svg {{ height: auto; }}
  .diagram:not(.overview) svg {{ max-width: none !important; }}
  .flow-step {{ margin: 24px 0; }}
  .flow-step h3 {{ margin: 0 0 8px; font-size: 17px; }}
  .notes {{ margin: 8px 0 0; color: #424a53; font-size: 14px; }}
  details summary {{ cursor: pointer; font-weight: 600; margin: 24px 0 8px; }}
  .zoom {{ display: flex; gap: 6px; align-items: center; margin: 8px 0; color: #59636e; font-size: 13px; }}
  .zoom button {{ font: inherit; padding: 2px 10px; border: 1px solid #d1d9e0; border-radius: 6px; background: #f6f8fa; cursor: pointer; }}
  table {{ border-collapse: collapse; width: 100%; font-size: 13px; }}
  td, th {{ border-bottom: 1px solid #d1d9e0; padding: 6px 8px; text-align: left; vertical-align: top; }}
  code {{ font-size: 12px; }}
</style>
</head>
<body>
<h1>{title}</h1>
<p class="stats">{stats}</p>
{summary}
<h2>Overview</h2>
<div class="diagram overview"><pre class="mermaid now">{overview}</pre></div>
<h2>Steps</h2>
{sections}
<details id="full"><summary>Full graph (all nodes and edges)</summary>
<div class="zoom">Zoom <button data-zoom="out">−</button><button data-zoom="fit">Fit</button><button data-zoom="actual">100%</button><button data-zoom="in">+</button></div>
<div class="diagram" id="diagram"><pre class="mermaid deferred">{diagram}</pre></div>
</details>
<h2>Verification problems</h2>
<ul>{problems}</ul>
<h2>Nodes</h2>
<table><tr><th>Step label</th><th>Symbol</th><th>Location</th><th>Note</th></tr>{rows}</table>
<h2>Static-analysis gaps (inferred edges)</h2>
<ul>{gaps}</ul>
<h2>Collapsed</h2>
<ul>{collapsed}</ul>
<h2>Process</h2>
<pre>{process}</pre>
<script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
<script>
  mermaid.initialize({{
    startOnLoad: false,
    theme: "neutral",
    themeVariables: {{ fontSize: "15px" }},
    flowchart: {{ htmlLabels: true, useMaxWidth: false, nodeSpacing: 24, rankSpacing: 48, curve: "basis" }},
  }});
  const MIN_STEP_SCALE = 0.7;

  // Shrink wide step diagrams to the page, but never below a legible scale; past that, scroll.
  mermaid.run({{ querySelector: ".mermaid.now" }}).then(() => {{
    document.querySelectorAll(".flow-step .diagram").forEach((container) => {{
      const svg = container.querySelector("svg");
      if (!svg) return;
      const natural = svg.viewBox.baseVal.width || svg.getBoundingClientRect().width;
      const scale = Math.max(MIN_STEP_SCALE, Math.min(1, (container.clientWidth - 32) / natural));
      svg.style.width = `${{natural * scale}}px`;
    }});
  }});

  // Mermaid cannot lay out hidden elements, so the full graph renders when first opened.
  const full = document.getElementById("full");
  full.addEventListener("toggle", async () => {{
    if (!full.open || full.dataset.rendered) return;
    full.dataset.rendered = "true";
    await mermaid.run({{ querySelector: ".mermaid.deferred" }});
    const svg = document.querySelector("#diagram svg");
    const natural = svg.viewBox.baseVal.width || svg.getBoundingClientRect().width;
    let scale = 1;
    const apply = () => {{ svg.style.width = `${{natural * scale}}px`; }};
    document.querySelectorAll("[data-zoom]").forEach((button) => button.addEventListener("click", () => {{
      const action = button.dataset.zoom;
      if (action === "fit") scale = Math.min(1, (document.getElementById("diagram").clientWidth - 32) / natural);
      else if (action === "actual") scale = 1;
      else scale = Math.max(0.2, Math.min(3, scale * (action === "in" ? 1.25 : 0.8)));
      apply();
    }}));
    apply();
  }});
</script>
</body>
</html>
"""


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("view", type=Path)
    parser.add_argument("--server", default=DEFAULT_SERVER)
    parser.add_argument("--project", help="Flowmap project name; defaults to the view's project, then 'default'")
    parser.add_argument("--root", help="repository root used to shorten paths; defaults to the files' common path")
    args = parser.parse_args(argv)

    view = json.loads(args.view.read_text())
    flowmap = Flowmap(args.server, args.project or view.get("project") or DEFAULT_PROJECT)
    try:
        nodes, results, problems = check(view, flowmap)
    except (RuntimeError, urllib.error.URLError) as error:
        print(f"check_view: {error}", file=sys.stderr)
        return 2

    facts = [n["fact"] for n in nodes.values() if n["fact"]]
    root = args.root or (os.path.commonpath([f["file"] for f in facts]) if facts else "")
    output = args.view.with_suffix(".html")
    output.write_text(render(view, nodes, results, problems, root))

    print(_stats(nodes, results))
    for line in _gap_breakdown(results, view):
        print(f"  gap {line}")
    for problem in problems:
        print(f"  ✗ {problem}")
    print(f"rendered {output}")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
